package unit

import (
	"math"
	"testing"

	"quiz/internal/quizengine"
)

func TestCentiPercent(t *testing.T) {
	cases := []struct {
		name           string
		correct, total int
		want           float64
	}{
		{"zero total is 0 not NaN", 0, 0, 0},
		{"negative total guarded", 3, -1, 0},
		{"none correct", 0, 10, 0},
		{"all correct", 7, 7, 100},
		{"exact half", 5, 10, 50},
		{"5/7 → 71.43 (half-up)", 5, 7, 71.43},
		{"1/3 → 33.33", 1, 3, 33.33},
		{"2/3 → 66.67", 2, 3, 66.67},
		{"1/16 → 6.25 exact", 1, 16, 6.25},
		{"1/32 → 3.13 (3.125 rounds half-up)", 1, 32, 3.13},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := quizengine.CentiPercent(c.correct, c.total)
			if math.Abs(got-c.want) > 1e-9 {
				t.Errorf("CentiPercent(%d,%d) = %v, want %v", c.correct, c.total, got, c.want)
			}
		})
	}
}

func TestIsCorrectPG(t *testing.T) {
	// pg: correct is the original option index; given is an array of them.
	cases := []struct {
		name           string
		correct, given any
		want           bool
	}{
		{"exact selection", float64(1), []any{float64(1)}, true},
		{"wrong selection", float64(1), []any{float64(0)}, false},
		{"extra selection", float64(1), []any{float64(0), float64(1)}, false},
		{"unanswered (nil)", float64(1), nil, false},
		{"int-typed key", 2, []any{float64(2)}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := quizengine.IsCorrect("pg", c.correct, c.given); got != c.want {
				t.Errorf("IsCorrect(pg,%v,%v) = %v, want %v", c.correct, c.given, got, c.want)
			}
		})
	}
}

func TestIsCorrectMulti(t *testing.T) {
	cases := []struct {
		name           string
		correct, given any
		want           bool
	}{
		{"exact match any order", []any{float64(0), float64(2)}, []any{float64(2), float64(0)}, true},
		{"partial is wrong", []any{float64(0), float64(2)}, []any{float64(0)}, false},
		{"extra is wrong", []any{float64(0), float64(2)}, []any{float64(0), float64(1), float64(2)}, false},
		{"missing one wrong", []any{float64(0), float64(2)}, []any{float64(1), float64(2)}, false},
		{"unanswered", []any{float64(0)}, nil, false},
		{"[]int key", []int{1, 3}, []any{float64(3), float64(1)}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := quizengine.IsCorrect("multi", c.correct, c.given); got != c.want {
				t.Errorf("IsCorrect(multi,%v,%v) = %v, want %v", c.correct, c.given, got, c.want)
			}
		})
	}
}

func TestIsCorrectEssayNeverAutoGraded(t *testing.T) {
	if quizengine.IsCorrect("essay", "any key", "any key") {
		t.Error("essay must never be auto-scored correct (spec §10)")
	}
	if quizengine.IsCorrect("essay", "k", nil) {
		t.Error("essay must never be auto-scored correct")
	}
}

func TestFinalScore(t *testing.T) {
	f := func(v float64) *float64 { return &v }

	// no essay → auto score passthrough
	if got := quizengine.FinalScore(f(80), nil, false); got == nil || *got != 80 {
		t.Errorf("FinalScore(no essay) = %v, want 80", got)
	}
	// essay graded → sum
	if got := quizengine.FinalScore(f(80), f(15), true); got == nil || *got != 95 {
		t.Errorf("FinalScore(graded) = %v, want 95", got)
	}
	// essay pending → nil
	if got := quizengine.FinalScore(f(80), nil, true); got != nil {
		t.Errorf("FinalScore(pending essay) = %v, want nil", got)
	}
	// auto nil with essay → nil
	if got := quizengine.FinalScore(nil, f(15), true); got != nil {
		t.Errorf("FinalScore(nil auto) = %v, want nil", got)
	}
	// auto nil without essay → nil passthrough
	if got := quizengine.FinalScore(nil, nil, false); got != nil {
		t.Errorf("FinalScore(nil auto, no essay) = %v, want nil", got)
	}
}
