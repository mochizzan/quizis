package integration

import (
	"net/http"
	"strings"
	"testing"

	"quiz/internal/testutil"
)

// The landing navbar carries exactly three menus (Home, Join quiz, About) and
// one profile icon whose target follows the session role: anonymous → /login,
// murid → /student, guru → /teacher. The old shared navbar leaked teacher
// links to every visitor — that must not come back.
func TestLandingNavbarThreeMenusAndProfileIcon(t *testing.T) {
	ts, _ := authFixture(t)
	pool := testutil.DB(t)
	stu := studentCookie(t, pool, "chrome-stu")
	guru := guruLogin(t, ts)

	cases := []struct {
		name     string
		session  string
		wantIcon string
	}{
		{"guest", "", `href="/login" aria-label="Sign in"`},
		{"murid", stu.Value, `href="/student" aria-label="Dashboard murid"`},
		{"guru", guru.Value, `href="/teacher" aria-label="Dashboard guru"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp, body := do(t, ts.URL+"/", c.session)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET / = %d, want 200", resp.StatusCode)
			}
			// collapse whitespace so multi-line markup asserts as one line
			body = strings.Join(strings.Fields(body), " ")
			if n := strings.Count(body, `<li class="nav-item">`); n != 3 {
				t.Errorf("navbar menu items = %d, want exactly 3", n)
			}
			for _, want := range []string{
				`href="/" aria-current="page"`,
				`href="/join"`,
				`href="/about"`,
				c.wantIcon,
			} {
				if !strings.Contains(body, want) {
					t.Errorf("landing missing %q", want)
				}
			}
			// role isolation on the public chrome: no dashboard links of the
			// other roles may appear
			if c.name != "guru" && strings.Contains(body, `href="/teacher`) {
				t.Error("landing leaks teacher links")
			}
			if c.name == "guru" && strings.Contains(body, `href="/student"`) {
				t.Error("guru landing links the student dashboard")
			}
			if strings.Contains(body, `href="/history"`) {
				t.Error("landing leaks the student history menu")
			}
		})
	}
}

// Both dashboards run on the sidebar shell: each role sees only its own menu,
// exactly one item is marked active, and the active item follows the path.
func TestDashboardSidebarIsolationAndActiveState(t *testing.T) {
	ts, _ := authFixture(t)
	pool := testutil.DB(t)
	stu := studentCookie(t, pool, "chrome-sidebar")
	guru := guruLogin(t, ts)

	assertActive := func(t *testing.T, body, item string) {
		t.Helper()
		// scope to the sidebar: the breadcrumb carries its own
		// aria-current="page" on the last crumb
		sidebar := body
		if start := strings.Index(body, `id="app-sidebar"`); start >= 0 {
			if end := strings.Index(body[start:], "</nav>"); end >= 0 {
				sidebar = body[start : start+end]
			}
		}
		want := `href="` + item + `" aria-current="page"`
		if !strings.Contains(sidebar, want) {
			t.Errorf("active item %q missing (%s)", item, want)
		}
		if n := strings.Count(sidebar, `aria-current="page"`); n != 1 {
			t.Errorf("sidebar aria-current count = %d, want exactly 1", n)
		}
	}

	// murid dashboard: own menu only
	resp, body := getWith(t, ts.URL+"/student", stu)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /student = %d: %s", resp.StatusCode, body)
	}
	for _, want := range []string{`href="/history"`, `href="/profile"`, `action="/logout"`} {
		if !strings.Contains(body, want) {
			t.Errorf("murid sidebar missing %q", want)
		}
	}
	for _, leak := range []string{`href="/teacher`, "Question Bank", "Reset Password"} {
		if strings.Contains(body, leak) {
			t.Errorf("murid dashboard leaks %q", leak)
		}
	}
	// feedback chrome ships on every dashboard page: the toast stack and the
	// generic confirmation modal (data-confirm actions skip without one)
	for _, id := range []string{`id="toast-stack"`, `id="confirm-modal"`} {
		if !strings.Contains(body, id) {
			t.Errorf("murid dashboard missing %s", id)
		}
	}
	assertActive(t, body, "/student")

	// guru dashboard: own menu only
	resp, body = getWith(t, ts.URL+"/teacher", guru)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /teacher = %d: %s", resp.StatusCode, body)
	}
	for _, want := range []string{
		`href="/teacher/quiz"`, `href="/teacher/questions"`,
		`href="/teacher/classes"`, `href="/teacher/majors"`,
		`href="/teacher/password-resets"`, `action="/logout"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("guru sidebar missing %q", want)
		}
	}
	for _, leak := range []string{`href="/history"`, `href="/profile"`, `href="/student"`, "Beranda", "Riwayat"} {
		if strings.Contains(body, leak) {
			t.Errorf("guru dashboard leaks %q", leak)
		}
	}
	assertActive(t, body, "/teacher")

	// active state follows the path on sub-pages
	resp, body = getWith(t, ts.URL+"/teacher/questions", guru)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /teacher/questions = %d", resp.StatusCode)
	}
	for _, id := range []string{`id="toast-stack"`, `id="confirm-modal"`} {
		if !strings.Contains(body, id) {
			t.Errorf("question bank page missing %s", id)
		}
	}
	assertActive(t, body, "/teacher/questions")

	for _, path := range []string{"/history", "/profile"} {
		resp, body := getWith(t, ts.URL+path, stu)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s = %d", path, resp.StatusCode)
		}
		assertActive(t, body, path)
	}
}

// Public pages render anonymously, the join form is murid-only, the student
// dashboard is session-gated, and the minimal chrome (auth pages) carries no
// navigation menus at all.
func TestPublicPagesJoinFormAndChromeAccess(t *testing.T) {
	ts, _ := authFixture(t)
	pool := testutil.DB(t)

	for _, path := range []string{"/", "/join", "/about"} {
		resp, body := do(t, ts.URL+path, "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", path, resp.StatusCode)
		}
		if !strings.Contains(body, `id="landingNav"`) {
			t.Errorf("GET %s missing the landing navbar", path)
		}
	}

	// The public join page carries the code form for EVERY visitor — and
	// nothing else: no active-quiz list may render for a guest (the leak
	// this replaced), no sign-in substitute for the form.
	_, joinGuest := do(t, ts.URL+"/join", "")
	if !strings.Contains(joinGuest, `id="join-form"`) {
		t.Error("guest join page missing the join form")
	}
	for _, leak := range []string{"Active quizzes", `class="list-group`} {
		if strings.Contains(joinGuest, leak) {
			t.Errorf("guest join page leaks %q", leak)
		}
	}

	// murid: the form is there
	stu := studentCookie(t, pool, "chrome-access")
	_, joinStu := do(t, ts.URL+"/join", stu.Value)
	if !strings.Contains(joinStu, `id="join-form"`) {
		t.Error("murid join page missing the join form")
	}

	// /student needs a murid session
	resp, _ := do(t, ts.URL+"/student", "")
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/login" {
		t.Errorf("guest /student = %d %q, want 302 /login",
			resp.StatusCode, resp.Header.Get("Location"))
	}
	guru := guruLogin(t, ts)
	resp, _ = do(t, ts.URL+"/student", guru.Value)
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/login" {
		t.Errorf("guru /student = %d %q, want 302 /login",
			resp.StatusCode, resp.Header.Get("Location"))
	}

	// minimal chrome: no navigation menus on the auth pages
	_, loginBody := do(t, ts.URL+"/login", "")
	for _, leak := range []string{`href="/teacher`, `href="/history"`, `href="/profile"`} {
		if strings.Contains(loginBody, leak) {
			t.Errorf("login page leaks %q", leak)
		}
	}
}
