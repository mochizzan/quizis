package integration

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// The student/teacher areas must never navigate back to the landing page:
// no exact href="/" renders on any app-shell page, both brand anchors
// (desktop sidebar + mobile topbar) point at the role dashboard, and the
// "Dashboard" crumb is a static root label instead of a link to "/".
func TestAppPagesHaveNoRootNavigation(t *testing.T) {
	ts, pool, _, _ := quizFixture(t)
	guru := guruLogin(t, ts)
	stu := studentCookie(t, pool, "navroot-stu")
	qid := createQuiz(t, ts, guru, quizForm("Navroot Quiz"))

	// Exact root href: the quoted value ends right after the slash, so
	// /login, /teacher etc. can never match; the follower accepts only the
	// attribute terminator (whitespace, tag end or a stray extra quote).
	rootLink := regexp.MustCompile(`href="/"([\s>"]|$)`)

	pages := []struct {
		path    string
		session string
	}{
		{"/teacher", guru.Value},
		{"/teacher/quiz", guru.Value},
		{"/teacher/quiz/new", guru.Value},
		{fmt.Sprintf("/teacher/quiz/%d", qid), guru.Value},
		{"/teacher/questions", guru.Value},
		{"/teacher/questions/new", guru.Value},
		{"/teacher/classes", guru.Value},
		{"/teacher/majors", guru.Value},
		{"/teacher/students", guru.Value},
		{"/teacher/password-resets", guru.Value},
		{"/student", stu.Value},
		{"/history", stu.Value},
		{"/profile", stu.Value},
	}
	for _, p := range pages {
		resp, body := do(t, ts.URL+p.path, p.session)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d: %s", p.path, resp.StatusCode, body)
			continue
		}
		if m := rootLink.FindString(body); m != "" {
			t.Errorf("GET %s renders a landing-page link (%q)", p.path, m)
		}
		// every app page carries the breadcrumb, and its root crumb is a
		// static label — never an anchor anywhere
		nav := crumbNav(t, body)
		if !strings.Contains(nav, `breadcrumb-item">Dashboard</li>`) {
			t.Errorf("GET %s: Dashboard crumb is not a static label\ngot: %s", p.path, nav)
		}
		if strings.Contains(nav, `>Dashboard</a>`) {
			t.Errorf("GET %s: Dashboard crumb links\ngot: %s", p.path, nav)
		}
	}

	// the two brand anchors are role-aware: they target the role dashboard
	for _, c := range []struct {
		name    string
		path    string
		session string
		brand   string
	}{
		{"guru", "/teacher", guru.Value, `href="/teacher"`},
		{"murid", "/student", stu.Value, `href="/student"`},
	} {
		resp, body := do(t, ts.URL+c.path, c.session)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s GET %s = %d", c.name, c.path, resp.StatusCode)
			continue
		}
		for _, anchor := range []string{
			`class="app-brand d-none d-lg-flex" ` + c.brand,
			`class="app-topbar-brand d-lg-none" ` + c.brand,
		} {
			if !strings.Contains(body, anchor) {
				t.Errorf("%s: brand anchor %q missing", c.name, anchor)
			}
		}
	}
}
