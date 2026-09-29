package integration

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"quiz/internal/cache"
	"quiz/internal/quizengine"
)

// reorderURL builds the reorder endpoint for a quiz.
func reorderURL(ts *httptest.Server, quizID uint64) string {
	return fmt.Sprintf("%s/teacher/quiz/%d/questions/reorder", ts.URL, quizID)
}

// storedQOrder reads one participant's qorder snapshot.
func storedQOrder(t *testing.T, pool *sql.DB, pid uint64) quizengine.Order {
	t.Helper()
	var raw sql.NullString
	if err := pool.QueryRow(`SELECT qorder FROM participants WHERE id = ?`, pid).
		Scan(&raw); err != nil {
		t.Fatalf("qorder read: %v", err)
	}
	if !raw.Valid {
		t.Fatalf("participant %d has no qorder", pid)
	}
	var order quizengine.Order
	if err := json.Unmarshal([]byte(raw.String), &order); err != nil {
		t.Fatalf("qorder JSON %q: %v", raw.String, err)
	}
	return order
}

// sameIDs compares two id sequences element-wise.
func sameIDs(got, want []uint64) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// seqLine renders an ordered id slice for failure messages.
func seqLine(ids []uint64) string {
	out := ""
	for i, id := range ids {
		if i > 0 {
			out += " "
		}
		out += fmt.Sprint(id)
	}
	return "[" + out + "]"
}

// TestReorderQuestionsValidation pins the payload ladder and the atomic
// renumber: every rejected payload leaves seq untouched, an accepted order
// rewrites seq 1..N in one transaction, both accepted encodings work, and
// an activated quiz is locked behind the RemoveQuestion guard.
func TestReorderQuestionsValidation(t *testing.T) {
	ts, pool, store, _ := quizFixture(t)
	ck := guruLogin(t, ts)

	quiz := createQuiz(t, ts, ck, quizForm("Reorder validation"))
	q1 := addQuestion(t, ts, pool, ck, "RO one", "pg", []string{"A", "B"}, []int{0})
	q2 := addQuestion(t, ts, pool, ck, "RO two", "pg", []string{"A", "B"}, []int{1})
	compose(t, ts, ck, quiz, q1, q2)
	foreign := addQuestion(t, ts, pool, ck, "RO foreign", "pg", []string{"A", "B"}, []int{0})

	// invalid path param
	resp, body := postJSON(t,
		fmt.Sprintf("%s/teacher/quiz/abc/questions/reorder", ts.URL),
		map[string]any{"question_ids": []uint64{q1}}, ck)
	assertFail(t, resp, body, http.StatusBadRequest, "VALIDATION", "Invalid quiz id.")

	// unknown quiz → 404
	resp, body = postJSON(t, reorderURL(ts, 999999),
		map[string]any{"question_ids": []uint64{q1}}, ck)
	assertFail(t, resp, body, http.StatusNotFound, "NOT_FOUND", "Quiz not found.")

	// empty order
	resp, body = postJSON(t, reorderURL(ts, quiz),
		map[string]any{"question_ids": []uint64{}}, ck)
	assertFail(t, resp, body, http.StatusBadRequest, "VALIDATION", "Choose at least one question.")

	// zero id / non-numeric id
	resp, body = postJSON(t, reorderURL(ts, quiz),
		map[string]any{"question_ids": []uint64{0}}, ck)
	assertFail(t, resp, body, http.StatusBadRequest, "VALIDATION", "Invalid question id.")
	resp, body = postJSON(t, reorderURL(ts, quiz),
		map[string]any{"question_ids": []string{"abc"}}, ck)
	assertFail(t, resp, body, http.StatusBadRequest, "VALIDATION", "Invalid question id.")

	// duplicate inside one payload
	resp, body = postJSON(t, reorderURL(ts, quiz),
		map[string]any{"question_ids": []uint64{q1, q1}}, ck)
	assertFail(t, resp, body, http.StatusBadRequest, "VALIDATION", "Duplicate question in the order.")

	// a composed id missing from the payload
	resp, body = postJSON(t, reorderURL(ts, quiz),
		map[string]any{"question_ids": []uint64{q1}}, ck)
	assertFail(t, resp, body, http.StatusBadRequest, "VALIDATION", "Question order does not match this quiz.")

	// a bank id that is not composed into this quiz
	resp, body = postJSON(t, reorderURL(ts, quiz),
		map[string]any{"question_ids": []uint64{q2, foreign}}, ck)
	assertFail(t, resp, body, http.StatusBadRequest, "VALIDATION", "Question order does not match this quiz.")

	// malformed body
	resp, body = postRaw(t, reorderURL(ts, quiz), "application/json", `{"question_ids": [`, ck)
	assertFail(t, resp, body, http.StatusBadRequest, "VALIDATION", "Invalid request body.")

	// none of the rejections wrote anything (all-or-nothing)
	if seqs := composedSeqs(t, pool, quiz); seqs[q1] != 1 || seqs[q2] != 2 {
		t.Fatalf("seqs after rejections = %v, want q1=1 q2=2", seqs)
	}

	// happy path: JSON order [q2, q1] → renumbered 1,2 and mirrors dropped
	store.Set(cache.QuizSetKey(quiz), "seed", time.Minute)
	store.Set(cache.QuizStateKey(quiz), "seed", time.Minute)
	resp, body = postJSON(t, reorderURL(ts, quiz),
		map[string]any{"question_ids": []uint64{q2, q1}}, ck)
	if resp.StatusCode != http.StatusOK || !decodeEnv(t, body).OK {
		t.Fatalf("reorder = %d: %s", resp.StatusCode, body)
	}
	seqs := composedSeqs(t, pool, quiz)
	if seqs[q2] != 1 || seqs[q1] != 2 {
		t.Fatalf("seqs after reorder = %v, want q2=1 q1=2", seqs)
	}
	for _, key := range []string{cache.QuizSetKey(quiz), cache.QuizStateKey(quiz)} {
		if _, ok := store.Get(key); ok {
			t.Errorf("%s survived reorder", key)
		}
	}

	// urlencoded encoding (compose accepts it too)
	resp, body = postForm(t, reorderURL(ts, quiz),
		url.Values{"question_ids[]": {fmt.Sprint(q1), fmt.Sprint(q2)}}, ck)
	if resp.StatusCode != http.StatusOK || !decodeEnv(t, body).OK {
		t.Fatalf("urlencoded reorder = %d: %s", resp.StatusCode, body)
	}
	if seqs = composedSeqs(t, pool, quiz); seqs[q1] != 1 || seqs[q2] != 2 {
		t.Fatalf("seqs after urlencoded reorder = %v, want q1=1 q2=2", seqs)
	}

	// activation locks reordering — exact RemoveQuestion locked message
	if resp, body := setStatus(t, ts, ck, quiz, "aktif"); resp.StatusCode != http.StatusOK {
		t.Fatalf("activate = %d: %s", resp.StatusCode, body)
	}
	resp, body = postJSON(t, reorderURL(ts, quiz),
		map[string]any{"question_ids": []uint64{q2, q1}}, ck)
	assertFail(t, resp, body, http.StatusConflict, "CONFLICT",
		"Quiz has participants or is active — editing is locked.")
	if seqs = composedSeqs(t, pool, quiz); seqs[q1] != 1 || seqs[q2] != 2 {
		t.Fatalf("locked reorder changed seqs = %v", seqs)
	}
}

// TestReorderQuestionOrderReachesStudent proves the end-to-end contract:
// the teacher's stored order is what a student's attempt snapshot takes,
// and the shuffle only overrides it when Shuffle question order is on.
func TestReorderQuestionOrderReachesStudent(t *testing.T) {
	ts, pool, _, _ := quizFixture(t)
	ck := guruLogin(t, ts)
	st := studentCookie(t, pool, "reorder-student")
	student := userOf(t, ts, st)

	// buildAndStart creates a per_soal quiz, composes 3 questions, reorders
	// them to [c, a, b], activates, joins and starts — returning the quiz id,
	// the teacher order and the ids in their original (a,b,c) composition.
	buildAndStart := func(title string, shuffle bool) (uint64, []uint64, []uint64) {
		t.Helper()
		form := quizForm(title)
		form.Set("timer_type", "per_soal")
		form.Set("per_question_seconds", "60")
		if shuffle {
			form.Set("shuffle_questions", "1")
		}
		quizID := createQuiz(t, ts, ck, form)
		ids := make([]uint64, 0, 3)
		for i, teks := range []string{"First", "Second", "Third"} {
			ids = append(ids, addQuestion(t, ts, pool, ck,
				fmt.Sprintf("%s %s", title, teks), "pg",
				[]string{"Alpha", "Beta"}, []int{i % 2}))
		}
		compose(t, ts, ck, quizID, ids...)

		want := []uint64{ids[2], ids[0], ids[1]}
		resp, body := postJSON(t, reorderURL(ts, quizID),
			map[string]any{"question_ids": want}, ck)
		if resp.StatusCode != http.StatusOK || !decodeEnv(t, body).OK {
			t.Fatalf("reorder %s = %d: %s", title, resp.StatusCode, body)
		}
		seqs := composedSeqs(t, pool, quizID)
		if seqs[want[0]] != 1 || seqs[want[1]] != 2 || seqs[want[2]] != 3 {
			t.Fatalf("%s seqs = %v, want order %s", title, seqs, seqLine(want))
		}

		if resp, body := setStatus(t, ts, ck, quizID, "aktif"); resp.StatusCode != http.StatusOK || !decodeEnv(t, body).OK {
			t.Fatalf("activate %s = %d: %s", title, resp.StatusCode, body)
		}
		code := joinCode(t, pool, quizID)
		if resp, body := postJoin(t, ts, code, st); resp.StatusCode != http.StatusOK {
			t.Fatalf("join %s = %d: %s", title, resp.StatusCode, body)
		}
		startAttempt(t, ts, code, st)
		return quizID, want, ids
	}

	// shuffle OFF → the attempt snapshot is byte-order the teacher's order
	quizOff, wantOff, _ := buildAndStart("Order No Shuffle", false)
	pidOff, _, attemptOff := participantRowFor(t, pool, quizOff, student)
	orderOff := storedQOrder(t, pool, pidOff)
	if !sameIDs(orderOff.Questions, wantOff) {
		t.Fatalf("shuffle off: student order = %s, want teacher order %s",
			seqLine(orderOff.Questions), seqLine(wantOff))
	}

	// shuffle ON → the snapshot is BuildOrder over that same teacher order
	// (per-participant seed, option shuffle left as configured)
	quizOn, wantOn, idsOn := buildAndStart("Order Shuffle", true)
	pidOn, _, attemptOn := participantRowFor(t, pool, quizOn, student)
	orderOn := storedQOrder(t, pool, pidOn)
	counts := map[uint64]int{idsOn[0]: 2, idsOn[1]: 2, idsOn[2]: 2}
	expected := quizengine.BuildOrder(
		quizengine.SeedFor(pidOn, quizOn, uint64(attemptOn)), wantOn, counts, true, false)
	if attemptOff != 1 || attemptOn != 1 {
		// both quizzes are fresh joins — the seed must stay attempt 1
		t.Fatalf("attempt numbers = %d / %d, want both 1", attemptOff, attemptOn)
	}
	if !sameIDs(orderOn.Questions, expected.Questions) {
		t.Fatalf("shuffle on: student order = %s, want BuildOrder %s (teacher order %s)",
			seqLine(orderOn.Questions), seqLine(expected.Questions), seqLine(wantOn))
	}
}
