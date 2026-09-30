package integration

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// rowHTMLFor extracts the rendered <tr> that holds the given title cell —
// the isolation seam for per-row action assertions on the quiz list.
func rowHTMLFor(t *testing.T, body, title string) string {
	t.Helper()
	marker := "<td>" + title + "</td>"
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatalf("row for %q not rendered", title)
	}
	start := strings.LastIndex(body[:i], "<tr>")
	end := strings.Index(body[i:], "</tr>")
	if start < 0 || end < 0 {
		t.Fatalf("row boundaries for %q not found", title)
	}
	return body[start : i+end+len("</tr>")]
}

// TestQuizListActionsFinishGate pins the SSR status gate of the Finish
// control on GET /teacher/quiz: its data-finish attribute renders for
// aktif and berjalan rows and never for nonaktif or selesai rows.
func TestQuizListActionsFinishGate(t *testing.T) {
	ts, pool, _, _ := quizFixture(t)
	ck := guruLogin(t, ts)

	// nonaktif — created, never activated
	nonaktifID := createQuiz(t, ts, ck, quizForm("Actions nonaktif"))

	// aktif — activation needs >=1 composed question
	aktifID := createQuiz(t, ts, ck, quizForm("Actions aktif"))
	q := addQuestion(t, ts, pool, ck, "Actions gate question?", "pg", []string{"yes", "no"}, []int{0})
	compose(t, ts, ck, aktifID, q)
	if resp, body := setStatus(t, ts, ck, aktifID, "aktif"); resp.StatusCode != http.StatusOK {
		t.Fatalf("activate aktif = %d: %s", resp.StatusCode, body)
	}

	// berjalan — global-timer quiz (quizForm default) + teacher START
	berjalanID := createQuiz(t, ts, ck, quizForm("Actions berjalan"))
	q2 := addQuestion(t, ts, pool, ck, "Actions running question?", "pg", []string{"yes", "no"}, []int{0})
	compose(t, ts, ck, berjalanID, q2)
	if resp, body := setStatus(t, ts, ck, berjalanID, "aktif"); resp.StatusCode != http.StatusOK {
		t.Fatalf("activate berjalan fixture = %d: %s", resp.StatusCode, body)
	}
	startGlobal(t, ts, ck, berjalanID)

	// selesai — activate, then close with nobody working (straight 200)
	selesaiID := createQuiz(t, ts, ck, quizForm("Actions selesai"))
	q3 := addQuestion(t, ts, pool, ck, "Actions finished question?", "pg", []string{"yes", "no"}, []int{0})
	compose(t, ts, ck, selesaiID, q3)
	if resp, body := setStatus(t, ts, ck, selesaiID, "aktif"); resp.StatusCode != http.StatusOK {
		t.Fatalf("activate selesai fixture = %d: %s", resp.StatusCode, body)
	}
	if resp, body := setStatus(t, ts, ck, selesaiID, "selesai"); resp.StatusCode != http.StatusOK {
		t.Fatalf("close selesai fixture = %d: %s", resp.StatusCode, body)
	}

	// the fixtures really reached their statuses (guards the gate itself)
	for _, want := range []struct {
		id     uint64
		status string
	}{
		{nonaktifID, "nonaktif"},
		{aktifID, "aktif"},
		{berjalanID, "berjalan"},
		{selesaiID, "selesai"},
	} {
		if got := quizStatus(t, pool, want.id); got != want.status {
			t.Fatalf("quiz %d status = %q, want %q", want.id, got, want.status)
		}
	}

	resp, body := getWith(t, ts.URL+"/teacher/quiz", ck)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /teacher/quiz = %d: %s", resp.StatusCode, body)
	}

	cases := []struct {
		name       string
		title      string
		id         uint64
		chip       string
		wantFinish bool
	}{
		{"nonaktif row hides finish", "Actions nonaktif", nonaktifID, "Tidak aktif", false},
		{"aktif row shows finish", "Actions aktif", aktifID, "Aktif", true},
		{"berjalan row shows finish", "Actions berjalan", berjalanID, "Berjalan", true},
		{"selesai row hides finish", "Actions selesai", selesaiID, "Selesai", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			row := rowHTMLFor(t, body, c.title)
			// the row carries the chip of the status the gate keys off
			if !strings.Contains(row, ">"+c.chip+"<") {
				t.Errorf("row for %q missing chip %q", c.title, c.chip)
			}
			has := strings.Contains(row, "data-finish=")
			if has != c.wantFinish {
				t.Fatalf("data-finish present = %v, want %v (row: %s)", has, c.wantFinish, row)
			}
			if c.wantFinish {
				want := fmt.Sprintf(`data-finish="/teacher/quiz/%d/status"`, c.id)
				if !strings.Contains(row, want) {
					t.Errorf("row missing %s", want)
				}
			}
		})
	}
}
