package integration

import (
	"context"
	"net/http"
	"testing"
	"time"

	"quiz/internal/handlers"
)

// TestJoinSeesFreshStatusAfterLock (N-004): a START that lands while a join
// is blocked on the in-transaction quiz re-read must be observed — the join
// resolves 409 QUIZ_IN_PROGRESS and leaves no orphan participant row.
func TestJoinSeesFreshStatusAfterLock(t *testing.T) {
	ts, _, pool := globalFixture(t)
	ck := guruLogin(t, ts)
	quizID, code, _ := linearQuiz(t, ts, pool, ck, "Join race", 1)
	st := studentCookie(t, pool, "join-racer")
	uid := userOf(t, ts, st)

	ctx := context.Background()
	tx1, err := pool.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx1: %v", err)
	}
	defer tx1.Rollback()
	var locked string
	if err := tx1.QueryRowContext(ctx,
		`SELECT status FROM quizzes WHERE id = ? FOR UPDATE`, quizID).Scan(&locked); err != nil {
		t.Fatalf("lock quiz row: %v", err)
	}
	if locked != "aktif" {
		t.Fatalf("locked status = %q, want aktif", locked)
	}

	type joinRes struct {
		resp *http.Response
		body string
	}
	ch := make(chan joinRes, 1)
	go func() {
		resp, body := postJoin(t, ts, code, st)
		ch <- joinRes{resp: resp, body: body}
	}()

	time.Sleep(300 * time.Millisecond) // let the join block at the in-tx re-read
	if _, err := tx1.ExecContext(ctx,
		`UPDATE quizzes SET status = 'berjalan' WHERE id = ?`, quizID); err != nil {
		t.Fatalf("START inside tx1: %v", err)
	}
	if err := tx1.Commit(); err != nil {
		t.Fatalf("commit tx1: %v", err)
	}

	select {
	case r := <-ch:
		assertFail(t, r.resp, r.body,
			http.StatusConflict, handlers.ErrQuizInProgress, handlers.MsgQuizInProgress)
	case <-time.After(15 * time.Second):
		t.Fatal("join stayed blocked after the START commit")
	}

	var orphans int
	if err := pool.QueryRow(
		`SELECT COUNT(*) FROM participants
		 WHERE quiz_id = ? AND user_id = ? AND status IN ('registered','pending')`,
		quizID, uid,
	).Scan(&orphans); err != nil {
		t.Fatalf("orphan count: %v", err)
	}
	if orphans != 0 {
		t.Fatalf("orphan participant rows = %d, want 0", orphans)
	}
}

// TestSweepConcurrentConnectSafe (N-003): SweepDisconnects racing Connect
// must finish promptly (no deadlock on the beat registry) and the freeze,
// when it lands, must pin ends_at to the last-heartbeat instant with resume
// handing the frozen remainder back.
func TestSweepConcurrentConnectSafe(t *testing.T) {
	ts, globals, pool := globalFixture(t)
	ck := guruLogin(t, ts)
	quizID, code, _ := linearQuiz(t, ts, pool, ck, "Sweep race", 1)
	st := studentCookie(t, pool, "sweep-racer")
	if resp, body := postJoin(t, ts, code, st); resp.StatusCode != http.StatusOK {
		t.Fatalf("join = %d: %s", resp.StatusCode, body)
	}
	startGlobal(t, ts, ck, quizID)
	pid, status, _ := participantRowFor(t, pool, quizID, userOf(t, ts, st))
	if status != "started" {
		t.Fatalf("participant status = %q, want started", status)
	}

	readEnds := func() time.Time {
		t.Helper()
		var ends time.Time
		if err := pool.QueryRow(
			`SELECT ends_at FROM participants WHERE id = ?`, pid,
		).Scan(&ends); err != nil {
			t.Fatalf("ends_at: %v", err)
		}
		return ends.UTC()
	}
	origEnds := readEnds()

	absDiff := func(a, b time.Time) time.Duration {
		d := a.Sub(b)
		if d < 0 {
			d = -d
		}
		return d
	}

	lastSent := time.Now().UTC().Truncate(time.Second).Add(-30 * time.Second)
	globals.Live.Disconnect(pid, lastSent)

	ctx := context.Background()
	sweepDone := make(chan struct{}, 1)
	connDone := make(chan struct{}, 1)
	go func() {
		globals.Live.SweepDisconnects(ctx, time.Now(), 5*time.Second)
		sweepDone <- struct{}{}
	}()
	go func() {
		globals.Live.Connect(pid)
		connDone <- struct{}{}
	}()
	for range 2 {
		select {
		case <-sweepDone:
		case <-connDone:
		case <-time.After(15 * time.Second):
			t.Fatal("deadlock: sweep/connect pair did not finish")
		}
	}

	switch ends := readEnds(); {
	case absDiff(ends, lastSent) < 5*time.Second:
		// The sweep won: ends_at is pinned to the last heartbeat, and a
		// reconnect must hand the frozen remainder back.
		globals.Live.Connect(pid)
		if after := readEnds(); !after.After(ends) {
			t.Fatalf("resume lost time: frozen=%s resumed=%s (orig %s)", ends, after, origEnds)
		}
	case absDiff(ends, origEnds) < 5*time.Second:
		// Connect won the race, so the clock is untouched; re-seed a stale
		// beat and freeze deterministically, then verify the same pin+resume.
		lastSent2 := time.Now().UTC().Truncate(time.Second).Add(-30 * time.Second)
		globals.Live.Disconnect(pid, lastSent2)
		globals.Live.SweepDisconnects(ctx, time.Now(), 5*time.Second)
		if frozen := readEnds(); absDiff(frozen, lastSent2) >= 5*time.Second {
			t.Fatalf("frozen ends_at %s, want last heartbeat %s", frozen, lastSent2)
		} else {
			globals.Live.Connect(pid)
			if after := readEnds(); !after.After(frozen) {
				t.Fatalf("resume lost time: frozen=%s resumed=%s", frozen, after)
			}
		}
	default:
		t.Fatalf("ends_at %s is neither the last heartbeat %s nor the original %s",
			ends, lastSent, origEnds)
	}
}
