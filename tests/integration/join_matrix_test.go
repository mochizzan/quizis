package integration

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"quiz/internal/handlers"
	mw "quiz/internal/middleware"
)

// contains is the plain substring assert used on rendered pages.
func contains(s, sub string) bool { return strings.Contains(s, sub) }

// --- helpers shared by the Step 10 test files ------------------------------

// studentCookie seeds a murid user and opens a session for it.
func studentCookie(t *testing.T, pool *sql.DB, username string) *http.Cookie {
	t.Helper()
	uid := seedUser(t, pool, username, false)
	sid := newSession(t, pool, uid, "murid")
	return &http.Cookie{Name: mw.CookieName, Value: sid}
}

// joinCode reads a quiz's join code.
func joinCode(t *testing.T, pool *sql.DB, quizID uint64) string {
	t.Helper()
	var code string
	if err := pool.QueryRow(`SELECT code FROM quizzes WHERE id = ?`, quizID).Scan(&code); err != nil {
		t.Fatalf("quiz code: %v", err)
	}
	return code
}

// setQuizStatusSQL flips the status directly — berjalan/selesai are not
// reachable through the Step 8 status route.
func setQuizStatusSQL(t *testing.T, pool *sql.DB, quizID uint64, status string) {
	t.Helper()
	if _, err := pool.Exec(`UPDATE quizzes SET status = ? WHERE id = ?`, status, quizID); err != nil {
		t.Fatalf("set quiz status: %v", err)
	}
}

// participantRowFor returns the newest attempt's (id, status, attempt_no).
func participantRowFor(t *testing.T, pool *sql.DB, quizID, userID uint64) (uint64, string, uint8) {
	t.Helper()
	var pid uint64
	var status string
	var attemptNo uint8
	err := pool.QueryRow(`SELECT id, status, attempt_no FROM participants
		WHERE quiz_id = ? AND user_id = ? ORDER BY attempt_no DESC LIMIT 1`,
		quizID, userID).Scan(&pid, &status, &attemptNo)
	if err != nil {
		t.Fatalf("participant row: %v", err)
	}
	return pid, status, attemptNo
}

// userOf extracts the user id from a session cookie (via the /me seam).
func userOf(t *testing.T, ts *httptest.Server, ck *http.Cookie) uint64 {
	t.Helper()
	resp, body := getWith(t, ts.URL+"/me", ck)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/me = %d: %s", resp.StatusCode, body)
	}
	parts := strings.SplitN(strings.TrimSpace(body), ":", 2)
	if len(parts) != 2 {
		t.Fatalf("/me body %q: want id:role", body)
	}
	id, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		t.Fatalf("/me body %q: %v", body, err)
	}
	return id
}

// activeQuiz builds a composed quiz with one bank question and sets it to the
// requested status (nonaktif → as-created; aktif → activated; berjalan/selesai
// → activated then flipped with SQL). Returns (quizID, code).
func activeQuiz(t *testing.T, ts *httptest.Server, pool *sql.DB, ck *http.Cookie,
	title, status, joinMode string,
) (uint64, string) {
	t.Helper()
	form := quizForm(title)
	form.Set("join_mode", joinMode)
	quizID := createQuiz(t, ts, ck, form)
	q := addQuestion(t, ts, pool, ck, "Q for "+title, "pg", []string{"One", "Two"}, []int{0})
	compose(t, ts, ck, quizID, q)
	if status != "nonaktif" {
		if resp, body := setStatus(t, ts, ck, quizID, "aktif"); resp.StatusCode != http.StatusOK {
			t.Fatalf("activate %q = %d: %s", title, resp.StatusCode, body)
		} else if env := decodeEnv(t, body); !env.OK {
			t.Fatalf("activate %q envelope: %s", title, body)
		}
		if status != "aktif" {
			setQuizStatusSQL(t, pool, quizID, status)
		}
	}
	return quizID, joinCode(t, pool, quizID)
}

// postJoin sends POST /join {code}.
func postJoin(t *testing.T, ts *httptest.Server, code string, ck *http.Cookie) (*http.Response, string) {
	t.Helper()
	return postJSON(t, ts.URL+"/join", map[string]string{"code": code}, ck)
}

// joinData is the 200 payload of POST /join.
type joinData struct {
	Redirect string `json:"redirect"`
	Status   string `json:"status"`
}

func decodeJoin(t *testing.T, body string) joinData {
	t.Helper()
	env := decodeEnv(t, body)
	var d joinData
	if err := json.Unmarshal(env.Data, &d); err != nil {
		t.Fatalf("join data %q: %v", env.Data, err)
	}
	return d
}

// assertFail checks an error envelope's exact code and message.
func assertFail(t *testing.T, resp *http.Response, body string, wantStatus int, wantCode, wantMsg string) {
	t.Helper()
	if resp.StatusCode != wantStatus {
		t.Fatalf("status = %d, want %d (body %s)", resp.StatusCode, wantStatus, body)
	}
	env := decodeEnv(t, body)
	if env.OK {
		t.Fatalf("expected failure envelope, got ok: %s", body)
	}
	if env.Error != wantCode || env.Message != wantMsg {
		t.Fatalf("error = %q / %q, want %q / %q", env.Error, env.Message, wantCode, wantMsg)
	}
}

// --- POST /join matrix ------------------------------------------------------

func TestJoinMatrix(t *testing.T) {
	ts, pool, _, _ := quizFixture(t)
	ck := guruLogin(t, ts)

	t.Run("unknown code", func(t *testing.T) {
		alice := studentCookie(t, pool, "alice-unknown")
		resp, body := postJoin(t, ts, "ZZZZZZ", alice)
		assertFail(t, resp, body, http.StatusNotFound, handlers.ErrInvalidCode, handlers.MsgInvalidCode)
	})

	t.Run("anonymous is sent to login", func(t *testing.T) {
		resp, _ := postJSON(t, ts.URL+"/join", map[string]string{"code": "ANYCODE"})
		if resp.StatusCode != http.StatusFound {
			t.Fatalf("anon join = %d, want 302", resp.StatusCode)
		}
		if loc := resp.Header.Get("Location"); loc != "/login" {
			t.Fatalf("Location = %q, want /login", loc)
		}
	})

	t.Run("nonaktif quiz is not found", func(t *testing.T) {
		alice := studentCookie(t, pool, "alice-nonaktif")
		_, code := activeQuiz(t, ts, pool, ck, "Join NA", "nonaktif", "open")
		resp, body := postJoin(t, ts, code, alice)
		assertFail(t, resp, body, http.StatusNotFound, handlers.ErrNotFound, "Kuis tidak ditemukan.")
	})

	t.Run("closed quiz is gone", func(t *testing.T) {
		alice := studentCookie(t, pool, "alice-closed")
		_, code := activeQuiz(t, ts, pool, ck, "Join Done", "selesai", "open")
		resp, body := postJoin(t, ts, code, alice)
		assertFail(t, resp, body, http.StatusGone, handlers.ErrQuizEnded, "Kuis ini sudah berakhir.")
	})

	t.Run("running quiz refuses new joiners", func(t *testing.T) {
		alice := studentCookie(t, pool, "alice-running")
		_, code := activeQuiz(t, ts, pool, ck, "Join Run", "berjalan", "open")
		resp, body := postJoin(t, ts, code, alice)
		assertFail(t, resp, body, http.StatusConflict, handlers.ErrQuizInProgress, handlers.MsgQuizInProgress)
	})

	t.Run("open join creates registered and rejoin never 409s", func(t *testing.T) {
		alice := studentCookie(t, pool, "alice-open")
		quizID, code := activeQuiz(t, ts, pool, ck, "Join Open", "aktif", "open")

		resp, body := postJoin(t, ts, code, alice)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("join = %d: %s", resp.StatusCode, body)
		}
		d := decodeJoin(t, body)
		if d.Redirect != "/quiz/"+code || d.Status != "registered" {
			t.Fatalf("join data = %+v", d)
		}
		_, status, attempt := participantRowFor(t, pool, quizID, userOf(t, ts, alice))
		if status != "registered" || attempt != 1 {
			t.Fatalf("row = %s/%d", status, attempt)
		}

		// rejoin (registered) → 200, same row, no new attempt
		resp, body = postJoin(t, ts, code, alice)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("rejoin = %d: %s", resp.StatusCode, body)
		}
		if d := decodeJoin(t, body); d.Status != "registered" {
			t.Fatalf("rejoin status = %q", d.Status)
		}
		_, status, attempt = participantRowFor(t, pool, quizID, userOf(t, ts, alice))
		if status != "registered" || attempt != 1 {
			t.Fatalf("after rejoin row = %s/%d", status, attempt)
		}

		// started → rejoin reports started (never 409)
		pid, _, _ := participantRowFor(t, pool, quizID, userOf(t, ts, alice))
		if _, err := pool.Exec(`UPDATE participants SET status = 'started' WHERE id = ?`, pid); err != nil {
			t.Fatalf("mark started: %v", err)
		}
		resp, body = postJoin(t, ts, code, alice)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("rejoin started = %d: %s", resp.StatusCode, body)
		}
		if d := decodeJoin(t, body); d.Status != "started" {
			t.Fatalf("rejoin started status = %q", d.Status)
		}

		// removed → 403 for every later attempt
		if _, err := pool.Exec(`UPDATE participants SET status = 'dikeluarkan' WHERE id = ?`, pid); err != nil {
			t.Fatalf("mark removed: %v", err)
		}
		resp, body = postJoin(t, ts, code, alice)
		assertFail(t, resp, body, http.StatusForbidden, handlers.ErrForbidden, "Anda dikeluarkan dari kuis ini.")
	})

	t.Run("approve join creates pending and rejoin keeps it", func(t *testing.T) {
		alice := studentCookie(t, pool, "alice-approve")
		quizID, code := activeQuiz(t, ts, pool, ck, "Join Approve", "aktif", "approve")

		resp, body := postJoin(t, ts, code, alice)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("join = %d: %s", resp.StatusCode, body)
		}
		if d := decodeJoin(t, body); d.Status != "pending" {
			t.Fatalf("join status = %q", d.Status)
		}
		_, status, _ := participantRowFor(t, pool, quizID, userOf(t, ts, alice))
		if status != "pending" {
			t.Fatalf("row = %s", status)
		}
		resp, body = postJoin(t, ts, code, alice)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("rejoin = %d: %s", resp.StatusCode, body)
		}
		if d := decodeJoin(t, body); d.Status != "pending" {
			t.Fatalf("rejoin status = %q", d.Status)
		}
	})

	t.Run("single attempt exhausted blocks with 409", func(t *testing.T) {
		alice := studentCookie(t, pool, "alice-exhausted")
		form := quizForm("Join Exhausted")
		form.Set("timer_type", "per_soal")
		form.Set("max_attempts", "1")
		quizID := createQuiz(t, ts, ck, form)
		q := addQuestion(t, ts, pool, ck, "Q exhausted", "pg", []string{"One", "Two"}, []int{0})
		compose(t, ts, ck, quizID, q)
		if resp, body := setStatus(t, ts, ck, quizID, "aktif"); resp.StatusCode != http.StatusOK || !decodeEnv(t, body).OK {
			t.Fatalf("activate = %d: %s", resp.StatusCode, body)
		}
		code := joinCode(t, pool, quizID)

		if resp, body := postJoin(t, ts, code, alice); resp.StatusCode != http.StatusOK {
			t.Fatalf("join = %d: %s", resp.StatusCode, body)
		}
		pid, _, _ := participantRowFor(t, pool, quizID, userOf(t, ts, alice))
		if _, err := pool.Exec(`UPDATE participants SET status = 'selesai' WHERE id = ?`, pid); err != nil {
			t.Fatalf("finish attempt: %v", err)
		}
		resp, body := postJoin(t, ts, code, alice)
		assertFail(t, resp, body, http.StatusConflict, handlers.ErrAttemptLimit, "Anda tidak punya sisa percobaan.")
	})

	t.Run("second attempt allowed when configured", func(t *testing.T) {
		alice := studentCookie(t, pool, "alice-attempt2")
		form := quizForm("Join Attempt2")
		form.Set("timer_type", "per_soal")
		form.Set("max_attempts", "2")
		quizID := createQuiz(t, ts, ck, form)
		q := addQuestion(t, ts, pool, ck, "Q attempt2", "pg", []string{"One", "Two"}, []int{0})
		compose(t, ts, ck, quizID, q)
		if resp, body := setStatus(t, ts, ck, quizID, "aktif"); resp.StatusCode != http.StatusOK || !decodeEnv(t, body).OK {
			t.Fatalf("activate = %d: %s", resp.StatusCode, body)
		}
		code := joinCode(t, pool, quizID)

		if resp, body := postJoin(t, ts, code, alice); resp.StatusCode != http.StatusOK {
			t.Fatalf("join = %d: %s", resp.StatusCode, body)
		}
		pid, _, _ := participantRowFor(t, pool, quizID, userOf(t, ts, alice))
		if _, err := pool.Exec(`UPDATE participants SET status = 'selesai' WHERE id = ?`, pid); err != nil {
			t.Fatalf("finish attempt: %v", err)
		}
		resp, body := postJoin(t, ts, code, alice)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("rejoin after selesai = %d: %s", resp.StatusCode, body)
		}
		if d := decodeJoin(t, body); d.Status != "registered" {
			t.Fatalf("attempt 2 status = %q", d.Status)
		}
		_, status, attempt := participantRowFor(t, pool, quizID, userOf(t, ts, alice))
		if status != "registered" || attempt != 2 {
			t.Fatalf("attempt 2 row = %s/%d", status, attempt)
		}
	})
}

// The public pages must never leak quiz data (title, code, participants) to
// anonymous visitors: /join carries only the code form, / and /about carry
// no quiz rows. The murid dashboard keeps its list — that page is
// session-gated by design.
func TestPublicPagesLeakNoQuizData(t *testing.T) {
	ts, pool, _, _ := quizFixture(t)
	ck := guruLogin(t, ts)
	_, code := activeQuiz(t, ts, pool, ck, "Public Leak Probe", "aktif", "open")

	for _, path := range []string{"/", "/join", "/about"} {
		resp, body := do(t, ts.URL+path, "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", path, resp.StatusCode)
		}
		for _, leak := range []string{"Public Leak Probe", code} {
			if strings.Contains(body, leak) {
				t.Errorf("GET %s leaks %q", path, leak)
			}
		}
	}

	// signed-in murid still sees the list on the dashboard (by design)
	stu := studentCookie(t, pool, "leakprobe-stu")
	resp, body := do(t, ts.URL+"/student", stu.Value)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /student = %d", resp.StatusCode)
	}
	if !strings.Contains(body, "Public Leak Probe") {
		t.Error("murid dashboard lost its active-quiz list")
	}
}

// --- invite-URL auto-join (GET /quiz/:code) --------------------------------

func TestInviteAutoJoin(t *testing.T) {
	ts, pool, _, _ := quizFixture(t)
	ck := guruLogin(t, ts)

	t.Run("anonymous redirects to login", func(t *testing.T) {
		_, code := activeQuiz(t, ts, pool, ck, "Invite Anon", "aktif", "open")
		resp, _ := getWith(t, ts.URL+"/quiz/"+code)
		if resp.StatusCode != http.StatusFound {
			t.Fatalf("anon invite = %d, want 302", resp.StatusCode)
		}
		if loc := resp.Header.Get("Location"); loc != "/login" {
			t.Fatalf("Location = %q, want /login", loc)
		}
	})

	t.Run("open quiz creates registered and renders waiting", func(t *testing.T) {
		bob := studentCookie(t, pool, "bob-open")
		quizID, code := activeQuiz(t, ts, pool, ck, "Invite Open", "aktif", "open")
		resp, body := getWith(t, ts.URL+"/quiz/"+code, bob)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("invite = %d: %s", resp.StatusCode, body)
		}
		if !contains(body, `data-state="waiting"`) {
			t.Fatalf("workspace state marker missing: %s", body)
		}
		_, status, attempt := participantRowFor(t, pool, quizID, userOf(t, ts, bob))
		if status != "registered" || attempt != 1 {
			t.Fatalf("auto-join row = %s/%d", status, attempt)
		}
	})

	t.Run("approve quiz creates pending and renders pending", func(t *testing.T) {
		bob := studentCookie(t, pool, "bob-approve")
		quizID, code := activeQuiz(t, ts, pool, ck, "Invite Approve", "aktif", "approve")
		resp, body := getWith(t, ts.URL+"/quiz/"+code, bob)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("invite = %d: %s", resp.StatusCode, body)
		}
		if !contains(body, `data-state="pending"`) {
			t.Fatalf("workspace state marker missing: %s", body)
		}
		_, status, _ := participantRowFor(t, pool, quizID, userOf(t, ts, bob))
		if status != "pending" {
			t.Fatalf("auto-join row = %s", status)
		}
	})

	t.Run("invite during a running quiz shows the in-progress page", func(t *testing.T) {
		bob := studentCookie(t, pool, "bob-running")
		_, code := activeQuiz(t, ts, pool, ck, "Invite Run", "berjalan", "open")
		resp, body := getWith(t, ts.URL+"/quiz/"+code, bob)
		if resp.StatusCode != http.StatusConflict {
			t.Fatalf("invite = %d, want 409 (body %s)", resp.StatusCode, body)
		}
		if !contains(body, handlers.MsgQuizInProgress) {
			t.Fatalf("in-progress message missing: %s", body)
		}
	})

	t.Run("removed student sees the removed page", func(t *testing.T) {
		carol := studentCookie(t, pool, "carol-removed")
		quizID, code := activeQuiz(t, ts, pool, ck, "Invite Removed", "aktif", "open")
		if resp, body := postJoin(t, ts, code, carol); resp.StatusCode != http.StatusOK {
			t.Fatalf("join = %d: %s", resp.StatusCode, body)
		}
		pid, _, _ := participantRowFor(t, pool, quizID, userOf(t, ts, carol))
		if _, err := pool.Exec(`UPDATE participants SET status = 'dikeluarkan' WHERE id = ?`, pid); err != nil {
			t.Fatalf("mark removed: %v", err)
		}
		resp, body := getWith(t, ts.URL+"/quiz/"+code, carol)
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("invite = %d, want 403 (body %s)", resp.StatusCode, body)
		}
		if !contains(body, "Anda dikeluarkan dari kuis ini.") {
			t.Fatalf("removed message missing: %s", body)
		}
	})

	t.Run("closed quiz with a registered row shows the ended page", func(t *testing.T) {
		dan := studentCookie(t, pool, "dan-ended")
		quizID, code := activeQuiz(t, ts, pool, ck, "Invite Ended", "aktif", "open")
		if resp, body := postJoin(t, ts, code, dan); resp.StatusCode != http.StatusOK {
			t.Fatalf("join = %d: %s", resp.StatusCode, body)
		}
		setQuizStatusSQL(t, pool, quizID, "selesai")
		resp, body := getWith(t, ts.URL+"/quiz/"+code, dan)
		if resp.StatusCode != http.StatusGone {
			t.Fatalf("invite = %d, want 410 (body %s)", resp.StatusCode, body)
		}
		if !contains(body, "Kuis ini sudah berakhir.") {
			t.Fatalf("ended message missing: %s", body)
		}
	})
}
