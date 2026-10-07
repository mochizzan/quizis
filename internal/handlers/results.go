package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/labstack/echo/v5"

	"quiz/internal/quizengine"
)

// --- GET /teacher/quiz/:id/results -----------------------------------------

// resultRow is one student's latest attempt: shared by the results page and
// both export writers (spec §6.11).
type resultRow struct {
	Rank      int
	Username  string
	Name      string
	Class     string
	Major     string
	Auto      sql.NullFloat64 // score_auto, set at finish
	Final     sql.NullFloat64 // NULL while essays await grading
	Status    string          // finished / removed / not-attempted
	Cheating  bool
	Attempts  int
	Awaiting  bool // finished, essay still ungraded
	ScoreText string
}

// resultStatus maps the participant row onto the three export states
// (plan Step 14: finished / removed / not-attempted).
func resultStatus(status string) string {
	switch status {
	case "selesai":
		return "finished"
	case "dikeluarkan":
		return "removed"
	default:
		return "not-attempted"
	}
}

// loadResults reads one row per student (their latest attempt) ordered by
// score descending; unanswered rows carry Rank 0.
func loadResults(ctx context.Context, db *sql.DB, quizID uint64) ([]resultRow, error) {
	rows, err := db.QueryContext(ctx, `SELECT u.username, u.nama_lengkap,
		COALESCE(k.nama, ''), COALESCE(j.nama, ''),
		p.status, p.cheating, p.score_auto, p.final_score,
		(SELECT COUNT(*) FROM participants pc
		 WHERE pc.quiz_id = p.quiz_id AND pc.user_id = p.user_id) AS attempts
		FROM (
			SELECT p.* FROM participants p
			JOIN (SELECT user_id, MAX(attempt_no) AS m FROM participants
			      WHERE quiz_id = ? GROUP BY user_id) x
			  ON x.user_id = p.user_id AND p.attempt_no = x.m
			WHERE p.quiz_id = ?
		) p
		JOIN users u ON u.id = p.user_id
		LEFT JOIN ref_kelas k ON k.id = u.kelas_id
		LEFT JOIN ref_jurusan j ON j.id = u.jurusan_id
		ORDER BY COALESCE(p.final_score, p.score_auto) DESC, u.nama_lengkap`,
		quizID, quizID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []resultRow
	for rows.Next() {
		var r resultRow
		var status string
		var cheating int
		if err := rows.Scan(&r.Username, &r.Name, &r.Class, &r.Major,
			&status, &cheating, &r.Auto, &r.Final, &r.Attempts); err != nil {
			return nil, err
		}
		r.Cheating = cheating != 0
		r.Status = resultStatus(status)
		switch {
		case r.Final.Valid:
			r.ScoreText = fmt.Sprintf("%.2f", r.Final.Float64)
		case r.Auto.Valid && r.Status == "finished":
			r.Awaiting = true // essay still ungraded (spec §6.11)
		}
		if r.Final.Valid || r.Auto.Valid {
			r.Rank = len(out) + 1
		}
		out = append(out, r)
	}
	// Rank counted only over scored rows — recount so NULLs stay at 0 while
	// keeping relative order
	rank := 0
	for i := range out {
		if out[i].Final.Valid || out[i].Auto.Valid {
			rank++
			out[i].Rank = rank
		} else {
			out[i].Rank = 0
			out[i].ScoreText = "—"
		}
	}
	return out, rows.Err()
}

// questionStat is one row of the per-question analysis (spec §11.12).
type questionStat struct {
	Seq        int
	Text       string
	Type       string
	Correct    int
	Wrong      int
	Unanswered int
	CorrectPct float64
	WrongPct   float64
	MissPct    float64
	MostChosen string
}

// lettersOf renders a stored pg/multi answer as option letters.
func lettersOf(raw string) string {
	var idx []int
	if err := json.Unmarshal([]byte(raw), &idx); err != nil || len(idx) == 0 {
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

// questionStats computes % correct/wrong/unanswered and the most-chosen
// option per question over the latest attempt of every student.
func questionStats(ctx context.Context, db *sql.DB, quizID uint64) ([]questionStat, error) {
	var nStudents int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM participants
		WHERE quiz_id = ? AND attempt_no = (SELECT MAX(attempt_no) FROM participants
			WHERE quiz_id = ? AND user_id = participants.user_id)`, quizID, quizID).
		Scan(&nStudents); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT qq.seq, q.teks, q.type,
		COALESCE(SUM(a.is_correct = 1), 0), COALESCE(SUM(a.is_correct = 0), 0),
		COUNT(a.id)
		FROM quiz_questions qq
		JOIN questions q ON q.id = qq.question_id
		LEFT JOIN answers a ON a.question_id = q.id AND a.participant_id IN (
			SELECT p.id FROM participants p
			JOIN (SELECT user_id, MAX(attempt_no) m FROM participants
			      WHERE quiz_id = ? GROUP BY user_id) x
			  ON x.user_id = p.user_id AND p.attempt_no = x.m
			WHERE p.quiz_id = ?)
		WHERE qq.quiz_id = ?
		GROUP BY qq.id, qq.seq, q.teks, q.type, q.options
		ORDER BY qq.seq`, quizID, quizID, quizID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []questionStat
	for rows.Next() {
		var s questionStat
		var answered int
		if err := rows.Scan(&s.Seq, &s.Text, &s.Type, &s.Correct, &s.Wrong, &answered); err != nil {
			return nil, err
		}
		if len(s.Text) > 80 {
			s.Text = s.Text[:77] + "..."
		}
		s.Unanswered = nStudents - answered
		s.CorrectPct = quizengine.CentiPercent(s.Correct, nStudents)
		s.WrongPct = quizengine.CentiPercent(s.Wrong, nStudents)
		s.MissPct = quizengine.CentiPercent(s.Unanswered, nStudents)
		s.MostChosen = "—"
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// most-chosen option (pg/multi only — essays have no options)
	if nStudents > 0 {
		for i := range out {
			if out[i].Type == "essay" {
				continue
			}
			var raw sql.NullString
			err := db.QueryRowContext(ctx, `SELECT a.answer
				FROM answers a
				JOIN participants p ON p.id = a.participant_id
				WHERE p.quiz_id = ? AND a.question_id = (
					SELECT question_id FROM quiz_questions
					WHERE quiz_id = ? AND seq = ?)
				GROUP BY a.answer ORDER BY COUNT(*) DESC, a.answer LIMIT 1`,
				quizID, quizID, out[i].Seq).Scan(&raw)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if letters := lettersOf(raw.String); letters != "" {
				out[i].MostChosen = letters
			}
		}
	}
	return out, nil
}

// --- per-student answer panel (spec §6.11) ---------------------------------

// answerDetail is one question line of a student's expandable answer panel:
// the stored answer beside the answer key with the grading state. Answers
// store ORIGINAL option indexes (spec §6.8), so both columns render
// original-order letters no matter how the options were shuffled for the
// attempt — the same rendering the CSV/XLSX export uses.
type answerDetail struct {
	Seq   int
	Text  string
	Given string // student's answer: letters / essay text; "—" when blank
	Kunci string // answer key: letters / essay key text; "—" when none
	State string // correct | wrong | unanswered | graded | pending
	Label string
}

// studentAnswers is one student's payload for the rows of the current page:
// every question in seq order plus the counts the Students table shows.
type studentAnswers struct {
	Lines     []answerDetail
	Correct   int
	Total     int
	Attempted bool // has at least one stored answer row (drives the count column)
}

// detailKey renders questions.correct for one panel line: option letters
// for pg/multi (Kunci), the optional free-text key for an essay — never
// auto-graded, so an essay key is informational — and "—" when there is
// none.
func detailKey(q exportQuestion) string {
	if q.Type == "essay" {
		if key := essayAnswerText(q.RawCorrect); key != "" {
			return key
		}
		return "—"
	}
	if key := q.Kunci; key != "" {
		return key
	}
	return "—"
}

// answerCell is one stored answer rendered for the panel.
type answerCell struct {
	Given   string
	State   string
	Label   string
	Correct bool
}

// loadAnswerCells reads every LATEST-attempt answer of the quiz keyed by
// RAW username → question seq — the same row policy as loadExportAnswers
// (results table keys off results[i].Username).
func loadAnswerCells(ctx context.Context, db *sql.DB, quizID uint64,
	questions []exportQuestion,
) (map[string]map[int]answerCell, error) {
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

	out := map[string]map[int]answerCell{}
	for rows.Next() {
		var username, raw string
		var seq int
		var isCorrect sql.NullInt64
		var score sql.NullFloat64
		if err := rows.Scan(&username, &seq, &raw, &isCorrect, &score); err != nil {
			return nil, err
		}
		isEssay, ok := essay[seq]
		if !ok {
			continue
		}
		var cell answerCell
		switch {
		case isEssay:
			cell.Given = essayAnswerText(raw)
			if score.Valid {
				cell.State, cell.Label = "graded", fmt.Sprintf("Dinilai %.2f", score.Float64)
			} else {
				cell.State, cell.Label = "pending", "Menunggu penilaian"
			}
		case isCorrect.Valid && isCorrect.Int64 != 0:
			cell.State, cell.Label, cell.Correct = "correct", "Benar", true
			cell.Given = lettersOf(raw)
		case isCorrect.Valid:
			cell.State, cell.Label = "wrong", "Salah"
			cell.Given = lettersOf(raw)
		default:
			// auto-graded rows always carry 0/1 — a NULL here predates
			// grading, so report it as pending instead of calling it wrong
			cell.State, cell.Label = "pending", "Menunggu penilaian"
			cell.Given = lettersOf(raw)
		}
		if cell.Given == "" {
			cell.Given = "—"
		}
		if out[username] == nil {
			out[username] = make(map[int]answerCell, len(questions))
		}
		out[username][seq] = cell
	}
	return out, rows.Err()
}

// answerPanels builds the per-row payload for the Students table: one line
// per question in seq order for every row of the current page. A question
// without an answers row is "Unanswered" — the engine scores it as wrong.
func answerPanels(rows []resultRow, questions []exportQuestion,
	cells map[string]map[int]answerCell,
) map[string]studentAnswers {
	out := make(map[string]studentAnswers, len(rows))
	for _, r := range rows {
		bySeq := cells[r.Username]
		panel := studentAnswers{
			Total: len(questions),
			Lines: make([]answerDetail, 0, len(questions)),
		}
		for _, q := range questions {
			line := answerDetail{Seq: q.Seq, Text: q.Text, Kunci: detailKey(q)}
			if c, answered := bySeq[q.Seq]; answered {
				line.Given, line.State, line.Label = c.Given, c.State, c.Label
				panel.Attempted = true
				if c.Correct {
					panel.Correct++
				}
			} else {
				line.Given, line.State, line.Label = "—", "unanswered", "Belum terjawab"
			}
			panel.Lines = append(panel.Lines, line)
		}
		out[r.Username] = panel
	}
	return out
}

// ResultsPage is GET /teacher/quiz/:id/results: participant list with the
// expandable per-student answer panel (answer + key + correct/wrong per
// question) and the export buttons (spec §6.11). The student list is
// searchable and sortable: ?q= + ?sort=nama + ?page= combine (see list.go);
// the per-question analysis lives on its own route so this page never pays
// for that query.
func (t *Teacher) ResultsPage(c *echo.Context) error {
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
	results, err := loadResults(ctx, t.DB, quiz.ID)
	if err != nil {
		return err
	}
	hasEssay, err := quizHasEssay(ctx, t.DB, quiz.ID)
	if err != nil {
		return err
	}
	questions, err := loadExportQuestions(ctx, t.DB, quiz.ID)
	if err != nil {
		return err
	}
	q := listQ(c)
	sortBy := strings.TrimSpace(c.QueryParam("sort"))
	filtered := make([]resultRow, 0, len(results))
	for _, r := range results {
		if !matchSearch(q, r.Name, r.Username) {
			continue
		}
		filtered = append(filtered, r)
	}
	if sortBy == "nama" {
		// Nama A–Z. filtered is a fresh slice built above, so reordering
		// it never mutates what loadResults returned (default: score desc).
		sort.SliceStable(filtered, func(i, j int) bool {
			return strings.ToLower(filtered[i].Name) < strings.ToLower(filtered[j].Name)
		})
	}
	page := listPage(c)
	rows, page, _ := paginate(filtered, page)
	tb := newTable(c, q, len(filtered), page)
	tb.Placeholder = "Cari nama atau username…"
	// one answers read for the rows actually shown (≤ TablePerPage), keyed
	// by the RAW usernames the rows carry
	cells, err := loadAnswerCells(ctx, t.DB, quiz.ID, questions)
	if err != nil {
		return err
	}
	return c.Render(http.StatusOK, "page-teacher-results", map[string]any{
		"Title":    "Hasil — " + quiz.Judul,
		"Judul":    quiz.Judul,
		"ID":       quiz.ID,
		"Code":     quiz.Code,
		"Rows":     rows,
		"Panels":   answerPanels(rows, questions, cells),
		"HasEssay": hasEssay,
		"Table":    tb,
		"Sort":     sortBy,
		"Crumbs":   QuizCrumbs(quiz.ID, quiz.Judul, "Hasil"),
	})
}

// ResultsAnalysisPage is GET /teacher/quiz/:id/results/analysis: the
// per-question analysis on its own route (spec §11.12). The split is the
// performance boundary — the analysis query (per-question aggregates plus a
// most-chosen query per question) runs only when this route is opened, and
// the two views never render into one page.
func (t *Teacher) ResultsAnalysisPage(c *echo.Context) error {
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
	stats, err := questionStats(ctx, t.DB, quiz.ID)
	if err != nil {
		return err
	}
	return c.Render(http.StatusOK, "page-teacher-results-analysis", map[string]any{
		"Title":  "Analisis pertanyaan — " + quiz.Judul,
		"Judul":  quiz.Judul,
		"ID":     quiz.ID,
		"Code":   quiz.Code,
		"Stats":  stats,
		"Crumbs": QuizCrumbs(quiz.ID, quiz.Judul, "Analisis pertanyaan"),
	})
}

// --- GET /teacher/quiz/:id/grading ------------------------------------

// gradingAnswer is one student's essay answer awaiting (or holding) a score.
type gradingAnswer struct {
	AnswerID uint64
	Name     string
	Text     string
	Score    sql.NullFloat64
	Removed  bool
}

// gradingQuestion is one essay question of the quiz together with every
// student's answer to it: the grading page renders one card per pertanyaan,
// and a question nobody answered still gets its card (empty state).
type gradingQuestion struct {
	Seq     int
	QID     uint64
	Teks    string
	Answers []gradingAnswer
}

// GrantingPage lists the quiz's essay questions grouped with their answers
// (is_correct IS NULL — essays are never auto-graded): two queries (essay
// questions by seq, then answers ordered by student name) grouped in Go by
// question. The page handler serves GET only — saves go to
// POST /teacher/quiz/:id/grading/:answerId.
func (t *Teacher) GrantingPage(c *echo.Context) error {
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
	// (1) the essay questions in seq order — the card shells
	qRows, err := t.DB.QueryContext(ctx, `SELECT qq.seq, q.id, q.teks
		FROM quiz_questions qq
		JOIN questions q ON q.id = qq.question_id
		WHERE qq.quiz_id = ? AND q.type = 'essay'
		ORDER BY qq.seq`, quiz.ID)
	if err != nil {
		return err
	}
	defer qRows.Close()
	var questions []gradingQuestion
	byQID := map[uint64]int{}
	for qRows.Next() {
		var q gradingQuestion
		if err := qRows.Scan(&q.Seq, &q.QID, &q.Teks); err != nil {
			return err
		}
		byQID[q.QID] = len(questions)
		questions = append(questions, q)
	}
	if err := qRows.Err(); err != nil {
		return err
	}
	// (2) the answers awaiting grading, grouped onto the cards in Go —
	// ordered by student name so every card lists the class the same way
	rows, err := t.DB.QueryContext(ctx, `SELECT a.id, a.question_id, u.nama_lengkap, a.answer, a.score, p.status
		FROM answers a
		JOIN participants p ON p.id = a.participant_id
		JOIN users u ON u.id = p.user_id
		WHERE p.quiz_id = ? AND a.is_correct IS NULL
		ORDER BY u.nama_lengkap, a.id`, quiz.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var g gradingAnswer
		var questionID uint64
		var text sql.NullString
		var status string
		if err := rows.Scan(&g.AnswerID, &questionID, &g.Name, &text, &g.Score, &status); err != nil {
			return err
		}
		idx, ok := byQID[questionID]
		if !ok {
			// the question left the quiz after this answer was written —
			// it no longer counts toward the score, so it has no card
			continue
		}
		if text.Valid {
			_ = json.Unmarshal([]byte(text.String), &g.Text)
			if g.Text == "" {
				g.Text = text.String
			}
		}
		g.Removed = status == "dikeluarkan"
		questions[idx].Answers = append(questions[idx].Answers, g)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return c.Render(http.StatusOK, "page-teacher-grading", map[string]any{
		"Title":     "Penilaian — " + quiz.Judul,
		"Judul":     quiz.Judul,
		"ID":        quiz.ID,
		"Questions": questions,
		"Crumbs":    QuizCrumbs(quiz.ID, quiz.Judul, "Penilaian"),
	})
}

// EssayScore is the essay contribution to the 100-point final (spec §6.7):
// the sum of the participant's graded essay scores divided by the quiz's
// FULL question count, rounded half-up to two decimals with integer
// arithmetic — the same rounding quizengine.CentiPercent uses, so an essay
// graded 100 contributes exactly CentiPercent(1, total) (one correct MCQ's
// weight) and every save adds exactly score/total. A non-positive total
// is 0 (never NaN).
func EssayScore(sum float64, total int) float64 {
	if total <= 0 {
		return 0
	}
	// stored scores are 2dp — snap the float sum back to whole centi points
	// so the half-up division never accumulates float error
	centiSum := int(sum*100 + 0.5)
	centi := (2*centiSum + total) / (2 * total)
	return float64(centi) / 100
}

// GradeAnswer is POST /teacher/quiz/:id/grading/:answerId {score} (0–100
// per essay): stores the score (is_correct stays NULL — an essay is never
// correct/incorrect by rule), then recomputes participants.essay_score as
// SUM(graded essay scores)/total questions (each save adds exactly
// score/total) and final_score = FinalScore(score_auto, essay_score) ONLY
// once every essay answer of this quiz carries a score — until then
// final_score stays NULL so results keep showing "Menunggu penilaian" and
// the student sees "Pending" (spec §6.7, §6.11). Idempotent.
func (t *Teacher) GradeAnswer(c *echo.Context) error {
	id, rerr := monitorQuizID(c)
	if rerr != nil {
		return fail(c, rerr.Status, rerr.Code, rerr.Msg)
	}
	answerID, err := strconv.ParseUint(c.Param("answerId"), 10, 64)
	if err != nil || answerID == 0 {
		return fail(c, http.StatusBadRequest, ErrValidation, "ID jawaban tidak valid.")
	}
	score := c.FormValue("score")
	// The JSON branch accepts a number OR a numeric string: data-json (the
	// fetch helper) serializes form fields as strings, so the grading form
	// posts {"score":"85.5"}.
	var body struct {
		Score any `json:"score"`
	}
	if score == "" {
		if rerr := decodeJSON(c, &body); rerr != nil {
			return fail(c, rerr.Status, rerr.Code, rerr.Msg)
		}
		switch v := body.Score.(type) {
		case nil: // absent or null
			return fail(c, http.StatusBadRequest, ErrValidation, "Nilai wajib diisi.")
		case string:
			if v == "" {
				return fail(c, http.StatusBadRequest, ErrValidation, "Nilai wajib diisi.")
			}
			score = v
		case float64:
			score = fmt.Sprintf("%g", v)
		default: // bool/object/array — same envelope as a JSON type error
			return fail(c, http.StatusBadRequest, ErrValidation, "Badan JSON tidak valid.")
		}
	}
	// strict parse: Sscanf accepted NaN (it slips BOTH bounds checks and
	// the int conversion below overflows → 500) and partial input like
	// "85abc" — ParseFloat requires the whole trimmed token.
	value, err := strconv.ParseFloat(strings.TrimSpace(score), 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 100 {
		return fail(c, http.StatusBadRequest, ErrValidation,
			"Nilai harus antara 0 dan 100.")
	}
	value = float64(int(value*100+0.5)) / 100 // round half-up, two decimals

	ctx := c.Request().Context()
	quiz, rerr, err := quizByID(ctx, t.DB, id)
	if err != nil {
		return err
	}
	if rerr != nil {
		return fail(c, rerr.Status, rerr.Code, rerr.Msg)
	}

	tx, err := t.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	var participantID uint64
	var stored sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT a.participant_id, a.answer
		FROM answers a JOIN participants p ON p.id = a.participant_id
		WHERE a.id = ? AND p.quiz_id = ? AND a.is_correct IS NULL FOR UPDATE`,
		answerID, quiz.ID).Scan(&participantID, &stored)
	if errors.Is(err, sql.ErrNoRows) {
		tx.Rollback()
		return fail(c, http.StatusNotFound, ErrNotFound, "Jawaban esai tidak ditemukan.")
	}
	if err != nil {
		tx.Rollback()
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE answers SET score = ?, is_correct = NULL WHERE id = ?`,
		value, answerID); err != nil {
		tx.Rollback()
		return err
	}

	// recompute the essay share for this participant: SUM(graded scores)/
	// total questions — each save adds exactly score/total, ungraded essays
	// contribute 0 — and hold final_score at NULL while any essay answer of
	// this quiz is still ungraded, so results stay "awaiting grading" and
	// the student's score stays "Pending" (FinalScore's pending semantics).
	total, _, hasEssay, err := quizScope(ctx, tx, quiz.ID)
	if err != nil {
		tx.Rollback()
		return err
	}
	var auto sql.NullFloat64
	if err := tx.QueryRowContext(ctx,
		`SELECT score_auto FROM participants WHERE id = ?`, participantID).
		Scan(&auto); err != nil {
		tx.Rollback()
		return err
	}
	var essayScore any
	var final any
	if hasEssay && total > 0 {
		var sum float64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(score),0) FROM answers
			WHERE participant_id = ? AND score IS NOT NULL`, participantID).
			Scan(&sum); err != nil {
			tx.Rollback()
			return err
		}
		// every essay of this quiz must hold a score before the final may
		// be computed (an unanswered essay has no answers row at all and
		// does not block — it simply contributes 0 to the SUM)
		var ungraded int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM answers a
			JOIN quiz_questions qq ON qq.quiz_id = ? AND qq.question_id = a.question_id
			JOIN questions q ON q.id = qq.question_id
			WHERE a.participant_id = ? AND q.type = 'essay' AND a.score IS NULL`,
			quiz.ID, participantID).Scan(&ungraded); err != nil {
			tx.Rollback()
			return err
		}
		v := EssayScore(sum, total)
		essayScore = v
		if ungraded == 0 && auto.Valid {
			final = quizengine.FinalScore(&auto.Float64, &v, true)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE participants SET essay_score = ?, final_score = ? WHERE id = ?`,
		essayScore, final, participantID); err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	if strings.HasPrefix(c.Request().Header.Get("Content-Type"), "application/json") {
		return ok(c, map[string]any{"answer_id": answerID, "score": value})
	}
	return c.Redirect(http.StatusSeeOther, fmt.Sprintf("/teacher/quiz/%d/grading", quiz.ID))
}
