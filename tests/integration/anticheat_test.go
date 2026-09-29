package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// TestVisibilityEventPipeline pins spec §6.9: every ENUM kind posts
// directly, rows land, the teacher SSE receives `cheat`, the card's
// violation predicate (count > 0 — the buttons' DOM gate) flips, same-kind
// floods collapse inside 10 s, and events after the attempt ended are
// ignored.
func TestVisibilityEventPipeline(t *testing.T) {
	ts, _, pool := globalFixture(t)
	ck := guruLogin(t, ts)
	quizID, code, _ := linearQuiz(t, ts, pool, ck, "Anti-cheat pipeline", 2)
	st := studentCookie(t, pool, "ac-student")
	if resp, body := postJoin(t, ts, code, st); resp.StatusCode != http.StatusOK {
		t.Fatalf("join = %d: %s", resp.StatusCode, body)
	}
	startGlobal(t, ts, ck, quizID)
	pid, _, _ := participantRowFor(t, pool, quizID, userOf(t, ts, st))

	// teacher stream: greeting first, then one `cheat` per recorded event
	teacher := openSSE(t, fmt.Sprintf("%s/teacher/quiz/%d/monitor/stream", ts.URL, quizID), ck)
	if ev, _, _, ok := teacher.readEvent(3 * time.Second); !ok || ev != "snapshot" {
		t.Fatalf("teacher greeting = %q ok=%v, want snapshot", ev, ok)
	}
	visibility := fmt.Sprintf("%s/quiz/%s/visibility", ts.URL, code)
	report := func(kind string) bool {
		t.Helper()
		resp, body := postJSON(t, visibility, map[string]string{"kind": kind}, st)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("visibility %q = %d: %s", kind, resp.StatusCode, body)
		}
		env := decodeEnv(t, body)
		var d struct {
			Recorded bool `json:"recorded"`
		}
		if err := json.Unmarshal(env.Data, &d); err != nil {
			t.Fatalf("visibility data %q: %v", env.Data, err)
		}
		return d.Recorded
	}
	rowsOf := func() int {
		t.Helper()
		var n int
		if err := pool.QueryRow(`SELECT COUNT(*) FROM anti_cheat_events
			WHERE participant_id = ?`, pid).Scan(&n); err != nil {
			t.Fatalf("event rows: %v", err)
		}
		return n
	}
	waitCheat := func(wantKind string) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			ev, data, _, ok := teacher.readEvent(time.Until(deadline))
			if !ok {
				break
			}
			if ev != "cheat" {
				continue
			}
			if wantKind == "" {
				return // any cheat event is enough (the negative case)
			}
			if contains(data, fmt.Sprintf(`"participant_id":%d`, pid)) &&
				contains(data, fmt.Sprintf(`"kind":"%s"`, wantKind)) {
				return
			}
		}
		t.Fatalf("no cheat event for kind %q", wantKind)
	}

	// first recorded event → row + teacher push + predicate flips to 1
	if !report("blur") {
		t.Fatalf("first blur not recorded")
	}
	if n := rowsOf(); n != 1 {
		t.Fatalf("rows after first blur = %d, want 1", n)
	}
	waitCheat("blur")
	resp, body := getWith(t, fmt.Sprintf("%s/teacher/quiz/%d/monitor", ts.URL, quizID), ck)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("monitor = %d", resp.StatusCode)
	}
	if !contains(body, `"violations":1`) {
		t.Fatalf("violation predicate not visible in monitor data: %s", body)
	}

	// identical event inside 10 s → dropped, still one row, no second push
	if report("blur") {
		t.Fatalf("second blur within 10 s recorded again")
	}
	if n := rowsOf(); n != 1 {
		t.Fatalf("rows after collapsed blur = %d, want 1", n)
	}

	// every other ENUM kind posts directly and is logged (switch included)
	for _, kind := range []string{"switch", "minimize", "sleep"} {
		if !report(kind) {
			t.Fatalf("kind %q not recorded", kind)
		}
		waitCheat(kind)
	}
	if n := rowsOf(); n != 4 {
		t.Fatalf("rows after all four kinds = %d, want 4", n)
	}

	// finish, then reports are ignored: no row, no push
	if resp, body := postJSON(t, ts.URL+"/quiz/"+code+"/finish", nil, st); resp.StatusCode != http.StatusOK {
		t.Fatalf("finish = %d: %s", resp.StatusCode, body)
	}
	if report("sleep") {
		t.Fatalf("post-selesai report was recorded")
	}
	if n := rowsOf(); n != 4 {
		t.Fatalf("rows after ended report = %d, want 4", n)
	}
	// drain: nothing that arrived late may be a cheat push
	drainDeadline := time.Now().Add(600 * time.Millisecond)
	for time.Now().Before(drainDeadline) {
		ev, _, _, ok := teacher.readEvent(time.Until(drainDeadline))
		if !ok {
			break
		}
		if ev == "cheat" {
			t.Fatalf("cheat event published after the attempt ended")
		}
	}
}

// TestCheatToggleTransitions pins the toggle: two transitions on an
// already-flagged student leave cheating=1 — never a third flip, never a
// sticky flag (spec §6.9 "toggle idempotent").
func TestCheatToggleTransitions(t *testing.T) {
	ts, _, pool := globalFixture(t)
	ck := guruLogin(t, ts)
	quizID, code, _ := linearQuiz(t, ts, pool, ck, "Cheat toggle", 1)
	st := studentCookie(t, pool, "ct-student")
	if resp, body := postJoin(t, ts, code, st); resp.StatusCode != http.StatusOK {
		t.Fatalf("join = %d: %s", resp.StatusCode, body)
	}
	startGlobal(t, ts, ck, quizID)
	pid, _, _ := participantRowFor(t, pool, quizID, userOf(t, ts, st))
	if _, err := pool.Exec(`UPDATE participants SET cheating = 1 WHERE id = ?`, pid); err != nil {
		t.Fatalf("seed cheating: %v", err)
	}

	toggle := func(want bool) {
		t.Helper()
		resp, body := postJSON(t,
			fmt.Sprintf("%s/teacher/quiz/%d/participants/%d/action", ts.URL, quizID, pid),
			map[string]string{"action": "cheat_toggle"}, ck)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("toggle = %d: %s", resp.StatusCode, body)
		}
		env := decodeEnv(t, body)
		var d struct {
			Cheating bool `json:"cheating"`
		}
		if err := json.Unmarshal(env.Data, &d); err != nil {
			t.Fatalf("toggle data %q: %v", env.Data, err)
		}
		if d.Cheating != want {
			t.Fatalf("toggle response cheating=%v, want %v", d.Cheating, want)
		}
	}

	toggle(false) // transition 1: 1 → 0
	toggle(true)  // transition 2: 0 → 1
	var cheating bool
	if err := pool.QueryRow(`SELECT cheating FROM participants WHERE id = ?`, pid).
		Scan(&cheating); err != nil {
		t.Fatalf("read cheating: %v", err)
	}
	if !cheating {
		t.Fatalf("after two toggles cheating=false, want true (exactly two transitions)")
	}
}
