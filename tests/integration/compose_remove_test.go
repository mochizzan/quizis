package integration

import (
	"bytes"
	"database/sql"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"quiz/internal/cache"
)

// composeURL builds the compose endpoint for a quiz.
func composeURL(ts *httptest.Server, quizID uint64) string {
	return fmt.Sprintf("%s/teacher/quiz/%d/questions", ts.URL, quizID)
}

// removeURL builds the remove endpoint for one composed question.
func removeURL(ts *httptest.Server, quizID, qid uint64) string {
	return fmt.Sprintf("%s/teacher/quiz/%d/questions/%d/delete", ts.URL, quizID, qid)
}

// postRaw issues a POST with an explicit Content-Type and raw body; an
// empty content type and body send neither (the frontend's body-less
// data-post pattern).
func postRaw(t *testing.T, endpoint, contentType, body string, cookies ...*http.Cookie) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", endpoint, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, string(raw)
}

// postMultipart issues a multipart/form-data POST built with
// mime/multipart — the browser FormData encoding that ParseForm ignores.
func postMultipart(t *testing.T, endpoint string, fields url.Values, cookies ...*http.Cookie) (*http.Response, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for key, vals := range fields {
		for _, v := range vals {
			if err := w.WriteField(key, v); err != nil {
				t.Fatalf("write field %s: %v", key, err)
			}
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, &buf)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", endpoint, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, string(raw)
}

// composedSeqs maps composed question id → seq for the quiz.
func composedSeqs(t *testing.T, pool *sql.DB, quizID uint64) map[uint64]int {
	t.Helper()
	rows, err := pool.Query(`SELECT question_id, seq FROM quiz_questions WHERE quiz_id = ?`, quizID)
	if err != nil {
		t.Fatalf("composed rows: %v", err)
	}
	defer rows.Close()
	out := make(map[uint64]int)
	for rows.Next() {
		var qid uint64
		var seq int
		if err := rows.Scan(&qid, &seq); err != nil {
			t.Fatalf("scan composed row: %v", err)
		}
		out[qid] = seq
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("composed rows: %v", err)
	}
	return out
}

// Regression for the live bug: the browser submits compose as
// multipart/form-data (FormData), which net/http's ParseForm never reads —
// the handler must branch on Content-Type and parse the multipart body.
func TestComposeAcceptsMultipartFormData(t *testing.T) {
	ts, pool, store, _ := quizFixture(t)
	ck := guruLogin(t, ts)

	quiz := createQuiz(t, ts, ck, quizForm("Multipart compose"))
	q1 := addQuestion(t, ts, pool, ck, "Multipart one", "pg", []string{"A", "B"}, []int{0})
	q2 := addQuestion(t, ts, pool, ck, "Multipart two", "essay", nil, nil)

	// seed the mirrors the handler must invalidate
	store.Set(cache.QuizSetKey(quiz), "seed", time.Minute)
	store.Set(cache.QuizStateKey(quiz), "seed", time.Minute)

	// explicit seq_<id> fields, as the legacy form sends them
	k1 := strconv.FormatUint(q1, 10)
	fields := url.Values{"length": {""}}
	fields.Add("question_ids[]", k1)
	fields.Set("seq_"+k1, "1")
	resp, body := postMultipart(t, composeURL(ts, quiz), fields, ck)
	if resp.StatusCode != http.StatusOK || !decodeEnv(t, body).OK {
		t.Fatalf("multipart compose = %d: %s", resp.StatusCode, body)
	}

	// no seq fields → appended after MAX(seq) (still multipart)
	fields = url.Values{"length": {""}}
	fields.Add("question_ids[]", strconv.FormatUint(q2, 10))
	resp, body = postMultipart(t, composeURL(ts, quiz), fields, ck)
	if resp.StatusCode != http.StatusOK || !decodeEnv(t, body).OK {
		t.Fatalf("multipart compose without seq = %d: %s", resp.StatusCode, body)
	}

	seqs := composedSeqs(t, pool, quiz)
	if seqs[q1] != 1 || seqs[q2] != 2 {
		t.Errorf("seqs = %v, want q1=1 q2=2 (append after MAX(seq))", seqs)
	}
	for _, key := range []string{cache.QuizSetKey(quiz), cache.QuizStateKey(quiz)} {
		if _, ok := store.Get(key); ok {
			t.Errorf("%s survived compose", key)
		}
	}
}

// JSON compose (the new bank modal): a single id, then two ids appended
// after the existing MAX(seq) with array order preserved.
func TestComposeJSONAppendsInArrivalOrder(t *testing.T) {
	ts, pool, _, _ := quizFixture(t)
	ck := guruLogin(t, ts)

	quiz := createQuiz(t, ts, ck, quizForm("JSON compose"))
	q1 := addQuestion(t, ts, pool, ck, "JSON one", "pg", []string{"A", "B"}, []int{0})
	q2 := addQuestion(t, ts, pool, ck, "JSON two", "pg", []string{"A", "B"}, []int{1})
	q3 := addQuestion(t, ts, pool, ck, "JSON three", "essay", nil, nil)

	resp, body := postJSON(t, composeURL(ts, quiz),
		map[string]any{"question_ids": []uint64{q1}, "length": ""}, ck)
	if resp.StatusCode != http.StatusOK || !decodeEnv(t, body).OK {
		t.Fatalf("JSON compose single = %d: %s", resp.StatusCode, body)
	}

	resp, body = postJSON(t, composeURL(ts, quiz),
		map[string]any{"question_ids": []uint64{q2, q3}}, ck)
	if resp.StatusCode != http.StatusOK || !decodeEnv(t, body).OK {
		t.Fatalf("JSON compose two = %d: %s", resp.StatusCode, body)
	}

	seqs := composedSeqs(t, pool, quiz)
	if len(seqs) != 3 {
		t.Fatalf("composed rows = %v, want 3", seqs)
	}
	if seqs[q1] != 1 || seqs[q2] != 2 || seqs[q3] != 3 {
		t.Errorf("seqs = %v, want q1=1 q2=2 q3=3 (append after max, array order)", seqs)
	}
}

// urlencoded compose without seq_<id> appends after MAX(seq); the legacy
// explicit-seq path (shared compose helper) must keep working unchanged.
func TestComposeURLEncodedWithoutSeqAppends(t *testing.T) {
	ts, pool, _, _ := quizFixture(t)
	ck := guruLogin(t, ts)

	quiz := createQuiz(t, ts, ck, quizForm("Append compose"))
	q1 := addQuestion(t, ts, pool, ck, "Append one", "pg", []string{"A", "B"}, []int{0})
	q2 := addQuestion(t, ts, pool, ck, "Append two", "essay", nil, nil)

	// legacy path with explicit seq_ fields (used across the whole suite)
	compose(t, ts, ck, quiz, q1)

	resp, body := postForm(t, composeURL(ts, quiz),
		url.Values{"length": {""}, "question_ids[]": {strconv.FormatUint(q2, 10)}}, ck)
	if resp.StatusCode != http.StatusOK || !decodeEnv(t, body).OK {
		t.Fatalf("seqless compose = %d: %s", resp.StatusCode, body)
	}

	seqs := composedSeqs(t, pool, quiz)
	if seqs[q1] != 1 || seqs[q2] != 2 {
		t.Errorf("seqs = %v, want q1=1 q2=2", seqs)
	}
}

// JSON validation ladder: empty list, non-numeric/zero ids, malformed body
// and an unknown length bucket — each 400 VALIDATION with its exact message.
func TestComposeJSONValidation(t *testing.T) {
	ts, pool, _, _ := quizFixture(t)
	ck := guruLogin(t, ts)

	quiz := createQuiz(t, ts, ck, quizForm("JSON validation"))
	q := addQuestion(t, ts, pool, ck, "Validation target", "pg", []string{"A", "B"}, []int{0})

	resp, body := postJSON(t, composeURL(ts, quiz),
		map[string]any{"question_ids": []uint64{}}, ck)
	assertFail(t, resp, body, http.StatusBadRequest, "VALIDATION", "Choose at least one question.")

	resp, body = postJSON(t, composeURL(ts, quiz),
		map[string]any{"question_ids": []string{"abc"}}, ck)
	assertFail(t, resp, body, http.StatusBadRequest, "VALIDATION", "Invalid question id.")

	resp, body = postJSON(t, composeURL(ts, quiz),
		map[string]any{"question_ids": []uint64{0}}, ck)
	assertFail(t, resp, body, http.StatusBadRequest, "VALIDATION", "Invalid question id.")

	resp, body = postRaw(t, composeURL(ts, quiz), "application/json", `{"question_ids": [`, ck)
	assertFail(t, resp, body, http.StatusBadRequest, "VALIDATION", "Invalid request body.")

	resp, body = postJSON(t, composeURL(ts, quiz),
		map[string]any{"question_ids": []uint64{q}, "length": "bogus"}, ck)
	assertFail(t, resp, body, http.StatusBadRequest, "VALIDATION", "Invalid length filter.")

	if n := len(composedSeqs(t, pool, quiz)); n != 0 {
		t.Errorf("composed rows = %d, want 0", n)
	}
}

// The schema has no FKs: composing into a missing quiz must 404 and insert
// no orphan rows — for every accepted encoding.
func TestComposeMissingQuizIs404(t *testing.T) {
	ts, pool, _, _ := quizFixture(t)
	ck := guruLogin(t, ts)

	endpoint := composeURL(ts, 999999)

	resp, body := postJSON(t, endpoint, map[string]any{"question_ids": []uint64{1}}, ck)
	assertFail(t, resp, body, http.StatusNotFound, "NOT_FOUND", "Quiz not found.")

	resp, body = postForm(t, endpoint,
		url.Values{"length": {""}, "question_ids[]": {"1"}}, ck)
	assertFail(t, resp, body, http.StatusNotFound, "NOT_FOUND", "Quiz not found.")

	resp, body = postMultipart(t, endpoint,
		url.Values{"length": {""}, "question_ids[]": {"1"}}, ck)
	assertFail(t, resp, body, http.StatusNotFound, "NOT_FOUND", "Quiz not found.")

	var orphans int
	if err := pool.QueryRow(`SELECT COUNT(*) FROM quiz_questions WHERE quiz_id = 999999`).
		Scan(&orphans); err != nil {
		t.Fatalf("orphan count: %v", err)
	}
	if orphans != 0 {
		t.Errorf("orphan quiz_questions rows = %d, want 0", orphans)
	}

	// quiz id 0 is an invalid path param (400), distinct from a missing quiz
	resp, body = postForm(t, composeURL(ts, 0),
		url.Values{"length": {""}, "question_ids[]": {"1"}}, ck)
	assertFail(t, resp, body, http.StatusBadRequest, "VALIDATION", "Invalid quiz id.")
}

// Already-composed id → 409; a mixed new+existing submission rolls the
// whole transaction back; a duplicate inside one submission hits the
// pre-check before any insert.
func TestComposeDuplicateConflict(t *testing.T) {
	ts, pool, _, _ := quizFixture(t)
	ck := guruLogin(t, ts)

	quiz := createQuiz(t, ts, ck, quizForm("Duplicate compose"))
	q1 := addQuestion(t, ts, pool, ck, "Dup one", "pg", []string{"A", "B"}, []int{0})
	q2 := addQuestion(t, ts, pool, ck, "Dup two", "essay", nil, nil)
	compose(t, ts, ck, quiz, q1)

	resp, body := postJSON(t, composeURL(ts, quiz),
		map[string]any{"question_ids": []uint64{q1}}, ck)
	assertFail(t, resp, body, http.StatusConflict, "CONFLICT", "Question already in this quiz.")

	// one new + one already composed → whole tx rolls back
	resp, body = postJSON(t, composeURL(ts, quiz),
		map[string]any{"question_ids": []uint64{q2, q1}}, ck)
	assertFail(t, resp, body, http.StatusConflict, "CONFLICT", "Question already in this quiz.")

	seqs := composedSeqs(t, pool, quiz)
	if len(seqs) != 1 || seqs[q1] != 1 {
		t.Errorf("after rollback seqs = %v, want only q1=1", seqs)
	}

	// duplicate inside one submission → 409 pre-check, no insert
	resp, body = postJSON(t, composeURL(ts, quiz),
		map[string]any{"question_ids": []uint64{q2, q2}}, ck)
	assertFail(t, resp, body, http.StatusConflict, "CONFLICT", "Question already in this quiz.")
	if _, ok := composedSeqs(t, pool, quiz)[q2]; ok {
		t.Error("duplicate submission inserted q2")
	}
}

// The declared length filter is re-applied server-side on the form path:
// out-of-bucket → 400, in-bucket passes, no declared length skips the
// bucket check, unknown bucket → 400.
func TestComposeLengthFilterEnforced(t *testing.T) {
	ts, pool, _, _ := quizFixture(t)
	ck := guruLogin(t, ts)

	quiz := createQuiz(t, ts, ck, quizForm("Bucket compose"))
	short := addQuestion(t, ts, pool, ck, "Short one", "essay", nil, nil)
	long := addQuestion(t, ts, pool, ck, strings.Repeat("a", 120), "essay", nil, nil)

	longKey := strconv.FormatUint(long, 10)
	outOfBucket := url.Values{"length": {"short"}}
	outOfBucket.Add("question_ids[]", longKey)
	outOfBucket.Set("seq_"+longKey, "1")
	resp, body := postForm(t, composeURL(ts, quiz), outOfBucket, ck)
	assertFail(t, resp, body, http.StatusBadRequest, "VALIDATION",
		"A selected question is outside the chosen length filter.")

	shortKey := strconv.FormatUint(short, 10)
	inBucket := url.Values{"length": {"short"}}
	inBucket.Add("question_ids[]", shortKey)
	inBucket.Set("seq_"+shortKey, "1")
	resp, body = postForm(t, composeURL(ts, quiz), inBucket, ck)
	if resp.StatusCode != http.StatusOK || !decodeEnv(t, body).OK {
		t.Fatalf("in-bucket compose = %d: %s", resp.StatusCode, body)
	}

	// no length key at all → no bucket check (the long one is accepted)
	noFilter := url.Values{}
	noFilter.Add("question_ids[]", longKey)
	noFilter.Set("seq_"+longKey, "2")
	resp, body = postForm(t, composeURL(ts, quiz), noFilter, ck)
	if resp.StatusCode != http.StatusOK || !decodeEnv(t, body).OK {
		t.Fatalf("unfiltered compose = %d: %s", resp.StatusCode, body)
	}

	bad := url.Values{"length": {"bogus"}, "question_ids[]": {shortKey}}
	resp, body = postForm(t, composeURL(ts, quiz), bad, ck)
	assertFail(t, resp, body, http.StatusBadRequest, "VALIDATION", "Invalid length filter.")
}

// getWithHeader issues a GET with one extra header (no redirect following).
func getWithHeader(t *testing.T, endpoint, key, value string, cookies ...*http.Cookie) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set(key, value)
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", endpoint, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, string(raw)
}

// The share URL (detail modal) and the QR payload take their scheme from
// X-Forwarded-Proto behind a TLS-terminating proxy; plain local requests
// keep the byte-identical http:// default.
func TestJoinURLSchemeFollowsForwardedProto(t *testing.T) {
	ts, pool, _, _ := quizFixture(t)
	ck := guruLogin(t, ts)

	quiz := createQuiz(t, ts, ck, quizForm("Scheme quiz"))
	var code string
	if err := pool.QueryRow(`SELECT code FROM quizzes WHERE id = ?`, quiz).Scan(&code); err != nil {
		t.Fatalf("quiz code: %v", err)
	}
	host := strings.TrimPrefix(ts.URL, "http://")
	joinHTTP := "http://" + host + "/quiz/" + code
	joinHTTPS := "https://" + host + "/quiz/" + code

	detail := fmt.Sprintf("%s/teacher/quiz/%d", ts.URL, quiz)
	resp, body := getWith(t, detail, ck)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("detail = %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, joinHTTP) {
		t.Errorf("detail page missing local join URL %q", joinHTTP)
	}
	resp, body = getWithHeader(t, detail, "X-Forwarded-Proto", "https", ck)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("detail over proxy = %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, joinHTTPS) {
		t.Errorf("proxied detail page missing https join URL %q", joinHTTPS)
	}
	if strings.Contains(body, joinHTTP) {
		t.Errorf("proxied detail page still carries %q", joinHTTP)
	}

	// QR payload: same input encodes to identical bytes, so a scheme flip
	// is observable as a different PNG (no QR decoder needed)
	qr := fmt.Sprintf("%s/teacher/quiz/%d/qr", ts.URL, quiz)
	if resp, body := getWith(t, qr, ck); resp.StatusCode != http.StatusOK || !strings.HasPrefix(body, "\x89PNG") {
		t.Fatalf("plain qr = %d", resp.StatusCode)
	} else {
		plain := body
		if resp2, proxied := getWithHeader(t, qr, "X-Forwarded-Proto", "https", ck); resp2.StatusCode != http.StatusOK {
			t.Fatalf("proxied qr = %d", resp2.StatusCode)
		} else if proxied == plain {
			t.Error("QR payload ignored X-Forwarded-Proto")
		}
		// an unrecognized first value falls back to the local scheme
		if resp3, fell := getWithHeader(t, qr, "X-Forwarded-Proto", "gopher", ck); resp3.StatusCode != http.StatusOK {
			t.Fatalf("fallback qr = %d", resp3.StatusCode)
		} else if fell != plain {
			t.Error("invalid X-Forwarded-Proto changed the QR payload")
		}
	}
}

// RemoveQuestion: body-less delete of a composed row while nonaktif drops
// the row and the cache mirrors; every guard is classified exactly.
func TestRemoveQuestion(t *testing.T) {
	ts, pool, store, _ := quizFixture(t)
	ck := guruLogin(t, ts)

	quiz := createQuiz(t, ts, ck, quizForm("Removable quiz"))
	q1 := addQuestion(t, ts, pool, ck, "Remove me", "pg", []string{"A", "B"}, []int{0})
	q2 := addQuestion(t, ts, pool, ck, "Keep me", "pg", []string{"A", "B"}, []int{1})
	compose(t, ts, ck, quiz, q1, q2)

	// invalid path params → 400
	resp, body := postForm(t,
		fmt.Sprintf("%s/teacher/quiz/abc/questions/%d/delete", ts.URL, q1), url.Values{}, ck)
	assertFail(t, resp, body, http.StatusBadRequest, "VALIDATION", "Invalid quiz id.")

	resp, body = postForm(t,
		fmt.Sprintf("%s/teacher/quiz/%d/questions/x/delete", ts.URL, quiz), url.Values{}, ck)
	assertFail(t, resp, body, http.StatusBadRequest, "VALIDATION", "Invalid question id.")

	resp, body = postForm(t, removeURL(ts, quiz, 0), url.Values{}, ck)
	assertFail(t, resp, body, http.StatusBadRequest, "VALIDATION", "Invalid question id.")

	resp, body = postForm(t,
		fmt.Sprintf("%s/teacher/quiz/0/questions/%d/delete", ts.URL, q1), url.Values{}, ck)
	assertFail(t, resp, body, http.StatusBadRequest, "VALIDATION", "Invalid quiz id.")

	// missing quiz → 404
	resp, body = postForm(t, removeURL(ts, 999999, q1), url.Values{}, ck)
	assertFail(t, resp, body, http.StatusNotFound, "NOT_FOUND", "Quiz not found.")

	// happy path — body-less POST like the data-post frontend pattern
	store.Set(cache.QuizSetKey(quiz), "seed", time.Minute)
	store.Set(cache.QuizStateKey(quiz), "seed", time.Minute)
	resp, body = postRaw(t, removeURL(ts, quiz, q1), "", "", ck)
	if resp.StatusCode != http.StatusOK || !decodeEnv(t, body).OK {
		t.Fatalf("remove = %d: %s", resp.StatusCode, body)
	}
	seqs := composedSeqs(t, pool, quiz)
	if _, ok := seqs[q1]; ok {
		t.Error("removed question still composed")
	}
	if _, ok := seqs[q2]; !ok {
		t.Error("second question was removed too")
	}
	for _, key := range []string{cache.QuizSetKey(quiz), cache.QuizStateKey(quiz)} {
		if _, ok := store.Get(key); ok {
			t.Errorf("%s survived remove", key)
		}
	}

	// remove again → 404 not composed
	resp, body = postRaw(t, removeURL(ts, quiz, q1), "", "", ck)
	assertFail(t, resp, body, http.StatusNotFound, "NOT_FOUND", "Question is not in this quiz.")

	// activation locks removal — exact existing locked message
	if resp, body := setStatus(t, ts, ck, quiz, "aktif"); resp.StatusCode != http.StatusOK {
		t.Fatalf("activate = %d: %s", resp.StatusCode, body)
	}
	resp, body = postRaw(t, removeURL(ts, quiz, q2), "", "", ck)
	assertFail(t, resp, body, http.StatusConflict, "CONFLICT",
		"Quiz has participants or is active — editing is locked.")
	if _, ok := composedSeqs(t, pool, quiz)[q2]; !ok {
		t.Error("active quiz lost its question")
	}
}
