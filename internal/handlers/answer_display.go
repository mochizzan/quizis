package handlers

import (
	"database/sql"
	"encoding/json"

	"quiz/internal/quizengine"
)

// answerDisplay renders stored answer JSON for the monitor:
// pg/multi JSON []int → DisplayLetters via qorder;
// essay/text → unquote + truncate to 60 runes (existing behavior).
// malformed JSON → return raw (existing behavior).
func answerDisplay(raw string, qorder sql.NullString, questionID uint64) string {
	if len(raw) > 0 && raw[0] == '[' {
		var idx []int
		if err := json.Unmarshal([]byte(raw), &idx); err != nil {
			return raw
		}
		return quizengine.DisplayLetters(decodeOrder(qorder), questionID, idx)
	}
	var text string
	if err := json.Unmarshal([]byte(raw), &text); err != nil {
		return raw
	}
	runes := []rune(text)
	if len(runes) > 60 {
		return string(runes[:57]) + "…"
	}
	return text
}
