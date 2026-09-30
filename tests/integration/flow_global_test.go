package integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"quiz/internal/cache"
	"quiz/internal/handlers"
	"quiz/internal/testutil"
)

// globalFixture builds the Step 11 stack: the real routes plus a Global the
// test drives directly with WatchOnce/Rehydrate (cmd/server runs the loop;
// tests never do).
func globalFixture(t *testing.T) (*httptest.Server, *handlers.Global, *sql.DB) {
	t.Helper()
	pool := testutil.DB(t)
	testutil.Migrate(t, pool)
	testutil.Reset(t, pool, "sessions", "users", "password_resets",
		"quizzes", "questions", "quiz_questions", "participants",
		"answers", "anti_cheat_events")
	store := cache.New()
	cfg := testutil.Config(t)
	ts, globals := newServerWith(t, pool, store, cfg,
		&handlers.Teacher{DB: pool, Store: store})
	return ts, globals, pool
}

// linearQuiz builds an ACTIVE global-timer quiz with n pg questions
// (correct answer = option 0 in every one).
func linearQuiz(t *testing.T, ts *httptest.Server, pool *sql.DB, ck *http.Cookie,
	title string, n int,
) (uint64, string, []uint64) {
	t.Helper()
	quizID := createQuiz(t, ts, ck, quizForm(title))
	qids := make([]uint64, 0, n)
	for i := 0; i < n; i++ {
		qids = append(qids, addQuestion(t, ts, pool, ck,
			fmt.Sprintf("Q%d pick A", i+1), "pg", []string{"A", "B"}, []int{0}))
	}
	compose(t, ts, ck, quizID, qids...)
	if resp, body := setStatus(t, ts, ck, quizID, "aktif"); resp.StatusCode != http.StatusOK || !decodeEnv(t, body).OK {
		t.Fatalf("activate = %d: %s", resp.StatusCode, body)
	}
	return quizID, joinCode(t, pool, quizID), qids
}

// startGlobal POSTs the teacher START and asserts 200.
func startGlobal(t *testing.T, ts *httptest.Server, ck *http.Cookie, quizID uint64) map[string]any {
	t.Helper()
	resp, body := postJSON(t, fmt.Sprintf("%s/teacher/quiz/%d/start", ts.URL, quizID), nil, ck)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("START = %d: %s", resp.StatusCode, body)
	}
	env := decodeEnv(t, body)
	var data map[string]any
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatalf("START data %q: %v", env.Data, err)
	}
	return data
}

// quizStatus reads quizzes.status.
func quizStatus(t *testing.T, pool *sql.DB, quizID uint64) string {
	t.Helper()
	var status string
	if err := pool.QueryRow(`SELECT status FROM quizzes WHERE id = ?`, quizID).Scan(&status); err != nil {
		t.Fatalf("quiz status: %v", err)
	}
	return status
}

// --- open + approve → waiting room → START ---------------------------------

func TestGlobalStartApproveFlow(t *testing.T) {
	ts, _, pool := globalFixture(t)
	ck := guruLogin(t, ts)

	form := quizForm("Approve flow")
	form.Set("join_mode", "approve")
	quizID := createQuiz(t, ts, ck, form)
	q := addQuestion(t, ts, pool, ck, "Pick A", "pg", []string{"A", "B"}, []int{0})
	compose(t, ts, ck, quizID, q)
	if resp, body := setStatus(t, ts, ck, quizID, "aktif"); resp.StatusCode != http.StatusOK || !decodeEnv(t, body).OK {
		t.Fatalf("activate = %d: %s", resp.StatusCode, body)
	}
	code := joinCode(t, pool, quizID)

	stA := studentCookie(t, pool, "approve-a")
	stB := studentCookie(t, pool, "approve-b")
	stC := studentCookie(t, pool, "approve-c")
	for i, st := range []*http.Cookie{stA, stB, stC} {
		if resp, body := postJoin(t, ts, code, st); resp.StatusCode != http.StatusOK {
			t.Fatalf("join %d = %d: %s", i, resp.StatusCode, body)
		} else if d := decodeJoin(t, body); d.Status != "pending" {
			t.Fatalf("join %d status = %q", i, d.Status)
		}
	}
	pidOf := func(st *http.Cookie) uint64 {
		pid, status, _ := participantRowFor(t, pool, quizID, userOf(t, ts, st))
		if status != "pending" {
			t.Fatalf("pre-START status = %s", status)
		}
		return pid
	}
	pidA, pidB, pidC := pidOf(stA), pidOf(stB), pidOf(stC)

	// waiting room on the monitor page
	resp, body := getWith(t, fmt.Sprintf("%s/teacher/quiz/%d/monitor", ts.URL, quizID), ck)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("monitor = %d: %s", resp.StatusCode, body)
	}
	if !contains(body, `id="pending-count">3<`) {
		t.Fatalf("waiting room count missing: %s", body)
	}

	// approve two, leave C pending
	approve := func(pid uint64) (*http.Response, string) {
		t.Helper()
		return postJSON(t, fmt.Sprintf("%s/teacher/quiz/%d/participants/%d/action", ts.URL, quizID, pid),
			map[string]string{"action": "approve"}, ck)
	}
	if resp, body := approve(pidA); resp.StatusCode != http.StatusOK {
		t.Fatalf("approve A = %d: %s", resp.StatusCode, body)
	} else if env := decodeEnv(t, body); string(env.Data) != `{"status":"registered"}` {
		t.Fatalf("approve data = %s", env.Data)
	}
	if resp, body := approve(pidB); resp.StatusCode != http.StatusOK {
		t.Fatalf("approve B = %d: %s", resp.StatusCode, body)
	}

	// START: A and B share ONE clock, C (still pending) is auto-rejected
	data := startGlobal(t, ts, ck, quizID)
	if data["status"] != "berjalan" {
		t.Fatalf("START status = %v", data["status"])
	}
	endsOf := func(pid uint64) (string, sql.NullString, sql.NullString) {
		var status string
		var ends, qorder sql.NullString
		err := pool.QueryRow(`SELECT status, ends_at, qorder FROM participants WHERE id = ?`, pid).
			Scan(&status, &ends, &qorder)
		if err != nil {
			t.Fatalf("read participant %d: %v", pid, err)
		}
		return status, ends, qorder
	}
	statusA, endsA, qorderA := endsOf(pidA)
	statusB, endsB, qorderB := endsOf(pidB)
	if statusA != "started" || statusB != "started" {
		t.Fatalf("after START: A=%s B=%s", statusA, statusB)
	}
	if !endsA.Valid || endsA.String == "" || endsA.String != endsB.String {
		t.Fatalf("global clock not shared: A ends_at=%v B ends_at=%v", endsA, endsB)
	}
	if !qorderA.Valid || !qorderB.Valid {
		t.Fatalf("qorder not written: %v / %v", qorderA, qorderB)
	}
	statusC, _, _ := endsOf(pidC)
	var finalC sql.NullString
	if err := pool.QueryRow(`SELECT final_score FROM participants WHERE id = ?`, pidC).Scan(&finalC); err != nil {
		t.Fatalf("C final_score: %v", err)
	}
	if statusC != "dikeluarkan" || finalC.Valid {
		t.Fatalf("pending at START: status=%s final_score=%v (want dikeluarkan/NULL)", statusC, finalC)
	}

	// waiting room is empty now
	resp, body = getWith(t, fmt.Sprintf("%s/teacher/quiz/%d/monitor", ts.URL, quizID), ck)
	if resp.StatusCode != http.StatusOK || !contains(body, `id="pending-count">0<`) {
		t.Fatalf("post-START monitor = %d: %s", resp.StatusCode, body)
	}

	// approve arriving AFTER START → 409 (row gone or quiz moved on)
	if resp, body := approve(pidC); resp.StatusCode != http.StatusConflict {
		t.Fatalf("late approve = %d, want 409: %s", resp.StatusCode, body)
	} else if env := decodeEnv(t, body); env.Message != "Permintaan ini tidak dapat disetujui lagi." {
		t.Fatalf("late approve message = %q", env.Message)
	}

	// the auto-rejected student cannot come back → 403
	if resp, body := postJoin(t, ts, code, stC); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("rejoin after rejection = %d, want 403: %s", resp.StatusCode, body)
	}

	// START twice → 409
	if resp, body := postJSON(t, fmt.Sprintf("%s/teacher/quiz/%d/start", ts.URL, quizID), nil, ck); resp.StatusCode != http.StatusConflict {
		t.Fatalf("second START = %d, want 409: %s", resp.StatusCode, body)
	}
}

// --- START with zero participants ------------------------------------------

func TestStartWithZeroParticipants(t *testing.T) {
	ts, _, pool := globalFixture(t)
	ck := guruLogin(t, ts)
	quizID, _, _ := linearQuiz(t, ts, pool, ck, "Lonely start", 1)

	data := startGlobal(t, ts, ck, quizID)
	if data["status"] != "berjalan" {
		t.Fatalf("START status = %v", data["status"])
	}
	if got := quizStatus(t, pool, quizID); got != "berjalan" {
		t.Fatalf("quiz status = %s", got)
	}
	resp, body := getWith(t, fmt.Sprintf("%s/teacher/quiz/%d/monitor", ts.URL, quizID), ck)
	if resp.StatusCode != http.StatusOK || !contains(body, "Belum ada yang bergabung.") {
		t.Fatalf("empty monitor = %d: %s", resp.StatusCode, body)
	}
}

// --- STOP / timeout races ---------------------------------------------------

func TestStopTwiceClosesOnce(t *testing.T) {
	ts, _, pool := globalFixture(t)
	ck := guruLogin(t, ts)
	quizID, code, _ := linearQuiz(t, ts, pool, ck, "Stop twice", 1)
	st := studentCookie(t, pool, "stopper")
	if resp, body := postJoin(t, ts, code, st); resp.StatusCode != http.StatusOK {
		t.Fatalf("join = %d: %s", resp.StatusCode, body)
	}
	startGlobal(t, ts, ck, quizID)

	type result struct {
		status int
		body   string
	}
	results := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func() {
			resp, body := postJSON(t, fmt.Sprintf("%s/teacher/quiz/%d/stop", ts.URL, quizID),
				map[string]bool{"confirm": true}, ck)
			results <- result{resp.StatusCode, body}
		}()
	}
	r1, r2 := <-results, <-results
	if r1.status == r2.status {
		t.Fatalf("both STOPs returned %d (%s | %s)", r1.status, r1.body, r2.body)
	}
	for _, r := range []result{r1, r2} {
		if r.status == http.StatusConflict {
			env := decodeEnv(t, r.body)
			if env.Message != "Kuis ini tidak sedang berjalan." {
				t.Fatalf("losing STOP message = %q", env.Message)
			}
		}
		if r.status != http.StatusOK && r.status != http.StatusConflict {
			t.Fatalf("STOP = %d: %s", r.status, r.body)
		}
	}
	if got := quizStatus(t, pool, quizID); got != "selesai" {
		t.Fatalf("quiz status = %s, want selesai", got)
	}
}

func TestStopVsTimeoutRace(t *testing.T) {
	ts, globals, pool := globalFixture(t)
	ck := guruLogin(t, ts)
	form := quizForm("Stop vs timeout")
	form.Set("total_seconds", "1") // overdue after ~1 s
	quizID := createQuiz(t, ts, ck, form)
	q := addQuestion(t, ts, pool, ck, "A", "pg", []string{"A", "B"}, []int{0})
	compose(t, ts, ck, quizID, q)
	if resp, body := setStatus(t, ts, ck, quizID, "aktif"); resp.StatusCode != http.StatusOK || !decodeEnv(t, body).OK {
		t.Fatalf("activate = %d: %s", resp.StatusCode, body)
	}
	st := studentCookie(t, pool, "svt")
	if resp, body := postJoin(t, ts, joinCode(t, pool, quizID), st); resp.StatusCode != http.StatusOK {
		t.Fatalf("join = %d: %s", resp.StatusCode, body)
	}
	startGlobal(t, ts, ck, quizID)
	time.Sleep(1300 * time.Millisecond) // the 1 s clock runs out

	statusCh := make(chan int, 1)
	go func() {
		resp, _ := postJSON(t, fmt.Sprintf("%s/teacher/quiz/%d/stop", ts.URL, quizID),
			map[string]bool{"confirm": true}, ck)
		statusCh <- resp.StatusCode
	}()
	watchDone := make(chan error, 1)
	go func() { watchDone <- globals.WatchOnce(context.Background()) }()

	stopStatus := <-statusCh
	if err := <-watchDone; err != nil {
		t.Fatalf("WatchOnce: %v", err)
	}
	if stopStatus != http.StatusOK && stopStatus != http.StatusConflict {
		t.Fatalf("STOP = %d", stopStatus)
	}
	if got := quizStatus(t, pool, quizID); got != "selesai" {
		t.Fatalf("quiz status = %s, want selesai exactly once", got)
	}
	// both end-triggers are idempotent after the race
	if err := globals.WatchOnce(context.Background()); err != nil {
		t.Fatalf("second WatchOnce: %v", err)
	}
	if got := quizStatus(t, pool, quizID); got != "selesai" {
		t.Fatalf("quiz status after re-tick = %s", got)
	}
}

func TestTimeoutScoresOverFullQuestionCount(t *testing.T) {
	ts, globals, pool := globalFixture(t)
	ck := guruLogin(t, ts)
	form := quizForm("Timeout scoring")
	form.Set("total_seconds", "1")
	quizID := createQuiz(t, ts, ck, form)
	qids := make([]uint64, 0, 10)
	for i := 0; i < 10; i++ {
		qids = append(qids, addQuestion(t, ts, pool, ck,
			fmt.Sprintf("TQ%d", i+1), "pg", []string{"A", "B"}, []int{0}))
	}
	compose(t, ts, ck, quizID, qids...)
	if resp, body := setStatus(t, ts, ck, quizID, "aktif"); resp.StatusCode != http.StatusOK || !decodeEnv(t, body).OK {
		t.Fatalf("activate = %d: %s", resp.StatusCode, body)
	}
	code := joinCode(t, pool, quizID)
	st := studentCookie(t, pool, "timeout-student")
	if resp, body := postJoin(t, ts, code, st); resp.StatusCode != http.StatusOK {
		t.Fatalf("join = %d: %s", resp.StatusCode, body)
	}
	startGlobal(t, ts, ck, quizID)
	// the attempt clock is irrelevant to the close-timeout here: keep it out
	// of the way so the 3 answers always land before WatchOnce fires (the
	// quiz-level deadline is what makes the watcher close)
	if _, err := pool.Exec(`UPDATE participants SET ends_at = DATE_ADD(NOW(), INTERVAL 300 SECOND)
		WHERE quiz_id = ? AND status = 'started'`, quizID); err != nil {
		t.Fatalf("extend participant clock: %v", err)
	}

	// 3 of 10 answered correctly (option 0 is correct everywhere)
	for i := 0; i < 3; i++ {
		if resp, body := answerPost(t, ts, code, st, qids[i], []int{0}); resp.StatusCode != http.StatusOK {
			t.Fatalf("answer %d = %d: %s", i, resp.StatusCode, body)
		}
	}
	_, status, _ := participantRowFor(t, pool, quizID, userOf(t, ts, st))
	if status != "started" {
		t.Fatalf("participant status = %s", status)
	}
	time.Sleep(1300 * time.Millisecond)
	if err := globals.WatchOnce(context.Background()); err != nil {
		t.Fatalf("WatchOnce: %v", err)
	}

	if got := quizStatus(t, pool, quizID); got != "selesai" {
		t.Fatalf("quiz status = %s, want selesai", got)
	}
	var pStatus, finishedAt sql.NullString
	var score sql.NullFloat64
	err := pool.QueryRow(`SELECT status, finished_at, score_auto FROM participants
		WHERE quiz_id = ? AND attempt_no = 1`, quizID).
		Scan(&pStatus, &finishedAt, &score)
	if err != nil {
		t.Fatalf("participant: %v", err)
	}
	if pStatus.String != "selesai" || !finishedAt.Valid {
		t.Fatalf("participant = %s / finished_at=%v", pStatus.String, finishedAt)
	}
	if !score.Valid || score.Float64 != 30 {
		t.Fatalf("score_auto = %v, want 30.00 (3 correct over the FULL 10)", score.Float64)
	}
}

// --- all-finished announcement ---------------------------------------------

func TestAllFinishedAnnouncement(t *testing.T) {
	ts, globals, pool := globalFixture(t)
	ck := guruLogin(t, ts)
	quizID, code, _ := linearQuiz(t, ts, pool, ck, "All finished", 2)
	st := studentCookie(t, pool, "allfin")
	if resp, body := postJoin(t, ts, code, st); resp.StatusCode != http.StatusOK {
		t.Fatalf("join = %d: %s", resp.StatusCode, body)
	}
	startGlobal(t, ts, ck, quizID)

	teacher := openSSE(t, fmt.Sprintf("%s/teacher/quiz/%d/monitor/stream", ts.URL, quizID), ck)
	if ev, _, _, ok := teacher.readEvent(3 * time.Second); !ok || ev != "snapshot" {
		t.Fatalf("teacher greeting = %q ok=%v, want snapshot", ev, ok)
	}

	// the only working student finishes — its `finished`/`rank` events are
	// queued ahead of the announcement, so drain until it shows up
	if resp, body := postJSON(t, ts.URL+"/quiz/"+code+"/finish", nil, st); resp.StatusCode != http.StatusOK {
		t.Fatalf("finish = %d: %s", resp.StatusCode, body)
	}
	if err := globals.WatchOnce(context.Background()); err != nil {
		t.Fatalf("WatchOnce: %v", err)
	}
	var sawAnnouncement bool
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		ev, _, _, ok := teacher.readEvent(time.Until(deadline))
		if !ok {
			break
		}
		if ev == "all_finished_pending" {
			sawAnnouncement = true
			break
		}
	}
	if !sawAnnouncement {
		t.Fatalf("all_finished_pending never arrived")
	}
	// the announcement is once-per-quiz until close
	if err := globals.WatchOnce(context.Background()); err != nil {
		t.Fatalf("second WatchOnce: %v", err)
	}
	deadline = time.Now().Add(700 * time.Millisecond)
	for time.Now().Before(deadline) {
		ev, _, _, ok := teacher.readEvent(time.Until(deadline))
		if !ok {
			break
		}
		if ev == "all_finished_pending" {
			t.Fatalf("all_finished_pending published twice")
		}
	}

	// teacher confirms → selesai, and the closing notice carries the
	// all_finished reason (plan's three force_stop reasons)
	resp, body := postJSON(t, fmt.Sprintf("%s/teacher/quiz/%d/stop", ts.URL, quizID),
		map[string]bool{"confirm": true}, ck)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("confirm stop = %d: %s", resp.StatusCode, body)
	}
	if got := quizStatus(t, pool, quizID); got != "selesai" {
		t.Fatalf("quiz status = %s", got)
	}
	var stopEvent, stopData string
	stopDeadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(stopDeadline) {
		ev, data, _, ok := teacher.readEvent(time.Until(stopDeadline))
		if !ok {
			break
		}
		if ev == "force_stop" {
			stopEvent, stopData = ev, data
			break
		}
	}
	if stopEvent != "force_stop" || !contains(stopData, "all_finished") {
		t.Fatalf("closing event = %q %s, want force_stop/all_finished", stopEvent, stopData)
	}
}

// --- disconnect freeze at the last heartbeat (spec §11.16) ------------------

func TestDisconnectFreezesAtLastHeartbeat(t *testing.T) {
	ts, globals, pool := globalFixture(t)
	ck := guruLogin(t, ts)
	quizID, code, _ := linearQuiz(t, ts, pool, ck, "Freeze clock", 2)
	st := studentCookie(t, pool, "freezer")
	uid := userOf(t, ts, st)
	if resp, body := postJoin(t, ts, code, st); resp.StatusCode != http.StatusOK {
		t.Fatalf("join = %d: %s", resp.StatusCode, body)
	}
	startGlobal(t, ts, ck, quizID)
	pid, _, _ := participantRowFor(t, pool, quizID, uid)
	globals.SetDisconnectSilence(400 * time.Millisecond)

	endsAt := func() time.Time {
		t.Helper()
		var parsed time.Time
		if err := pool.QueryRow(`SELECT ends_at FROM participants WHERE id = ?`, pid).Scan(&parsed); err != nil {
			t.Fatalf("ends_at: %v", err)
		}
		return parsed.UTC()
	}
	origEnds := endsAt()

	// connect (the greeting write is the connection's heartbeat), read it,
	// then hang up
	openedAt := time.Now()
	conn := openSSE(t, ts.URL+"/quiz/"+code+"/stream", st)
	if ev, _, _, ok := conn.readEvent(3 * time.Second); !ok || ev != "snapshot" {
		t.Fatalf("greeting = %q ok=%v, want snapshot", ev, ok)
	}
	hangupAt := time.Now()
	conn.resp.Body.Close()
	time.Sleep(600 * time.Millisecond) // > silence; server notices the close

	// sweep until the freeze lands (the handler exit is asynchronous)
	deadline := time.Now().Add(3 * time.Second)
	for {
		if err := globals.WatchOnce(context.Background()); err != nil {
			t.Fatalf("WatchOnce: %v", err)
		}
		if endsAt().Before(origEnds.Add(-time.Minute)) {
			break // pinned back to the heartbeat
		}
		if time.Now().After(deadline) {
			t.Fatalf("clock never froze (ends_at still %s)", endsAt())
		}
		time.Sleep(100 * time.Millisecond)
	}
	frozen := endsAt()
	// pinned to the LAST HEARTBEAT (≈ the greeting write at/before hangup),
	// never the detection instant (≥ hangup + 400 ms)
	if frozen.After(hangupAt.Add(200 * time.Millisecond)) {
		t.Fatalf("frozen at %s — detection-time freeze, not last heartbeat (hangup %s)", frozen, hangupAt)
	}
	if frozen.Before(openedAt.Add(-2 * time.Second)) {
		t.Fatalf("frozen ends_at %s precedes the connection %s", frozen, openedAt)
	}

	// reconnect → the frozen remainder is handed back, no time lost or gained
	conn2 := openSSE(t, ts.URL+"/quiz/"+code+"/stream", st)
	if ev, _, _, ok := conn2.readEvent(3 * time.Second); !ok || ev != "snapshot" {
		t.Fatalf("reconnect greeting = %q ok=%v", ev, ok)
	}
	after := endsAt()
	if !after.After(frozen.Add(400 * time.Second)) {
		t.Fatalf("resume lost time: frozen=%s resumed=%s (orig %s)", frozen, after, origEnds)
	}
	if after.After(time.Now().UTC().Add(610 * time.Second)) {
		t.Fatalf("resume gained time: resumed %s", after)
	}
	if _, st2, _ := participantRowFor(t, pool, quizID, uid); st2 != "started" {
		t.Fatalf("status after resume = %s", st2)
	}
}

// --- boot rehydrate (spec §6.3) ---------------------------------------------

func TestRehydrateAfterRestart(t *testing.T) {
	ts, globals, pool := globalFixture(t)
	ck := guruLogin(t, ts)
	quizID, _, qids := linearQuiz(t, ts, pool, ck, "Rehydrate", 10)
	stA := studentCookie(t, pool, "rehy-a")
	stB := studentCookie(t, pool, "rehy-b")
	uidA, uidB := userOf(t, ts, stA), userOf(t, ts, stB)

	// simulate an expired-while-down attempt (3 of 10 correct) and a live one
	for _, uid := range []uint64{uidA, uidB} {
		if _, err := pool.Exec(`INSERT INTO participants
			(quiz_id, user_id, status, started_at, ends_at, current_q, current_q_since)
			VALUES (?, ?, 'started', NOW(), NOW(), 1, NOW())`, quizID, uid); err != nil {
			t.Fatalf("seed participant: %v", err)
		}
	}
	var pidA uint64
	if err := pool.QueryRow(`SELECT id FROM participants WHERE quiz_id = ? AND user_id = ?`,
		quizID, uidA).Scan(&pidA); err != nil {
		t.Fatalf("pidA: %v", err)
	}
	// A is long expired, B still has time on the clock
	if _, err := pool.Exec(`UPDATE participants SET ends_at = DATE_SUB(NOW(), INTERVAL 1 HOUR) WHERE id = ?`, pidA); err != nil {
		t.Fatalf("expire A: %v", err)
	}
	if _, err := pool.Exec(`UPDATE participants SET ends_at = DATE_ADD(NOW(), INTERVAL 300 SECOND)
		WHERE quiz_id = ? AND user_id = ?`, quizID, uidB); err != nil {
		t.Fatalf("extend B: %v", err)
	}
	// A answered 3 of 10 correctly while the server was down
	for i := 0; i < 3; i++ {
		if _, err := pool.Exec(`INSERT INTO answers (participant_id, question_id, answer, is_correct, answered_at)
			VALUES (?, ?, '[0]', 1, NOW())`, pidA, qids[i]); err != nil {
			t.Fatalf("seed answer %d: %v", i, err)
		}
	}
	var endsB string
	if err := pool.QueryRow(`SELECT ends_at FROM participants WHERE quiz_id = ? AND user_id = ?`,
		quizID, uidB).Scan(&endsB); err != nil {
		t.Fatalf("endsB: %v", err)
	}

	if err := globals.Rehydrate(context.Background()); err != nil {
		t.Fatalf("Rehydrate: %v", err)
	}

	var statusA string
	var scoreA sql.NullFloat64
	var finishedA sql.NullString
	if err := pool.QueryRow(`SELECT status, score_auto, finished_at FROM participants WHERE id = ?`, pidA).
		Scan(&statusA, &scoreA, &finishedA); err != nil {
		t.Fatalf("A after rehydrate: %v", err)
	}
	if statusA != "selesai" || !finishedA.Valid {
		t.Fatalf("expired attempt = %s / finished_at %v", statusA, finishedA)
	}
	if !scoreA.Valid || scoreA.Float64 != 30 {
		t.Fatalf("expired score = %v, want 30.00 (unanswered = wrong over 10)", scoreA.Float64)
	}
	var statusB, endsBAfter string
	if err := pool.QueryRow(`SELECT status, ends_at FROM participants WHERE quiz_id = ? AND user_id = ?`,
		quizID, uidB).Scan(&statusB, &endsBAfter); err != nil {
		t.Fatalf("B after rehydrate: %v", err)
	}
	if statusB != "started" || endsBAfter != endsB {
		t.Fatalf("live attempt changed: status=%s ends_at %s → %s", statusB, endsB, endsBAfter)
	}

	// rankers rebuilt: both are in the running with their stored scores
	var pidB uint64
	if err := pool.QueryRow(`SELECT id FROM participants WHERE quiz_id = ? AND user_id = ?`,
		quizID, uidB).Scan(&pidB); err != nil {
		t.Fatalf("pidB: %v", err)
	}
	entries := globals.Live.Ranker(quizID).Snapshot()
	var foundA, foundB bool
	for _, e := range entries {
		switch e.ParticipantID {
		case pidA:
			foundA = true
			if !e.Finished || e.Score != 30 {
				t.Fatalf("entry A = %+v", e)
			}
		case pidB:
			foundB = true
			if e.Finished || e.Score != 0 {
				t.Fatalf("entry B = %+v", e)
			}
		}
	}
	if !foundA || !foundB {
		t.Fatalf("rebuild missing entries: A=%v B=%v (n=%d)", foundA, foundB, len(entries))
	}
}
