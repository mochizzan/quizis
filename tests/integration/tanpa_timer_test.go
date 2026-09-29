package integration

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// --- creation: timer_on derives from timer_type ----------------------------

// TestCreateTanpaTimerQuiz proves a no-timer quiz needs no timer seconds at
// all: the form may even carry a stray timer_on=1, yet the stored row must
// come out timer_on=0 with both timer seconds zeroed.
func TestCreateTanpaTimerQuiz(t *testing.T) {
	ts, pool, _, _ := quizFixture(t)
	ck := guruLogin(t, ts)

	form := quizForm("Tanpa Timer Create")
	form.Set("timer_type", "tanpa_timer")
	form.Del("total_seconds")
	form.Del("per_question_seconds")
	// timer_on stays at quizForm's "1" — the server must ignore it
	quizID := createQuiz(t, ts, ck, form)

	var timerType string
	var timerOn, total, perQ int
	if err := pool.QueryRow(`SELECT timer_type, timer_on, total_seconds, per_question_seconds
		FROM quizzes WHERE id = ?`, quizID).
		Scan(&timerType, &timerOn, &total, &perQ); err != nil {
		t.Fatalf("read quiz: %v", err)
	}
	if timerType != "tanpa_timer" || timerOn != 0 || total != 0 || perQ != 0 {
		t.Fatalf("quiz = (%s, timer_on=%d, total=%d, per_q=%d), want (tanpa_timer, 0, 0, 0)",
			timerType, timerOn, total, perQ)
	}
}

// TestCreateQuizInvalidTimerType keeps the 400 for unknown timer types.
func TestCreateQuizInvalidTimerType(t *testing.T) {
	ts, _, _, _ := quizFixture(t)
	ck := guruLogin(t, ts)

	form := quizForm("Bogus Timer Type")
	form.Set("timer_type", "bogus")
	resp, body := postForm(t, ts.URL+"/teacher/quiz/new", form, ck)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("create = %d, want 400: %s", resp.StatusCode, body)
	}
	if env := decodeEnv(t, body); env.OK || env.Message != "Invalid timer type." {
		t.Fatalf("envelope = %+v, want 400 Invalid timer type.", env)
	}
}

// --- student flow: start without a clock, free navigation ------------------

func TestTanpaTimerStartAndFreeNavigation(t *testing.T) {
	ts, pool, _, _ := quizFixture(t)
	ck := guruLogin(t, ts)
	st := studentCookie(t, pool, "tanpa1")

	form := quizForm("Tanpa Timer Flow")
	form.Set("timer_type", "tanpa_timer")
	quizID := createQuiz(t, ts, ck, form)
	q1 := addQuestion(t, ts, pool, ck, "TN one", "pg", []string{"A", "B"}, []int{0})
	q2 := addQuestion(t, ts, pool, ck, "TN two", "pg", []string{"A", "B"}, []int{1})
	compose(t, ts, ck, quizID, q1, q2)
	if resp, body := setStatus(t, ts, ck, quizID, "aktif"); resp.StatusCode != http.StatusOK || !decodeEnv(t, body).OK {
		t.Fatalf("activate = %d: %s", resp.StatusCode, body)
	}
	code := joinCode(t, pool, quizID)
	if resp, body := postJoin(t, ts, code, st); resp.StatusCode != http.StatusOK {
		t.Fatalf("join = %d: %s", resp.StatusCode, body)
	}

	// START (global endpoint) must still reject: this is not a global quiz
	resp, body := postJSON(t, fmt.Sprintf("%s/teacher/quiz/%d/start", ts.URL, quizID), nil, ck)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("global START = %d, want 409: %s", resp.StatusCode, body)
	}
	if env := decodeEnv(t, body); env.Message != "This quiz has no timer." {
		t.Fatalf("global START message = %q", env.Message)
	}

	// student starts their own attempt — no countdown, ends_at stays NULL
	first := startAttempt(t, ts, code, st)
	if first.Status != "started" || first.CurrentQ != 1 || first.EndsAt != 0 {
		t.Fatalf("start = %+v, want started/current 1/no ends_at", first)
	}
	pid, _, _ := participantRowFor(t, pool, quizID, userOf(t, ts, st))
	var endsAt sql.NullString
	if err := pool.QueryRow(`SELECT ends_at FROM participants WHERE id = ?`, pid).Scan(&endsAt); err != nil {
		t.Fatalf("read ends_at: %v", err)
	}
	if endsAt.Valid {
		t.Fatalf("ends_at = %s, want NULL", endsAt.String)
	}
	if got := quizStatus(t, pool, quizID); got != "aktif" {
		t.Fatalf("quiz status = %q, want aktif (never berjalan)", got)
	}

	// free navigation: advance, then jump back — both persist current_q
	var nd struct {
		CurrentQ int `json:"current_q"`
		Total    int `json:"total"`
	}
	resp, body = postJSON(t, ts.URL+"/quiz/"+code+"/next", nil, st)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("next = %d: %s", resp.StatusCode, body)
	}
	if err := json.Unmarshal(decodeEnv(t, body).Data, &nd); err != nil {
		t.Fatalf("next data: %v", err)
	}
	if nd.CurrentQ != 2 || nd.Total != 2 {
		t.Fatalf("next = %+v, want current 2 of 2", nd)
	}
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
	var storedCurrent int
	if err := pool.QueryRow(`SELECT current_q FROM participants WHERE id = ?`, pid).
		Scan(&storedCurrent); err != nil {
		t.Fatalf("read current_q: %v", err)
	}
	if storedCurrent != 1 {
		t.Fatalf("stored current_q = %d, want 1", storedCurrent)
	}
}
