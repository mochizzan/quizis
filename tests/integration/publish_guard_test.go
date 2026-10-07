package integration

import (
	"fmt"
	"net/url"
	"testing"
	"time"

	"quiz/internal/testutil"
)

// DB write failure must suppress the broadcast: with the pool closed, Stop
// cannot commit, so it must return 5xx and no force_stop may reach the
// teacher monitor stream (spec §11.17 publish-after-commit invariant).
func TestStopBroadcastSuppressedOnDBFailure(t *testing.T) {
	ts, globals, pool := globalFixture(t)
	ck := guruLogin(t, ts)
	quizID, _, _ := linearQuiz(t, ts, pool, ck, "Stop guard quiz", 1)
	startGlobal(t, ts, ck, quizID)

	// warm the guru session into the mirror, then attach the monitor stream
	// and consume the snapshot greeting before breaking the database.
	getWith(t, fmt.Sprintf("%s/teacher/quiz/%d/monitor", ts.URL, quizID), ck)
	globals.Hub.SetHeartbeatEvery(50 * time.Millisecond)
	monitor := openSSE(t, fmt.Sprintf("%s/teacher/quiz/%d/monitor/stream", ts.URL, quizID), ck)
	if ev, _, _, ok := monitor.readEvent(5 * time.Second); !ok || ev != "snapshot" {
		t.Fatalf("monitor greeting = %q ok=%v, want snapshot", ev, ok)
	}

	if err := pool.Close(); err != nil {
		t.Fatalf("close pool: %v", err)
	}

	resp, body := postJSON(t, fmt.Sprintf("%s/teacher/quiz/%d/stop", ts.URL, quizID),
		map[string]bool{"confirm": true}, ck)
	if resp.StatusCode < 500 {
		t.Fatalf("stop with dead DB = %d, want 5xx (%s)", resp.StatusCode, body)
	}

	// heartbeat window: only pings may arrive, never the force_stop payload.
	end := time.Now().Add(600 * time.Millisecond)
	for time.Now().Before(end) {
		ev, _, raw, _ := monitor.readEvent(time.Until(end))
		if ev != "" && ev != "ping" {
			t.Fatalf("broadcast escaped failed stop: event %q (raw %q)", ev, raw)
		}
	}
}

// Approve must fail closed when the database is down: 5xx, the reset row
// stays pending, and must_change_pw stays 0 (verified through an independent
// handle opened before the close).
func TestApproveResetFailsClosedOnDBFailure(t *testing.T) {
	ts, pool, _, _ := quizFixture(t)
	nama := "Guard Test"
	registerUser(t, ts, "guarduser", "guarduser@example.test", nama, "secret123")
	postForm(t, ts.URL+"/forgot-password", url.Values{"identity": {"guarduser"}, "nama": {nama}})
	id := pendingID(t, ts)
	guru := guruLogin(t, ts)

	// warm the guru session into the mirror before breaking the database.
	getWith(t, ts.URL+"/teacher/password-resets", guru)
	assertPool := testutil.DB(t)

	var userID uint64
	if err := assertPool.QueryRow(`SELECT user_id FROM password_resets WHERE id = ?`, id).Scan(&userID); err != nil {
		t.Fatalf("reset user: %v", err)
	}

	if err := pool.Close(); err != nil {
		t.Fatalf("close pool: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })

	resp, body := postForm(t, ts.URL+"/teacher/password-resets/"+id+"/approve",
		url.Values{"temp_password": {"Temp12345"}}, guru)
	if resp.StatusCode < 500 {
		t.Fatalf("approve with dead DB = %d, want 5xx (%s)", resp.StatusCode, body)
	}

	var status string
	if err := assertPool.QueryRow(`SELECT status FROM password_resets WHERE id = ?`, id).Scan(&status); err != nil {
		t.Fatalf("reset status: %v", err)
	}
	if status != "pending" {
		t.Errorf("reset status = %q, want pending", status)
	}
	var mustChange int
	if err := assertPool.QueryRow(`SELECT must_change_pw FROM users WHERE id = ?`, userID).Scan(&mustChange); err != nil {
		t.Fatalf("must_change_pw: %v", err)
	}
	if mustChange != 0 {
		t.Errorf("must_change_pw = %d, want 0", mustChange)
	}
}
