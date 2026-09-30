package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"quiz/internal/quizengine"
)

// pagerWindow extracts a short window around needle for failure messages.
func pagerWindow(body, needle string) string {
	i := strings.Index(body, needle)
	if i < 0 {
		return "<" + needle + "> not found"
	}
	start := i - 40
	if start < 0 {
		start = 0
	}
	end := i + 180
	if end > len(body) {
		end = len(body)
	}
	return body[start:end]
}

// --- one-question-per-page pager (static HTML contract) --------------------

// TestWorkspacePagerStaticContract pins the static half of the
// one-question-per-page workspace in review mode: the SSR markup hides
// every non-current question section, the prev/next pager exists with its
// boundary states (disabled with the Bootstrap disabled class — never
// hidden — so the bound stays discoverable), and every question's answer
// inputs are still in the response. Click transitions are JS; their code
// path is reviewed against workspace.js render()/jumpTo().
func TestWorkspacePagerStaticContract(t *testing.T) {
	ts, pool, _, _ := quizFixture(t)
	ck := guruLogin(t, ts)
	st := studentCookie(t, pool, "wpager")

	code, ids := persoalFixture(t, ts, pool, ck, st,
		"Workspace Pager", []string{"Pager one", "Pager two", "Pager three"}, "open")
	startAttempt(t, ts, code, st)

	page := func() string {
		t.Helper()
		resp, body := getWith(t, ts.URL+"/quiz/"+code, st)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("workspace page = %d", resp.StatusCode)
		}
		if !contains(body, `data-state="started"`) {
			t.Fatalf("workspace not started: %s", pagerWindow(body, "data-state="))
		}
		return body
	}
	// the DOM-submission invariant: every question's inputs are shipped
	// regardless of which page is currently visible
	assertInputs := func(body string) {
		t.Helper()
		for _, qid := range ids {
			needle := fmt.Sprintf(`name="q_%d"`, qid)
			if n := strings.Count(body, needle); n != 2 {
				t.Fatalf("inputs for question %d = %d, want 2 (%s)",
					qid, n, pagerWindow(body, needle))
			}
		}
	}

	// --- Q1: only Q1 visible; prev disabled, next enabled
	body := page()
	if !contains(body, `id="btn-prev"`) || !contains(body, `id="btn-next"`) {
		t.Fatalf("pager controls missing (prev %v / next %v)",
			contains(body, `id="btn-prev"`), contains(body, `id="btn-next"`))
	}
	if !contains(body, `id="btn-prev" class="btn btn-outline-secondary disabled" disabled title="Sebelumnya"`) {
		t.Fatalf("prev not disabled on the first question: %s", pagerWindow(body, `id="btn-prev"`))
	}
	if !contains(body, `id="btn-next" class="btn btn-outline-secondary" title="Berikutnya"`) {
		t.Fatalf("next not enabled on the first question: %s", pagerWindow(body, `id="btn-next"`))
	}
	if !contains(body, `data-index="0" data-type="pg">`) {
		t.Fatalf("current question not visible in markup: %s", pagerWindow(body, `data-index="0"`))
	}
	for _, idx := range []int{1, 2} {
		needle := fmt.Sprintf(`data-index="%d" data-type="pg" hidden>`, idx)
		if !contains(body, needle) {
			t.Fatalf("non-current question %d lacks the hidden marker: %s",
				idx+1, pagerWindow(body, fmt.Sprintf(`data-index="%d"`, idx)))
		}
	}
	assertInputs(body)

	advance := func(step int) {
		t.Helper()
		resp, rbody := postJSON(t, ts.URL+"/quiz/"+code+"/next", nil, st)
		if resp.StatusCode != http.StatusOK || !decodeEnv(t, rbody).OK {
			t.Fatalf("next step %d = %d: %s", step, resp.StatusCode, rbody)
		}
	}

	// --- Q2 (middle): both buttons enabled, only Q2 visible
	advance(1)
	body = page()
	if !contains(body, `id="btn-prev" class="btn btn-outline-secondary" title="Sebelumnya"`) ||
		!contains(body, `id="btn-next" class="btn btn-outline-secondary" title="Berikutnya"`) {
		t.Fatalf("middle pager not fully enabled:\nprev: %s\nnext: %s",
			pagerWindow(body, `id="btn-prev"`), pagerWindow(body, `id="btn-next"`))
	}
	if contains(body, `id="btn-prev" class="btn btn-outline-secondary disabled"`) ||
		contains(body, `id="btn-next" class="btn btn-outline-secondary disabled"`) {
		t.Fatalf("middle pager carries a disabled bound:\nprev: %s\nnext: %s",
			pagerWindow(body, `id="btn-prev"`), pagerWindow(body, `id="btn-next"`))
	}
	if !contains(body, `data-index="1" data-type="pg">`) {
		t.Fatalf("middle question not visible in markup: %s", pagerWindow(body, `data-index="1"`))
	}
	for _, idx := range []int{0, 2} {
		needle := fmt.Sprintf(`data-index="%d" data-type="pg" hidden>`, idx)
		if !contains(body, needle) {
			t.Fatalf("off-page question %d lacks the hidden marker: %s",
				idx+1, pagerWindow(body, fmt.Sprintf(`data-index="%d"`, idx)))
		}
	}

	// --- Q3 (last): next disabled, prev enabled, only Q3 visible
	advance(2)
	body = page()
	if !contains(body, `id="btn-next" class="btn btn-outline-secondary disabled" disabled title="Berikutnya"`) {
		t.Fatalf("next not disabled on the last question: %s", pagerWindow(body, `id="btn-next"`))
	}
	if !contains(body, `id="btn-prev" class="btn btn-outline-secondary" title="Sebelumnya"`) {
		t.Fatalf("prev not enabled on the last question: %s", pagerWindow(body, `id="btn-prev"`))
	}
	if !contains(body, `data-index="2" data-type="pg">`) {
		t.Fatalf("last question not visible in markup: %s", pagerWindow(body, `data-index="2"`))
	}
	for _, idx := range []int{0, 1} {
		needle := fmt.Sprintf(`data-index="%d" data-type="pg" hidden>`, idx)
		if !contains(body, needle) {
			t.Fatalf("off-page question %d lacks the hidden marker: %s",
				idx+1, pagerWindow(body, fmt.Sprintf(`data-index="%d"`, idx)))
		}
	}
	// hidden sections still ship their inputs
	assertInputs(body)
}

// TestWorkspacePagerLinearStaticContract pins the linear half: global-mode
// attempts never show the pager (the server refuses /next there — free
// navigation is review-mode only), and the SSR markup still ships exactly
// one visible question with every input present.
func TestWorkspacePagerLinearStaticContract(t *testing.T) {
	ts, pool, _, _ := quizFixture(t)
	ck := guruLogin(t, ts)
	st := studentCookie(t, pool, "wpager-lin")

	form := quizForm("Linear Pager")
	quizID := createQuiz(t, ts, ck, form)
	q1 := addQuestion(t, ts, pool, ck, "LP one", "pg", []string{"One", "Two"}, []int{0})
	q2 := addQuestion(t, ts, pool, ck, "LP two", "pg", []string{"One", "Two"}, []int{0})
	compose(t, ts, ck, quizID, q1, q2)
	if resp, body := setStatus(t, ts, ck, quizID, "aktif"); resp.StatusCode != http.StatusOK || !decodeEnv(t, body).OK {
		t.Fatalf("activate = %d: %s", resp.StatusCode, body)
	}
	code := joinCode(t, pool, quizID)
	if resp, body := postJoin(t, ts, code, st); resp.StatusCode != http.StatusOK {
		t.Fatalf("join = %d: %s", resp.StatusCode, body)
	}

	// mimic the global-mode START (recipe of TestLinearAnswerPreviewAndDwell)
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
		WHERE quiz_id = ? AND user_id = (SELECT id FROM users WHERE username = 'wpager-lin')`,
		string(raw), quizID); err != nil {
		t.Fatalf("simulate start: %v", err)
	}

	resp, body := getWith(t, ts.URL+"/quiz/"+code, st)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("workspace page = %d", resp.StatusCode)
	}
	if !contains(body, `data-state="started"`) {
		t.Fatalf("workspace not started: %s", pagerWindow(body, "data-state="))
	}
	// pager buttons exist but stay hidden in linear mode
	if !contains(body, `id="btn-prev" class="btn btn-outline-secondary disabled" hidden disabled title="Sebelumnya"`) {
		t.Fatalf("linear prev not hidden: %s", pagerWindow(body, `id="btn-prev"`))
	}
	if !contains(body, `id="btn-next" class="btn btn-outline-secondary" hidden title="Berikutnya"`) {
		t.Fatalf("linear next not hidden: %s", pagerWindow(body, `id="btn-next"`))
	}
	// one question per page already holds server-side
	if !contains(body, `data-index="0" data-type="pg">`) {
		t.Fatalf("current question not visible in markup: %s", pagerWindow(body, `data-index="0"`))
	}
	if !contains(body, `data-index="1" data-type="pg" hidden>`) {
		t.Fatalf("non-current question lacks the hidden marker: %s", pagerWindow(body, `data-index="1"`))
	}
	// inputs for BOTH questions are present
	for _, qid := range []uint64{q1, q2} {
		needle := fmt.Sprintf(`name="q_%d"`, qid)
		if n := strings.Count(body, needle); n != 2 {
			t.Fatalf("inputs for question %d = %d, want 2 (%s)", qid, n, pagerWindow(body, needle))
		}
	}
}
