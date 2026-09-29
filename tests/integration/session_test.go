package integration

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"

	"quiz/internal/cache"
	"quiz/internal/db"
	mw "quiz/internal/middleware"
	"quiz/internal/testutil"
)

// newSessionServer builds a server around the REAL middleware under test.
func newSessionServer(t *testing.T, pool *sql.DB, store *cache.Store) *httptest.Server {
	t.Helper()
	e := echo.New()
	e.Use(mw.LoadSession(pool, store))

	who := func(c *echo.Context) error {
		s := mw.SessionFrom(c)
		if s == nil {
			return c.String(200, "anon")
		}
		return c.String(200, fmt.Sprintf("%d:%s", s.UserID, s.Role))
	}
	e.GET("/me", who)
	e.GET("/teacher/only", func(c *echo.Context) error { return c.String(200, "teacher-ok") }, mw.AuthTeacher)
	e.GET("/student/only", func(c *echo.Context) error { return c.String(200, "student-ok") }, mw.AuthStudent)
	e.GET("/forced", func(c *echo.Context) error { return c.String(200, "forced-ok") }, mw.ForceChangePassword)
	e.GET("/change-password", func(c *echo.Context) error { return c.String(200, "change-ok") }, mw.ForceChangePassword)
	e.GET("/logout", func(c *echo.Context) error { return c.String(200, "logout-ok") }, mw.ForceChangePassword)

	ts := httptest.NewServer(e)
	t.Cleanup(ts.Close)
	return ts
}

// do issues GET with an optional raw session cookie value; redirects are NOT
// followed so tests can inspect 302s.
func do(t *testing.T, url, sessionID string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if sessionID != "" {
		req.AddCookie(&http.Cookie{Name: mw.CookieName, Value: sessionID})
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, string(body)
}

func seedUser(t *testing.T, pool *sql.DB, username string, mustChange bool) uint64 {
	t.Helper()
	res, err := pool.Exec(
		`INSERT INTO users (username, email, password_hash, nama_lengkap, kelas_id, jurusan_id, must_change_pw)
		 VALUES (?, ?, 'x', 'Test User', 1, 1, ?)`,
		username, username+"@test.example", mustChange)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("user id: %v", err)
	}
	return uint64(id)
}

func newSession(t *testing.T, pool *sql.DB, userID uint64, role string) string {
	t.Helper()
	sid, err := db.NewSessionID()
	if err != nil {
		t.Fatalf("session id: %v", err)
	}
	if err := db.InsertSession(context.Background(), pool, sid, userID, role,
		time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	return sid
}

// flipping the last character keeps the 43-char length (a realistic tamper).
func tamper(id string) string {
	last := byte('A')
	if id[len(id)-1] == 'A' {
		last = 'B'
	}
	return id[:len(id)-1] + string(last)
}

func TestSessionCookieRoundTripAndInstantRevocation(t *testing.T) {
	pool := testutil.DB(t)
	testutil.Migrate(t, pool)
	testutil.Reset(t, pool, "sessions", "users")
	store := cache.New()
	ts := newSessionServer(t, pool, store)

	uid := seedUser(t, pool, "alice", false)
	sid := newSession(t, pool, uid, "murid")

	// 1) cookie authenticates
	resp, body := do(t, ts.URL+"/me", sid)
	if resp.StatusCode != 200 || body != fmt.Sprintf("%d:murid", uid) {
		t.Fatalf("first request = %d %q, want 200 %q", resp.StatusCode, body, fmt.Sprintf("%d:murid", uid))
	}

	// 2) mirror is warm after the load (read-through proven)
	if _, ok := store.Get(cache.SessionKey(sid)); !ok {
		t.Fatal("session mirror not populated after load")
	}

	// 3) revoke: mirror first, then DB (the real logout order)
	if err := mw.RevokeSession(context.Background(), pool, store, sid); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	// 4) the SAME cookie is rejected immediately — TTL not waited out
	resp, body = do(t, ts.URL+"/me", sid)
	if resp.StatusCode != 200 || body != "anon" {
		t.Fatalf("after revoke = %d %q, want 200 anon", resp.StatusCode, body)
	}
	if ck := clearedCookie(resp); ck == nil || ck.Value != "" {
		t.Error("revoked cookie not cleared on the response")
	}
}

func TestTamperedCookieTreatedAsAnonymous(t *testing.T) {
	pool := testutil.DB(t)
	testutil.Migrate(t, pool)
	testutil.Reset(t, pool, "sessions", "users")
	store := cache.New()
	ts := newSessionServer(t, pool, store)

	uid := seedUser(t, pool, "bob", false)
	sid := newSession(t, pool, uid, "murid")

	if _, body := do(t, ts.URL+"/me", sid); body != fmt.Sprintf("%d:murid", uid) {
		t.Fatalf("valid cookie = %q, want authenticated", body)
	}

	// flipped character, same length → DB lookup fails → anonymous + cleared
	resp, body := do(t, ts.URL+"/me", tamper(sid))
	if body != "anon" {
		t.Errorf("tampered cookie = %q, want anon", body)
	}
	if ck := clearedCookie(resp); ck == nil || ck.Value != "" {
		t.Error("tampered cookie not cleared on the response")
	}

	// truncated id → fast-path rejection, also cleared
	resp, body = do(t, ts.URL+"/me", sid[:20])
	if body != "anon" {
		t.Errorf("truncated cookie = %q, want anon", body)
	}
	if ck := clearedCookie(resp); ck == nil || ck.Value != "" {
		t.Error("truncated cookie not cleared on the response")
	}
}

func TestRoleRedirects(t *testing.T) {
	pool := testutil.DB(t)
	testutil.Migrate(t, pool)
	testutil.Reset(t, pool, "sessions", "users")
	store := cache.New()
	ts := newSessionServer(t, pool, store)

	uid := seedUser(t, pool, "carol", false)
	murid := newSession(t, pool, uid, "murid")
	guru := newSession(t, pool, 0, "guru") // guru: .env account, user_id 0

	cases := []struct {
		name, path, cookie string
		wantStatus         int
		wantLocation       string // "" → expect 200 body instead
	}{
		{"guest teacher route", "/teacher/only", "", 302, "/login"},
		{"guest student route", "/student/only", "", 302, "/login"},
		{"student blocked from teacher", "/teacher/only", murid, 302, "/"},
		{"student enters student route", "/student/only", murid, 200, ""},
		{"guru enters teacher route", "/teacher/only", guru, 200, ""},
		{"guru blocked from student workspace", "/student/only", guru, 302, "/login"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, body := do(t, ts.URL+tc.path, tc.cookie)
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("GET %s = %d, want %d", tc.path, resp.StatusCode, tc.wantStatus)
			}
			if tc.wantLocation != "" {
				if loc := resp.Header.Get("Location"); loc != tc.wantLocation {
					t.Errorf("Location = %q, want %q", loc, tc.wantLocation)
				}
				return
			}
			if body == "" {
				t.Errorf("expected body for %s", tc.path)
			}
		})
	}
}

func TestPasswordChangeRevokesEverySession(t *testing.T) {
	pool := testutil.DB(t)
	testutil.Migrate(t, pool)
	testutil.Reset(t, pool, "sessions", "users")
	store := cache.New()
	ts := newSessionServer(t, pool, store)

	uid := seedUser(t, pool, "dave", false)
	sid1 := newSession(t, pool, uid, "murid")
	sid2 := newSession(t, pool, uid, "murid")

	// both devices authenticated
	for i, sid := range []string{sid1, sid2} {
		if _, body := do(t, ts.URL+"/me", sid); body != fmt.Sprintf("%d:murid", uid) {
			t.Fatalf("device %d before change = %q, want authenticated", i+1, body)
		}
	}

	if err := mw.RevokeUserSessions(context.Background(), pool, store, uid); err != nil {
		t.Fatalf("revoke user sessions: %v", err)
	}

	// both cookies dead immediately (not after the 5 min mirror TTL)
	for i, sid := range []string{sid1, sid2} {
		if _, body := do(t, ts.URL+"/me", sid); body != "anon" {
			t.Errorf("device %d after password change = %q, want anon", i+1, body)
		}
	}
}

func TestForceChangePasswordRedirect(t *testing.T) {
	pool := testutil.DB(t)
	testutil.Migrate(t, pool)
	testutil.Reset(t, pool, "sessions", "users")
	store := cache.New()
	ts := newSessionServer(t, pool, store)

	flagged := seedUser(t, pool, "erin", true)
	plain := seedUser(t, pool, "frank", false)
	sidFlagged := newSession(t, pool, flagged, "murid")
	sidPlain := newSession(t, pool, plain, "murid")
	sidGuru := newSession(t, pool, 0, "guru")

	// flagged user: every page redirects to /change-password
	resp, _ := do(t, ts.URL+"/forced", sidFlagged)
	if resp.StatusCode != 302 || resp.Header.Get("Location") != "/change-password" {
		t.Errorf("flagged /forced = %d %q, want 302 /change-password",
			resp.StatusCode, resp.Header.Get("Location"))
	}

	// exempt paths pass through
	for _, p := range []string{"/change-password", "/logout"} {
		resp, body := do(t, ts.URL+p, sidFlagged)
		if resp.StatusCode != 200 || !strings.Contains(body, "ok") {
			t.Errorf("flagged %s = %d %q, want 200", p, resp.StatusCode, body)
		}
	}

	// unflagged and guru pass through
	resp, _ = do(t, ts.URL+"/forced", sidPlain)
	if resp.StatusCode != 200 {
		t.Errorf("unflagged /forced = %d, want 200", resp.StatusCode)
	}
	resp, _ = do(t, ts.URL+"/forced", sidGuru)
	if resp.StatusCode != 200 {
		t.Errorf("guru /forced = %d, want 200 (guru has no users row)", resp.StatusCode)
	}
}

func clearedCookie(resp *http.Response) *http.Cookie {
	for _, ck := range resp.Cookies() {
		if ck.Name == mw.CookieName {
			return ck
		}
	}
	return nil
}
