package handlers

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v5"
)

// exportRow is the row type shared by both writers (spec §6.11).
type exportRow struct {
	Rank     int     `csv:"Rank" json:"rank"`
	Name     string  `csv:"Name" json:"name"`
	Username string  `csv:"Username" json:"username"`
	Class    string  `csv:"Class" json:"class"`
	Major    string  `csv:"Major" json:"major"`
	Score    float64 `csv:"Score" json:"score"`
	Status   string  `csv:"Status" json:"status"`     // finished / removed / not-attempted
	Cheating string  `csv:"Cheating" json:"cheating"` // yes / no
	Attempts int     `csv:"Attempts" json:"attempts"`
}

// exportHeaders mirrors the csv tags above, in order. The CSV writer appends
// the dynamic Q<i> Answer / Q<i> Score pairs after these nine.
var exportHeaders = []string{
	"Peringkat", "Nama", "Username", "Kelas", "Jurusan",
	"Nilai", "Status", "Kecurangan", "Percobaan",
}

// guardValue prefixes ' to anything a spreadsheet would otherwise parse as
// a formula (spec §6.11 injection guard, both formats).
func guardValue(v string) string {
	if v == "" {
		return v
	}
	switch v[0] {
	case '=', '+', '-', '@':
		return "'" + v
	}
	return v
}

// exportRows converts results into injection-guarded export rows.
func exportRows(results []resultRow) []exportRow {
	rows := make([]exportRow, 0, len(results))
	for _, r := range results {
		var score float64
		switch {
		case r.Final.Valid:
			score = r.Final.Float64
		case r.Auto.Valid:
			score = r.Auto.Float64
		}
		cheating := "no"
		if r.Cheating {
			cheating = "yes"
		}
		rows = append(rows, exportRow{
			Rank:     r.Rank,
			Name:     guardValue(r.Name),
			Username: guardValue(r.Username),
			Class:    guardValue(r.Class),
			Major:    guardValue(r.Major),
			Score:    score,
			Status:   guardValue(r.Status),
			Cheating: cheating,
			Attempts: r.Attempts,
		})
	}
	return rows
}

// --- shared export data (both writers) ------------------------------------

// exportQuestion is one quiz question in seq order for the export: the FULL
// text (questionStats truncates at 80 chars for the results page — the
// export must not) plus the stored key rendered with the engine's own JSON
// shapes (see correctLetters).
type exportQuestion struct {
	Seq   int
	Text  string
	Type  string
	Kunci string // option letters; essays have no key → em dash
	// RawCorrect is the stored questions.correct JSON, untouched: the
	// results answer panel renders it (letters for pg/multi, the optional
	// free-text key for an essay) where Kunci's essay branch is "—".
	RawCorrect string
}

// exportAnswer is one student's rendered answer cell for one question.
// Text is option letters for pg/multi (lettersOf), the typed text for essays
// and "" when unanswered. Score is UNIFORM across question types: the
// question's contribution to the 100-point final — the exact points that
// roll into score_auto/final_score (rule on loadExportAnswers); nil = blank
// cell (nothing to award — not 0, not "—").
type exportAnswer struct {
	Text  string
	Score *float64
}

// answerCells maps username → question seq → cell. Keys are RAW database
// usernames (lookup with results[i].Username — exportRows guards for
// display only). username is UNIQUE (migrations/0001_init.sql) and
// loadResults emits exactly one row per user, so the keys line up 1:1 with
// exportRows.
type answerCells map[string]map[int]exportAnswer

// get returns the cell for (username, seq); the zero value (blank text, no
// score) covers unscored-attempt gaps and unknown keys.
func (c answerCells) get(username string, seq int) exportAnswer {
	if bySeq, ok := c[username]; ok {
		return bySeq[seq]
	}
	return exportAnswer{}
}

// loadExportQuestions reads the quiz's questions in seq order with FULL text
// and the stored correct key. results.go questionStats is reused unchanged
// for the statistics — only its 80-char UI Text is bypassed here.
func loadExportQuestions(ctx context.Context, db *sql.DB, quizID uint64) ([]exportQuestion, error) {
	rows, err := db.QueryContext(ctx, `SELECT qq.seq, q.teks, q.type, COALESCE(q.correct, '')
		FROM quiz_questions qq JOIN questions q ON q.id = qq.question_id
		WHERE qq.quiz_id = ? ORDER BY qq.seq`, quizID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []exportQuestion
	for rows.Next() {
		var q exportQuestion
		var correct string
		if err := rows.Scan(&q.Seq, &q.Text, &q.Type, &correct); err != nil {
			return nil, err
		}
		q.RawCorrect = correct
		q.Kunci = correctLetters(q.Type, correct)
		out = append(out, q)
	}
	return out, rows.Err()
}

// correctLetters renders questions.correct with the engine's own JSON
// shapes: quizengine.IsCorrect decodes a JSON number for pg ("pg correct is
// a scalar index, multi is an index array" — teacher_questions.go
// questionInput) or an index array for multi via indexSet; essays are never
// auto-graded and have no key. The letter mapping mirrors lettersOf
// (results.go): original option index → A–Z, out-of-range → no key.
func correctLetters(qType, raw string) string {
	if qType == "essay" {
		return "—"
	}
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return ""
	}
	var idx []int
	switch t := v.(type) {
	case float64:
		idx = []int{int(t)}
	case []any:
		for _, item := range t {
			n, ok := item.(float64)
			if !ok {
				return ""
			}
			idx = append(idx, int(n))
		}
	default:
		return ""
	}
	if len(idx) == 0 {
		return ""
	}
	letters := make([]string, 0, len(idx))
	for _, i := range idx {
		if i < 0 || i > 25 {
			return ""
		}
		letters = append(letters, string(rune('A'+i)))
	}
	return strings.Join(letters, ", ")
}

// essayAnswerText decodes a stored essay answer (a JSON string, spec §6.8)
// with the grading page's fallback (results.go GrantingPage: plain text that
// fails to decode, or an empty string, is shown raw).
func essayAnswerText(raw string) string {
	if raw == "" {
		return ""
	}
	var text string
	if err := json.Unmarshal([]byte(raw), &text); err == nil && text != "" {
		return text
	}
	return raw
}

// loadExportAnswers renders every latest-attempt answer of the quiz, keyed
// username → question seq (both writers address cells through answerCells).
//
// Q Score is UNIFORM: every cell is the question's CONTRIBUTION to the
// 100-point exam — the same points the engine counts, so a student's cells
// sum to Total Score (within the engine's 2-decimal rounding):
//   - pg/multi: 100/total when is_correct = 1, 0 when is_correct = 0 —
//     score_auto = CentiPercent(#is_correct=1, FULL question count)
//     (student.go scoreAndFinish → quizengine.CentiPercent): equal weight,
//     unanswered questions have no answers row at all and count as wrong.
//   - graded essay: stored answers.score / total — GradeAnswer keeps the
//     raw 0–100 per-question score in answers.score but contributes
//     essay_score = Σanswers.score/total to the final (results.go), so a
//     cell holds its marginal score/total (nEssay=1 → 80 → 80/total).
//   - answers.score is written ONLY by GradeAnswer for essays (results.go
//     "UPDATE answers SET score = …"; the auto-grade INSERT in student.go
//     has no score column, so score stays NULL there).
//
// Blank cells (nil Score): an ungraded essay (row, no score), students with
// no attempt, and unanswered questions on an UNSCORED attempt — the scored
// ones get an explicit 0 from fillUnansweredScores below. "Nilai Maks" on
// Pertanyaan (100/total) shares these units.
func loadExportAnswers(ctx context.Context, db *sql.DB, quizID uint64,
	questions []exportQuestion,
) (answerCells, error) {
	essay := make(map[int]bool, len(questions))
	for _, q := range questions {
		essay[q.Seq] = q.Type == "essay"
	}
	rows, err := db.QueryContext(ctx, `
		SELECT u.username, qq.seq, COALESCE(a.answer, ''), a.is_correct, a.score
		FROM answers a
		JOIN participants p ON p.id = a.participant_id
		JOIN users u ON u.id = p.user_id
		JOIN quiz_questions qq ON qq.quiz_id = p.quiz_id AND qq.question_id = a.question_id
		WHERE p.quiz_id = ?
		  AND (p.user_id, p.attempt_no) IN (
		    SELECT user_id, MAX(attempt_no) FROM participants
		    WHERE quiz_id = ? GROUP BY user_id)`,
		quizID, quizID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := answerCells{}
	total := float64(len(questions))
	for rows.Next() {
		var username, raw string
		var seq int
		var isCorrect sql.NullInt64
		var stored sql.NullFloat64
		if err := rows.Scan(&username, &seq, &raw, &isCorrect, &stored); err != nil {
			return nil, err
		}
		isEssay, ok := essay[seq]
		if !ok {
			continue
		}
		var a exportAnswer
		if isEssay {
			a.Text = essayAnswerText(raw)
		} else {
			a.Text = lettersOf(raw)
		}
		switch {
		case stored.Valid:
			// graded essay: the stored 0–100 score contributes
			// score/total points (GradeAnswer essay_score =
			// Σanswers.score/total — see the rule above)
			if total > 0 {
				v := stored.Float64 / total
				a.Score = &v
			}
		case isCorrect.Valid:
			var v float64
			if isCorrect.Int64 != 0 && total > 0 {
				v = 100 / total
			}
			a.Score = &v
		}
		if out[username] == nil {
			out[username] = make(map[int]exportAnswer, len(questions))
		}
		out[username][seq] = a
	}
	return out, rows.Err()
}

// fillUnansweredScores gives every question of a SCORED attempt (Auto or
// Final set) an explicit 0 where the student never answered: the engine
// counts unanswered as wrong — score_auto = CentiPercent(#correct, FULL
// question count), so 0 is exactly what rolls into the final there.
// Unscored (in-progress) attempts keep those cells blank, and an existing
// row without a score (an essay awaiting grading) is left untouched.
func fillUnansweredScores(results []resultRow, questions []exportQuestion,
	cells answerCells,
) {
	for _, r := range results {
		if !r.Final.Valid && !r.Auto.Valid {
			continue
		}
		bySeq, ok := cells[r.Username]
		if !ok {
			bySeq = make(map[int]exportAnswer, len(questions))
			cells[r.Username] = bySeq
		}
		for _, q := range questions {
			if _, answered := bySeq[q.Seq]; answered {
				continue
			}
			zero := 0.0
			bySeq[q.Seq] = exportAnswer{Score: &zero}
		}
	}
}

// writeExportCSV emits the flat export: a UTF-8 BOM first, then row 1 =
// exactly the existing nine headers in order followed by the Q<i> Answer /
// Q<i> Score pairs, row 2 = the full question text under each Answer column
// (its Score column blank), rows 3+ = one student per row with the legacy
// nine values untouched and the new columns appended. Q<i> Score carries
// the same contribution values as sheet 1 (loadExportAnswers); results
// provides the RAW usernames answerCells is keyed by (rows hold guarded
// display values).
func writeExportCSV(w io.Writer, results []resultRow, rows []exportRow,
	questions []exportQuestion, answers answerCells,
) error {
	if _, err := w.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
		return err
	}
	enc := csv.NewWriter(w)

	header := append([]string{}, exportHeaders...)
	for i := range questions {
		header = append(header, fmt.Sprintf("Q%d Jawaban", i+1), fmt.Sprintf("Q%d Nilai", i+1))
	}
	if err := enc.Write(header); err != nil {
		return err
	}

	qrow := make([]string, len(header))
	qrow[0] = "Teks pertanyaan"
	for i, q := range questions {
		qrow[9+2*i] = guardValue(q.Text)
	}
	if err := enc.Write(qrow); err != nil {
		return err
	}

	for i, r := range rows {
		rec := make([]string, 0, len(header))
		rec = append(rec,
			strconv.Itoa(r.Rank),
			r.Name,
			r.Username,
			r.Class,
			r.Major,
			strconv.FormatFloat(r.Score, 'f', -1, 64),
			r.Status,
			r.Cheating,
			strconv.Itoa(r.Attempts))
		for _, q := range questions {
			a := answers.get(results[i].Username, q.Seq)
			rec = append(rec, guardValue(a.Text))
			if a.Score != nil {
				rec = append(rec, strconv.FormatFloat(*a.Score, 'f', -1, 64))
			} else {
				rec = append(rec, "")
			}
		}
		if err := enc.Write(rec); err != nil {
			return err
		}
	}
	enc.Flush()
	return enc.Error()
}

// ExportResults is GET /teacher/quiz/:id/results/export?format=csv|xlsx —
// an absent format defaults to csv; every other value is a 400.
func (t *Teacher) ExportResults(c *echo.Context) error {
	id, rerr := monitorQuizID(c)
	if rerr != nil {
		return fail(c, rerr.Status, rerr.Code, rerr.Msg)
	}
	ctx := c.Request().Context()
	quiz, rerr, err := quizByID(ctx, t.DB, id)
	if err != nil {
		return err
	}
	if rerr != nil {
		return fail(c, rerr.Status, rerr.Code, rerr.Msg)
	}
	format := c.QueryParam("format")
	if format == "" {
		format = "csv"
	}
	if format != "csv" && format != "xlsx" {
		return fail(c, http.StatusBadRequest, ErrValidation, "Format ekspor tidak valid.")
	}
	results, err := loadResults(ctx, t.DB, quiz.ID)
	if err != nil {
		return err
	}
	questions, err := loadExportQuestions(ctx, t.DB, quiz.ID)
	if err != nil {
		return err
	}
	answers, err := loadExportAnswers(ctx, t.DB, quiz.ID, questions)
	if err != nil {
		return err
	}
	// unanswered questions of SCORED attempts → explicit 0 (engine counts
	// them wrong); unscored attempts keep blank cells
	fillUnansweredScores(results, questions, answers)
	rows := exportRows(results)

	w := c.Response()
	if format == "csv" {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition",
			`attachment; filename="quiz-`+quiz.Code+`.csv"`)
		w.WriteHeader(http.StatusOK)
		return writeExportCSV(w, results, rows, questions, answers)
	}

	// XLSX: user-data strings go through SetCellStr (guarded), numbers
	// through SetCellValue and Rekapitulasi metrics through SetCellFormula —
	// see export_sheets.go for the fixed 3-sheet workbook.
	stats, err := questionStats(ctx, t.DB, quiz.ID)
	if err != nil {
		return err
	}
	f, err := buildExportWorkbook(quiz, rows, results, questions, stats, answers)
	if err != nil {
		return err
	}
	defer f.Close()
	w.Header().Set("Content-Type",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition",
		`attachment; filename="quiz-`+quiz.Code+`.xlsx"`)
	w.WriteHeader(http.StatusOK)
	return f.Write(w)
}
