package quizengine

import "time"

// CollapseWindow is the anti-cheat event collapse window (spec §6.5):
// repeats of the SAME kind inside the window are swallowed; a different
// kind always records.
const CollapseWindow = 10 * time.Second

// ShouldRecord decides whether a visibility/anti-cheat event is written and
// published. lastAt/lastKind are the participant's most recent event
// (zero lastAt = none yet). Events after the quiz ended are never recorded
// — the caller passes quizEnded to enforce that (spec §6.5).
func ShouldRecord(lastAt, now time.Time, kind, lastKind string, quizEnded bool) bool {
	if quizEnded {
		return false
	}
	if kind == lastKind && !lastAt.IsZero() && now.Sub(lastAt) < CollapseWindow {
		return false // same kind collapsed within the window
	}
	return true
}
