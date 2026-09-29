package unit

import (
	"strings"
	"testing"

	"quiz/internal/quizengine"
)

// Spec §9 authlogic: password validation. (Forgot-password match/mismatch
// and the generic login message are DB-backed and covered — including their
// message identity — by tests/integration/auth_test.go; the join-code
// collision RETRY loop is covered by tests/integration/quiz_crud_test.go.)
func TestPasswordMeetsPolicy(t *testing.T) {
	cases := []struct {
		password string
		want     bool
	}{
		{"", false},
		{"1234567", false},  // 7 chars → rejected (spec §438)
		{"12345678", true},  // 8 chars → accepted
		{"123456789", true}, // longer accepted
		{"🔒🔒🔒🔒", false},     // 4 characters (16 bytes) → rejected: chars, not bytes
		{"пароль12", true},  // 8 characters → accepted
	}
	for _, c := range cases {
		if got := quizengine.PasswordMeetsPolicy(c.password); got != c.want {
			t.Errorf("PasswordMeetsPolicy(%q) = %v, want %v", c.password, got, c.want)
		}
	}
}

// Spec §7/§9: 6-char code from the join-code alphabet (A–Z without I/O, no
// 0/1 ambiguity).
func TestNewJoinCodeShape(t *testing.T) {
	if len(quizengine.JoinCodeAlphabet) != 32 {
		t.Fatalf("alphabet length = %d, want 32 (32 | 256 keeps byte-modulo unbiased)",
			len(quizengine.JoinCodeAlphabet))
	}
	for _, excluded := range []string{"0", "1", "I", "O"} {
		if strings.Contains(quizengine.JoinCodeAlphabet, excluded) {
			t.Errorf("alphabet must exclude ambiguous %q", excluded)
		}
	}

	for i := 0; i < 64; i++ {
		code, err := quizengine.NewJoinCode()
		if err != nil {
			t.Fatalf("NewJoinCode error: %v", err)
		}
		if len(code) != quizengine.JoinCodeLen {
			t.Fatalf("code %q length = %d, want %d", code, len(code), quizengine.JoinCodeLen)
		}
		for _, ch := range code {
			if !strings.ContainsRune(quizengine.JoinCodeAlphabet, ch) {
				t.Fatalf("code %q contains character %q outside the alphabet", code, ch)
			}
		}
	}
}
