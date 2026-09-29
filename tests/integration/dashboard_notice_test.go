package integration

import (
	"net/http"
	"strings"
	"testing"
)

// Sign-in lands on the murid dashboard (spec §7 home): while an attempt is
// still open, the dashboard renders the ongoing-quiz notice modal with a
// return link. A per-question / no-timer quiz stays 'aktif' while students
// work (only the global START flips a quiz to 'berjalan'), so the notice
// must fire for BOTH running statuses — and never before the attempt has
// actually started.
func TestDashboardOngoingQuizNotice(t *testing.T) {
	ts, pool, _, _ := quizFixture(t)
	ck := guruLogin(t, ts)
	st := studentCookie(t, pool, "notices-stu")

	code, _ := persoalFixture(t, ts, pool, ck, st,
		"Notice Quiz", []string{"One", "Two"}, "open")

	dashboard := func() string {
		t.Helper()
		resp, body := getWith(t, ts.URL+"/student", st)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /student = %d: %s", resp.StatusCode, body)
		}
		return body
	}
	const modal = `id="active-quiz-modal"`

	// joined but not working yet → nothing to notice
	if resp, body := postJoin(t, ts, code, st); resp.StatusCode != http.StatusOK {
		t.Fatalf("join = %d: %s", resp.StatusCode, body)
	}
	if body := dashboard(); strings.Contains(body, modal) {
		t.Errorf("ongoing-quiz notice before the attempt started:\n%s", body)
	}

	// working on it (quiz status still 'aktif' in per-question mode) →
	// the notice modal, the auto-show hook and the return link
	startAttempt(t, ts, code, st)
	body := dashboard()
	if !strings.Contains(body, modal) {
		t.Fatalf("no ongoing-quiz notice while the attempt is open")
	}
	if !strings.Contains(body, `data-ssr-modal`) {
		t.Error("notice modal missing the data-ssr-modal auto-show hook")
	}
	if !strings.Contains(body, `href="/quiz/`+code+`"`) {
		t.Errorf("notice modal missing the return link to /quiz/%s", code)
	}
}
