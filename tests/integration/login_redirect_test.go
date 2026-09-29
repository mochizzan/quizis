package integration

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"quiz/internal/testutil"
)

// Post-login landing contract: the redirect after a successful login is
// chosen from the session that login just created — guru → /teacher,
// murid → /student — never root, and never influenced by the previous
// session in the same browser (bug sequence: log out as guru, log in as
// murid, landed on /).
func TestLoginLandsOnRoleDashboard(t *testing.T) {
	cfg := testutil.Config(t)

	// fresh-state rows: each owns a fresh fixture and a cookie-less POST
	cases := []struct {
		name      string
		seedMurid bool
		identity  string
		password  string
		want      string
	}{
		{"fresh guru login lands on /teacher", false, cfg.GuruUser, cfg.GuruPass, "/teacher"},
		{"fresh murid login lands on /student", true, "dashu", "secret123", "/student"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts, _ := authFixture(t)
			if tc.seedMurid {
				registerUser(t, ts, tc.identity, tc.identity+"@example.test", "Dash U", tc.password)
			}
			resp, body := postForm(t, ts.URL+"/login",
				url.Values{"identity": {tc.identity}, "password": {tc.password}})
			if resp.StatusCode != http.StatusFound {
				t.Fatalf("login = %d, want 302 (%s)", resp.StatusCode, body)
			}
			if loc := resp.Header.Get("Location"); loc != tc.want {
				t.Fatalf("login Location = %q, want %q (never root)", loc, tc.want)
			}
			if sessionCookie(resp) == nil {
				t.Fatal("login: no session cookie")
			}
		})
	}

	// the reported bug sequence: guru signs out, the same browser signs in
	// as a murid → dashboard, not /
	t.Run("guru logout then murid login lands on /student not /", func(t *testing.T) {
		ts, _ := authFixture(t)
		registerUser(t, ts, "switchu", "switchu@example.test", "Switch U", "secret123")

		guru := guruLogin(t, ts)
		resp, body := postForm(t, ts.URL+"/logout", url.Values{}, guru)
		if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/login" {
			t.Fatalf("logout = %d %q, want 302 /login (%s)",
				resp.StatusCode, resp.Header.Get("Location"), body)
		}

		// same browser (no session cookie after logout): sign in as murid
		resp, body = postForm(t, ts.URL+"/login",
			url.Values{"identity": {"switchu"}, "password": {"secret123"}})
		if resp.StatusCode != http.StatusFound {
			t.Fatalf("murid login = %d, want 302 (%s)", resp.StatusCode, body)
		}
		if loc := resp.Header.Get("Location"); loc != "/student" {
			t.Fatalf("murid login after guru logout = %q, want /student (never root)", loc)
		}

		ck := sessionCookie(resp)
		if ck == nil {
			t.Fatal("murid login: no session cookie")
		}
		// the session behind the new landing is the murid one
		if _, body := do(t, ts.URL+"/me", ck.Value); !strings.HasSuffix(body, ":murid") {
			t.Errorf("/me after switch = %q, want authenticated murid", body)
		}
		// the landing itself renders for that session
		if r, _ := do(t, ts.URL+"/student", ck.Value); r.StatusCode != http.StatusOK {
			t.Errorf("GET /student = %d, want 200", r.StatusCode)
		}
	})
}
