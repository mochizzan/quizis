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

	"quiz/internal/handlers"
	"quiz/internal/quizengine"
)

// --- helpers ---------------------------------------------------------------

// startAttempt POSTs /start and returns the decoded state.
type startData struct {
	Status   string `json:"status"`
	EndsAt   int64  `json:"ends_at"`
	CurrentQ int    `json:"current_q"`
}

func startAttempt(t *testing.T, ts *httptest.Server, code string, ck *http.Cookie) startData {
	t.Helper()
	resp, body := postJSON(t, ts.URL+"/quiz/"+code+"/start", nil, ck)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("start = %d: %s", resp.StatusCode, body)
	}
	env := decodeEnv(t, body)
	if !env.OK {
		t.Fatalf("start envelope: %s", body)
	}
	var d startData
	if err := json.Unmarshal(env.Data, &d); err != nil {
		t.Fatalf("start data %q: %v", env.Data, err)
	}
	return d
}

// answerBody is the /answer payload.
func answerPost(t *testing.T, ts *httptest.Server, code string, ck *http.Cookie, qid uint64, answer any) (*http.Response, string) {
	t.Helper()
	return postJSON(t, ts.URL+"/quiz/"+code+"/answer",
		map[string]any{"question_id": qid, "answer": answer}, ck)
}

// countAnswers counts a participant's answer rows.
func countAnswers(t *testing.T, pool *sql.DB, pid uint64) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(`SELECT COUNT(*) FROM answers WHERE participant_id = ?`, pid).Scan(&n); err != nil {
		t.Fatalf("count answers: %v", err)
	}
	return n
}

// answerRow reads one answer's (answer, is_correct) — found=false when absent.
func answerRow(t *testing.T, pool *sql.DB, pid, qid uint64) (string, sql.NullInt64, bool) {
	t.Helper()
	var raw string
	var okBit sql.NullInt64
	err := pool.QueryRow(`SELECT answer, is_correct FROM answers
		WHERE participant_id = ? AND question_id = ?`, pid, qid).
		Scan(&raw, &okBit)
	if err == sql.ErrNoRows {
		return "", sql.NullInt64{}, false
	}
	if err != nil {
		t.Fatalf("answer row: %v", err)
	}
	return raw, okBit, true
}

// persoalFixture builds an ACTIVE per_soal quiz with n pg questions and joins
// the student; returns (code, question ids in seq order).
func persoalFixture(t *testing.T, ts *httptest.Server, pool *sql.DB, ck, st *http.Cookie,
	title string, questions []string, joinMode string,
) (string, []uint64) {
	t.Helper()
	form := quizForm(title)
	form.Set("timer_type", "per_soal")
	form.Set("per_question_seconds", "60")
	form.Set("join_mode", joinMode)
	quizID := createQuiz(t, ts, ck, form)
	ids := make([]uint64, 0, len(questions))
	for i, teks := range questions {
		correct := 0
		if i%2 == 1 {
			correct = 1
		}
		ids = append(ids, addQuestion(t, ts, pool, ck, teks, "pg",
			[]string{"Alpha", "Beta"}, []int{correct}))
	}
	compose(t, ts, ck, quizID, ids...)
	if resp, body := setStatus(t, ts, ck, quizID, "aktif"); resp.StatusCode != http.StatusOK || !decodeEnv(t, body).OK {
		t.Fatalf("activate = %d: %s", resp.StatusCode, body)
	}
	code := joinCode(t, pool, quizID)
	if resp, body := postJoin(t, ts, code, st); resp.StatusCode != http.StatusOK {
		t.Fatalf("join = %d: %s", resp.StatusCode, body)
	}
	return code, ids
}

// --- start idempotency + answer upsert + finish ----------------------------

func TestPerQuestionStartAnswerFinishFlow(t *testing.T) {
	ts, pool, _, _ := quizFixture(t)
	ck := guruLogin(t, ts)
	st := studentCookie(t, pool, "flow1")

	code, ids := persoalFixture(t, ts, pool, ck, st,
		"Flow Personal", []string{"Pick Beta", "Pick Alpha"}, "open")
	q1, q2 := ids[0], ids[1]
	quizID, err := func() (uint64, error) {
		var id uint64
		err := pool.QueryRow(`SELECT id FROM quizzes WHERE code = ?`, code).Scan(&id)
		return id, err
	}()
	if err != nil {
		t.Fatalf("quiz id: %v", err)
	}
	pid, _, _ := participantRowFor(t, pool, quizID, userOf(t, ts, st))

	// --- start: fresh write of qorder + personal clock
	first := startAttempt(t, ts, code, st)
	if first.Status != "started" || first.CurrentQ != 1 || first.EndsAt <= 0 {
		t.Fatalf("start = %+v", first)
	}
	var qorder1, endsAt1 sql.NullString
	if err := pool.QueryRow(`SELECT qorder, ends_at FROM participants WHERE id = ?`, pid).
		Scan(&qorder1, &endsAt1); err != nil {
		t.Fatalf("read start columns: %v", err)
	}
	if !qorder1.Valid || !endsAt1.Valid {
		t.Fatalf("start did not write qorder/ends_at: %+v / %+v", qorder1, endsAt1)
	}
	var order quizengine.Order
	if err := json.Unmarshal([]byte(qorder1.String), &order); err != nil {
		t.Fatalf("qorder JSON: %v", err)
	}
	if len(order.Questions) != 2 || order.Questions[0] != q1 || order.Questions[1] != q2 {
		t.Fatalf("qorder = %v, want [%d %d] (no shuffle configured)", order.Questions, q1, q2)
	}

	// --- start again: idempotent, identical snapshot
	second := startAttempt(t, ts, code, st)
	var qorder2, endsAt2 sql.NullString
	if err := pool.QueryRow(`SELECT qorder, ends_at FROM participants WHERE id = ?`, pid).
		Scan(&qorder2, &endsAt2); err != nil {
		t.Fatalf("re-read start columns: %v", err)
	}
	if qorder1.String != qorder2.String || endsAt1.String != endsAt2.String {
		t.Fatalf("second start regenerated qorder/ends_at:\n%s\n%s", qorder1.String, qorder2.String)
	}
	if second.Status != "started" || second.CurrentQ != first.CurrentQ || second.EndsAt != first.EndsAt {
		t.Fatalf("second start state = %+v, want %+v", second, first)
	}

	// --- answer q1 correctly (per_soal never previews); fixture q1 correct = 0
	resp, body := answerPost(t, ts, code, st, q1, []int{0})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("answer = %d: %s", resp.StatusCode, body)
	}
	env := decodeEnv(t, body)
	if !env.OK || string(env.Data) != `{"preview":null}` {
		t.Fatalf("answer data = %s, want {\"preview\":null}", env.Data)
	}
	if raw, okBit, found := answerRow(t, pool, pid, q1); !found || raw != "[0]" || !okBit.Valid || okBit.Int64 != 1 {
		t.Fatalf("stored answer = %q/%v found=%v", raw, okBit, found)
	}

	// --- upsert last-wins, no second row (the new pick is wrong)
	resp, body = answerPost(t, ts, code, st, q1, []int{1})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("re-answer = %d: %s", resp.StatusCode, body)
	}
	if n := countAnswers(t, pool, pid); n != 1 {
		t.Fatalf("answer rows = %d, want 1", n)
	}
	if raw, okBit, _ := answerRow(t, pool, pid, q1); raw != "[1]" || !okBit.Valid || okBit.Int64 != 0 {
		t.Fatalf("after overwrite = %q/%v", raw, okBit)
	}

	// --- validation branches
	if resp, body := answerPost(t, ts, code, st, q1, []int{7}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("out-of-range = %d: %s", resp.StatusCode, body)
	} else if env := decodeEnv(t, body); env.Message != "Pilihan tidak valid." {
		t.Fatalf("out-of-range message = %q", env.Message)
	}
	if resp, body := answerPost(t, ts, code, st, q1, []float64{}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty answer = %d: %s", resp.StatusCode, body)
	} else if env := decodeEnv(t, body); env.Message != "Pilih setidaknya satu pilihan." {
		t.Fatalf("empty message = %q", env.Message)
	}
	if resp, body := answerPost(t, ts, code, st, q1, "nope"); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("string answer = %d: %s", resp.StatusCode, body)
	} else if env := decodeEnv(t, body); env.Message != "Format jawaban tidak valid." {
		t.Fatalf("string message = %q", env.Message)
	}
	if resp, body := answerPost(t, ts, code, st, 999999, []int{0}); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown question = %d: %s", resp.StatusCode, body)
	}
	if resp, body := answerPost(t, ts, code, st, 0, []int{0}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing question = %d: %s", resp.StatusCode, body)
	}
	if n := countAnswers(t, pool, pid); n != 1 {
		t.Fatalf("validation attempts changed row count: %d", n)
	}

	// --- answer q2 correctly
	if resp, body := answerPost(t, ts, code, st, q2, []int{1}); resp.StatusCode != http.StatusOK {
		t.Fatalf("answer q2 = %d: %s", resp.StatusCode, body)
	}
	if raw, okBit, _ := answerRow(t, pool, pid, q2); !okBit.Valid || okBit.Int64 != 1 || raw != "[1]" {
		t.Fatalf("q2 stored = %q/%v", raw, okBit)
	}

	// --- answer AFTER the quiz closed → 410, stored answer untouched
	setQuizStatusSQL(t, pool, quizID, "selesai")
	resp, body = answerPost(t, ts, code, st, q2, []int{0})
	if resp.StatusCode != http.StatusGone {
		t.Fatalf("answer after close = %d, want 410 (body %s)", resp.StatusCode, body)
	}
	if env := decodeEnv(t, body); env.Error != "QUIZ_ENDED" || env.Message != "Kuis ini sudah berakhir." {
		t.Fatalf("close error = %q / %q", env.Error, env.Message)
	}
	if raw, _, _ := answerRow(t, pool, pid, q2); raw != "[1]" {
		t.Fatalf("closed attempt modified stored answer: %q", raw)
	}

	// --- finish: 1 of 2 correct → 50.00, then idempotent
	finish := func() (float64, float64) {
		t.Helper()
		resp, body := postJSON(t, ts.URL+"/quiz/"+code+"/finish", nil, st)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("finish = %d: %s", resp.StatusCode, body)
		}
		env := decodeEnv(t, body)
		var d struct {
			Status     string  `json:"status"`
			ScoreAuto  float64 `json:"score_auto"`
			FinalScore float64 `json:"final_score"`
		}
		if err := json.Unmarshal(env.Data, &d); err != nil {
			t.Fatalf("finish data %q: %v", env.Data, err)
		}
		if d.Status != "selesai" {
			t.Fatalf("finish status = %q", d.Status)
		}
		return d.ScoreAuto, d.FinalScore
	}
	auto, final := finish()
	if auto != 50 || final != 50 {
		t.Fatalf("finish = auto %v final %v, want 50/50", auto, final)
	}
	auto2, final2 := finish()
	if auto2 != auto || final2 != final {
		t.Fatalf("second finish changed scores: %v/%v vs %v/%v", auto2, final2, auto, final)
	}

	// --- answering after finishing → attempt conflict
	if resp, body := answerPost(t, ts, code, st, q1, []int{1}); resp.StatusCode != http.StatusConflict {
		t.Fatalf("answer after finish = %d, want 409 (body %s)", resp.StatusCode, body)
	}

	// --- finished workspace renders the score
	resp, body = getWith(t, ts.URL+"/quiz/"+code, st)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("finished page = %d", resp.StatusCode)
	}
	if !contains(body, `data-state="finished"`) {
		t.Fatalf("finished marker missing: %s", body)
	}
	if !contains(body, "50.00") {
		t.Fatalf("score missing from finished page: %s", body)
	}
}

// --- free navigation (/next) ------------------------------------------------

func TestNextNavigationAndFreeModeGuard(t *testing.T) {
	ts, pool, _, _ := quizFixture(t)
	ck := guruLogin(t, ts)
	st := studentCookie(t, pool, "flow2")

	code, ids := persoalFixture(t, ts, pool, ck, st,
		"Free Navigation", []string{"First", "Second"}, "open")
	q1 := ids[0]
	startAttempt(t, ts, code, st)

	// advance → current_q 2
	resp, body := postJSON(t, ts.URL+"/quiz/"+code+"/next", nil, st)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("next = %d: %s", resp.StatusCode, body)
	}
	env := decodeEnv(t, body)
	var nd struct {
		CurrentQ int `json:"current_q"`
		Total    int `json:"total"`
	}
	if err := json.Unmarshal(env.Data, &nd); err != nil {
		t.Fatalf("next data %q: %v", env.Data, err)
	}
	if nd.CurrentQ != 2 || nd.Total != 2 {
		t.Fatalf("next = %+v", nd)
	}

	// jump back → current_q 1 (persisted server-side)
	resp, body = postJSON(t, ts.URL+"/quiz/"+code+"/next",
		map[string]any{"question_id": q1}, st)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("jump = %d: %s", resp.StatusCode, body)
	}
	if err := json.Unmarshal(decodeEnv(t, body).Data, &nd); err != nil {
		t.Fatalf("jump data: %v", err)
	}
	if nd.CurrentQ != 1 {
		t.Fatalf("jump current_q = %d, want 1", nd.CurrentQ)
	}
	quizID := func() uint64 {
		var id uint64
		if err := pool.QueryRow(`SELECT id FROM quizzes WHERE code = ?`, code).Scan(&id); err != nil {
			t.Fatalf("quiz id: %v", err)
		}
		return id
	}()
	pid, _, _ := participantRowFor(t, pool, quizID, userOf(t, ts, st))
	var storedCurrent int
	if err := pool.QueryRow(`SELECT current_q FROM participants WHERE id = ?`, pid).Scan(&storedCurrent); err != nil {
		t.Fatalf("current_q: %v", err)
	}
	if storedCurrent != 1 {
		t.Fatalf("stored current_q = %d, want 1", storedCurrent)
	}

	// linear quiz → /next refuses (no free navigation)
	gForm := quizForm("Linear Guard")
	globalID := createQuiz(t, ts, ck, gForm)
	q := addQuestion(t, ts, pool, ck, "G", "pg", []string{"One", "Two"}, []int{0})
	compose(t, ts, ck, globalID, q)
	if resp, body := setStatus(t, ts, ck, globalID, "aktif"); resp.StatusCode != http.StatusOK || !decodeEnv(t, body).OK {
		t.Fatalf("activate global = %d: %s", resp.StatusCode, body)
	}
	gCode := joinCode(t, pool, globalID)
	if resp, body := postJoin(t, ts, gCode, st); resp.StatusCode != http.StatusOK {
		t.Fatalf("join global = %d: %s", resp.StatusCode, body)
	}
	resp, body = postJSON(t, ts.URL+"/quiz/"+gCode+"/next", nil, st)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("next on linear = %d, want 409 (body %s)", resp.StatusCode, body)
	}
	if env := decodeEnv(t, body); env.Message != "Kuis ini tidak mengizinkan navigasi bebas." {
		t.Fatalf("linear next message = %q", env.Message)
	}
}

// --- wall clock expiry ------------------------------------------------------

func TestAnswerAfterTimerExpiry(t *testing.T) {
	ts, pool, _, _ := quizFixture(t)
	ck := guruLogin(t, ts)
	st := studentCookie(t, pool, "flow3")

	code, ids := persoalFixture(t, ts, pool, ck, st,
		"Expiry Flow", []string{"Late"}, "open")
	startAttempt(t, ts, code, st)

	// the personal clock ran out while the page was open
	if _, err := pool.Exec(`UPDATE participants SET ends_at = DATE_SUB(NOW(), INTERVAL 1 SECOND)
		WHERE user_id = (SELECT id FROM users WHERE username = 'flow3')`); err != nil {
		t.Fatalf("expire clock: %v", err)
	}
	resp, body := answerPost(t, ts, code, st, ids[0], []int{0})
	if resp.StatusCode != http.StatusGone {
		t.Fatalf("expired answer = %d, want 410 (body %s)", resp.StatusCode, body)
	}
	if env := decodeEnv(t, body); env.Error != "QUIZ_ENDED" || env.Message != "Kuis ini sudah berakhir." {
		t.Fatalf("expired error = %q / %q", env.Error, env.Message)
	}
	quizID := func() uint64 {
		var id uint64
		if err := pool.QueryRow(`SELECT id FROM quizzes WHERE code = ?`, code).Scan(&id); err != nil {
			t.Fatalf("quiz id: %v", err)
		}
		return id
	}()
	pid, _, _ := participantRowFor(t, pool, quizID, userOf(t, ts, st))
	if n := countAnswers(t, pool, pid); n != 0 {
		t.Fatalf("expired answer wrote %d rows", n)
	}

	// the expiry auto-finished the attempt over the FULL count (spec §11:
	// unanswered = wrong) and the rejected answer stored nothing
	var pStatus string
	var finishedAt sql.NullString
	var score sql.NullFloat64
	if err := pool.QueryRow(`SELECT status, finished_at, score_auto FROM participants
		WHERE quiz_id = ? AND user_id = (SELECT id FROM users WHERE username = 'flow3')`, quizID).
		Scan(&pStatus, &finishedAt, &score); err != nil {
		t.Fatalf("participant after expiry: %v", err)
	}
	if pStatus != "selesai" || !finishedAt.Valid {
		t.Fatalf("expired attempt = %s / finished_at %v, want selesai with finished_at", pStatus, finishedAt)
	}
	if !score.Valid || score.Float64 != 0 {
		t.Fatalf("expired score = %v, want 0.00 (0 of 1 answered)", score.Float64)
	}
}

// --- linear mode: preview + monitor dwell clock ----------------------------

func TestLinearAnswerPreviewAndDwell(t *testing.T) {
	ts, pool, _, _ := quizFixture(t)
	ck := guruLogin(t, ts)
	st := studentCookie(t, pool, "flow4")

	gForm := quizForm("Linear Answer")
	quizID := createQuiz(t, ts, ck, gForm)
	q1 := addQuestion(t, ts, pool, ck, "L pick Beta", "pg", []string{"Alpha", "Beta"}, []int{1})
	q2 := addQuestion(t, ts, pool, ck, "L pick Alpha", "pg", []string{"Alpha", "Beta"}, []int{0})
	compose(t, ts, ck, quizID, q1, q2)
	if resp, body := setStatus(t, ts, ck, quizID, "aktif"); resp.StatusCode != http.StatusOK || !decodeEnv(t, body).OK {
		t.Fatalf("activate = %d: %s", resp.StatusCode, body)
	}
	code := joinCode(t, pool, quizID)
	if resp, body := postJoin(t, ts, code, st); resp.StatusCode != http.StatusOK {
		t.Fatalf("join = %d: %s", resp.StatusCode, body)
	}
	_, status, _ := participantRowFor(t, pool, quizID, userOf(t, ts, st))
	if status != "registered" {
		t.Fatalf("global join status = %s", status)
	}

	// fixture: mimic the Step 11 global START (written in one UPDATE)
	order := quizengine.Order{
		Questions: []uint64{q1, q2},
		Options:   map[uint64][]int{},
	}
	raw, err := json.Marshal(order)
	if err != nil {
		t.Fatalf("order JSON: %v", err)
	}
	if _, err := pool.Exec(`UPDATE participants SET status = 'started', qorder = ?,
		started_at = NOW(), ends_at = DATE_ADD(NOW(), INTERVAL 600 SECOND),
		current_q = 1, current_q_since = NOW()
		WHERE quiz_id = ? AND user_id = (SELECT id FROM users WHERE username = 'flow4')`,
		string(raw), quizID); err != nil {
		t.Fatalf("simulate start: %v", err)
	}
	pid, _, _ := participantRowFor(t, pool, quizID, userOf(t, ts, st))

	type previewData struct {
		Preview *struct {
			Correct bool    `json:"correct"`
			Key     float64 `json:"key"`
		} `json:"preview"`
	}
	readPreview := func(t *testing.T, resp *http.Response, body string) *previewData {
		t.Helper()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("linear answer = %d: %s", resp.StatusCode, body)
		}
		env := decodeEnv(t, body)
		var d previewData
		if err := json.Unmarshal(env.Data, &d); err != nil {
			t.Fatalf("answer data %q: %v", env.Data, err)
		}
		return &d
	}

	// correct answer → preview {correct:true, key:1} + dwell clock advances
	resp, body := answerPost(t, ts, code, st, q1, []int{1})
	d := readPreview(t, resp, body)
	if d.Preview == nil || !d.Preview.Correct || d.Preview.Key != 1 {
		t.Fatalf("preview = %+v", d.Preview)
	}
	var cur int
	if err := pool.QueryRow(`SELECT current_q FROM participants WHERE id = ?`, pid).Scan(&cur); err != nil {
		t.Fatalf("current_q: %v", err)
	}
	if cur != 2 {
		t.Fatalf("dwell clock current_q = %d, want 2", cur)
	}

	// wrong answer → preview {correct:false, key:0} + last question stays put
	resp, body = answerPost(t, ts, code, st, q2, []int{1})
	d = readPreview(t, resp, body)
	if d.Preview == nil || d.Preview.Correct || d.Preview.Key != 0 {
		t.Fatalf("preview = %+v", d.Preview)
	}
	if err := pool.QueryRow(`SELECT current_q FROM participants WHERE id = ?`, pid).Scan(&cur); err != nil {
		t.Fatalf("current_q: %v", err)
	}
	if cur != 2 {
		t.Fatalf("current_q past the end = %d, want 2", cur)
	}
}

// --- essay answers ----------------------------------------------------------

func TestEssayAnswerContract(t *testing.T) {
	ts, pool, _, _ := quizFixture(t)
	ck := guruLogin(t, ts)
	st := studentCookie(t, pool, "flow5")

	form := quizForm("Essay Flow")
	form.Set("timer_type", "per_soal")
	form.Set("per_question_seconds", "60")
	quizID := createQuiz(t, ts, ck, form)
	essayID := addQuestion(t, ts, pool, ck, "Explain it.", "essay", nil, nil)
	compose(t, ts, ck, quizID, essayID)
	if resp, body := setStatus(t, ts, ck, quizID, "aktif"); resp.StatusCode != http.StatusOK || !decodeEnv(t, body).OK {
		t.Fatalf("activate = %d: %s", resp.StatusCode, body)
	}
	code := joinCode(t, pool, quizID)
	if resp, body := postJoin(t, ts, code, st); resp.StatusCode != http.StatusOK {
		t.Fatalf("join = %d: %s", resp.StatusCode, body)
	}
	startAttempt(t, ts, code, st)

	// empty text → 400
	resp, body := answerPost(t, ts, code, st, essayID, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty essay = %d: %s", resp.StatusCode, body)
	}
	if env := decodeEnv(t, body); env.Message != "Jawaban wajib diisi." {
		t.Fatalf("empty essay message = %q", env.Message)
	}
	// wrong encoding → 400
	if resp, body := answerPost(t, ts, code, st, essayID, []int{1}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("array essay = %d: %s", resp.StatusCode, body)
	}
	// real text → stored, never auto-graded (is_correct NULL)
	resp, body = answerPost(t, ts, code, st, essayID, "My long answer.")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("essay = %d: %s", resp.StatusCode, body)
	}
	if env := decodeEnv(t, body); string(env.Data) != `{"preview":null}` {
		t.Fatalf("essay data = %s", env.Data)
	}
	var raw string
	var okBit sql.NullInt64
	if err := pool.QueryRow(`SELECT answer, is_correct FROM answers
		WHERE participant_id = (SELECT id FROM participants WHERE quiz_id = ? AND attempt_no = 1)
		AND question_id = ?`, quizID, essayID).Scan(&raw, &okBit); err != nil {
		t.Fatalf("essay row: %v", err)
	}
	if raw != `"My long answer."` {
		t.Fatalf("stored essay = %q", raw)
	}
	if okBit.Valid {
		t.Fatalf("essay is_correct = %v, want NULL", okBit.Int64)
	}
}

// --- anti-cheat visibility reporting ---------------------------------------

func TestVisibilityReporting(t *testing.T) {
	ts, pool, _, _ := quizFixture(t)
	ck := guruLogin(t, ts)
	st := studentCookie(t, pool, "flow6")

	code, _ := persoalFixture(t, ts, pool, ck, st,
		"Visibility Flow", []string{"One question"}, "open")

	report := func(kind string) (*http.Response, string) {
		t.Helper()
		return postJSON(t, ts.URL+"/quiz/"+code+"/visibility",
			map[string]string{"kind": kind}, st)
	}

	resp, body := report("blur")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("visibility = %d: %s", resp.StatusCode, body)
	}
	if env := decodeEnv(t, body); string(env.Data) != `{"recorded":true}` {
		t.Fatalf("first report = %s", env.Data)
	}

	// same kind within the 10 s collapse window → dropped
	resp, body = report("blur")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("visibility 2 = %d: %s", resp.StatusCode, body)
	}
	if env := decodeEnv(t, body); string(env.Data) != `{"recorded":false}` {
		t.Fatalf("collapsed report = %s", env.Data)
	}

	// unknown kind → 400
	resp, body = report("bounce")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad kind = %d: %s", resp.StatusCode, body)
	}
	if env := decodeEnv(t, body); env.Message != "Jenis peristiwa tidak dikenal." {
		t.Fatalf("bad kind message = %q", env.Message)
	}
}

// --- close with modal + attempt limit (spec §6.5, §11) ---------------------

// TestCloseWithWorkingModal pins the per-question close: teacher START is
// refused (never berjalan), the unconfirmed close returns {working:N} for
// the modal, the confirmed close scores working students, parks registered
// ones at final_score=NULL outside the ranker, and lands on selesai once.
func TestCloseWithWorkingModal(t *testing.T) {
	ts, globals, pool := globalFixture(t)
	ck := guruLogin(t, ts)

	form := quizForm("Close With Modal")
	form.Set("timer_type", "per_soal")
	form.Set("per_question_seconds", "60")
	quizID := createQuiz(t, ts, ck, form)
	q1 := addQuestion(t, ts, pool, ck, "CQ one", "pg", []string{"One", "Two"}, []int{0})
	q2 := addQuestion(t, ts, pool, ck, "CQ two", "pg", []string{"One", "Two"}, []int{0})
	compose(t, ts, ck, quizID, q1, q2)
	if resp, body := setStatus(t, ts, ck, quizID, "aktif"); resp.StatusCode != http.StatusOK || !decodeEnv(t, body).OK {
		t.Fatalf("activate = %d: %s", resp.StatusCode, body)
	}

	// per-question quizzes never enter berjalan: teacher START refuses and
	// the quiz stays aktif (spec §6.5)
	if resp, body := postJSON(t, fmt.Sprintf("%s/teacher/quiz/%d/start", ts.URL, quizID), nil, ck); resp.StatusCode != http.StatusConflict {
		t.Fatalf("teacher start per-soal = %d, want 409: %s", resp.StatusCode, body)
	}
	if got := quizStatus(t, pool, quizID); got != "aktif" {
		t.Fatalf("quiz status after refused start = %s", got)
	}

	code := joinCode(t, pool, quizID)
	stA := studentCookie(t, pool, "close-a")
	stB := studentCookie(t, pool, "close-b")
	stC := studentCookie(t, pool, "close-c")
	for i, st := range []*http.Cookie{stA, stB, stC} {
		if resp, body := postJoin(t, ts, code, st); resp.StatusCode != http.StatusOK {
			t.Fatalf("join %d = %d: %s", i, resp.StatusCode, body)
		}
	}
	startAttempt(t, ts, code, stA)
	startAttempt(t, ts, code, stB) // C stays registered, never started

	statusURL := fmt.Sprintf("%s/teacher/quiz/%d/status", ts.URL, quizID)

	// unconfirmed close → 409 {"working":2} so the client can show the modal
	resp, body := postJSON(t, statusURL, map[string]any{"status": "selesai"}, ck)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("unconfirmed close = %d: %s", resp.StatusCode, body)
	}
	env := decodeEnv(t, body)
	if env.Message != "2 murid masih mengerjakan — tetap tutup?" {
		t.Fatalf("modal message = %q", env.Message)
	}
	var wd struct {
		Working int `json:"working"`
	}
	if err := json.Unmarshal(env.Data, &wd); err != nil || wd.Working != 2 {
		t.Fatalf("working data = %s (%v)", env.Data, err)
	}

	// confirmed close → 200 and everyone lands in their final state
	resp, body = postJSON(t, statusURL, map[string]any{"status": "selesai", "confirm": true}, ck)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("confirmed close = %d: %s", resp.StatusCode, body)
	}
	if env := decodeEnv(t, body); string(env.Data) != `{"status":"selesai"}` {
		t.Fatalf("close data = %s", env.Data)
	}

	// the quiz is selesai — close never writes nonaktif (spec §6.5)
	if got := quizStatus(t, pool, quizID); got != "selesai" {
		t.Fatalf("quiz status = %s, want selesai (never nonaktif)", got)
	}

	scoreOf := func(st *http.Cookie) (string, sql.NullFloat64, sql.NullString) {
		t.Helper()
		var status string
		var score sql.NullFloat64
		var finished sql.NullString
		if err := pool.QueryRow(`SELECT status, score_auto, finished_at FROM participants
			WHERE quiz_id = ? AND user_id = ?`, quizID, userOf(t, ts, st)).
			Scan(&status, &score, &finished); err != nil {
			t.Fatalf("participant read: %v", err)
		}
		return status, score, finished
	}
	for i, st := range []*http.Cookie{stA, stB} {
		status, score, finished := scoreOf(st)
		if status != "selesai" || !finished.Valid {
			t.Fatalf("working %d after close = %s / finished_at %v", i, status, finished)
		}
		if !score.Valid || score.Float64 != 0 {
			t.Fatalf("working %d score = %v, want 0.00 over the full count", i, score.Float64)
		}
	}
	statusC, scoreC, _ := scoreOf(stC)
	if statusC != "selesai" || scoreC.Valid {
		t.Fatalf("registered-not-started = %s / score %v, want selesai with NULL", statusC, scoreC)
	}

	// the registered-not-started student never entered the ranker (the
	// Step 12 protocol check: only started rows are ever upserted)
	pidOf := func(st *http.Cookie) uint64 {
		t.Helper()
		pid, _, _ := participantRowFor(t, pool, quizID, userOf(t, ts, st))
		return pid
	}
	pidA, pidC := pidOf(stA), pidOf(stC)
	var seenA, seenC bool
	for _, e := range globals.Live.Ranker(quizID).Snapshot() {
		if e.ParticipantID == pidA {
			seenA = true
			if !e.Finished || e.Score != 0 {
				t.Fatalf("ranker A entry = %+v", e)
			}
		}
		if e.ParticipantID == pidC {
			seenC = true
		}
	}
	if !seenA || seenC {
		t.Fatalf("ranker: A present=%v, registered C present=%v (C must stay out)", seenA, seenC)
	}

	// closing again → 409: the quiz moves to selesai exactly once
	resp, body = postJSON(t, statusURL, map[string]any{"status": "selesai", "confirm": true}, ck)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("second close = %d, want 409: %s", resp.StatusCode, body)
	}
	if env := decodeEnv(t, body); env.Message != "Kuis ini tidak sedang berjalan." {
		t.Fatalf("second close message = %q", env.Message)
	}
	if got := quizStatus(t, pool, quizID); got != "selesai" {
		t.Fatalf("quiz status after second close = %s", got)
	}
}

// TestAttemptLimitThroughFinish enforces the attempt cap through the real
// flow: finish attempt 1, rejoin → 409 ATTEMPT_LIMIT (spec §11).
func TestAttemptLimitThroughFinish(t *testing.T) {
	ts, _, pool := globalFixture(t)
	ck := guruLogin(t, ts)
	st := studentCookie(t, pool, "attempt-limit")

	form := quizForm("Attempt Limit")
	form.Set("timer_type", "per_soal")
	form.Set("per_question_seconds", "60")
	form.Set("max_attempts", "1")
	quizID := createQuiz(t, ts, ck, form)
	q := addQuestion(t, ts, pool, ck, "AL question", "pg", []string{"One", "Two"}, []int{0})
	compose(t, ts, ck, quizID, q)
	if resp, body := setStatus(t, ts, ck, quizID, "aktif"); resp.StatusCode != http.StatusOK || !decodeEnv(t, body).OK {
		t.Fatalf("activate = %d: %s", resp.StatusCode, body)
	}
	code := joinCode(t, pool, quizID)
	if resp, body := postJoin(t, ts, code, st); resp.StatusCode != http.StatusOK {
		t.Fatalf("join = %d: %s", resp.StatusCode, body)
	}
	startAttempt(t, ts, code, st)
	if resp, body := postJSON(t, ts.URL+"/quiz/"+code+"/finish", nil, st); resp.StatusCode != http.StatusOK {
		t.Fatalf("finish = %d: %s", resp.StatusCode, body)
	}

	resp, body := postJoin(t, ts, code, st)
	assertFail(t, resp, body, http.StatusConflict, handlers.ErrAttemptLimit, "Anda tidak punya sisa percobaan.")
}

// TestPerQuestionDisconnectFreezeAndResume pins spec §11.16 for the
// per-question timer: freeze at the last heartbeat, resume with no time
// lost (the paused remainder comes back on reconnect).
func TestPerQuestionDisconnectFreezeAndResume(t *testing.T) {
	ts, globals, pool := globalFixture(t)
	ck := guruLogin(t, ts)

	form := quizForm("PerQ Freeze")
	form.Set("timer_type", "per_soal")
	form.Set("per_question_seconds", "60")
	quizID := createQuiz(t, ts, ck, form)
	q1 := addQuestion(t, ts, pool, ck, "FZ one", "pg", []string{"One", "Two"}, []int{0})
	q2 := addQuestion(t, ts, pool, ck, "FZ two", "pg", []string{"One", "Two"}, []int{0})
	compose(t, ts, ck, quizID, q1, q2)
	if resp, body := setStatus(t, ts, ck, quizID, "aktif"); resp.StatusCode != http.StatusOK || !decodeEnv(t, body).OK {
		t.Fatalf("activate = %d: %s", resp.StatusCode, body)
	}
	st := studentCookie(t, pool, "fz-student")
	code := joinCode(t, pool, quizID)
	if resp, body := postJoin(t, ts, code, st); resp.StatusCode != http.StatusOK {
		t.Fatalf("join = %d: %s", resp.StatusCode, body)
	}
	startAttempt(t, ts, code, st)
	pid, _, _ := participantRowFor(t, pool, quizID, userOf(t, ts, st))
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

	// connect (greeting write = heartbeat), read it, hang up
	openedAt := time.Now()
	conn := openSSE(t, ts.URL+"/quiz/"+code+"/stream", st)
	if ev, _, _, ok := conn.readEvent(3 * time.Second); !ok || ev != "snapshot" {
		t.Fatalf("greeting = %q ok=%v, want snapshot", ev, ok)
	}
	hangupAt := time.Now()
	conn.resp.Body.Close()
	time.Sleep(600 * time.Millisecond) // > silence

	deadline := time.Now().Add(3 * time.Second)
	for endsAt().After(origEnds.Add(-time.Second)) {
		if err := globals.WatchOnce(context.Background()); err != nil {
			t.Fatalf("WatchOnce: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("clock never froze (ends_at still %s)", endsAt())
		}
		time.Sleep(100 * time.Millisecond)
	}
	frozen := endsAt()
	if frozen.After(hangupAt.Add(200 * time.Millisecond)) {
		t.Fatalf("frozen at %s — detection-time freeze, not last heartbeat (hangup %s)", frozen, hangupAt)
	}
	if frozen.Before(openedAt.Add(-2 * time.Second)) {
		t.Fatalf("frozen ends_at %s precedes the connection %s", frozen, openedAt)
	}

	// reconnect → the remainder is handed back: no time lost (≈ orig) or gained
	conn2 := openSSE(t, ts.URL+"/quiz/"+code+"/stream", st)
	if ev, _, _, ok := conn2.readEvent(3 * time.Second); !ok || ev != "snapshot" {
		t.Fatalf("reconnect greeting = %q ok=%v", ev, ok)
	}
	after := endsAt()
	if !after.After(frozen) {
		t.Fatalf("resume did not extend the clock: frozen=%s resumed=%s", frozen, after)
	}
	if after.Before(origEnds.Add(-5*time.Second)) || after.After(origEnds.Add(5*time.Second)) {
		t.Fatalf("time lost/gained: original=%s resumed=%s (frozen=%s)", origEnds, after, frozen)
	}
	if _, status, _ := participantRowFor(t, pool, quizID, userOf(t, ts, st)); status != "started" {
		t.Fatalf("status after resume = %s", status)
	}
}
