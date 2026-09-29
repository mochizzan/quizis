package quizengine

// IsCorrect reports whether a student's answer matches the key.
//
// correct/given are JSON-decoded values:
//   - pg:   correct is a number (original option index); given is an array
//     of original option indexes (e.g. [1]).
//   - multi: both are arrays of original option indexes; exact match is
//     required — missing or extra selections are wrong (spec §10).
//   - essay: never auto-graded, always false (spec §10).
func IsCorrect(qType string, correct, given any) bool {
	if qType == "essay" {
		return false
	}
	want, ok := indexSet(correct)
	if !ok {
		return false
	}
	got, ok := indexSet(given)
	if !ok {
		return false
	}
	if len(want) != len(got) {
		return false
	}
	for idx := range want {
		if !got[idx] {
			return false
		}
	}
	return true
}

// FinalScore combines the auto score with the essay score per spec §6.4:
// no essay questions → scoreAuto as-is; essay present but not yet graded →
// nil (pending); essay graded → scoreAuto + essayScore. A nil scoreAuto
// with an essay cannot form a total and stays nil.
func FinalScore(scoreAuto, essayScore *float64, hasEssay bool) *float64 {
	if !hasEssay {
		return scoreAuto
	}
	if scoreAuto == nil || essayScore == nil {
		return nil
	}
	sum := *scoreAuto + *essayScore
	return &sum
}

// indexSet decodes a JSON number / number array / []int into a set of
// original option indexes. ok=false for shapes that cannot represent
// selections (nil, objects, non-numeric members).
func indexSet(v any) (map[int]bool, bool) {
	set := make(map[int]bool)
	switch t := v.(type) {
	case nil:
		return set, true
	case int:
		set[t] = true
	case int64:
		set[int(t)] = true
	case uint64:
		set[int(t)] = true
	case float64:
		set[int(t)] = true
	case []int:
		for _, n := range t {
			set[n] = true
		}
	case []any:
		for _, item := range t {
			n, ok := toInt(item)
			if !ok {
				return nil, false
			}
			set[n] = true
		}
	default:
		return nil, false
	}
	return set, true
}

func toInt(v any) (int, bool) {
	switch t := v.(type) {
	case float64:
		return int(t), true
	case int:
		return t, true
	case int64:
		return int(t), true
	case uint64:
		return int(t), true
	default:
		return 0, false
	}
}
