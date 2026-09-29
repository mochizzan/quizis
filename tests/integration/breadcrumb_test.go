package integration

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// crumbNav extracts the rendered breadcrumb (normalized) or fails: every
// dashboard page must carry one.
func crumbNav(t *testing.T, body string) string {
	t.Helper()
	i := strings.Index(body, `aria-label="breadcrumb"`)
	if i < 0 {
		t.Fatal("page has no breadcrumb")
	}
	j := strings.Index(body[i:], "</nav>")
	if j < 0 {
		t.Fatal("breadcrumb not closed")
	}
	return strings.Join(strings.Fields(body[i:i+j]), " ")
}

// assertCrumbs checks every fragment appears IN ORDER: parents first (the
// static root label or an <a href> to its own route), the current crumb last
// (active, no link).
func assertCrumbs(t *testing.T, nav string, want ...string) {
	t.Helper()
	pos := 0
	for _, w := range want {
		k := strings.Index(nav[pos:], w)
		if k < 0 {
			t.Errorf("breadcrumb missing %q in order\ngot: %s", w, nav)
			return
		}
		pos += k + len(w)
	}
}

// Every dashboard route (guru + murid) renders the Bootstrap breadcrumb with
// the ACTUAL route hierarchy: Dashboard (static root label — never a link to
// the landing page) → role root → section → page, each other parent linking
// to its route. Quiz/attempt pages use the real title.
func TestDashboardBreadcrumbs(t *testing.T) {
	ts, pool, _, _ := quizFixture(t)
	guru := guruLogin(t, ts)
	stu := studentCookie(t, pool, "crumb-stu")
	uid := userOf(t, ts, stu)

	get := func(t *testing.T, path, session string) string {
		t.Helper()
		resp, body := do(t, ts.URL+path, session)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s = %d: %s", path, resp.StatusCode, body)
		}
		return crumbNav(t, body)
	}

	// --- guru: the section roots ------------------------------------------
	nav := get(t, "/teacher", guru.Value)
	assertCrumbs(t, nav,
		`breadcrumb-item">Dashboard</li>`,
		`breadcrumb-item active" aria-current="page">Guru<`)

	nav = get(t, "/teacher/students", guru.Value)
	assertCrumbs(t, nav,
		`breadcrumb-item">Dashboard</li>`,
		`<a href="/teacher">Guru</a>`,
		`breadcrumb-item active" aria-current="page">Manage Akun Murid<`)

	nav = get(t, "/teacher/questions", guru.Value)
	assertCrumbs(t, nav,
		`<a href="/teacher">Guru</a>`,
		`breadcrumb-item active" aria-current="page">Question Bank<`)

	// --- guru: the quiz subtree carries the real title ---------------------
	qid := createQuiz(t, ts, guru, quizForm("Crumb Quiz Judul"))
	nav = get(t, fmt.Sprintf("/teacher/quiz/%d", qid), guru.Value)
	assertCrumbs(t, nav,
		`<a href="/teacher/quiz">Quiz</a>`,
		`breadcrumb-item active" aria-current="page">Crumb Quiz Judul<`)

	nav = get(t, fmt.Sprintf("/teacher/quiz/%d/results", qid), guru.Value)
	assertCrumbs(t, nav,
		`<a href="/teacher/quiz">Quiz</a>`,
		`<a href="/teacher/quiz/`+fmt.Sprint(qid)+`">Crumb Quiz Judul</a>`,
		`breadcrumb-item active" aria-current="page">Results<`)

	nav = get(t, fmt.Sprintf("/teacher/quiz/%d/grading", qid), guru.Value)
	assertCrumbs(t, nav,
		`<a href="/teacher/quiz/`+fmt.Sprint(qid)+`">Crumb Quiz Judul</a>`,
		`breadcrumb-item active" aria-current="page">Grading<`)

	// --- murid: Beranda subtree -------------------------------------------
	nav = get(t, "/student", stu.Value)
	assertCrumbs(t, nav,
		`breadcrumb-item">Dashboard</li>`,
		`breadcrumb-item active" aria-current="page">Beranda<`)

	nav = get(t, "/history", stu.Value)
	assertCrumbs(t, nav,
		`breadcrumb-item">Dashboard</li>`,
		`<a href="/student">Beranda</a>`,
		`breadcrumb-item active" aria-current="page">Riwayat<`)

	nav = get(t, "/profile", stu.Value)
	assertCrumbs(t, nav,
		`<a href="/student">Beranda</a>`,
		`breadcrumb-item active" aria-current="page">Profil<`)

	// attempt detail: the route hierarchy ends in the real quiz title
	res, err := pool.Exec(`INSERT INTO participants (quiz_id, user_id, attempt_no, status)
		VALUES (?, ?, 1, 'selesai')`, qid, uid)
	if err != nil {
		t.Fatalf("insert attempt: %v", err)
	}
	pid, _ := res.LastInsertId()
	nav = get(t, fmt.Sprintf("/history/%d", pid), stu.Value)
	assertCrumbs(t, nav,
		`<a href="/student">Beranda</a>`,
		`<a href="/history">Riwayat</a>`,
		`breadcrumb-item active" aria-current="page">Crumb Quiz Judul<`)

	// --- the public shell carries no dashboard breadcrumb -------------------
	resp, body := getWith(t, ts.URL+"/join")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /join = %d", resp.StatusCode)
	}
	if strings.Contains(body, `aria-label="breadcrumb"`) {
		t.Error("public join page carries a dashboard breadcrumb")
	}
}
