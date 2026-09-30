package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// monitorCardData mirrors one entry of the monitor-data JSON blob (the same
// monitorCard json the SSE `page`/`snapshot` events carry).
type monitorCardData struct {
	ParticipantID uint64 `json:"participant_id"`
	Status        string `json:"status"`
	CurrentQ      int    `json:"current_q"`
	Page          string `json:"page"`
	Cheating      bool   `json:"cheating"`
	Violations    int    `json:"violations"`
	Spent         []struct {
		Q   int   `json:"q"`
		Sec int64 `json:"sec"`
	} `json:"spent"`
}

type monitorBlob struct {
	Participants []monitorCardData `json:"participants"`
}

// pageEvent is the decoded frame of an SSE `page` event (spec §6.3/§7).
type pageEvent struct {
	Type string `json:"type"`
	Data struct {
		ParticipantID uint64 `json:"participant_id"`
		Name          string `json:"name"`
		Status        string `json:"status"`
		Page          string `json:"page"`
		CurrentQ      int    `json:"current_q"`
		Connected     bool   `json:"connected"`
	} `json:"data"`
}

// readPageEvent skips other events (greeting/heartbeat/rank) until a
// `page` event arrives; fails when the stream ends first.
func readPageEvent(t *testing.T, conn *sseConn) pageEvent {
	t.Helper()
	for range 5 {
		ev, data, _, ok := conn.readEvent(3 * time.Second)
		if !ok {
			break
		}
		if ev != "page" {
			continue
		}
		var frame pageEvent
		if err := json.Unmarshal([]byte(data), &frame); err != nil {
			t.Fatalf("page event JSON: %v (%s)", err, data)
		}
		if frame.Type != "page" {
			t.Fatalf("page frame type = %q, want page", frame.Type)
		}
		return frame
	}
	t.Fatal("no `page` event reached the monitor stream")
	return pageEvent{}
}

// monitorBlob decodes the id="monitor-data" JSON blob out of a rendered
// monitor page (spec §7 wire shape, independent of card markup).
func monitorBlobOf(t *testing.T, body string) monitorBlob {
	t.Helper()
	const openTag = `<script type="application/json" id="monitor-data">`
	_, rest, ok := strings.Cut(body, openTag)
	if !ok {
		t.Fatalf("monitor-data blob missing from monitor page")
	}
	payload, _, ok := strings.Cut(rest, "</script>")
	if !ok {
		t.Fatalf("monitor-data blob not closed")
	}
	var blob monitorBlob
	if err := json.Unmarshal([]byte(payload), &blob); err != nil {
		t.Fatalf("monitor-data JSON: %v (%s)", err, payload)
	}
	return blob
}

func monitorCardOf(b monitorBlob, pid uint64) *monitorCardData {
	for i := range b.Participants {
		if b.Participants[i].ParticipantID == pid {
			return &b.Participants[i]
		}
	}
	return nil
}

// The workspace page beacon (spec §6.7b/§7) drives the live monitor: start →
// preview → question, the per-question time ledger, and the entry leaving
// the cards on submit. The `page` SSE event must carry the move live.
func TestWorkspacePageBeaconTracksMonitor(t *testing.T) {
	ts, pool, _, _ := quizFixture(t)
	ck := guruLogin(t, ts)
	st := studentCookie(t, pool, "track1")
	reg := studentCookie(t, pool, "track2")
	stranger := studentCookie(t, pool, "track3")

	code, _ := persoalFixture(t, ts, pool, ck, st,
		"Page Tracking", []string{"Track one", "Track two"}, "open")
	quizID := uint64(0)
	if err := pool.QueryRow(`SELECT id FROM quizzes WHERE code = ?`, code).Scan(&quizID); err != nil {
		t.Fatalf("quiz id: %v", err)
	}

	// registered but never started: the beacon must not accept it yet
	if resp, body := postJoin(t, ts, code, reg); resp.StatusCode != http.StatusOK {
		t.Fatalf("join reg = %d: %s", resp.StatusCode, body)
	}
	resp, body := postJSON(t, ts.URL+"/quiz/"+code+"/page",
		map[string]string{"page": "preview"}, reg)
	assertFail(t, resp, body, http.StatusConflict, "CONFLICT", "Mulai kuis terlebih dahulu.")

	// never joined at all → 404
	resp, body = postJSON(t, ts.URL+"/quiz/"+code+"/page",
		map[string]string{"page": "preview"}, stranger)
	assertFail(t, resp, body, http.StatusNotFound, "NOT_FOUND",
		"Anda bukan peserta kuis ini.")

	startAttempt(t, ts, code, st)
	pid1, _, _ := participantRowFor(t, pool, quizID, userOf(t, ts, st))
	pid2, _, _ := participantRowFor(t, pool, quizID, userOf(t, ts, reg))

	// live monitor stream: greeting first, then the preview move arrives
	conn := openSSE(t, ts.URL+fmt.Sprintf("/teacher/quiz/%d/monitor/stream", quizID), ck)
	defer conn.resp.Body.Close()
	if ev, _, _, ok := conn.readEvent(3 * time.Second); !ok || ev != "snapshot" {
		t.Fatalf("monitor greeting = %q (ok=%v), want snapshot", ev, ok)
	}

	// preview beacon → 200 + the `page` event on the teacher topic
	resp, body = postJSON(t, ts.URL+"/quiz/"+code+"/page",
		map[string]string{"page": "preview"}, st)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("page preview = %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, `"page":"preview"`) {
		t.Fatalf("page preview envelope = %s", body)
	}
	// readEvent hands back the whole event frame {type, data}
	frame := readPageEvent(t, conn)
	if frame.Data.Page != "preview" || frame.Data.ParticipantID != pid1 {
		t.Errorf("page event = %+v, want page=preview for pid %d", frame.Data, pid1)
	}
	if frame.Data.Status != "started" {
		t.Errorf("page event status = %q, want started", frame.Data.Status)
	}
	if frame.Data.CurrentQ != 1 {
		t.Errorf("page event current_q = %d, want 1", frame.Data.CurrentQ)
	}

	monitorGet := func() (monitorBlob, string) {
		t.Helper()
		mResp, mBody := getWith(t,
			fmt.Sprintf("%s/teacher/quiz/%d/monitor", ts.URL, quizID), ck)
		if mResp.StatusCode != http.StatusOK {
			t.Fatalf("monitor = %d: %s", mResp.StatusCode, mBody)
		}
		return monitorBlobOf(t, mBody), mBody
	}

	blob, _ := monitorGet()
	if card := monitorCardOf(blob, pid1); card == nil || card.Page != "preview" {
		t.Fatalf("started card after preview beacon = %+v, want page=preview", card)
	}
	if card := monitorCardOf(blob, pid2); card == nil || card.Status != "registered" {
		t.Errorf("registered card = %+v, want status=registered", card)
	}

	// back to the question page
	resp, body = postJSON(t, ts.URL+"/quiz/"+code+"/page",
		map[string]string{"page": "question"}, st)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("page question = %d: %s", resp.StatusCode, body)
	}
	blob, _ = monitorGet()
	if card := monitorCardOf(blob, pid1); card == nil || card.Page != "question" {
		t.Fatalf("card after back-beacon = %+v, want page=question", card)
	}

	// unknown page name → 400 VALIDATION
	resp, body = postJSON(t, ts.URL+"/quiz/"+code+"/page",
		map[string]string{"page": "sideways"}, st)
	assertFail(t, resp, body, http.StatusBadRequest, "VALIDATION", "Halaman tidak dikenal.")

	// free navigation: the left question enters the per-question ledger and
	// the monitor follows current_q live (per_soal timer mode, spec §7)
	resp, body = postJSON(t, ts.URL+"/quiz/"+code+"/next", nil, st)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("next = %d: %s", resp.StatusCode, body)
	}
	blob, _ = monitorGet()
	card := monitorCardOf(blob, pid1)
	if card == nil || card.CurrentQ != 2 {
		t.Fatalf("card after next = %+v, want current_q=2", card)
	}
	if len(card.Spent) != 1 || card.Spent[0].Q != 1 {
		t.Fatalf("spent ledger = %+v, want exactly the left question 1", card.Spent)
	}

	// submit → the entry leaves the cards; a still-waiting murid remains
	resp, body = postJSON(t, ts.URL+"/quiz/"+code+"/finish", nil, st)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("finish = %d: %s", resp.StatusCode, body)
	}
	blob, rawBody := monitorGet()
	if monitorCardOf(blob, pid1) != nil {
		t.Errorf("submitted participant still in monitor cards: %s", rawBody)
	}
	if monitorCardOf(blob, pid2) == nil {
		t.Errorf("registered participant vanished from monitor cards: %s", rawBody)
	}
}

// Spec §6.9/§7: a detected (violation) or flagged (cheating) student gets a
// yellow card with its badge and BOTH action buttons; clean cards get none.
func TestMonitorYellowCheatingCardsAndActions(t *testing.T) {
	ts, pool, _, _ := quizFixture(t)
	ck := guruLogin(t, ts)
	stA := studentCookie(t, pool, "cheat-a")
	stB := studentCookie(t, pool, "cheat-b")
	stC := studentCookie(t, pool, "cheat-c")

	code, _ := persoalFixture(t, ts, pool, ck, stA,
		"Cheating Cards", []string{"Only question"}, "open")
	quizID := uint64(0)
	if err := pool.QueryRow(`SELECT id FROM quizzes WHERE code = ?`, code).Scan(&quizID); err != nil {
		t.Fatalf("quiz id: %v", err)
	}
	for _, s := range []*http.Cookie{stB, stC} {
		if resp, body := postJoin(t, ts, code, s); resp.StatusCode != http.StatusOK {
			t.Fatalf("join = %d: %s", resp.StatusCode, body)
		}
	}
	for _, s := range []*http.Cookie{stA, stB, stC} {
		startAttempt(t, ts, code, s)
	}

	pidA, _, _ := participantRowFor(t, pool, quizID, userOf(t, ts, stA))
	pidB, _, _ := participantRowFor(t, pool, quizID, userOf(t, ts, stB))
	pidC, _, _ := participantRowFor(t, pool, quizID, userOf(t, ts, stC))

	// A was DETECTED (one blur), B was FLAGGED by the teacher, C is clean
	if _, err := pool.Exec(`INSERT INTO anti_cheat_events (participant_id, kind) VALUES (?, 'blur')`,
		pidA); err != nil {
		t.Fatalf("seed violation: %v", err)
	}
	if _, err := pool.Exec(`UPDATE participants SET cheating = 1 WHERE id = ?`, pidB); err != nil {
		t.Fatalf("seed flag: %v", err)
	}

	resp, body := getWith(t, fmt.Sprintf("%s/teacher/quiz/%d/monitor", ts.URL, quizID), ck)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("monitor = %d: %s", resp.StatusCode, body)
	}

	// exactly the two problem cards are yellow and expose both actions
	if n := strings.Count(body, "bg-warning-subtle"); n != 2 {
		t.Errorf("yellow cards = %d, want 2\n%s", n, body)
	}
	if n := strings.Count(body, `data-act="cheat_toggle"`); n != 2 {
		t.Errorf("tandai-curang buttons = %d, want 2 (only cheating cards)", n)
	}
	if n := strings.Count(body, `data-act="remove"`); n != 2 {
		t.Errorf("keluarkan-dari-quiz buttons = %d, want 2 (only cheating cards)", n)
	}
	if !strings.Contains(body, `badge text-bg-danger">Ditandai<`) {
		t.Error("flagged badge missing for the flagged student")
	}
	if !strings.Contains(body, `badge text-bg-danger">1 pelanggaran<`) {
		t.Error("violation badge missing for the detected student")
	}

	blob := monitorBlobOf(t, body)
	a, b, c := monitorCardOf(blob, pidA), monitorCardOf(blob, pidB), monitorCardOf(blob, pidC)
	if a == nil || a.Violations != 1 || a.Cheating {
		t.Errorf("detected card = %+v, want violations=1, cheating=false", a)
	}
	if b == nil || !b.Cheating {
		t.Errorf("flagged card = %+v, want cheating=true", b)
	}
	if c == nil || c.Cheating || c.Violations != 0 {
		t.Errorf("clean card = %+v, want no indication", c)
	}
}

// Every timer mode must put the murid on the live monitor the moment they
// begin (spec §6.3/§7): no-timer and per-question announce through the
// attempt start; global announces when the workspace renders right after
// the teacher's START. The `page` event always carries name + page.
func TestStartAnnouncesNameAndPageInEveryTimerMode(t *testing.T) {
	ts, pool, _, _ := quizFixture(t)
	ck := guruLogin(t, ts)

	mkQuiz := func(t *testing.T, title, timerType string) (uint64, string) {
		t.Helper()
		form := quizForm(title)
		form.Set("timer_type", timerType)
		quizID := createQuiz(t, ts, ck, form)
		q1 := addQuestion(t, ts, pool, ck, title+" q1", "pg", []string{"A", "B"}, []int{0})
		q2 := addQuestion(t, ts, pool, ck, title+" q2", "pg", []string{"A", "B"}, []int{1})
		compose(t, ts, ck, quizID, q1, q2)
		if resp, body := setStatus(t, ts, ck, quizID, "aktif"); resp.StatusCode != http.StatusOK {
			t.Fatalf("activate = %d: %s", resp.StatusCode, body)
		}
		return quizID, joinCode(t, pool, quizID)
	}
	assertStartFrame := func(t *testing.T, frame pageEvent, pid uint64) {
		t.Helper()
		if frame.Data.ParticipantID != pid || frame.Data.Status != "started" ||
			frame.Data.Page != "question" || frame.Data.CurrentQ != 1 {
			t.Errorf("start page event = %+v, want pid %d started / question / 1",
				frame.Data, pid)
		}
		if frame.Data.Name == "" {
			t.Error("start page event carries no name — the monitor cannot show the murid")
		}
	}
	beginStream := func(t *testing.T, quizID uint64) *sseConn {
		t.Helper()
		conn := openSSE(t, ts.URL+fmt.Sprintf("/teacher/quiz/%d/monitor/stream", quizID), ck)
		if ev, _, _, ok := conn.readEvent(3 * time.Second); !ok || ev != "snapshot" {
			t.Fatalf("monitor greeting = %q (ok=%v), want snapshot", ev, ok)
		}
		return conn
	}

	t.Run("no-timer", func(t *testing.T) {
		st := studentCookie(t, pool, "live-notimer")
		quizID, code := mkQuiz(t, "Live NoTimer", "tanpa_timer")
		if resp, body := postJoin(t, ts, code, st); resp.StatusCode != http.StatusOK {
			t.Fatalf("join = %d: %s", resp.StatusCode, body)
		}
		conn := beginStream(t, quizID)
		defer conn.resp.Body.Close()
		startAttempt(t, ts, code, st)
		pid, _, _ := participantRowFor(t, pool, quizID, userOf(t, ts, st))
		assertStartFrame(t, readPageEvent(t, conn), pid)
	})

	t.Run("per-question", func(t *testing.T) {
		st := studentCookie(t, pool, "live-persoal")
		quizID, code := mkQuiz(t, "Live PerQuestion", "per_soal")
		if resp, body := postJoin(t, ts, code, st); resp.StatusCode != http.StatusOK {
			t.Fatalf("join = %d: %s", resp.StatusCode, body)
		}
		conn := beginStream(t, quizID)
		defer conn.resp.Body.Close()
		startAttempt(t, ts, code, st)
		pid, _, _ := participantRowFor(t, pool, quizID, userOf(t, ts, st))
		assertStartFrame(t, readPageEvent(t, conn), pid)
	})

	t.Run("global", func(t *testing.T) {
		st := studentCookie(t, pool, "live-global")
		quizID, code := mkQuiz(t, "Live Global", "global")
		if resp, body := postJoin(t, ts, code, st); resp.StatusCode != http.StatusOK {
			t.Fatalf("join = %d: %s", resp.StatusCode, body)
		}
		conn := beginStream(t, quizID)
		defer conn.resp.Body.Close()
		startGlobal(t, ts, ck, quizID)
		// the student's tab reloads on the `start` event → the workspace
		// render is what announces name + page for the global mode
		if resp, body := getWith(t, ts.URL+"/quiz/"+code, st); resp.StatusCode != http.StatusOK {
			t.Fatalf("workspace = %d: %s", resp.StatusCode, body)
		}
		pid, _, _ := participantRowFor(t, pool, quizID, userOf(t, ts, st))
		assertStartFrame(t, readPageEvent(t, conn), pid)
	})
}

// The started workspace must expose exactly two per-question nav buttons
// (Previous + Next), no question-level submit or save button, and the
// pre-submit preview panel with its green/red grid slot + submit action
// (spec §6.7b).
func TestWorkspaceTwoNavButtonsAndPreviewPanel(t *testing.T) {
	ts, pool, _, _ := quizFixture(t)
	ck := guruLogin(t, ts)
	st := studentCookie(t, pool, "preview-nav")

	code, _ := persoalFixture(t, ts, pool, ck, st,
		"Preview Nav", []string{"Nav one", "Nav two"}, "open")
	startAttempt(t, ts, code, st)

	resp, body := getWith(t, ts.URL+"/quiz/"+code, st)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("workspace = %d: %s", resp.StatusCode, body)
	}
	for _, gone := range []string{`id="btn-finish"`, "data-save"} {
		if strings.Contains(body, gone) {
			t.Errorf("workspace must not carry %s any more", gone)
		}
	}
	for _, want := range []string{
		`id="btn-prev"`, `id="btn-next"`, `id="ws-pager"`,
		`id="ws-preview"`, `id="preview-grid"`,
		`id="btn-preview-back"`, `id="btn-preview-submit"`,
		"Hijau = terjawab", // the green/red mapping hint
	} {
		if !strings.Contains(body, want) {
			t.Errorf("workspace missing %q", want)
		}
	}
	if n := strings.Count(body, `id="btn-prev"`); n != 1 {
		t.Errorf("Previous buttons = %d, want exactly 1", n)
	}
	if n := strings.Count(body, `id="btn-next"`); n != 1 {
		t.Errorf("Next buttons = %d, want exactly 1", n)
	}
	// the preview panel owns the ONLY submit control on the page
	if n := strings.Count(body, "Kirim kuis"); n != 1 {
		t.Errorf("Kirim kuis controls = %d, want exactly 1 (preview panel)", n)
	}
}

// Refresh / leave-and-return (spec §6.3): the workspace announces only
// CHANGED pages, so a reconnect that keeps the same page is invisible on
// its own — the murid's stream lifecycle must repaint the monitor instead.
// Opening the student stream pushes the participant's card (name + connected
// state) to the teacher topic; closing it flips connected to false while the
// attempt stays open. This is what keeps the live monitor truthful when a
// murid exits the quiz page and comes back in every timer mode.
func TestStudentStreamLifecycleRepaintsMonitor(t *testing.T) {
	ts, pool, _, _ := quizFixture(t)
	ck := guruLogin(t, ts)
	st := studentCookie(t, pool, "presence-murid")

	code, _ := persoalFixture(t, ts, pool, ck, st,
		"Presence", []string{"One", "Two"}, "open")
	quizID := uint64(0)
	if err := pool.QueryRow(`SELECT id FROM quizzes WHERE code = ?`, code).Scan(&quizID); err != nil {
		t.Fatalf("quiz id: %v", err)
	}
	startAttempt(t, ts, code, st)
	pid, _, _ := participantRowFor(t, pool, quizID, userOf(t, ts, st))

	monitor := openSSE(t, ts.URL+fmt.Sprintf("/teacher/quiz/%d/monitor/stream", quizID), ck)
	defer monitor.resp.Body.Close()
	if ev, _, _, ok := monitor.readEvent(3 * time.Second); !ok || ev != "snapshot" {
		t.Fatalf("monitor greeting = %q (ok=%v), want snapshot", ev, ok)
	}

	// the murid (re)opens their stream — a refresh, or leaving the quiz
	// page and coming back: the monitor learns they are here even though
	// no page actually changed
	student := openSSE(t, ts.URL+"/quiz/"+code+"/stream", st)
	if ev, _, _, ok := student.readEvent(3 * time.Second); !ok || ev != "snapshot" {
		t.Fatalf("student greeting = %q (ok=%v), want snapshot", ev, ok)
	}
	frame := readPageEvent(t, monitor)
	if frame.Data.ParticipantID != pid || frame.Data.Status != "started" {
		t.Errorf("presence event = %+v, want pid %d status started", frame.Data, pid)
	}
	if frame.Data.Name == "" {
		t.Error("presence event carries no name — the monitor cannot show the murid")
	}
	if !frame.Data.Connected {
		t.Error("presence on connect = connected false, want true")
	}

	// closing the stream (tab closed / navigation away) flips the badge —
	// the teacher now SEES the murid leave instead of a stale "connected"
	student.resp.Body.Close()
	deadline := time.Now().Add(4 * time.Second)
	for {
		frame = readPageEvent(t, monitor)
		if frame.Data.ParticipantID != pid {
			continue
		}
		if !frame.Data.Connected {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("stream close never reached the monitor (connected stayed true)")
		}
	}
}
