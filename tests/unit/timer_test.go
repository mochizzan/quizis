package unit

import (
	"testing"
	"time"

	"quiz/internal/quizengine"
)

func base() time.Time {
	return time.Date(2026, 9, 27, 8, 0, 0, 0, time.UTC)
}

func TestGlobalEndsAt(t *testing.T) {
	t0 := base()
	got := quizengine.GlobalEndsAt(t0, 300)
	if want := t0.Add(300 * time.Second); !got.Equal(want) {
		t.Errorf("GlobalEndsAt = %v, want %v", got, want)
	}
	// timer_on=0 → no deadline (zero time)
	if !quizengine.GlobalEndsAt(t0, 0).IsZero() {
		t.Error("totalSeconds=0 must yield a zero (no-deadline) time")
	}
}

func TestRemaining(t *testing.T) {
	t0 := base()

	if d := quizengine.Remaining(t0.Add(90*time.Second), t0); d != 90*time.Second {
		t.Errorf("Remaining(future) = %v, want 90s", d)
	}
	// now.Equal(endsAt) → time is up
	if d := quizengine.Remaining(t0, t0); d != 0 {
		t.Errorf("Remaining(now==endsAt) = %v, want 0 (expired)", d)
	}
	if d := quizengine.Remaining(t0, t0.Add(10*time.Second)); d != 0 {
		t.Errorf("Remaining(past) = %v, want 0 (clamped)", d)
	}
	// timer_on=0: zero endsAt → unbounded, never expired
	if d := quizengine.Remaining(time.Time{}, t0); d != quizengine.NoDeadline || d <= 0 {
		t.Errorf("Remaining(no deadline) = %v, want positive NoDeadline", d)
	}
}

func TestOnDisconnectFreezesAtLastHeartbeatNotDetection(t *testing.T) {
	t0 := base()
	endsAt := t0.Add(100 * time.Second)
	lastBeat := t0.Add(30*time.Second + 700*time.Millisecond)
	detected := t0.Add(45 * time.Second) // server noticed 14.3s after the beat

	freeze, remaining := quizengine.OnDisconnect(endsAt, lastBeat, detected)

	// Freeze at the LAST-HEARTBEAT second (truncated); a detection-time
	// freeze would return t0+45s / 55s and fail here (spec §6.6).
	if !freeze.Equal(t0.Add(30 * time.Second)) {
		t.Errorf("freeze = %v, want last-heartbeat second %v", freeze, t0.Add(30*time.Second))
	}
	if remaining != 70 {
		t.Errorf("remaining = %d, want 70 (endsAt - lastBeat, whole seconds)", remaining)
	}
}

func TestOnDisconnectEdgeCases(t *testing.T) {
	t0 := base()

	// zero endsAt (timer_on=0): nothing to freeze
	freeze, rem := quizengine.OnDisconnect(time.Time{}, t0.Add(10*time.Second), t0.Add(20*time.Second))
	if !freeze.IsZero() || rem != 0 {
		t.Errorf("OnDisconnect(no deadline) = (%v,%d), want (zero,0)", freeze, rem)
	}

	// lastBeat missing → fall back to now
	freeze, rem = quizengine.OnDisconnect(t0.Add(100*time.Second), time.Time{}, t0.Add(45*time.Second))
	if !freeze.Equal(t0.Add(45*time.Second)) || rem != 55 {
		t.Errorf("OnDisconnect(no beat) = (%v,%d), want (t0+45s,55)", freeze, rem)
	}

	// lastBeat after now (clock skew) → clamp to now
	freeze, rem = quizengine.OnDisconnect(t0.Add(100*time.Second), t0.Add(70*time.Second), t0.Add(60*time.Second))
	if !freeze.Equal(t0.Add(60*time.Second)) || rem != 40 {
		t.Errorf("OnDisconnect(skew) = (%v,%d), want (t0+60s,40)", freeze, rem)
	}

	// lastBeat beyond the original deadline → never negative
	freeze, rem = quizengine.OnDisconnect(t0.Add(10*time.Second), t0.Add(50*time.Second), t0.Add(60*time.Second))
	if !freeze.Equal(t0.Add(10*time.Second)) || rem != 0 {
		t.Errorf("OnDisconnect(past deadline) = (%v,%d), want (t0+10s,0)", freeze, rem)
	}
}

func TestOnReconnect(t *testing.T) {
	t0 := base()
	got := quizengine.OnReconnect(70, t0)
	if want := t0.Add(70 * time.Second); !got.Equal(want) {
		t.Errorf("OnReconnect(70) = %v, want %v (no paused time lost)", got, want)
	}
	// negative guard → treated as 0 remaining
	if got := quizengine.OnReconnect(-5, t0); !got.Equal(t0) {
		t.Errorf("OnReconnect(-5) = %v, want now", got)
	}
}

func TestPersonalTotal(t *testing.T) {
	t0 := base()
	_ = t0
	if got := quizengine.PersonalTotal(10, 30); got != 300*time.Second {
		t.Errorf("PersonalTotal(10,30) = %v, want 300s", got)
	}
	if got := quizengine.PersonalTotal(0, 30); got != 0 {
		t.Errorf("PersonalTotal(0,30) = %v, want 0", got)
	}
	if got := quizengine.PersonalTotal(5, -1); got != 0 {
		t.Errorf("PersonalTotal(negative) = %v, want 0", got)
	}
}
