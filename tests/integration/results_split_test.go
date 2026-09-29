package integration

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// detailSegment slices the first expandable answer row out of the results
// page (the id anchors the per-student panel).
func detailSegment(t *testing.T, body string, index int) string {
	t.Helper()
	marker := fmt.Sprintf(`id="answer-detail-%d"`, index)
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatalf("results page has no %s (analysis split or panel missing)", marker)
	}
	end := i + 4000
	if end > len(body) {
		end = len(body)
	}
	return body[i:end]
}

// TestResultsAnswerPanelAndAnalysisRoute covers the results split:
// /results renders the students table with an expandable per-student answer
// panel (answer + key + correct/wrong per question, correct count per row),
// and the heavy per-question analysis lives on its own route behind its own
// query.
func TestResultsAnswerPanelAndAnalysisRoute(t *testing.T) {
	ts, pool, _, _ := quizFixture(t)
	ck := guruLogin(t, ts)
	st := studentCookie(t, pool, "results-student")

	form := quizForm("Results Split")
	form.Set("timer_type", "per_soal")
	form.Set("per_question_seconds", "60")
	quizID := createQuiz(t, ts, ck, form)

	pg := addQuestion(t, ts, pool, ck, "Panel pick Beta", "pg",
		[]string{"Alpha", "Beta"}, []int{1})
	// essay WITH a free-text key — the panel must show it beside the answer
	resp, body := postForm(t, ts.URL+"/teacher/questions", url.Values{
		"teks":      {"Panel explain it"},
		"type":      {"essay"},
		"essay_key": {"Expected essay key"},
	}, ck)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("add essay = %d: %s", resp.StatusCode, body)
	}
	var essay uint64
	if err := pool.QueryRow(`SELECT MAX(id) FROM questions`).Scan(&essay); err != nil {
		t.Fatalf("essay id: %v", err)
	}
	compose(t, ts, ck, quizID, pg, essay)

	if resp, body := setStatus(t, ts, ck, quizID, "aktif"); resp.StatusCode != http.StatusOK {
		t.Fatalf("activate = %d: %s", resp.StatusCode, body)
	}
	code := joinCode(t, pool, quizID)
	if resp, body := postJoin(t, ts, code, st); resp.StatusCode != http.StatusOK {
		t.Fatalf("join = %d: %s", resp.StatusCode, body)
	}
	startAttempt(t, ts, code, st)
	// wrong on purpose (key is Beta → index 1), essay left unanswered
	if resp, body := answerPost(t, ts, code, st, pg, []int{0}); resp.StatusCode != http.StatusOK {
		t.Fatalf("answer = %d: %s", resp.StatusCode, body)
	}
	if resp, body := postJSON(t, ts.URL+"/quiz/"+code+"/finish", nil, st); resp.StatusCode != http.StatusOK {
		t.Fatalf("finish = %d: %s", resp.StatusCode, body)
	}

	resultsURL := fmt.Sprintf("%s/teacher/quiz/%d/results", ts.URL, quizID)
	analysisURL := fmt.Sprintf("%s/teacher/quiz/%d/results/analysis", ts.URL, quizID)

	// --- /results: students table + expandable answer panel ----------------
	resp, body = getWith(t, resultsURL, ck)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET results = %d: %s", resp.StatusCode, body)
	}
	for _, want := range []string{
		`data-answer-toggle`,              // expander control
		`aria-controls="answer-detail-0"`, // its panel id
		`<th class="text-nowrap">Answer</th>`,
		`<th class="text-nowrap">Correct answer</th>`,
		`>0/2</td>`, // correct count: 0 of 2
	} {
		if !strings.Contains(body, want) {
			t.Errorf("results page missing %q", want)
		}
	}
	if !strings.Contains(body, fmt.Sprintf(`/teacher/quiz/%d/results/analysis"`, quizID)) {
		t.Errorf("results page has no link to the analysis route")
	}
	// the analysis table must NOT render on this route anymore
	if strings.Contains(body, "Most chosen") {
		t.Errorf("results page still renders the per-question analysis")
	}

	seg := detailSegment(t, body, 0)
	for _, want := range []string{
		"Panel pick Beta",                      // question text
		"<td>A</td>",                           // student answer (picked Alpha = original index 0)
		"<td>B</td>",                           // answer key (Beta = original index 1)
		`text-bg-danger">Wrong</span>`,         // correct/salah status
		"Panel explain it",                     // next question line
		"Expected essay key",                   // essay key rendered as the correct answer
		`text-bg-secondary">Unanswered</span>`, // no answer row at all
	} {
		if !strings.Contains(seg, want) {
			t.Errorf("answer panel missing %q", want)
		}
	}
	// the panel is server-rendered but hidden until expanded
	if !strings.Contains(body, `<tr class="d-none" id="answer-detail-0">`) {
		t.Errorf("answer panel row is not the collapsed default")
	}

	// --- /results/analysis: the analysis table on its own route ------------
	resp, body = getWith(t, analysisURL, ck)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET analysis = %d: %s", resp.StatusCode, body)
	}
	for _, want := range []string{
		"Question analysis",
		"Most chosen",
		"Panel pick Beta",
		`aria-current="page"`, // this view is the active tab
	} {
		if !strings.Contains(body, want) {
			t.Errorf("analysis page missing %q", want)
		}
	}
	for _, avoid := range []string{`data-answer-toggle`, "Awaiting grading"} {
		if strings.Contains(body, avoid) {
			t.Errorf("analysis page renders %q (students table leaked in)", avoid)
		}
	}
	if !strings.Contains(body, fmt.Sprintf(`href="/teacher/quiz/%d/results"`, quizID)) {
		t.Errorf("analysis page has no link back to the students route")
	}

	// --- unknown ids → 404 on both routes ----------------------------------
	for _, endpoint := range []string{
		fmt.Sprintf("%s/teacher/quiz/%d/results", ts.URL, 999999),
		fmt.Sprintf("%s/teacher/quiz/%d/results/analysis", ts.URL, 999999),
	} {
		if resp, body := getWith(t, endpoint, ck); resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404 (%s)", endpoint, resp.StatusCode, body)
		}
	}
}
