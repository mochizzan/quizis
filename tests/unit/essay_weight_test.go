package unit

import (
	"fmt"
	"testing"

	"quiz/internal/handlers"
	"quiz/internal/quizengine"
)

// TestEssayScoreMatchesMCQWeight pins contract C11: an essay graded 100 on
// an N-question quiz contributes exactly quizengine.CentiPercent(1, N) —
// the same weight as one correct multiple-choice answer — for every N
// (both round half-up with the same integer arithmetic).
func TestEssayScoreMatchesMCQWeight(t *testing.T) {
	for _, n := range []int{1, 2, 3, 4, 5, 6, 7, 8, 10, 16, 32, 64, 100} {
		t.Run(fmt.Sprintf("total %d", n), func(t *testing.T) {
			got := handlers.EssayScore(100, n)
			want := quizengine.CentiPercent(1, n)
			if got != want {
				t.Errorf("EssayScore(100, %d) = %v, want CentiPercent(1, %d) = %v",
					n, got, n, want)
			}
		})
	}
}

// TestEssayScoreValues pins the SUM/total arithmetic: partial sums add up
// as sum/total rounded half-up to 2dp, and a non-positive total is 0.
func TestEssayScoreValues(t *testing.T) {
	cases := []struct {
		name  string
		sum   float64
		total int
		want  float64
	}{
		{"single essay 80 of 2 → 40", 80, 2, 40},
		{"single essay 100 of 4 → 25", 100, 4, 25},
		{"two essays 200 of 5 → 40", 200, 5, 40},
		{"half-up 100/3 → 33.33", 100, 3, 33.33},
		{"half-up 80/3 → 26.67", 80, 3, 26.67},
		{"all essays graded 0 → 0", 0, 4, 0},
		{"zero total is 0 not NaN", 80, 0, 0},
		{"negative total guarded", 80, -1, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := handlers.EssayScore(c.sum, c.total); got != c.want {
				t.Errorf("EssayScore(%v, %d) = %v, want %v", c.sum, c.total, got, c.want)
			}
		})
	}
}
