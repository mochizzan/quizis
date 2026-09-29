package quizengine

// MaxAttemptsFor returns the attempt cap for a quiz: global-timer quizzes
// always allow exactly one attempt regardless of the stored setting
// (spec §6.7); per-question (per_soal) and no-timer (tanpa_timer) quizzes
// honour the configured value (minimum 1).
func MaxAttemptsFor(timerType string, configured uint8) uint8 {
	if timerType == "global" {
		return 1
	}
	if configured < 1 {
		return 1
	}
	return configured
}

// AttemptAllowed reports whether starting nextAttemptNo (1-based) is within
// maxAttempts. Callers pass the attempt number they are about to create.
func AttemptAllowed(nextAttemptNo, maxAttempts uint8) bool {
	return nextAttemptNo >= 1 && nextAttemptNo <= maxAttempts
}
