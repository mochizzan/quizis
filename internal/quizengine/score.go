package quizengine

// CentiPercent returns correct/total as a percent with two decimals,
// rounded half-up using integer arithmetic only:
//
//	centi = round(correct * 10000 / total); result = centi / 100
//
// Spec §10: integer half-up rounding, no floating-point accumulation.
// A zero total scores 0 (never NaN).
func CentiPercent(correct, total int) float64 {
	if total <= 0 {
		return 0
	}
	centi := (correct*10000*2 + total) / (2 * total)
	return float64(centi) / 100
}
