package quizengine

import (
	"crypto/rand"
	"strings"
)

// JoinCodeAlphabet is the join-code character set (spec §7): A–Z minus the
// ambiguous I/O, plus 2–9 (no0/1). Length 32 divides 256 evenly, so a
// single random byte mapped by modulo is unbiased.
const JoinCodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// JoinCodeLen is the code length (spec §7).
const JoinCodeLen = 6

// NewJoinCode draws a fresh 6-character join code from JoinCodeAlphabet
// using crypto/rand. Duplicate codes are handled by the caller: creation
// retries on the quizzes.code UNIQUE key (spec §7).
func NewJoinCode() (string, error) {
	var buf [JoinCodeLen]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	out := make([]byte, JoinCodeLen)
	for i, b := range buf {
		out[i] = JoinCodeAlphabet[int(b)%len(JoinCodeAlphabet)]
	}
	return string(out), nil
}

// ValidJoinCode reports whether raw is a structurally valid join code
// (JoinCodeLen alphabet characters, case-insensitive). It never checks the
// database — it only bounds untrusted input such as the pending_join cookie.
func ValidJoinCode(raw string) bool {
	if len(raw) != JoinCodeLen {
		return false
	}
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		if !strings.ContainsRune(JoinCodeAlphabet, rune(c)) {
			return false
		}
	}
	return true
}
