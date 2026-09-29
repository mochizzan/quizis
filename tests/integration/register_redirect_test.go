package integration

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"quiz/internal/handlers"
	"quiz/internal/testutil"
)

// The mandated post-registration flow: POST /register answers 302 to
// /login?registered=1 with NO cookie of any kind (no session row either),
// the login page renders the success toast, an authed route still bounces
// to /login until the student signs in manually, and those credentials
// then work.
func TestRegisterRedirectsToLoginWithoutSession(t *testing.T) {
	ts, _ := authFixture(t)
	pool := testutil.DB(t)

	resp, body := postForm(t, ts.URL+"/register", url.Values{
		"nama_lengkap": {"Baru Murid"},
		"username":     {"barumurid"},
		"email":        {"barumurid@example.test"},
		"password":     {"secret123"},
		"kelas_id":     {"1"},
		"jurusan_id":   {"1"},
	})
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("register = %d, want 302 (%s)", resp.StatusCode, body)
	}
	if loc := resp.Header.Get("Location"); loc != "/login?registered=1" {
		t.Errorf("Location = %q, want /login?registered=1", loc)
	}
	if cks := resp.Header.Values("Set-Cookie"); len(cks) != 0 {
		t.Errorf("Set-Cookie after register = %q, want none", cks)
	}

	// following the redirect renders the success flash
	resp2, loginBody := do(t, ts.URL+resp.Header.Get("Location"), "")
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("GET /login?registered=1 = %d, want 200", resp2.StatusCode)
	}
	if msg := banner(t, loginBody); msg != handlers.MsgRegistered {
		t.Errorf("flash = %q, want %q", msg, handlers.MsgRegistered)
	}
	if _, plain := do(t, ts.URL+"/login", ""); strings.Contains(plain, handlers.MsgRegistered) {
		t.Error("plain /login also shows the registration flash")
	}

	// immediately after register the student is still anonymous
	resp3, _ := do(t, ts.URL+"/student", "")
	if resp3.StatusCode != http.StatusFound || resp3.Header.Get("Location") != "/login" {
		t.Errorf("/student = %d %q, want 302 /login",
			resp3.StatusCode, resp3.Header.Get("Location"))
	}

	// ... and no session row exists for the new account
	var sessions int
	if err := pool.QueryRow(
		`SELECT COUNT(*) FROM sessions
		 WHERE user_id = (SELECT id FROM users WHERE username = ?)`,
		"barumurid",
	).Scan(&sessions); err != nil {
		t.Fatalf("session count: %v", err)
	}
	if sessions != 0 {
		t.Errorf("session rows after register = %d, want 0", sessions)
	}

	// the just-registered credentials sign in fine
	resp4, body := postForm(t, ts.URL+"/login", url.Values{
		"identity": {"barumurid"}, "password": {"secret123"},
	})
	if resp4.StatusCode != http.StatusFound || resp4.Header.Get("Location") != "/student" {
		t.Fatalf("login = %d %q, want 302 /student (%s)",
			resp4.StatusCode, resp4.Header.Get("Location"), body)
	}
	ck := sessionCookie(resp4)
	if ck == nil {
		t.Fatal("login: no session cookie")
	}
	if _, body := do(t, ts.URL+"/me", ck.Value); !strings.HasSuffix(body, ":murid") {
		t.Errorf("after login /me = %q, want authenticated murid", body)
	}
}
