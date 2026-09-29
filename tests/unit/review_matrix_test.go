package unit

import (
	"fmt"
	"testing"

	"quiz/internal/quizengine"
)

func sampleQuestions(n int) []quizengine.QuestionView {
	qs := make([]quizengine.QuestionView, n)
	for i := range qs {
		qs[i] = quizengine.QuestionView{
			Question:      fmt.Sprintf("Q%d", i+1),
			YourAnswer:    fmt.Sprintf("A%d", i+1),
			Correct:       i%2 == 0,
			CorrectAnswer: fmt.Sprintf("K%d", i+1),
		}
	}
	return qs
}

// Spec §9: 3 review levels × show_final_score × ranking_live → correct
// history data (12 combinations).
func TestReviewMatrix(t *testing.T) {
	levels := []quizengine.ReviewLevel{
		quizengine.ReviewNone,
		quizengine.ReviewText,
		quizengine.ReviewFull,
	}
	for _, level := range levels {
		for _, showScore := range []bool{false, true} {
			for _, showRanking := range []bool{false, true} {
				name := fmt.Sprintf("%s/score=%v/ranking=%v", level, showScore, showRanking)
				t.Run(name, func(t *testing.T) {
					qs := sampleQuestions(3)
					hv := quizengine.BuildHistory(level, showScore, showRanking, qs)

					// header flags mirror the quiz settings verbatim
					if hv.ShowScore != showScore {
						t.Errorf("ShowScore = %v, want %v", hv.ShowScore, showScore)
					}
					if hv.ShowRanking != showRanking {
						t.Errorf("ShowRanking = %v, want %v", hv.ShowRanking, showRanking)
					}

					switch level {
					case quizengine.ReviewNone:
						if len(hv.Questions) != 0 {
							t.Errorf("none: %d questions shown, want 0", len(hv.Questions))
						}
					case quizengine.ReviewText:
						if len(hv.Questions) != 3 {
							t.Fatalf("text: %d questions, want 3", len(hv.Questions))
						}
						for i, q := range hv.Questions {
							if q.CorrectAnswer != "" {
								t.Errorf("text: Q%d leaked the answer key %q", i+1, q.CorrectAnswer)
							}
							if q.Question == "" || q.YourAnswer == "" {
								t.Errorf("text: Q%d lost question/own answer", i+1)
							}
						}
					case quizengine.ReviewFull:
						if len(hv.Questions) != 3 {
							t.Fatalf("full: %d questions, want 3", len(hv.Questions))
						}
						for i, q := range hv.Questions {
							if want := fmt.Sprintf("K%d", i+1); q.CorrectAnswer != want {
								t.Errorf("full: Q%d key = %q, want %q", i+1, q.CorrectAnswer, want)
							}
						}
					}
				})
			}
		}
	}
}

func TestBuildHistoryDoesNotMutateInput(t *testing.T) {
	qs := sampleQuestions(2)
	quizengine.BuildHistory(quizengine.ReviewText, true, true, qs)
	if qs[0].CorrectAnswer == "" {
		t.Error("BuildHistory must copy — the caller's data was mutated")
	}
}

func TestBuildHistoryUnknownLevelTreatedAsNone(t *testing.T) {
	hv := quizengine.BuildHistory(quizengine.ReviewLevel("bogus"), true, true, sampleQuestions(2))
	if len(hv.Questions) != 0 {
		t.Errorf("unknown level showed %d questions, want 0 (fail closed)", len(hv.Questions))
	}
}
