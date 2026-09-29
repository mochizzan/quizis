package quizengine

import "time"

// Clock abstracts the current time for components that need it; all timer
// helpers below take explicit times so they stay pure and testable. The
// package never calls time.Now() directly (plan protocol).
type Clock interface {
	Now() time.Time
}

// NoDeadline is the remaining time reported for a zero deadline
// (timer_on=0 → ends_at NULL): the countdown is unbounded, never expired.
const NoDeadline = time.Duration(1<<63 - 1)

// GlobalEndsAt computes the global quiz deadline. totalSeconds <= 0 means
// timer_on=0 → zero time (no deadline) (spec §6.6).
func GlobalEndsAt(startedAt time.Time, totalSeconds int) time.Time {
	if totalSeconds <= 0 {
		return time.Time{}
	}
	return startedAt.Add(time.Duration(totalSeconds) * time.Second)
}

// Remaining reports the time left until endsAt, clamped at 0. A zero endsAt
// is "no deadline" (timer_on=0) and reports NoDeadline. The boundary
// now.Equal(endsAt) counts as expired → 0.
func Remaining(endsAt, now time.Time) time.Duration {
	if endsAt.IsZero() {
		return NoDeadline
	}
	d := endsAt.Sub(now)
	if d < 0 {
		return 0
	}
	return d
}

// OnDisconnect freezes the countdown on a lost connection (spec §6.6). The
// freeze point is the LAST-HEARTBEAT second, never the detection time
// (now): time between the student's last beat and the server noticing the
// drop must not be charged to the student. Returns the frozen ends_at and
// the whole remaining seconds to persist (remaining_seconds). A zero endsAt
// is a no-deadline quiz: nothing to freeze, remaining 0.
func OnDisconnect(endsAt, lastBeat, now time.Time) (newEndsAt time.Time, remaining int) {
	if endsAt.IsZero() {
		return time.Time{}, 0
	}
	if lastBeat.IsZero() || lastBeat.After(now) {
		lastBeat = now
	}
	freeze := lastBeat.Truncate(time.Second)
	if freeze.After(endsAt) {
		freeze = endsAt
	}
	d := endsAt.Sub(freeze)
	if d < 0 {
		d = 0
	}
	return freeze, int(d / time.Second)
}

// OnReconnect resumes a frozen countdown: now + remaining seconds, so no
// paused time is lost (spec §6.6).
func OnReconnect(remainingSeconds int, now time.Time) time.Time {
	if remainingSeconds < 0 {
		remainingSeconds = 0
	}
	return now.Add(time.Duration(remainingSeconds) * time.Second)
}

// PersonalTotal is the per-question attempt budget: n × secondsPerQuestion.
func PersonalTotal(questionCount, secondsPerQuestion int) time.Duration {
	if questionCount < 0 || secondsPerQuestion < 0 {
		return 0
	}
	return time.Duration(questionCount) * time.Duration(secondsPerQuestion) * time.Second
}
