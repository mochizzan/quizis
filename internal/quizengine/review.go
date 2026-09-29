package quizengine

// ReviewLevel selects how much of a finished attempt is shown back
// (question_review setting, spec §6.9).
type ReviewLevel string

const (
	ReviewNone ReviewLevel = "none" // questions hidden
	ReviewText ReviewLevel = "text" // question + own answer + right/wrong, no key
	ReviewFull ReviewLevel = "full" // text + correct answers
)

// QuestionView is one question in a review/history view.
type QuestionView struct {
	Question      string
	YourAnswer    string
	Correct       bool
	CorrectAnswer string // stripped unless ReviewFull
}

// HistoryView is the rendered result of one attempt's review.
type HistoryView struct {
	ShowScore   bool
	ShowRanking bool
	Questions   []QuestionView // empty when ReviewNone
}

// BuildHistory applies the review matrix: the level gates question detail,
// showScore/showRanking gate the header numbers (spec §10 review matrix).
// The answer key is stripped here for ReviewText so callers can pass full
// data safely.
func BuildHistory(level ReviewLevel, showScore, showRanking bool, qs []QuestionView) HistoryView {
	hv := HistoryView{ShowScore: showScore, ShowRanking: showRanking}
	switch level {
	case ReviewText:
		hv.Questions = make([]QuestionView, len(qs))
		copy(hv.Questions, qs)
		for i := range hv.Questions {
			hv.Questions[i].CorrectAnswer = ""
		}
	case ReviewFull:
		hv.Questions = make([]QuestionView, len(qs))
		copy(hv.Questions, qs)
	default: // ReviewNone and any unknown level: no questions
		hv.Questions = nil
	}
	return hv
}
