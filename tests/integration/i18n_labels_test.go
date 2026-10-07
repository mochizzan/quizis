package integration

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// stripMonitorBlob removes the monitor-data JSON blob (raw status/timer
// codes legitimately remain there for JS logic) before badge-text asserts.
func stripMonitorBlob(t *testing.T, body string) string {
	t.Helper()
	const openTag = `<script type="application/json" id="monitor-data">`
	before, rest, ok := strings.Cut(body, openTag)
	if !ok {
		return body
	}
	_, after, ok := strings.Cut(rest, "</script>")
	if !ok {
		t.Fatalf("monitor-data blob not closed")
	}
	return before + after
}

// TestI18nLabels locks the Indonesian badge text on history + monitor:
// raw EN codes must not reach the DOM outside the wire blob.
func TestI18nLabels(t *testing.T) {
	ts, pool, _, _ := quizFixture(t)
	guru := guruLogin(t, ts)

	quizID, code := activeQuiz(t, ts, pool, guru, "Label Quiz", "aktif", "approve")

	approve := func(pid uint64) {
		t.Helper()
		resp, body := postJSON(t, fmt.Sprintf("%s/teacher/quiz/%d/participants/%d/action", ts.URL, quizID, pid),
			map[string]string{"action": "approve"}, guru)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("approve = %d: %s", resp.StatusCode, body)
		}
	}

	pend := studentCookie(t, pool, "i18n-pending")
	if resp, body := postJoin(t, ts, code, pend); resp.StatusCode != http.StatusOK {
		t.Fatalf("pending join = %d: %s", resp.StatusCode, body)
	}
	reg := studentCookie(t, pool, "i18n-registered")
	if resp, body := postJoin(t, ts, code, reg); resp.StatusCode != http.StatusOK {
		t.Fatalf("registered join = %d: %s", resp.StatusCode, body)
	}
	pidReg, _, _ := participantRowFor(t, pool, quizID, userOf(t, ts, reg))
	approve(pidReg)
	st := studentCookie(t, pool, "i18n-started")
	if resp, body := postJoin(t, ts, code, st); resp.StatusCode != http.StatusOK {
		t.Fatalf("started join = %d: %s", resp.StatusCode, body)
	}
	pidSt, _, _ := participantRowFor(t, pool, quizID, userOf(t, ts, st))
	approve(pidSt)
	if _, err := pool.Exec(`UPDATE participants SET status = 'started' WHERE id = ?`, pidSt); err != nil {
		t.Fatalf("seed started: %v", err)
	}

	// --- history: each status renders its Indonesian label -----------------
	cases := []struct {
		ck   *http.Cookie
		want string
	}{
		{pend, "Menunggu persetujuan"},
		{reg, "Terdaftar"},
		{st, "Berlangsung"},
	}
	for _, c := range cases {
		resp, body := getWith(t, ts.URL+"/history", c.ck)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("history = %d: %s", resp.StatusCode, body)
		}
		if !strings.Contains(body, c.want) {
			t.Errorf("history missing %q: %s", c.want, body)
		}
		for _, raw := range []string{">pending<", ">registered<", ">started<"} {
			if strings.Contains(body, raw) {
				t.Errorf("history leaks raw status %q: %s", raw, body)
			}
		}
	}

	// --- monitor: SSR page without the wire blob --------------------------
	resp, mbody := getWith(t, fmt.Sprintf("%s/teacher/quiz/%d/monitor", ts.URL, quizID), guru)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("monitor = %d: %s", resp.StatusCode, mbody)
	}
	stripped := stripMonitorBlob(t, mbody)
	for _, raw := range []string{">pending<", ">registered<", ">started<", ">per_soal<", ">tanpa_timer<", "global timer"} {
		if strings.Contains(stripped, raw) {
			t.Errorf("monitor leaks raw %q outside wire blob: %s", raw, stripped)
		}
	}
	for _, want := range []string{"Menunggu persetujuan", "Terdaftar", "Berlangsung", "Global"} {
		if !strings.Contains(stripped, want) {
			t.Errorf("monitor missing %q: %s", want, stripped)
		}
	}
}
