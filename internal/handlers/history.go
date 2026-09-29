package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v5"

	mw "quiz/internal/middleware"
	"quiz/internal/quizengine"
)

// --- GET /history -----------------------------------------------------------

// historyRow is one attempt of the session user in the history list.
type historyRow struct {
	ParticipantID uint64
	QuizID        uint64
	Judul         string
	AttemptNo     int
	Status        string
	Removed       bool
	Cheating      bool
	Score         sql.NullFloat64 // COALESCE(final, auto)
	Rank          int             // 0 = no rank
	ShowScore     bool            // per-quiz show_final_score
	ShowRanking   bool            // per-quiz ranking_live
}

// rankFor is the position of the user's BEST attempt among every user's
// best attempt in the quiz (spec §11.10: ranking across attempts uses the
// highest score). Returns 0 when the user has no scored attempt.
func rankFor(ctx context.Context, db *sql.DB, quizID, userID uint64, myBest float64) (int, error) {
	var better int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM (
			SELECT user_id, MAX(COALESCE(final_score, score_auto)) AS best
			FROM participants WHERE quiz_id = ? GROUP BY user_id
		) x WHERE x.user_id <> ? AND x.best > ?`, quizID, userID, myBest).Scan(&better)
	if err != nil {
		return 0, err
	}
	return better + 1, nil
}

// HistoryPage is GET /history: every attempt of the session user, newest
// first. Per-quiz settings gate the score and rank columns (spec §6.11);
// ?q= (title) + ?status= + ?page= combine (see list.go).
func (s *Student) HistoryPage(c *echo.Context) error {
	sess := mw.SessionFrom(c)
	if sess == nil {
		return c.Redirect(http.StatusFound, "/login")
	}
	ctx := c.Request().Context()
	rows, err := s.DB.QueryContext(ctx, `SELECT p.id, p.quiz_id, q.judul,
		q.show_final_score, q.ranking_live, p.attempt_no, p.status,
		p.score_auto, p.final_score, p.cheating
		FROM participants p JOIN quizzes q ON q.id = p.quiz_id
		WHERE p.user_id = ?
		ORDER BY p.id DESC`, sess.UserID)
	if err != nil {
		return err
	}
	defer rows.Close()

	var list []historyRow
	showScoreColumn, showRankingColumn := false, false
	best := map[uint64]float64{}
	rankQuizzes := map[uint64]bool{}
	for rows.Next() {
		var r historyRow
		var auto, final sql.NullFloat64
		var cheating int
		if err := rows.Scan(&r.ParticipantID, &r.QuizID, &r.Judul,
			&r.ShowScore, &r.ShowRanking, &r.AttemptNo, &r.Status,
			&auto, &final, &cheating); err != nil {
			return err
		}
		r.Cheating = cheating != 0
		r.Removed = r.Status == "dikeluarkan"
		switch {
		case final.Valid:
			r.Score = final
		case auto.Valid:
			r.Score = auto
		}
		if r.Score.Valid {
			if prev, ok := best[r.QuizID]; !ok || r.Score.Float64 > prev {
				best[r.QuizID] = r.Score.Float64
			}
		}
		showScoreColumn = showScoreColumn || r.ShowScore
		showRankingColumn = showRankingColumn || r.ShowRanking
		if r.ShowRanking {
			rankQuizzes[r.QuizID] = true
		}
		list = append(list, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for i := range list {
		if !rankQuizzes[list[i].QuizID] {
			continue
		}
		myBest, ok := best[list[i].QuizID]
		if !ok {
			list[i].Rank = 0
			continue
		}
		rank, err := rankFor(ctx, s.DB, list[i].QuizID, sess.UserID, myBest)
		if err != nil {
			return err
		}
		list[i].Rank = rank
	}

	q := listQ(c)
	status := strings.TrimSpace(c.QueryParam("status"))
	filtered := make([]historyRow, 0, len(list))
	for _, r := range list {
		if status != "" && r.Status != status {
			continue
		}
		if !matchSearch(q, r.Judul) {
			continue
		}
		filtered = append(filtered, r)
	}
	page := listPage(c)
	pageRows, page, _ := paginate(filtered, page)
	tb := newTable(c, q, len(filtered), page)
	tb.Placeholder = "Search quiz title…"

	return c.Render(http.StatusOK, "page-student-history", map[string]any{
		"Title":             "History",
		"Rows":              pageRows,
		"ShowScoreColumn":   showScoreColumn,
		"ShowRankingColumn": showRankingColumn,
		"Table":             tb,
		"Status":            status,
	})
}

// --- GET /history/:id -------------------------------------------------------

// questionBankRow is the question metadata a review needs.
type questionBankRow struct {
	ID      uint64
	Text    string
	Type    string
	Options sql.NullString
	Correct sql.NullString
}

// optionTexts parses questions.options JSON.
func (q questionBankRow) optionTexts() []string {
	if !q.Options.Valid {
		return nil
	}
	var texts []string
	if err := json.Unmarshal([]byte(q.Options.String), &texts); err != nil {
		return nil
	}
	return texts
}

// answerFor renders a stored answer as option TEXTS (pg/multi, original
// indexes) or essay text; "—" when the question was not answered.
func (q questionBankRow) answerFor(raw string) string {
	if raw == "" {
		return "—"
	}
	if q.Type == "essay" {
		var text string
		if err := json.Unmarshal([]byte(raw), &text); err == nil && text != "" {
			return text
		}
		return raw
	}
	var idx []int
	if err := json.Unmarshal([]byte(raw), &idx); err != nil {
		return "—"
	}
	texts := q.optionTexts()
	parts := make([]string, 0, len(idx))
	for _, i := range idx {
		if i >= 0 && i < len(texts) {
			parts = append(parts, texts[i])
		}
	}
	if len(parts) == 0 {
		return "—"
	}
	return strings.Join(parts, ", ")
}

// keyFor renders the answer key (ReviewFull only — BuildHistory strips it
// for lower levels).
func (q questionBankRow) keyFor() string {
	if !q.Correct.Valid || q.Correct.String == "" {
		return ""
	}
	if q.Type == "essay" {
		var text string
		if err := json.Unmarshal([]byte(q.Correct.String), &text); err == nil {
			return text
		}
		return q.Correct.String
	}
	texts := q.optionTexts()
	var idx []int
	if err := json.Unmarshal([]byte(q.Correct.String), &idx); err != nil {
		// pg stores a bare number
		var one int
		if json.Unmarshal([]byte(q.Correct.String), &one) == nil {
			idx = []int{one}
		}
	}
	parts := make([]string, 0, len(idx))
	for _, i := range idx {
		if i >= 0 && i < len(texts) {
			parts = append(parts, texts[i])
		}
	}
	return strings.Join(parts, ", ")
}

// HistoryDetail is GET /history/:id — one attempt, rendered through
// quizengine.BuildHistory at the quiz's question_review level. Row
// ownership: participants.user_id must be the session user, else 403.
func (s *Student) HistoryDetail(c *echo.Context) error {
	sess := mw.SessionFrom(c)
	if sess == nil {
		return c.Redirect(http.StatusFound, "/login")
	}
	pid, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || pid == 0 {
		return fail(c, http.StatusBadRequest, ErrValidation, "Invalid attempt id.")
	}
	ctx := c.Request().Context()

	var ownerID, quizID uint64
	var status, judul, review string
	var attemptNo int
	var qorder sql.NullString
	var auto, final sql.NullFloat64
	var cheating int
	var showScore, showRank bool
	err = s.DB.QueryRowContext(ctx, `SELECT p.user_id, p.quiz_id, p.status, p.attempt_no,
		p.qorder, p.score_auto, p.final_score, p.cheating,
		q.judul, q.question_review, q.show_final_score, q.ranking_live
		FROM participants p JOIN quizzes q ON q.id = p.quiz_id
		WHERE p.id = ?`, pid).
		Scan(&ownerID, &quizID, &status, &attemptNo, &qorder,
			&auto, &final, &cheating, &judul, &review, &showScore, &showRank)
	if errors.Is(err, sql.ErrNoRows) {
		return fail(c, http.StatusNotFound, ErrNotFound, "Attempt not found.")
	}
	if err != nil {
		return err
	}
	// ownership (spec §7): another student's attempt is never readable
	if ownerID != sess.UserID {
		return fail(c, http.StatusForbidden, ErrForbidden,
			"You are not allowed to view this attempt.")
	}

	// question order: qorder snapshot, falling back to the bank order
	var qids []uint64
	if qorder.Valid && qorder.String != "" {
		var ord quizengine.Order
		if err := json.Unmarshal([]byte(qorder.String), &ord); err == nil {
			qids = ord.Questions
		}
	}
	if len(qids) == 0 {
		r, err := s.DB.QueryContext(ctx,
			`SELECT question_id FROM quiz_questions WHERE quiz_id = ? ORDER BY seq`, quizID)
		if err != nil {
			return err
		}
		for r.Next() {
			var qid uint64
			if err := r.Scan(&qid); err != nil {
				r.Close()
				return err
			}
			qids = append(qids, qid)
		}
		r.Close()
		if err := r.Err(); err != nil {
			return err
		}
	}

	// question metadata + this attempt's answers
	questions := make(map[uint64]questionBankRow, len(qids))
	for _, qid := range qids {
		var q questionBankRow
		q.ID = qid
		if err := s.DB.QueryRowContext(ctx,
			`SELECT teks, type, options, correct FROM questions WHERE id = ?`, qid).
			Scan(&q.Text, &q.Type, &q.Options, &q.Correct); err != nil {
			return err
		}
		questions[qid] = q
	}
	answerRows, err := s.DB.QueryContext(ctx,
		`SELECT question_id, COALESCE(answer, ''), is_correct FROM answers
		 WHERE participant_id = ?`, pid)
	if err != nil {
		return err
	}
	own := map[uint64]string{}
	correctByQ := map[uint64]bool{}
	for answerRows.Next() {
		var qid uint64
		var raw string
		var okBit sql.NullInt64
		if err := answerRows.Scan(&qid, &raw, &okBit); err != nil {
			answerRows.Close()
			return err
		}
		own[qid] = raw
		correctByQ[qid] = okBit.Valid && okBit.Int64 == 1
	}
	answerRows.Close()
	if err := answerRows.Err(); err != nil {
		return err
	}

	views := make([]quizengine.QuestionView, 0, len(qids))
	for _, qid := range qids {
		q, ok := questions[qid]
		if !ok {
			continue
		}
		views = append(views, quizengine.QuestionView{
			Question:      q.Text,
			YourAnswer:    q.answerFor(own[qid]),
			Correct:       correctByQ[qid],
			CorrectAnswer: q.keyFor(),
		})
	}
	hv := quizengine.BuildHistory(quizengine.ReviewLevel(review), showScore, showRank, views)

	// rank + score for the header (gated by the same flags)
	rank, myBest := 0, 0.0
	if showRank {
		var best sql.NullFloat64
		if err := s.DB.QueryRowContext(ctx,
			`SELECT MAX(COALESCE(final_score, score_auto)) FROM participants
			 WHERE quiz_id = ? AND user_id = ?`, quizID, sess.UserID).Scan(&best); err != nil {
			return err
		}
		if best.Valid {
			myBest = best.Float64
			if rank, err = rankFor(ctx, s.DB, quizID, sess.UserID, myBest); err != nil {
				return err
			}
		}
	}
	scoreText := "—"
	switch {
	case final.Valid:
		scoreText = fmt.Sprintf("%.2f", final.Float64)
	case auto.Valid && status == "selesai":
		scoreText = fmt.Sprintf("%.2f", auto.Float64)
	}

	return c.Render(http.StatusOK, "page-student-history-detail", map[string]any{
		"Title":     judul,
		"Judul":     judul,
		"AttemptNo": attemptNo,
		"Status":    status,
		"Removed":   status == "dikeluarkan",
		"Cheating":  cheating != 0,
		"ScoreText": scoreText,
		"Rank":      rank,
		"HV":        hv,
		"Crumbs":    AttemptCrumbs(judul),
	})
}

// --- GET /profile, POST /profile/edit --------------------------------------

// profileUser is the editable profile row (username/email are immutable,
// spec §7).
type profileUser struct {
	Username  string
	Email     string
	Nama      string
	KelasID   uint64
	JurusanID uint64
}

// ProfilePage is GET /profile — full name, class and major (spec §7).
func (s *Student) ProfilePage(c *echo.Context) error {
	sess := mw.SessionFrom(c)
	if sess == nil {
		return c.Redirect(http.StatusFound, "/login")
	}
	ctx := c.Request().Context()
	var u profileUser
	if err := s.DB.QueryRowContext(ctx,
		`SELECT username, email, nama_lengkap, kelas_id, jurusan_id FROM users WHERE id = ?`,
		sess.UserID).Scan(&u.Username, &u.Email, &u.Nama, &u.KelasID, &u.JurusanID); err != nil {
		return err
	}
	kelas, err := refsList(ctx, s.DB, s.Store, "kelas")
	if err != nil {
		return err
	}
	jurusan, err := refsList(ctx, s.DB, s.Store, "jurusan")
	if err != nil {
		return err
	}
	data := map[string]any{
		"Title":   "Profile",
		"User":    u,
		"Kelas":   kelas,
		"Jurusan": jurusan,
	}
	if c.QueryParam("saved") == "1" {
		data["Flash"] = flash("success", "Profile saved successfully.")
	}
	return c.Render(http.StatusOK, "page-student-profile", data)
}

// EditProfile is POST /profile/edit — validates the three editable fields
// against the reference tables; username and email are never writable.
func (s *Student) EditProfile(c *echo.Context) error {
	sess := mw.SessionFrom(c)
	if sess == nil {
		return c.Redirect(http.StatusFound, "/login")
	}
	nama := strings.TrimSpace(c.FormValue("nama"))
	kelasRaw, jurusanRaw := c.FormValue("kelas_id"), c.FormValue("jurusan_id")
	if nama == "" && kelasRaw == "" {
		var body struct {
			Nama      string `json:"nama"`
			KelasID   uint64 `json:"kelas_id"`
			JurusanID uint64 `json:"jurusan_id"`
		}
		if rerr := decodeJSON(c, &body); rerr != nil {
			return fail(c, rerr.Status, rerr.Code, rerr.Msg)
		}
		nama = strings.TrimSpace(body.Nama)
		kelasRaw, jurusanRaw = strconv.FormatUint(body.KelasID, 10),
			strconv.FormatUint(body.JurusanID, 10)
	}
	if nama == "" {
		return fail(c, http.StatusBadRequest, ErrValidation, "Full name is required.")
	}
	if len([]rune(nama)) > 100 {
		return fail(c, http.StatusBadRequest, ErrValidation,
			"Full name must be at most 100 characters.")
	}
	kelasID, err1 := strconv.ParseUint(kelasRaw, 10, 16)
	jurusanID, err2 := strconv.ParseUint(jurusanRaw, 10, 16)
	if err1 != nil || err2 != nil || kelasID == 0 || jurusanID == 0 {
		return fail(c, http.StatusBadRequest, ErrValidation, "Invalid class or major.")
	}
	ctx := c.Request().Context()
	var exists bool
	if err := s.DB.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM ref_kelas WHERE id = ?)
		 AND EXISTS(SELECT 1 FROM ref_jurusan WHERE id = ?)`,
		kelasID, jurusanID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return fail(c, http.StatusBadRequest, ErrValidation, "Invalid class or major.")
	}
	if _, err := s.DB.ExecContext(ctx,
		`UPDATE users SET nama_lengkap = ?, kelas_id = ?, jurusan_id = ? WHERE id = ?`,
		nama, kelasID, jurusanID, sess.UserID); err != nil {
		return err
	}
	if strings.HasPrefix(c.Request().Header.Get("Content-Type"), "application/json") {
		return ok(c, nil)
	}
	return c.Redirect(http.StatusSeeOther, "/profile?saved=1")
}
