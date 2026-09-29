package quizengine

import "unicode/utf8"

// PasswordMeetsPolicy is the password rule shared by register and
// change-password: at least 8 characters (spec §438). Counted in runes, so
// multi-byte characters count as one character, matching "8 characters".
func PasswordMeetsPolicy(password string) bool {
	return utf8.RuneCountInString(password) >= 8
}
