package integration

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"quiz/internal/cache"
	"quiz/internal/config"
	"quiz/internal/handlers"
	"quiz/internal/testutil"
)

// envelope is the JSON contract from spec §2.
type envelope struct {
	OK      bool            `json:"ok"`
	Data    json.RawMessage `json:"data"`
	Error   string          `json:"error"`
	Message string          `json:"message"`
}

func decodeEnv(t *testing.T, body string) envelope {
	t.Helper()
	var env envelope
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("decode envelope %q: %v", body, err)
	}
	return env
}

// quizFixture resets the quiz-side tables and serves every route from
// cmd/server (mirrored by newAuthServer).
func quizFixture(t *testing.T) (*httptest.Server, *sql.DB, *cache.Store, *config.Config) {
	t.Helper()
	pool := testutil.DB(t)
	testutil.Migrate(t, pool)
	testutil.Reset(t, pool, "sessions", "users", "password_resets",
		"quizzes", "questions", "quiz_questions", "participants",
		"answers", "anti_cheat_events")
	store := cache.New()
	cfg := testutil.Config(t)
	return newAuthServer(t, pool, store, cfg), pool, store, cfg
}

// getWith issues a GET without following redirects.
func getWith(t *testing.T, endpoint string, cookies ...*http.Cookie) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
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
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, string(body)
}

// postJSON issues a JSON POST without following redirects.
func postJSON(t *testing.T, endpoint string, payload any, cookies ...*http.Cookie) (*http.Response, string) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
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
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, string(body)
}

// quizForm is a fully valid settings payload (parseQuizSettings requires
// every enum and number field).
func quizForm(title string) url.Values {
	return url.Values{
		"judul":                {title},
		"timer_type":           {"global"},
		"timer_on":             {"1"},
		"total_seconds":        {"600"},
		"per_question_seconds": {"300"},
		"join_mode":            {"open"},
		"max_attempts":         {"1"},
		"question_review":      {"none"},
		"show_correct_wrong":   {"1"},
		"show_final_score":     {"1"},
	}
}

func createQuiz(t *testing.T, ts *httptest.Server, ck *http.Cookie, form url.Values) uint64 {
	t.Helper()
	resp, body := postForm(t, ts.URL+"/teacher/quiz/new", form, ck)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create quiz = %d: %s", resp.StatusCode, body)
	}
	env := decodeEnv(t, body)
	if !env.OK {
		t.Fatalf("create quiz envelope: %s", body)
	}
	var data struct {
		ID uint64 `json:"id"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil || data.ID == 0 {
		t.Fatalf("create quiz data %q: %v", env.Data, err)
	}
	return data.ID
}

// postQuestion posts the bank form raw; callers assert the status themselves.
func postQuestion(t *testing.T, ts *httptest.Server, ck *http.Cookie,
	teks, qtype string, opts []string, correct []int,
) (*http.Response, string) {
	t.Helper()
	form := url.Values{"teks": {teks}, "type": {qtype}}
	for _, o := range opts {
		form.Add("options[]", o)
	}
	for _, ci := range correct {
		form.Add("correct[]", strconv.Itoa(ci))
	}
	return postForm(t, ts.URL+"/teacher/questions", form, ck)
}

// addQuestion asserts a successful insert and returns the new bank id.
func addQuestion(t *testing.T, ts *httptest.Server, pool *sql.DB, ck *http.Cookie,
	teks, qtype string, opts []string, correct []int,
) uint64 {
	t.Helper()
	resp, body := postQuestion(t, ts, ck, teks, qtype, opts, correct)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("add question %q = %d: %s", teks, resp.StatusCode, body)
	}
	var id uint64
	if err := pool.QueryRow(`SELECT MAX(id) FROM questions`).Scan(&id); err != nil {
		t.Fatalf("question id: %v", err)
	}
	return id
}

// compose picks questions into the quiz (seq_<id> fields, spec §6.8).
func compose(t *testing.T, ts *httptest.Server, ck *http.Cookie, quizID uint64, qids ...uint64) {
	t.Helper()
	form := url.Values{"length": {""}}
	for i, q := range qids {
		key := strconv.FormatUint(q, 10)
		form.Add("question_ids[]", key)
		form.Set("seq_"+key, strconv.Itoa(i+1))
	}
	resp, body := postForm(t,
		fmt.Sprintf("%s/teacher/quiz/%d/questions", ts.URL, quizID), form, ck)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("compose = %d: %s", resp.StatusCode, body)
	}
}

func setStatus(t *testing.T, ts *httptest.Server, ck *http.Cookie, quizID uint64, status string) (*http.Response, string) {
	t.Helper()
	return postForm(t,
		fmt.Sprintf("%s/teacher/quiz/%d/status", ts.URL, quizID),
		url.Values{"status": {status}}, ck)
}

// --- spec §9 quiz_crud cases ----------------------------------------------

// Two-tab race: after one tab activates, the other tab's settings save must
// hit the WHERE-status guard → 409 with the exact locked message.
func TestEditLockedAfterActivation(t *testing.T) {
	ts, pool, _, _ := quizFixture(t)
	ck := guruLogin(t, ts)

	qid := createQuiz(t, ts, ck, quizForm("Race quiz"))
	q := addQuestion(t, ts, pool, ck, "What is 2+2?", "pg", []string{"3", "4"}, []int{1})
	compose(t, ts, ck, qid, q)

	resp, body := setStatus(t, ts, ck, qid, "aktif")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("activate = %d: %s", resp.StatusCode, body)
	}

	resp, body = postForm(t,
		fmt.Sprintf("%s/teacher/quiz/%d/edit", ts.URL, qid),
		quizForm("Changed in the other tab"), ck)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("edit after activate = %d, want 409 (%s)", resp.StatusCode, body)
	}
	env := decodeEnv(t, body)
	if env.Error != "CONFLICT" {
		t.Errorf("error code = %q, want CONFLICT", env.Error)
	}
	const want = "Kuis memiliki peserta atau sedang aktif — pengubahan dikunci."
	if env.Message != want {
		t.Errorf("message = %q, want %q", env.Message, want)
	}
}

// Delete quiz with participants → 409; without → removed with its questions.
func TestDeleteQuizGuardsParticipants(t *testing.T) {
	ts, pool, _, _ := quizFixture(t)
	ck := guruLogin(t, ts)

	qid := createQuiz(t, ts, ck, quizForm("Doomed quiz"))
	seedUser(t, pool, "dee", false)
	resp, body := postJSON(t,
		fmt.Sprintf("%s/teacher/quiz/%d/participants/add", ts.URL, qid),
		map[string]string{"username": "dee"}, ck)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("add participant = %d: %s", resp.StatusCode, body)
	}

	resp, body = postForm(t,
		fmt.Sprintf("%s/teacher/quiz/%d/delete", ts.URL, qid), url.Values{}, ck)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("delete with participants = %d, want 409 (%s)", resp.StatusCode, body)
	}
	if env := decodeEnv(t, body); env.Error != "CONFLICT" {
		t.Errorf("error = %q, want CONFLICT", env.Error)
	}
	var still int
	if err := pool.QueryRow(`SELECT COUNT(*) FROM quizzes WHERE id = ?`, qid).Scan(&still); err != nil || still != 1 {
		t.Errorf("quiz rows = %d (err %v), want 1", still, err)
	}

	// a fresh quiz with no participants deletes cleanly, questions included
	free := createQuiz(t, ts, ck, quizForm("Free quiz"))
	q := addQuestion(t, ts, pool, ck, "Delete me alongside", "pg", []string{"a", "b"}, []int{0})
	compose(t, ts, ck, free, q)
	resp, body = postForm(t,
		fmt.Sprintf("%s/teacher/quiz/%d/delete", ts.URL, free), url.Values{}, ck)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete free quiz = %d: %s", resp.StatusCode, body)
	}
	var quizzes, qq int
	pool.QueryRow(`SELECT COUNT(*) FROM quizzes WHERE id = ?`, free).Scan(&quizzes)
	pool.QueryRow(`SELECT COUNT(*) FROM quiz_questions WHERE quiz_id = ?`, free).Scan(&qq)
	if quizzes != 0 || qq != 0 {
		t.Errorf("after delete: quizzes=%d quiz_questions=%d, want 0/0", quizzes, qq)
	}
}

// Activation with zero selected questions → 400 VALIDATION, exact message.
func TestActivateWithoutQuestions(t *testing.T) {
	ts, _, _, _ := quizFixture(t)
	ck := guruLogin(t, ts)

	qid := createQuiz(t, ts, ck, quizForm("Empty quiz"))
	resp, body := setStatus(t, ts, ck, qid, "aktif")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("activate empty = %d, want 400 (%s)", resp.StatusCode, body)
	}
	env := decodeEnv(t, body)
	if env.Error != "VALIDATION" || env.Message != "Tambahkan setidaknya satu pertanyaan." {
		t.Errorf("envelope = %+v, want VALIDATION %q", env, "Tambahkan setidaknya satu pertanyaan.")
	}
	// still inactive
	var status string
	if err := testutil.DB(t).QueryRow(`SELECT status FROM quizzes WHERE id = ?`, qid).Scan(&status); err != nil {
		t.Fatalf("status: %v", err)
	}
	if status != "nonaktif" {
		t.Errorf("status = %q, want nonaktif", status)
	}
}

// Sequential order chosen → shuffle_options forced to 0 server-side at
// create AND edit (spec §6.7 / §9).
func TestSequentialForcesShuffleOptionsOff(t *testing.T) {
	ts, pool, _, _ := quizFixture(t)
	ck := guruLogin(t, ts)

	form := quizForm("Sequential quiz")
	form.Set("shuffle_questions", "0")
	form.Set("shuffle_options", "1") // teacher ticked it anyway
	qid := createQuiz(t, ts, ck, form)

	read := func() (sq, so bool) {
		t.Helper()
		if err := pool.QueryRow(`SELECT shuffle_questions, shuffle_options FROM quizzes WHERE id = ?`, qid).
			Scan(&sq, &so); err != nil {
			t.Fatalf("shuffle flags: %v", err)
		}
		return sq, so
	}
	if sq, so := read(); sq || so {
		t.Errorf("after create: shuffle_questions=%v shuffle_options=%v, want false/false", sq, so)
	}

	// edit path re-asserts the force
	edit := quizForm("Sequential quiz v2")
	edit.Set("shuffle_questions", "0")
	edit.Set("shuffle_options", "1")
	resp, body := postForm(t,
		fmt.Sprintf("%s/teacher/quiz/%d/edit", ts.URL, qid), edit, ck)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("edit = %d: %s", resp.StatusCode, body)
	}
	if sq, so := read(); sq || so {
		t.Errorf("after edit: shuffle_questions=%v shuffle_options=%v, want false/false", sq, so)
	}
}

// Unique-code collision: first generated code already exists (forced by a
// direct DB insert + stub) → bounded retry succeeds with the next code;
// exhausted retries → 500 SERVER_ERROR (spec §7).
func TestCodeCollisionRetriesThenSucceeds(t *testing.T) {
	pool := testutil.DB(t)
	testutil.Migrate(t, pool)
	testutil.Reset(t, pool, "sessions", "users", "password_resets",
		"quizzes", "questions", "quiz_questions", "participants")
	store := cache.New()
	cfg := testutil.Config(t)

	// blocker row occupies the first generated code
	if _, err := pool.Exec(`INSERT INTO quizzes (code, judul, timer_type) VALUES ('AAAA11', 'Blocker', 'global')`); err != nil {
		t.Fatalf("blocker insert: %v", err)
	}

	calls := 0
	teach := &handlers.Teacher{DB: pool, Store: store, NewCode: func() (string, error) {
		calls++
		if calls == 1 {
			return "AAAA11", nil
		}
		return "BBBB22", nil
	}}
	ts, _ := newServerWith(t, pool, store, cfg, teach)
	ck := guruLogin(t, ts)

	qid := createQuiz(t, ts, ck, quizForm("Collision quiz"))
	if calls != 2 {
		t.Errorf("generator called %d times, want 2 (collision + retry)", calls)
	}
	var code string
	if err := pool.QueryRow(`SELECT code FROM quizzes WHERE id = ?`, qid).Scan(&code); err != nil {
		t.Fatalf("code: %v", err)
	}
	if code != "BBBB22" {
		t.Errorf("code = %q, want BBBB22", code)
	}
	var blockers int
	pool.QueryRow(`SELECT COUNT(*) FROM quizzes WHERE code = 'AAAA11'`).Scan(&blockers)
	if blockers != 1 {
		t.Errorf("blocker rows = %d, want 1 (collision must not insert)", blockers)
	}

	// exhaustion: every candidate collides → bounded retry ends in 500
	dup := 0
	teach2 := &handlers.Teacher{DB: pool, Store: store, NewCode: func() (string, error) {
		dup++
		return "AAAA11", nil
	}}
	ts2, _ := newServerWith(t, pool, store, cfg, teach2)
	ck2 := guruLogin(t, ts2)
	resp, body := postForm(t, ts2.URL+"/teacher/quiz/new", quizForm("Doomed"), ck2)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("exhausted collision = %d, want 500 (%s)", resp.StatusCode, body)
	}
	if env := decodeEnv(t, body); env.Error != "SERVER_ERROR" {
		t.Errorf("error = %q, want SERVER_ERROR", env.Error)
	}
	// the retry actually happened (more than one candidate generated) and
	// no doomed row was ever committed
	if dup < 2 {
		t.Errorf("generator called %d times, want at least 2 (bounded retry)", dup)
	}
	var doomed int
	pool.QueryRow(`SELECT COUNT(*) FROM quizzes WHERE judul = 'Doomed'`).Scan(&doomed)
	if doomed != 0 {
		t.Errorf("doomed rows = %d, want 0 (failed create must roll back)", doomed)
	}
}

// Bank filter buckets by CHAR_LENGTH (short <80, medium 80–200, long >200),
// characters not bytes — the 79-rune multi-byte question would land in
// medium under a LENGTH() implementation.
func TestBankLengthFilterBuckets(t *testing.T) {
	ts, pool, _, _ := quizFixture(t)
	ck := guruLogin(t, ts)

	insert := func(marker string, runes int) {
		t.Helper()
		teks := marker + strings.Repeat("a", runes-len([]rune(marker)))
		if _, err := pool.Exec(`INSERT INTO questions (teks, type) VALUES (?, 'essay')`, teks); err != nil {
			t.Fatalf("insert %s: %v", marker, err)
		}
	}
	insert("MkS50", 50)
	insert("MkS79", 79)
	// multi-byte: 3-rune marker + 76 é = 79 runes / 155 bytes — a byte-based
	// LENGTH() implementation would misclassify this as medium
	if _, err := pool.Exec(`INSERT INTO questions (teks, type) VALUES (?, 'essay')`,
		"u79"+strings.Repeat("é", 76)); err != nil {
		t.Fatalf("unicode insert: %v", err)
	}
	insert("MkM80", 80)
	insert("MkM200", 200)
	insert("MkL201", 201)

	get := func(length string) (int, string) {
		t.Helper()
		endpoint := ts.URL + "/teacher/questions"
		if length != "" {
			endpoint += "?length=" + length
		}
		resp, body := getWith(t, endpoint, ck)
		return resp.StatusCode, body
	}

	if code, body := get("short"); code != 200 {
		t.Fatalf("short = %d: %s", code, body)
	} else {
		for _, want := range []string{"MkS50", "MkS79", "u79"} {
			if !strings.Contains(body, want) {
				t.Errorf("short bucket missing %q", want)
			}
		}
		for _, avoid := range []string{"MkM80", "MkM200", "MkL201"} {
			if strings.Contains(body, avoid) {
				t.Errorf("short bucket wrongly contains %q", avoid)
			}
		}
	}
	if code, body := get("medium"); code != 200 {
		t.Fatalf("medium = %d", code)
	} else {
		for _, want := range []string{"MkM80", "MkM200"} {
			if !strings.Contains(body, want) {
				t.Errorf("medium bucket missing %q", want)
			}
		}
		for _, avoid := range []string{"MkS50", "MkS79", "u79", "MkL201"} {
			if strings.Contains(body, avoid) {
				t.Errorf("medium bucket wrongly contains %q", avoid)
			}
		}
	}
	if code, body := get("long"); code != 200 {
		t.Fatalf("long = %d", code)
	} else {
		if !strings.Contains(body, "MkL201") || strings.Contains(body, "MkM200") {
			t.Errorf("long bucket wrong contents")
		}
	}
	if code, _ := get(""); code != 200 {
		t.Errorf("unfiltered bank = %d, want 200", code)
	}
	if code, _ := get("bogus"); code != http.StatusBadRequest {
		t.Errorf("invalid filter = %d, want 400", code)
	}
}

// The length-filter links on the question bank must carry the current
// search term — list.go contract: "the search form preserves filters,
// filter links preserve q". Drops q → this fails.
func TestQuestionsFilterLinksPreserveQ(t *testing.T) {
	ts, _, _, _ := quizFixture(t)
	ck := guruLogin(t, ts)

	resp, body := getWith(t, ts.URL+"/teacher/questions?q=preserve", ck)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET questions?q=preserve = %d: %s", resp.StatusCode, body)
	}

	// isolate the length-filter button group
	start := strings.Index(body, `aria-label="Filter panjang pertanyaan"`)
	if start < 0 {
		t.Fatal("length-filter group missing from page")
	}
	end := strings.Index(body[start:], "</div>")
	if end < 0 {
		t.Fatal("length-filter group not closed")
	}
	group := body[start : start+end]

	var hrefs []string
	for rest := group; ; {
		i := strings.Index(rest, `href="`)
		if i < 0 {
			break
		}
		rest = rest[i+len(`href="`):]
		j := strings.Index(rest, `"`)
		if j < 0 {
			break
		}
		hrefs = append(hrefs, rest[:j])
		rest = rest[j+1:]
	}
	if len(hrefs) != 4 {
		t.Fatalf("filter links = %d, want 4\ngroup: %s", len(hrefs), group)
	}
	for _, h := range hrefs {
		if !strings.Contains(h, "q=preserve") {
			t.Errorf("filter link %q does not preserve q", h)
		}
	}
	// the three bucket links keep their own param alongside q
	for _, want := range []string{"length=short", "length=medium", "length=long"} {
		found := false
		for _, h := range hrefs {
			if strings.Contains(h, want) && strings.Contains(h, "q=preserve") {
				found = true
			}
		}
		if !found {
			t.Errorf("no filter link carries both %q and q=preserve", want)
		}
	}
}

// QR endpoint: PNG bytes and correct content type; unknown id → 404.
func TestQuizQRCode(t *testing.T) {
	ts, _, _, _ := quizFixture(t)
	ck := guruLogin(t, ts)

	qid := createQuiz(t, ts, ck, quizForm("QR quiz"))
	resp, body := getWith(t, fmt.Sprintf("%s/teacher/quiz/%d/qr", ts.URL, qid), ck)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("qr = %d: %s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/png" {
		t.Errorf("content-type = %q, want image/png", ct)
	}
	if !strings.HasPrefix(body, "\x89PNG") {
		t.Errorf("body does not start with PNG magic")
	}

	resp, _ = getWith(t, fmt.Sprintf("%s/teacher/quiz/%d/qr", ts.URL, 999999), ck)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown quiz qr = %d, want 404", resp.StatusCode)
	}
}

// participants/add matrix: unknown → 404, duplicate → 409, success keeps
// the join-mode initial status (open → registered, approve → pending).
func TestAddParticipantMatrix(t *testing.T) {
	ts, pool, _, _ := quizFixture(t)
	ck := guruLogin(t, ts)
	uid := seedUser(t, pool, "aya", false)

	openID := createQuiz(t, ts, ck, quizForm("Open quiz"))
	addURL := func(qid uint64) string {
		return fmt.Sprintf("%s/teacher/quiz/%d/participants/add", ts.URL, qid)
	}

	// unknown user → 404
	resp, body := postJSON(t, addURL(openID), map[string]string{"username": "ghost"}, ck)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown user = %d, want 404 (%s)", resp.StatusCode, body)
	}
	if env := decodeEnv(t, body); env.Error != "NOT_FOUND" {
		t.Errorf("error = %q, want NOT_FOUND", env.Error)
	}

	// success → registered under open mode
	resp, body = postJSON(t, addURL(openID), map[string]string{"username": "aya"}, ck)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("add aya = %d: %s", resp.StatusCode, body)
	}
	var status string
	if err := pool.QueryRow(`SELECT status FROM participants WHERE quiz_id = ? AND user_id = ?`,
		openID, uid).Scan(&status); err != nil {
		t.Fatalf("participant row: %v", err)
	}
	if status != "registered" {
		t.Errorf("open-mode status = %q, want registered", status)
	}

	// duplicate → 409
	resp, body = postJSON(t, addURL(openID), map[string]string{"username": "aya"}, ck)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate = %d, want 409 (%s)", resp.StatusCode, body)
	}
	if env := decodeEnv(t, body); env.Error != "CONFLICT" {
		t.Errorf("error = %q, want CONFLICT", env.Error)
	}

	// approve mode → pending
	approve := quizForm("Approval quiz")
	approve.Set("join_mode", "approve")
	approveID := createQuiz(t, ts, ck, approve)
	resp, body = postJSON(t, addURL(approveID), map[string]string{"username": "aya"}, ck)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("add to approve quiz = %d: %s", resp.StatusCode, body)
	}
	if err := pool.QueryRow(`SELECT status FROM participants WHERE quiz_id = ? AND user_id = ?`,
		approveID, uid).Scan(&status); err != nil {
		t.Fatalf("participant row: %v", err)
	}
	if status != "pending" {
		t.Errorf("approve-mode status = %q, want pending", status)
	}
}

// Question validation (spec §9): PG exactly 1 correct; multi ≥2 options and
// ≥1 correct; <2 options → 400; valid rows save.
func TestQuestionValidation(t *testing.T) {
	ts, _, _, _ := quizFixture(t)
	ck := guruLogin(t, ts)

	cases := []struct {
		name    string
		qtype   string
		opts    []string
		correct []int
		want    int
	}{
		{"pg one option", "pg", []string{"only"}, []int{0}, 400},
		{"pg two correct", "pg", []string{"a", "b", "c"}, []int{0, 1}, 400},
		{"pg no correct", "pg", []string{"a", "b"}, nil, 400},
		{"pg valid", "pg", []string{"a", "b"}, []int{1}, 200},
		{"multi one option", "multi", []string{"only"}, []int{0}, 400},
		{"multi no correct", "multi", []string{"a", "b"}, nil, 400},
		{"multi valid", "multi", []string{"a", "b", "c"}, []int{0, 2}, 200},
		{"essay without key", "essay", nil, nil, 200},
		{"unknown type", "truefalse", []string{"a", "b"}, []int{0}, 400},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp, body := postQuestion(t, ts, ck,
				fmt.Sprintf("Validation question %d (%s)", i, c.name), c.qtype, c.opts, c.correct)
			if resp.StatusCode != c.want {
				t.Errorf("= %d, want %d (%s)", resp.StatusCode, c.want, body)
			}
			if c.want == 400 {
				if env := decodeEnv(t, body); env.Error != "VALIDATION" {
					t.Errorf("error = %q, want VALIDATION", env.Error)
				}
			}
		})
	}
}

// Class delete in use → 409; free class → removed (spec §9 CRUD).
func TestClassDeleteGuard(t *testing.T) {
	ts, pool, _, _ := quizFixture(t)
	ck := guruLogin(t, ts)

	resp, body := postForm(t, ts.URL+"/teacher/classes", url.Values{"nama": {"Tokyo"}}, ck)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create class = %d: %s", resp.StatusCode, body)
	}
	var classID uint64
	if err := pool.QueryRow(`SELECT id FROM ref_kelas WHERE nama = 'Tokyo' ORDER BY id DESC LIMIT 1`).
		Scan(&classID); err != nil {
		t.Fatalf("class id: %v", err)
	}
	if _, err := pool.Exec(
		`INSERT INTO users (username, email, password_hash, nama_lengkap, kelas_id, jurusan_id)
		 VALUES ('clsuser', 'clsuser@test.example', 'x', 'Class User', ?, 1)`, classID,
	); err != nil {
		t.Fatalf("user insert: %v", err)
	}

	resp, body = postForm(t,
		fmt.Sprintf("%s/teacher/classes/%d/delete", ts.URL, classID), url.Values{}, ck)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("delete in-use class = %d, want 409 (%s)", resp.StatusCode, body)
	}

	resp, body = postForm(t, ts.URL+"/teacher/classes", url.Values{"nama": {"Osaka"}}, ck)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create free class = %d: %s", resp.StatusCode, body)
	}
	var freeID uint64
	pool.QueryRow(`SELECT id FROM ref_kelas WHERE nama = 'Osaka' ORDER BY id DESC LIMIT 1`).Scan(&freeID)
	resp, body = postForm(t,
		fmt.Sprintf("%s/teacher/classes/%d/delete", ts.URL, freeID), url.Values{}, ck)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete free class = %d: %s", resp.StatusCode, body)
	}
	var exists bool
	pool.QueryRow(`SELECT EXISTS(SELECT 1 FROM ref_kelas WHERE id = ?)`, freeID).Scan(&exists)
	if exists {
		t.Error("free class still present after delete")
	}
}

// Every teacher page renders through the real templates (catches parse
// errors and missing view data), and the detail page carries its tabs.
func TestTeacherPagesRender(t *testing.T) {
	ts, pool, _, _ := quizFixture(t)
	ck := guruLogin(t, ts)

	qid := createQuiz(t, ts, ck, quizForm("Render quiz"))
	q := addQuestion(t, ts, pool, ck, "Rendered question?", "pg", []string{"yes", "no"}, []int{0})
	compose(t, ts, ck, qid, q)
	reg := registerUser(t, ts, "editpref", "editpref@example.test", "Edit Pref", "secret123")
	stuID := userOf(t, ts, reg)

	cases := []struct {
		path string
		want []string
	}{
		{"/teacher", []string{"Dasbor", "Render quiz", "Kuis baru"}},
		{"/teacher/classes", []string{"Kelas", `href="/teacher/classes/new"`}},
		{"/teacher/majors", []string{"Jurusan", `href="/teacher/majors/new"`}},
		{"/teacher/classes/new", []string{
			"Kelas baru", "Nama kelas", `action="/teacher/classes"`,
			`data-next="/teacher/classes"`, "Batal",
		}},
		{"/teacher/majors/new", []string{
			"Jurusan baru", "Nama jurusan", `action="/teacher/majors"`,
			`data-next="/teacher/majors"`, "Batal",
		}},
		{fmt.Sprintf("/teacher/students/%d/edit", stuID), []string{
			"Ubah akun murid", "editpref", `action="/teacher/students/`,
			`data-next="/teacher/students"`, `aria-current="page">Ubah</li>`,
		}},
		{"/teacher/questions", []string{"Bank pertanyaan", "Rendered question?", "Tambah pertanyaan"}},
		{"/teacher/questions/new", []string{"Pertanyaan baru", `action="/teacher/questions"`, "Batal"}},
		{fmt.Sprintf("/teacher/questions/%d/edit", q), []string{
			"Ubah pertanyaan", "Rendered question?", fmt.Sprintf(`action="/teacher/questions/%d/edit`, q),
		}},
		{"/teacher/quiz", []string{"Kuis", "Render quiz"}},
		{"/teacher/quiz/new", []string{"Kuis baru", "Buat kuis"}},
		{fmt.Sprintf("/teacher/quiz/%d", qid), []string{
			"Render quiz", "Pertanyaan (1)", "Pengaturan", "Peserta (0)", "Hasil",
			"Tambahkan dari bank pertanyaan", "/qr",
		}},
	}
	for _, c := range cases {
		resp, body := getWith(t, ts.URL+c.path, ck)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d: %s", c.path, resp.StatusCode, body)
			continue
		}
		for _, want := range c.want {
			if !strings.Contains(body, want) {
				t.Errorf("GET %s missing %q", c.path, want)
			}
		}
	}

	// edit form page for an unknown id → 404 (spec §8 style)
	if resp, body := getWith(t, fmt.Sprintf("%s/teacher/questions/%d/edit", ts.URL, 999999), ck); resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET unknown question edit = %d, want 404 (%s)", resp.StatusCode, body)
	}
	if resp, body := getWith(t, fmt.Sprintf("%s/teacher/students/%d/edit", ts.URL, 999999), ck); resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET unknown student edit = %d, want 404 (%s)", resp.StatusCode, body)
	}
}
