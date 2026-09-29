package unit

import (
	"testing"

	"quiz/internal/quizengine"
)

// Global-timer quizzes always allow exactly ONE attempt regardless of the
// stored max_attempts setting (spec §6.7).
func TestGlobalTimerForcesSingleAttempt(t *testing.T) {
	for _, configured := range []uint8{0, 1, 5, 99} {
		if got := quizengine.MaxAttemptsFor("global", configured); got != 1 {
			t.Errorf("MaxAttemptsFor(global, %d) = %d, want 1", configured, got)
		}
	}
}

// Per-question quizzes honour the configured cap (minimum 1).
func TestPerQuestionHonoursSetting(t *testing.T) {
	if got := quizengine.MaxAttemptsFor("per_soal", 3); got != 3 {
		t.Errorf("MaxAttemptsFor(per_soal, 3) = %d, want 3", got)
	}
	if got := quizengine.MaxAttemptsFor("per_soal", 1); got != 1 {
		t.Errorf("MaxAttemptsFor(per_soal, 1) = %d, want 1", got)
	}
	if got := quizengine.MaxAttemptsFor("per_soal", 0); got != 1 {
		t.Errorf("MaxAttemptsFor(per_soal, 0) = %d, want floor 1", got)
	}
}

// The pure attempt rule: a new attempt numbered N may start iff N ≤ cap.
// The concurrent-start guard (two rows in 'started' at once) lives in the
// Step 10/11 handlers; this pins the arithmetic rule they rely on.
func TestAttemptAllowed(t *testing.T) {
	cases := []struct {
		next uint8
		max  uint8
		want bool
	}{
		{1, 1, true},  // first attempt under a single-attempt quiz
		{2, 1, false}, // cap reached — attempt 2 rejected (global timer: always this case)
		{2, 2, true},  // second attempt allowed when configured
		{3, 2, false}, // cap reached
		{1, 3, true},
		{0, 3, false}, // attempt numbers are 1-based
	}
	for _, c := range cases {
		if got := quizengine.AttemptAllowed(c.next, c.max); got != c.want {
			t.Errorf("AttemptAllowed(%d,%d) = %v, want %v", c.next, c.max, got, c.want)
		}
	}
}
