package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v5"

	"quiz/internal/cache"
	mw "quiz/internal/middleware"
	"quiz/internal/quizengine"
	"quiz/internal/realtime"
)

// Student serves the student side (spec §7): home + join, the workspace
// (invite-URL auto-join, linear/free layouts, waiting states) and the
// attempt endpoints. Mutation protocol: BEGIN → validate → write → COMMIT →
// publish → envelope; the SSE push is always strictly after COMMIT (the DB
// is the source of truth, §6.2).
type Student struct {
	DB    *sql.DB
	Hub   *realtime.Hub
	Store *cache.Store
	Live  *Live
}

// respError is an expected failure: JSON envelope for POSTs, error page for
// SSR routes (same status/code/message either way).
type respError struct {
	Status int
	Code   string
	Msg    string
}

func (e *respError) Error() string { return e.Msg }

func errResp(status int, code, msg string) *respError {
	return &respError{Status: status, Code: code, Msg: msg}
}

// quizDetail is every quiz column the student flow branches on.
type quizDetail struct {
	ID               uint64
	Code             string
	Judul            string
	Deskripsi        sql.NullString
	TimerType        string
	TimerOn          bool
	Status           string
	JoinMode         string
	TotalSeconds     int
	PerQuestionSecs  int
	ShuffleQuestions bool
	ShuffleOptions   bool
	ShowCorrectWrong bool
	ShowFinalScore   bool
	RankingLive      bool
	MaxAttempts      uint8
	QuestionReview   string
	StartedAt        sql.NullTime
}

// attemptRow is one attempt (latest row drives join/workspace).
type attemptRow struct {
	ID            uint64
	QuizID        uint64
	UserID        uint64
	AttemptNo     uint8
	Status        string
	QOrder        sql.NullString
	StartedAt     sql.NullTime
	EndsAt        sql.NullTime
	FinishedAt    sql.NullTime
	CurrentQ      sql.NullInt64
	CurrentQSince sql.NullTime
	ScoreAuto     sql.NullFloat64
	FinalScore    sql.NullFloat64
	Cheating      bool
}

const attemptCols = `id, quiz_id, user_id, attempt_no, status, qorder,
	started_at, ends_at, finished_at, current_q, current_q_since,
	score_auto, final_score, cheating`

func scanParticipant(scan func(dest ...any) error) (attemptRow, error) {
	var p attemptRow
	err := scan(&p.ID, &p.QuizID, &p.UserID, &p.AttemptNo, &p.Status, &p.QOrder,
		&p.StartedAt, &p.EndsAt, &p.FinishedAt, &p.CurrentQ, &p.CurrentQSince,
		&p.ScoreAuto, &p.FinalScore, &p.Cheating)
	return p, err
}

// qRow is what a QueryRowContext provider offers — satisfied by *sql.DB and
// *sql.Tx, so the participant read works outside and inside a transaction.
type qRow interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// participantName is the display name carried by the monitor's page events
// (a page event with no name cannot create a missing monitor card).
func participantName(ctx context.Context, db *sql.DB, userID uint64) (string, error) {
	var name string
	err := db.QueryRowContext(ctx,
		`SELECT nama_lengkap FROM users WHERE id = ?`, userID).Scan(&name)
	return name, err
}

// pageEvent is the `page` frame the live monitor consumes (spec §6.3, §7):
// one participant's full card state — start → question changes → preview →
// submit in every timer mode, plus the connection state the monitor needs
// when a stream (re)opens after a refresh or a leave-and-return. Mirror-only
// payload — never a DB write, so it is safe to fire right after the commit
// (or heartbeat) that produced it.
func pageEvent(live *Live, part attemptRow, name, page string) realtime.Event {
	var endsUnix, sinceUnix int64
	if part.EndsAt.Valid {
		endsUnix = part.EndsAt.Time.Unix()
	}
	if part.CurrentQSince.Valid {
		sinceUnix = part.CurrentQSince.Time.Unix()
	}
	pos := 0
	if part.CurrentQ.Valid {
		pos = int(part.CurrentQ.Int64)
	}
	return realtime.Event{
		Type: "page",
		Data: map[string]any{
			"participant_id":  part.ID,
			"name":            name,
			"status":          part.Status,
			"current_q":       pos,
			"current_q_since": sinceUnix,
			"ends_at":         endsUnix,
			"page":            page,
			"spent":           live.Spent(part.ID),
			"connected":       live.Connected(part.ID),
		},
	}
}

// publishPage pushes one participant's page move to the monitor topic
// (spec §6.3, §7): the live monitor follows start → question changes →
// preview → submit in every timer mode.
func (s *Student) publishPage(quizID uint64, part attemptRow, name, page string) {
	s.Hub.Publish(realtime.TeacherTopicForQuizID(quizID), pageEvent(s.Live, part, name, page))
}

// latestParticipant reads the newest attempt (attempt_no DESC), optionally
// locking it for update.
func latestParticipant(ctx context.Context, q qRow, quizID, userID uint64, forUpdate bool) (attemptRow, bool, error) {
	query := `SELECT ` + attemptCols + ` FROM participants
		WHERE quiz_id = ? AND user_id = ? ORDER BY attempt_no DESC LIMIT 1`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	p, err := scanParticipant(func(dest ...any) error {
		return q.QueryRowContext(ctx, query, quizID, userID).Scan(dest...)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return attemptRow{}, false, nil
	}
	if err != nil {
		return attemptRow{}, false, err
	}
	return p, true, nil
}

// --- quiz lookup ------------------------------------------------------------

// loadQuiz fetches one quiz row by WHERE clause. The second return is an
// expected failure (the row is missing → missing); the third is a real DB
// error, which must surface as a 500 instead of masquerading as a 404.
func loadQuiz(ctx context.Context, db *sql.DB, where string, arg any, missing *respError) (quizDetail, *respError, error) {
	var q quizDetail
	var timerOn, shuffleQ, shuffleO, showCW, showFS, ranking, totalSecs, perQ, maxAtt int
	err := db.QueryRowContext(ctx, `SELECT id, code, judul, deskripsi, timer_type,
		timer_on, status, join_mode, total_seconds, per_question_seconds,
		shuffle_questions, shuffle_options, show_correct_wrong, show_final_score,
		ranking_live, max_attempts, question_review, started_at
		FROM quizzes WHERE `+where, arg).
		Scan(&q.ID, &q.Code, &q.Judul, &q.Deskripsi, &q.TimerType, &timerOn, &q.Status,
			&q.JoinMode, &totalSecs, &perQ, &shuffleQ, &shuffleO, &showCW, &showFS,
			&ranking, &maxAtt, &q.QuestionReview, &q.StartedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return q, missing, nil
	}
	if err != nil {
		return q, nil, err
	}
	q.TimerOn = timerOn != 0
	q.TotalSeconds, q.PerQuestionSecs = totalSecs, perQ
	q.ShuffleQuestions, q.ShuffleOptions = shuffleQ != 0, shuffleO != 0
	q.ShowCorrectWrong, q.ShowFinalScore = showCW != 0, showFS != 0
	q.RankingLive = ranking != 0
	q.MaxAttempts = uint8(maxAtt)
	return q, nil, nil
}

// quizByCode resolves a join code (trimmed + upper-cased); unknown code and
// malformed input share one 404 INVALID_CODE branch (spec §9 join matrix).
func quizByCode(ctx context.Context, db *sql.DB, raw string) (quizDetail, *respError, error) {
	code := strings.ToUpper(strings.TrimSpace(raw))
	return loadQuiz(ctx, db, "code = ?", code,
		errResp(http.StatusNotFound, ErrInvalidCode, MsgInvalidCode))
}

// quizByID resolves a quiz primary key (teacher routes and the monitor).
func quizByID(ctx context.Context, db *sql.DB, id uint64) (quizDetail, *respError, error) {
	return loadQuiz(ctx, db, "id = ?", id,
		errResp(http.StatusNotFound, ErrNotFound, "Kuis tidak ditemukan."))
}

// --- join chain (shared by POST /join and invite-URL auto-join) -------------

const (
	msgRemoved        = "Anda dikeluarkan dari kuis ini."
	msgNoAttempts     = "Anda tidak punya sisa percobaan."
	msgNotParticipant = "Anda bukan peserta kuis ini."
	msgEnded          = "Kuis ini sudah berakhir."
	msgAwaiting       = "Menunggu persetujuan guru."
	msgAttemptDone    = "Anda sudah menyelesaikan percobaan ini."
)

// joinQuiz runs the validation chain inside one transaction and creates the
// participant row when the student is new (or has an attempt left).
func (s *Student) joinQuiz(ctx context.Context, quiz quizDetail, userID uint64) (attemptRow, bool, *respError, error) {
	maxAttempts := quizengine.MaxAttemptsFor(quiz.TimerType, quiz.MaxAttempts)
	for attempt := 0; attempt < 3; attempt++ {
		tx, err := s.DB.BeginTx(ctx, nil)
		if err != nil {
			return attemptRow{}, false, nil, err
		}
		part, found, err := latestParticipant(ctx, tx, quiz.ID, userID, true)
		if err != nil {
			tx.Rollback()
			return attemptRow{}, false, nil, err
		}
		if found {
			switch part.Status {
			case "dikeluarkan":
				tx.Rollback()
				return part, false, errResp(http.StatusForbidden, ErrForbidden, msgRemoved), nil
			case "selesai":
				// that attempt is over: exhausted → block; else next attempt
				var count int
				if err := tx.QueryRowContext(ctx,
					`SELECT COUNT(*) FROM participants WHERE quiz_id = ? AND user_id = ?`,
					quiz.ID, userID).Scan(&count); err != nil {
					tx.Rollback()
					return attemptRow{}, false, nil, err
				}
				if uint8(count) >= maxAttempts {
					tx.Rollback()
					return part, false, errResp(http.StatusConflict, ErrAttemptLimit, msgNoAttempts), nil
				}
				if err := s.createAttempt(ctx, tx, quiz, userID, uint8(count)+1); err != nil {
					tx.Rollback()
					if isDuplicateKey(err) && attempt < 2 {
						continue // concurrent join created the row — re-read
					}
					return attemptRow{}, false, nil, err
				}
				if err := tx.Commit(); err != nil {
					return attemptRow{}, false, nil, err
				}
				created, _, err := latestParticipant(ctx, s.DB, quiz.ID, userID, false)
				return created, true, nil, err
			default:
				// pending / registered / started — rejoining after a
				// disconnect is always allowed, never 409 (plan §Step 10)
				tx.Rollback()
				return part, false, nil, nil
			}
		}
		// no row: re-read quiz gates under lock, then create attempt 1
		var freshStatus, freshJoinMode, freshTimerType string
		var freshMaxAttempts int
		if err := tx.QueryRowContext(ctx,
			`SELECT status, join_mode, max_attempts, timer_type FROM quizzes WHERE id = ? FOR UPDATE`,
			quiz.ID).Scan(&freshStatus, &freshJoinMode, &freshMaxAttempts, &freshTimerType); err != nil {
			tx.Rollback()
			if errors.Is(err, sql.ErrNoRows) {
				return attemptRow{}, false, errResp(http.StatusNotFound, ErrNotFound, "Kuis tidak ditemukan."), nil
			}
			return attemptRow{}, false, nil, err
		}
		quizFresh := quiz
		quizFresh.Status = freshStatus
		quizFresh.JoinMode = freshJoinMode
		quizFresh.MaxAttempts = uint8(freshMaxAttempts)
		quizFresh.TimerType = freshTimerType
		if rerr := quizGates(quizFresh); rerr != nil {
			tx.Rollback()
			return attemptRow{}, false, rerr, nil
		}
		if err := s.createAttempt(ctx, tx, quizFresh, userID, 1); err != nil {
			tx.Rollback()
			if isDuplicateKey(err) && attempt < 2 {
				continue
			}
			return attemptRow{}, false, nil, err
		}
		if err := tx.Commit(); err != nil {
			return attemptRow{}, false, nil, err
		}
		created, _, err := latestParticipant(ctx, s.DB, quiz.ID, userID, false)
		return created, true, nil, err
	}
	return attemptRow{}, false, errResp(http.StatusConflict, ErrConflict, "Silakan coba lagi."), nil
}

// quizGates is the no-row branch of the join matrix: only an active quiz may
// be entered.
func quizGates(quiz quizDetail) *respError {
	switch quiz.Status {
	case "nonaktif":
		return errResp(http.StatusNotFound, ErrNotFound, "Kuis tidak ditemukan.")
	case "selesai":
		return errResp(http.StatusGone, ErrQuizEnded, msgEnded)
	case "berjalan":
		return errResp(http.StatusConflict, ErrQuizInProgress, MsgQuizInProgress)
	}
	return nil
}

// createAttempt inserts the attempt row (open → registered, approve → pending).
func (s *Student) createAttempt(ctx context.Context, tx *sql.Tx, quiz quizDetail, userID uint64, attemptNo uint8) error {
	status := "registered"
	if quiz.JoinMode == "approve" {
		status = "pending"
	}
	_, err := tx.ExecContext(ctx,
		`INSERT INTO participants (quiz_id, user_id, attempt_no, status)
		 VALUES (?, ?, ?, ?)`, quiz.ID, userID, attemptNo, status)
	return err
}

// --- GET /student (murid dashboard) ----------------------------------------

// activeQuizList reads the shared quizlist mirror and keeps the quizzes a
// student can still enter (aktif/berjalan). Only the murid dashboard renders
// this list — the public join page never receives it (no quiz data for
// anonymous visitors).
func activeQuizList(ctx context.Context, db *sql.DB, store *cache.Store) ([]quizListRow, error) {
	list, err := quizListMirror(ctx, db, store)
	if err != nil {
		return nil, err
	}
	active := []quizListRow{}
	for _, r := range list {
		if r.Status == "aktif" || r.Status == "berjalan" {
			active = append(active, r)
		}
	}
	return active, nil
}

// activeQuizNotice is the "you are still in a running quiz" payload the
// murid dashboard renders as a notice modal (spec §7 home): the latest
// attempt that is open RIGHT NOW.
type activeQuizNotice struct {
	Code  string
	Judul string
}

// currentActiveQuiz reads the murid's in-progress attempt, if any. Only a
// 'started' attempt in a still-running quiz counts — exactly the state the
// return link can re-enter (pending/registered are not being worked on yet,
// finished is over). 'aktif' AND 'berjalan' both mean running: only the
// global timer flips the quiz to 'berjalan' (Global.Start), while
// per-question and no-timer quizzes stay 'aktif' until the teacher closes
// them. nil, nil = nothing to notice.
func currentActiveQuiz(ctx context.Context, db *sql.DB, userID uint64) (*activeQuizNotice, error) {
	var n activeQuizNotice
	err := db.QueryRowContext(ctx, `SELECT q.code, q.judul
		FROM participants p JOIN quizzes q ON q.id = p.quiz_id
		WHERE p.user_id = ? AND p.status = 'started'
		  AND q.status IN ('aktif', 'berjalan')
		ORDER BY p.id DESC LIMIT 1`, userID).Scan(&n.Code, &n.Judul)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &n, nil
}

// HomePage: GET /student — the murid dashboard (sidebar shell): join-code
// input + the active quizzes list (spec §7 home; session-gated — the public
// copy of the form lives at GET /join without the list). A still-open
// attempt surfaces as the ongoing-quiz notice modal — sign-in lands here,
// so a murid who left a running quiz is told as soon as they come back.
func (s *Student) HomePage(c *echo.Context) error {
	ctx := c.Request().Context()
	active, err := activeQuizList(ctx, s.DB, s.Store)
	if err != nil {
		return err
	}
	sess := mw.SessionFrom(c)
	notice, err := currentActiveQuiz(ctx, s.DB, sess.UserID)
	if err != nil {
		return err
	}
	return c.Render(http.StatusOK, "page-student-home", map[string]any{
		"Title": "Beranda", "Quizzes": active, "ShowActive": true,
		"ActiveQuiz": notice,
	})
}

// --- POST /join -------------------------------------------------------------

// Join validates the code chain and answers with the redirect target (the
// home page's fetch navigates there — spec §7).
func (s *Student) Join(c *echo.Context) error {
	sess := mw.SessionFrom(c)
	var body struct {
		Code string `json:"code"`
	}
	if rerr := decodeJSON(c, &body); rerr != nil {
		return fail(c, rerr.Status, rerr.Code, rerr.Msg)
	}
	ctx := c.Request().Context()
	quiz, rerr, err := quizByCode(ctx, s.DB, body.Code)
	if err != nil {
		return err
	}
	if rerr != nil {
		return fail(c, rerr.Status, rerr.Code, rerr.Msg)
	}
	part, _, rerr, err := s.joinQuiz(ctx, quiz, sess.UserID)
	if err != nil {
		return err
	}
	if rerr != nil {
		return fail(c, rerr.Status, rerr.Code, rerr.Msg)
	}
	return ok(c, map[string]string{
		"redirect": "/quiz/" + quiz.Code,
		"status":   part.Status,
	})
}

// --- GET /quiz/:code (workspace) --------------------------------------------

// WorkspacePage resolves the attempt state and renders the matching view.
// With no participant row it runs the same join chain server-side, which is
// the invite-URL / QR auto-join path (spec §6.11, user decision).
func (s *Student) WorkspacePage(c *echo.Context) error {
	sess := mw.SessionFrom(c)
	ctx := c.Request().Context()
	quiz, rerr, err := quizByCode(ctx, s.DB, c.Param("code"))
	if err != nil {
		return err
	}
	if rerr != nil {
		return s.errorPage(c, rerr)
	}
	part, found, err := latestParticipant(ctx, s.DB, quiz.ID, sess.UserID, false)
	if err != nil {
		return err
	}
	if !found {
		part, _, rerr, err = s.joinQuiz(ctx, quiz, sess.UserID)
		if err != nil {
			return err
		}
		if rerr != nil {
			return s.errorPage(c, rerr)
		}
	}
	return s.renderWorkspace(c, quiz, part)
}

// errorPage renders an expected failure as an SSR page (spec: redirect is
// not possible mid-fetch; the state page shows the message).
func (s *Student) errorPage(c *echo.Context, rerr *respError) error {
	return c.Render(rerr.Status, "page-student-error", map[string]any{
		"Title":   "Kuis",
		"Heading": headingFor(rerr),
		"Message": rerr.Msg,
	})
}

func headingFor(rerr *respError) string {
	switch rerr.Code {
	case ErrInvalidCode, ErrNotFound:
		return "Kuis tidak ditemukan"
	case ErrQuizEnded:
		return "Kuis telah berakhir"
	case ErrQuizInProgress:
		return "Kuis sedang berlangsung"
	case ErrAttemptLimit:
		return "Batas percobaan tercapai"
	case ErrForbidden:
		return "Tidak diizinkan"
	}
	return "Terjadi kesalahan"
}

// classifyView maps (quiz, attempt) to the workspace view state — the one
// render machine behind GET /quiz/:code and the SSE snapshot (spec §6.7).
// A non-empty rerr means the page is an error page instead of a state page.
func classifyView(quiz quizDetail, part attemptRow) (state string, rerr *respError) {
	if part.Status == "dikeluarkan" {
		return "", errResp(http.StatusForbidden, ErrForbidden, msgRemoved)
	}
	if part.Status == "selesai" { // finished, whether or not the quiz closed
		return "finished", nil
	}
	if quiz.Status == "selesai" {
		if part.Status == "started" {
			return "finished", nil
		}
		return "", errResp(http.StatusGone, ErrQuizEnded, msgEnded) // never entered
	}
	switch part.Status {
	case "pending":
		return "pending", nil
	case "registered":
		if quiz.Status == "nonaktif" {
			return "", errResp(http.StatusNotFound, ErrNotFound, "Kuis tidak ditemukan.")
		}
		if quiz.Status == "berjalan" { // missed START (join race) → closed
			return "", errResp(http.StatusConflict, ErrQuizInProgress, MsgQuizInProgress)
		}
		if quiz.TimerType == "global" {
			return "waiting", nil
		}
		return "ready", nil
	case "started":
		return "started", nil
	}
	return "", errResp(http.StatusConflict, ErrConflict, msgAttemptDone)
}

// renderWorkspace resolves the state page (or the error page) for one
// attempt. With no participant row the caller has already run the join
// chain — that is the invite-URL / QR auto-join path (spec §6.11).
func (s *Student) renderWorkspace(c *echo.Context, quiz quizDetail, part attemptRow) error {
	state, rerr := classifyView(quiz, part)
	if rerr != nil {
		return s.errorPage(c, rerr)
	}
	if state == "finished" {
		return s.renderFinished(c, quiz, part)
	}
	if state == "started" {
		return s.renderStarted(c, quiz, part, workspaceBase(quiz))
	}
	data := workspaceBase(quiz)
	data["State"] = state
	return c.Render(http.StatusOK, "page-student-workspace", data)
}

// workspaceBase is the part of the page shared by every non-finished state.
func workspaceBase(quiz quizDetail) map[string]any {
	data := map[string]any{
		"Title":    quiz.Judul,
		"Judul":    quiz.Judul,
		"Code":     quiz.Code,
		"StartURL": "/quiz/" + quiz.Code + "/start",
	}
	if quiz.Deskripsi.Valid {
		data["Deskripsi"] = quiz.Deskripsi.String
	}
	return data
}

// renderFinished shows the attempt result (score only when the setting is on,
// "awaiting grading" while essays are pending — spec §6.7.2).
func (s *Student) renderFinished(c *echo.Context, quiz quizDetail, part attemptRow) error {
	ctx := c.Request().Context()
	hasEssay, err := quizHasEssay(ctx, s.DB, quiz.ID)
	if err != nil {
		return err
	}
	notAttempted := !part.FinishedAt.Valid
	data := map[string]any{
		"Title":        quiz.Judul,
		"State":        "finished",
		"Judul":        quiz.Judul,
		"Code":         quiz.Code,
		"NotAttempted": notAttempted,
		"ShowScore":    quiz.ShowFinalScore && !notAttempted,
		"Cheating":     part.Cheating,
	}
	if !notAttempted {
		if part.FinalScore.Valid {
			data["FinalText"] = fmt.Sprintf("%.2f", part.FinalScore.Float64)
			data["Pending"] = false
		} else if hasEssay {
			data["Pending"] = true // awaiting teacher grading
		} else if part.ScoreAuto.Valid {
			data["FinalText"] = fmt.Sprintf("%.2f", part.ScoreAuto.Float64)
		}
		if part.ScoreAuto.Valid {
			data["AutoText"] = fmt.Sprintf("%.2f", part.ScoreAuto.Float64)
		}
	}
	return c.Render(http.StatusOK, "page-student-workspace", data)
}

// --- workspace view model ---------------------------------------------------

type wsOption struct {
	Letter   string `json:"letter"`
	Original int    `json:"original"`
	Text     string `json:"text"`
}

type wsQuestion struct {
	ID       uint64      `json:"id"`
	Index    int         `json:"index"` // 0-based display position
	Teks     string      `json:"teks"`
	Type     string      `json:"type"`
	ImageURL string      `json:"image_url"` // /media/question/<id>; "" = no image
	Options  []wsOption  `json:"options"`
	Answered bool        `json:"answered"`
	Given    interface{} `json:"given"`
}

type rankView struct {
	Rank      int
	Name      string
	ScoreText string
	Finished  bool
	Cheating  bool
	Me        bool
}

// wsBlob is embedded as JSON for workspace.js (spec §6.7 client flow).
type wsBlob struct {
	QuizID             uint64       `json:"quiz_id"`
	ParticipantID      uint64       `json:"participant_id"` // this murid's row — lets the quiz-wide SSE frames tell "about me" from "about a classmate"
	Code               string       `json:"code"`
	State              string       `json:"state"`
	Linear             bool         `json:"linear"`
	TimerOn            bool         `json:"timer_on"`
	EndsAt             int64        `json:"ends_at"`              // unix seconds, 0 = none
	PerQuestionSeconds int          `json:"per_question_seconds"` // quiz.PerQuestionSeconds (0 for tanpa_timer)
	QSince             int64        `json:"q_since"`              // part.CurrentQSince unix, 0 = none
	ServerNow          int64        `json:"server_now"`
	Current            int          `json:"current"` // 1-based
	Total              int          `json:"total"`
	ShowCorrectWrong   bool         `json:"show_correct_wrong"`
	RankingLive        bool         `json:"ranking_live"`
	AnswerURL          string       `json:"answer_url"`
	NextURL            string       `json:"next_url"`
	FinishURL          string       `json:"finish_url"`
	PageURL            string       `json:"page_url"`
	StreamURL          string       `json:"stream_url"`
	Questions          []wsQuestion `json:"questions"`
}

type bankQuestion struct {
	ID      uint64
	Teks    string
	Type    string
	Options []string
	Correct sql.NullString
}

// loadQuestions reads the quiz's questions in seq order.
func loadQuestions(ctx context.Context, db *sql.DB, quizID uint64) ([]bankQuestion, error) {
	rows, err := db.QueryContext(ctx, `SELECT q.id, q.teks, q.type, q.options, q.correct
		FROM quiz_questions qq JOIN questions q ON q.id = qq.question_id
		WHERE qq.quiz_id = ? ORDER BY qq.seq`, quizID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []bankQuestion{}
	for rows.Next() {
		var q bankQuestion
		var opts, corr sql.NullString
		if err := rows.Scan(&q.ID, &q.Teks, &q.Type, &opts, &corr); err != nil {
			return nil, err
		}
		if opts.Valid && opts.String != "" {
			if err := json.Unmarshal([]byte(opts.String), &q.Options); err != nil {
				return nil, err
			}
		}
		q.Correct = corr
		out = append(out, q)
	}
	return out, rows.Err()
}

func decodeOrder(ns sql.NullString) *quizengine.Order {
	if !ns.Valid || ns.String == "" {
		return nil
	}
	var o quizengine.Order
	if err := json.Unmarshal([]byte(ns.String), &o); err != nil {
		log.Printf("qorder: corrupt json (participant attempt row): %v", err)
		return nil
	}
	return &o
}

// renderStarted builds the full attempt view: qorder-driven question cards,
// timer anchors, the ranking panel and the JSON blob for workspace.js.
func (s *Student) renderStarted(c *echo.Context, quiz quizDetail, part attemptRow, data map[string]any) error {
	ctx := c.Request().Context()
	questions, err := loadQuestions(ctx, s.DB, quiz.ID)
	if err != nil {
		return err
	}
	answers := map[uint64]string{}
	if rows, err := s.DB.QueryContext(ctx,
		`SELECT question_id, answer FROM answers WHERE participant_id = ?`, part.ID); err == nil {
		for rows.Next() {
			var qid uint64
			var ans sql.NullString
			if err := rows.Scan(&qid, &ans); err != nil {
				rows.Close()
				return err
			}
			answers[qid] = ans.String
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
	} else {
		return err
	}

	// display order: qorder snapshot, else seq order (identity)
	order := decodeOrder(part.QOrder)
	ids := make([]uint64, 0, len(questions))
	byID := map[uint64]bankQuestion{}
	for _, q := range questions {
		ids = append(ids, q.ID)
		byID[q.ID] = q
	}
	perms := map[uint64][]int{}
	if order != nil && len(order.Questions) > 0 {
		ids = order.Questions
		perms = order.Options
	}

	// one image per question at most (uq_qi_question); image ids by question
	// so each card can point at /media/question/<id> (spec §7)
	imgByQ := map[uint64]uint64{}
	if rows, err := s.DB.QueryContext(ctx,
		`SELECT qi.question_id, qi.id FROM question_images qi
		  JOIN quiz_questions qq ON qq.question_id = qi.question_id
		 WHERE qq.quiz_id = ? AND qi.active = 1`, quiz.ID); err == nil {
		for rows.Next() {
			var qid, imgID uint64
			if err := rows.Scan(&qid, &imgID); err != nil {
				rows.Close()
				return err
			}
			imgByQ[qid] = imgID
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
	} else {
		return err
	}

	current := 1
	if part.CurrentQ.Valid && int(part.CurrentQ.Int64) >= 1 {
		current = int(part.CurrentQ.Int64)
	}
	if current > len(ids) {
		current = len(ids)
	}
	if current < 1 && len(ids) > 0 {
		current = 1
	}

	linear := quiz.TimerType == "global"
	wsQuestions := make([]wsQuestion, 0, len(ids))
	for pos, qid := range ids {
		q, ok := byID[qid]
		if !ok {
			continue
		}
		perm := perms[qid]
		if len(perm) == 0 {
			perm = make([]int, len(q.Options))
			for i := range perm {
				perm[i] = i
			}
		}
		opts := make([]wsOption, 0, len(perm))
		for letterPos, orig := range perm {
			if orig < 0 || orig >= len(q.Options) {
				continue
			}
			opts = append(opts, wsOption{
				Letter:   string(rune('A' + letterPos)),
				Original: orig,
				Text:     q.Options[orig],
			})
		}
		wsq := wsQuestion{
			ID:      q.ID,
			Index:   pos,
			Teks:    q.Teks,
			Type:    q.Type,
			Options: opts,
			Given:   nil,
		}
		if imgID, ok := imgByQ[q.ID]; ok {
			wsq.ImageURL = fmt.Sprintf("/media/question/%d", imgID)
		}
		if raw, hit := answers[q.ID]; hit {
			wsq.Answered = true
			var v any
			if err := json.Unmarshal([]byte(raw), &v); err == nil {
				wsq.Given = v
			}
		}
		wsQuestions = append(wsQuestions, wsq)
	}

	// timer: the participant's personal clock, else the global quiz clock
	var endsUnix int64
	if quiz.TimerOn {
		switch {
		case part.EndsAt.Valid:
			endsUnix = part.EndsAt.Time.Unix()
		case linear && quiz.Status == "berjalan" && quiz.StartedAt.Valid:
			endsUnix = quizengine.GlobalEndsAt(quiz.StartedAt.Time, quiz.TotalSeconds).Unix()
		}
	}

	// per-question clock anchor: when the current question was opened
	var qSince int64
	if part.CurrentQSince.Valid {
		qSince = part.CurrentQSince.Time.Unix()
	}

	blob := wsBlob{
		QuizID:             quiz.ID,
		ParticipantID:      part.ID,
		Code:               quiz.Code,
		State:              "started",
		Linear:             linear,
		TimerOn:            endsUnix > 0,
		EndsAt:             endsUnix,
		PerQuestionSeconds: quiz.PerQuestionSecs,
		QSince:             qSince,
		ServerNow:          time.Now().Unix(),
		Current:            current,
		Total:              len(ids),
		ShowCorrectWrong:   quiz.ShowCorrectWrong,
		RankingLive:        quiz.RankingLive,
		AnswerURL:          "/quiz/" + quiz.Code + "/answer",
		NextURL:            "/quiz/" + quiz.Code + "/next",
		FinishURL:          "/quiz/" + quiz.Code + "/finish",
		PageURL:            "/quiz/" + quiz.Code + "/page",
		StreamURL:          "/quiz/" + quiz.Code + "/stream",
		Questions:          wsQuestions,
	}
	blobJSON, err := json.Marshal(blob)
	if err != nil {
		return err
	}

	data["State"] = "started"
	data["Linear"] = linear
	data["PerQ"] = quiz.TimerType == "per_soal"
	data["TimerOn"] = endsUnix > 0
	data["TimerEndsAt"] = endsUnix
	data["ServerNow"] = time.Now().Unix()
	data["Current"] = current
	data["Total"] = len(ids)
	data["Questions"] = wsQuestions
	data["WSData"] = template.JS(blobJSON)

	if quiz.RankingLive {
		views := []rankView{}
		for _, e := range s.Live.Ranker(quiz.ID).Snapshot() {
			views = append(views, rankView{
				Rank: e.Rank, Name: e.Name,
				ScoreText: fmt.Sprintf("%.2f", e.Score),
				Finished:  e.Finished, Cheating: e.Cheating,
				Me: e.ParticipantID == part.ID,
			})
		}
		data["Ranking"] = views
	}

	// The workspace always renders the question page — if the student was
	// last seen on the preview (F5 while reviewing) or the page was never
	// announced (global START), tell the monitor now.
	if s.Live.SetPage(part.ID, "question") {
		name, _ := participantName(ctx, s.DB, part.UserID)
		s.publishPage(quiz.ID, part, name, "question")
	}
	return c.Render(http.StatusOK, "page-student-workspace", data)
}

// --- POST /quiz/:code/start (per-question / no-timer flow) ------------------

// StartAttempt is idempotent: an already-started attempt returns its current
// state unchanged. A fresh start writes qorder once, sets the personal clock
// (only when the quiz is timed) and flips registered → started (spec §6.8).
func (s *Student) StartAttempt(c *echo.Context) error {
	sess := mw.SessionFrom(c)
	ctx := c.Request().Context()
	quiz, rerr, err := quizByCode(ctx, s.DB, c.Param("code"))
	if err != nil {
		return err
	}
	if rerr != nil {
		return fail(c, rerr.Status, rerr.Code, rerr.Msg)
	}
	if quiz.TimerType != "per_soal" && quiz.TimerType != "tanpa_timer" {
		return fail(c, http.StatusConflict, ErrConflict,
			"Kuis ini tidak menggunakan timer per soal.")
	}
	if quiz.Status == "selesai" {
		return fail(c, http.StatusGone, ErrQuizEnded, msgEnded)
	}
	if quiz.Status == "nonaktif" {
		return fail(c, http.StatusNotFound, ErrNotFound, "Kuis tidak ditemukan.")
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	part, found, err := latestParticipant(ctx, tx, quiz.ID, sess.UserID, true)
	if err != nil {
		tx.Rollback()
		return err
	}
	if !found {
		tx.Rollback()
		return fail(c, http.StatusNotFound, ErrNotFound, msgNotParticipant)
	}
	switch part.Status {
	case "dikeluarkan":
		tx.Rollback()
		return fail(c, http.StatusForbidden, ErrForbidden, msgRemoved)
	case "pending":
		tx.Rollback()
		return fail(c, http.StatusForbidden, ErrForbidden, msgAwaiting)
	case "selesai":
		tx.Rollback()
		return fail(c, http.StatusConflict, ErrConflict, msgAttemptDone)
	case "started":
		tx.Rollback() // idempotent: nothing to write
		// a reconnect/re-post still announces the page once per process
		if s.Live.SetPage(part.ID, "question") {
			name, _ := participantName(ctx, s.DB, sess.UserID)
			s.publishPage(quiz.ID, part, name, "question")
		}
		return ok(c, startState(quiz, part))
	}

	// fresh start: qorder once (deterministic seed), personal timer, dwell clock
	questions, err := loadQuestions(ctx, s.DB, quiz.ID)
	if err != nil {
		tx.Rollback()
		return err
	}
	orderJSON, err := makeQOrder(part.ID, quiz.ID, part.AttemptNo, questions,
		quiz.ShuffleQuestions, quiz.ShuffleOptions)
	if err != nil {
		tx.Rollback()
		return err
	}
	var endsAt any
	if quiz.TimerOn {
		endsAt = time.Now().UTC().Add(
			quizengine.PersonalTotal(len(questions), quiz.PerQuestionSecs),
		).UTC().Format("2006-01-02 15:04:05")
	}
	res, err := tx.ExecContext(ctx, `UPDATE participants
		SET qorder = ?, started_at = NOW(), ends_at = ?, status = 'started',
		    current_q = 1, current_q_since = NOW()
		WHERE id = ? AND status = 'registered' AND started_at IS NULL`,
		orderJSON, endsAt, part.ID)
	if err != nil {
		tx.Rollback()
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// lost a concurrent start — report the winner's state
		tx.Rollback()
		current, _, err := latestParticipant(ctx, s.DB, quiz.ID, sess.UserID, false)
		if err != nil {
			return err
		}
		return ok(c, startState(quiz, current))
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	part, _, err = latestParticipant(ctx, s.DB, quiz.ID, sess.UserID, false)
	if err != nil {
		return err
	}
	name, _ := participantName(ctx, s.DB, sess.UserID)
	// the attempt just started: put the murid in the live ranking at
	// score 0 so they show up before their first answer (spec §6.7.3);
	// registered-but-never-started students stay out (spec §8).
	s.Live.Ranker(quiz.ID).Upsert(quizengine.Entry{
		ParticipantID: part.ID,
		Name:          name,
		Score:         0,
	})
	s.publishRank(quiz)
	// the attempt just started: the monitor learns name + first page live
	// (spec §6.3/§7) — this is what puts the murid on the monitor in the
	// no-timer and per-question flows the moment they press Start.
	if s.Live.SetPage(part.ID, "question") {
		s.publishPage(quiz.ID, part, name, "question")
	}
	return ok(c, startState(quiz, part))
}

func startState(quiz quizDetail, part attemptRow) map[string]any {
	var endsUnix int64
	if part.EndsAt.Valid {
		endsUnix = part.EndsAt.Time.Unix()
	}
	current := 1
	if part.CurrentQ.Valid && int(part.CurrentQ.Int64) >= 1 {
		current = int(part.CurrentQ.Int64)
	}
	return map[string]any{
		"status":    part.Status,
		"ends_at":   endsUnix,
		"current_q": current,
	}
}

// --- POST /quiz/:code/answer ------------------------------------------------

// SubmitAnswer upserts one answer inside a transaction (wall clock wins:
// the participant's ended state is re-checked under the row lock), then
// publishes and answers with the optional correct/wrong preview (linear
// mode only — review mode never leaks, spec §6.7).
func (s *Student) SubmitAnswer(c *echo.Context) error {
	sess := mw.SessionFrom(c)
	ctx := c.Request().Context()
	quiz, rerr, err := quizByCode(ctx, s.DB, c.Param("code"))
	if err != nil {
		return err
	}
	if rerr != nil {
		return fail(c, rerr.Status, rerr.Code, rerr.Msg)
	}

	var body struct {
		QuestionID uint64          `json:"question_id"`
		Answer     json.RawMessage `json:"answer"`
	}
	if rerr := decodeJSON(c, &body); rerr != nil {
		return fail(c, rerr.Status, rerr.Code, rerr.Msg)
	}
	if body.QuestionID == 0 {
		return fail(c, http.StatusBadRequest, ErrValidation, "Pertanyaan wajib diisi.")
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	part, found, err := latestParticipant(ctx, tx, quiz.ID, sess.UserID, true)
	if err != nil {
		tx.Rollback()
		return err
	}
	if !found {
		tx.Rollback()
		return fail(c, http.StatusNotFound, ErrNotFound, msgNotParticipant)
	}
	if part.Status == "dikeluarkan" {
		tx.Rollback()
		return fail(c, http.StatusForbidden, ErrForbidden, msgRemoved)
	}
	if part.Status == "pending" {
		tx.Rollback()
		return fail(c, http.StatusForbidden, ErrForbidden, msgAwaiting)
	}
	if part.Status != "started" {
		tx.Rollback()
		return fail(c, http.StatusConflict, ErrConflict, msgAttemptDone)
	}
	if quiz.Status == "selesai" {
		tx.Rollback()
		return fail(c, http.StatusGone, ErrQuizEnded, msgEnded)
	}
	if part.EndsAt.Valid && !time.Now().UTC().Before(part.EndsAt.Time) {
		// wall clock passed inside the transaction (§8): close the attempt
		// over the FULL question count (unanswered = wrong), then answer 410
		scoreAuto, final, name, _, err := s.scoreAndFinish(ctx, tx, quiz, part, sess.UserID)
		if err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		s.publishFinished(quiz, part, name, scoreAuto, final)
		return fail(c, http.StatusGone, ErrQuizEnded, msgEnded)
	}

	var qType string
	var correct sql.NullString
	var optCount sql.NullInt64
	var qTeks string
	if err := tx.QueryRowContext(ctx, `SELECT q.type, q.correct, JSON_LENGTH(q.options), q.teks
		FROM quiz_questions qq JOIN questions q ON q.id = qq.question_id
		WHERE qq.quiz_id = ? AND qq.question_id = ?`, quiz.ID, body.QuestionID).
		Scan(&qType, &correct, &optCount, &qTeks); errors.Is(err, sql.ErrNoRows) {
		tx.Rollback()
		return fail(c, http.StatusNotFound, ErrNotFound, "Pertanyaan tidak ditemukan.")
	} else if err != nil {
		tx.Rollback()
		return err
	}

	given, stored, rerr := parseAnswer(qType, int(optCount.Int64), body.Answer)
	if rerr != nil {
		tx.Rollback()
		return fail(c, rerr.Status, rerr.Code, rerr.Msg)
	}
	var isCorrect any
	if qType != "essay" {
		okCorrect := quizengine.IsCorrect(qType, decodeJSONValue(correct), given)
		isCorrect = okCorrect
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO answers
		(participant_id, question_id, answer, is_correct, answered_at)
		VALUES (?, ?, ?, ?, NOW())
		ON DUPLICATE KEY UPDATE answer = VALUES(answer),
			is_correct = VALUES(is_correct), answered_at = NOW()`,
		part.ID, body.QuestionID, stored, isCorrect); err != nil {
		tx.Rollback()
		return err
	}

	// linear mode: the answer itself advances the monitor's question clock
	if quiz.TimerType == "global" && part.QOrder.Valid {
		if order := decodeOrder(part.QOrder); order != nil && len(order.Questions) > 0 {
			pos := 1
			if part.CurrentQ.Valid && int(part.CurrentQ.Int64) >= 1 {
				pos = int(part.CurrentQ.Int64)
			}
			if pos < len(order.Questions) {
				pos++
			}
			if _, err := tx.ExecContext(ctx,
				`UPDATE participants SET current_q = ?, current_q_since = NOW() WHERE id = ?`,
				pos, part.ID); err != nil {
				tx.Rollback()
				return err
			}
			part.CurrentQ = sql.NullInt64{Int64: int64(pos), Valid: true}
		}
	}

	// progress points for the live ranker (correct relative to total)
	var correctCount, totalQuestions int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM answers
		WHERE participant_id = ? AND is_correct = 1`, part.ID).Scan(&correctCount); err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM quiz_questions WHERE quiz_id = ?`, quiz.ID).Scan(&totalQuestions); err != nil {
		tx.Rollback()
		return err
	}
	var name string
	if err := tx.QueryRowContext(ctx,
		`SELECT nama_lengkap FROM users WHERE id = ?`, sess.UserID).Scan(&name); err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	// publish strictly after COMMIT
	s.Hub.Publish(realtime.TeacherTopicForQuizID(quiz.ID), realtime.Event{
		Type: "answer",
		Data: map[string]any{
			"participant_id": part.ID,
			"name":           name,
			"question_id":    body.QuestionID,
			// the answer's own qorder position: the monitor attributes it
			// to THIS question even after current_q moved on (spec §6.3)
			"question_pos":    answerPos(part.QOrder, body.QuestionID),
			"answer":          json.RawMessage(stored),
			"answer_display":  answerDisplay(stored, part.QOrder, body.QuestionID),
			"q_teks":          qTeks,
			"is_correct":      isCorrect,
			"current_q":       part.CurrentQ.Int64,
			"current_q_since": time.Now().Unix(),
		},
	})
	s.Live.Ranker(quiz.ID).Upsert(quizengine.Entry{
		ParticipantID: part.ID,
		Name:          name,
		Score:         quizengine.CentiPercent(correctCount, totalQuestions),
		Finished:      false,
		Cheating:      part.Cheating,
	})
	s.publishRank(quiz)

	// preview: linear + show_correct_wrong + auto-graded question only
	var preview any
	if quiz.TimerType == "global" && quiz.ShowCorrectWrong && isCorrect != nil {
		if correct.Valid {
			var key any
			if err := json.Unmarshal([]byte(correct.String), &key); err == nil {
				preview = map[string]any{"correct": isCorrect, "key": key}
			}
		}
	}
	return ok(c, map[string]any{"preview": preview})
}

// parseAnswer validates and normalizes one submitted answer. pg/multi become
// a JSON array of ORIGINAL option indexes; essay a JSON-encoded string
// (spec §6.8 / plan §Step 10).
func parseAnswer(qType string, optionCount int, raw json.RawMessage) (given any, stored string, rerr *respError) {
	if len(raw) == 0 {
		return nil, "", errResp(http.StatusBadRequest, ErrValidation, "Jawaban wajib diisi.")
	}
	switch qType {
	case "pg", "multi":
		var nums []float64
		if err := json.Unmarshal(raw, &nums); err != nil {
			return nil, "", errResp(http.StatusBadRequest, ErrValidation, "Format jawaban tidak valid.")
		}
		if len(nums) == 0 {
			return nil, "", errResp(http.StatusBadRequest, ErrValidation, "Pilih setidaknya satu pilihan.")
		}
		if qType == "pg" && len(nums) != 1 {
			return nil, "", errResp(http.StatusBadRequest, ErrValidation, "Format jawaban tidak valid.")
		}
		idx := make([]int, 0, len(nums))
		for _, n := range nums {
			if n != float64(int(n)) || int(n) < 0 || int(n) >= optionCount {
				return nil, "", errResp(http.StatusBadRequest, ErrValidation, "Pilihan tidak valid.")
			}
			idx = append(idx, int(n))
		}
		b, err := json.Marshal(idx)
		if err != nil {
			return nil, "", errResp(http.StatusBadRequest, ErrValidation, "Format jawaban tidak valid.")
		}
		return idx, string(b), nil
	case "essay":
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return nil, "", errResp(http.StatusBadRequest, ErrValidation, "Format jawaban tidak valid.")
		}
		if strings.TrimSpace(text) == "" {
			return nil, "", errResp(http.StatusBadRequest, ErrValidation, "Jawaban wajib diisi.")
		}
		b, err := json.Marshal(text)
		if err != nil {
			return nil, "", errResp(http.StatusBadRequest, ErrValidation, "Format jawaban tidak valid.")
		}
		return text, string(b), nil
	}
	return nil, "", errResp(http.StatusBadRequest, ErrValidation, "Jenis pertanyaan tidak valid.")
}

// decodeJSONValue turns a stored JSON column into a value IsCorrect understands.
func decodeJSONValue(ns sql.NullString) any {
	if !ns.Valid || ns.String == "" {
		return nil
	}
	var v any
	if err := json.Unmarshal([]byte(ns.String), &v); err != nil {
		return nil
	}
	return v
}

// --- POST /quiz/:code/next (review mode only) -------------------------------

// NextQuestion returns the preview for the question being left (never
// earlier — review mode leaks nothing, spec §6.7) and advances current_q.
// An optional body {question_id} jumps straight to a question (the free
// navigation's back/jump persists current_q server-side).
func (s *Student) NextQuestion(c *echo.Context) error {
	sess := mw.SessionFrom(c)
	ctx := c.Request().Context()
	quiz, rerr, err := quizByCode(ctx, s.DB, c.Param("code"))
	if err != nil {
		return err
	}
	if rerr != nil {
		return fail(c, rerr.Status, rerr.Code, rerr.Msg)
	}
	if quiz.TimerType != "per_soal" && quiz.TimerType != "tanpa_timer" {
		return fail(c, http.StatusConflict, ErrConflict,
			"Kuis ini tidak mengizinkan navigasi bebas.")
	}
	var body struct {
		QuestionID uint64 `json:"question_id"`
	}
	if rerr := decodeJSON(c, &body); rerr != nil {
		return fail(c, rerr.Status, rerr.Code, rerr.Msg)
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	part, found, err := latestParticipant(ctx, tx, quiz.ID, sess.UserID, true)
	if err != nil {
		tx.Rollback()
		return err
	}
	if !found {
		tx.Rollback()
		return fail(c, http.StatusNotFound, ErrNotFound, msgNotParticipant)
	}
	if part.Status != "started" {
		tx.Rollback()
		return fail(c, http.StatusConflict, ErrConflict, "Mulai kuis terlebih dahulu.")
	}
	order := decodeOrder(part.QOrder)
	if order == nil || len(order.Questions) == 0 {
		tx.Rollback()
		return fail(c, http.StatusConflict, ErrConflict, "Percobaan ini tidak berisi pertanyaan.")
	}

	pos := 1
	if part.CurrentQ.Valid && int(part.CurrentQ.Int64) >= 1 {
		pos = int(part.CurrentQ.Int64)
	}
	target := pos + 1
	if body.QuestionID != 0 {
		found := false
		for i, qid := range order.Questions {
			if qid == body.QuestionID {
				target, found = i+1, true
				break
			}
		}
		if !found {
			tx.Rollback()
			return fail(c, http.StatusNotFound, ErrNotFound, "Pertanyaan tidak ditemukan.")
		}
	}
	if target > len(order.Questions) {
		target = len(order.Questions)
	}
	// per_soal is forward-only: the per-question clock pins the murid to the
	// current question — reopening an earlier one would reset its countdown
	// (spec §6.7 / design C7). tanpa_timer keeps full free navigation.
	if quiz.TimerType == "per_soal" && target < pos {
		tx.Rollback()
		return fail(c, http.StatusConflict, ErrConflict,
			"Pertanyaan sebelumnya tidak dapat dibuka kembali.")
	}

	// preview for the question being LEFT (the one just answered/skipped)
	var preview any
	if target != pos && quiz.ShowCorrectWrong {
		leftID := order.Questions[pos-1]
		var ans sql.NullString
		var isCorrect sql.NullInt64
		var qType string
		var correct sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT a.answer, a.is_correct, q.type, q.correct
			FROM answers a JOIN questions q ON q.id = a.question_id
			WHERE a.participant_id = ? AND a.question_id = ?`, part.ID, leftID).
			Scan(&ans, &isCorrect, &qType, &correct)
		if err == nil && ans.Valid && isCorrect.Valid && qType != "essay" && correct.Valid {
			var key any
			if json.Unmarshal([]byte(correct.String), &key) == nil {
				preview = map[string]any{"correct": isCorrect.Int64 == 1, "key": key}
			}
		} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
			tx.Rollback()
			return err
		}
	}

	// the segment just ended: bank its seconds for the per-question monitor
	// (mirror write only — it lands after COMMIT, spec §6.2)
	var dwell time.Duration
	leftQuestion := target != pos && part.CurrentQSince.Valid
	if leftQuestion {
		dwell = time.Since(part.CurrentQSince.Time)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE participants SET current_q = ?, current_q_since = NOW() WHERE id = ?`,
		target, part.ID); err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	if leftQuestion {
		s.Live.AddSpent(part.ID, pos, dwell)
	}
	// navigation also leaves the preview behind (box-jump from the review)
	s.Live.SetPage(part.ID, "question")
	part.CurrentQ = sql.NullInt64{Int64: int64(target), Valid: true}
	part.CurrentQSince = sql.NullTime{Time: time.Now(), Valid: true}
	name, _ := participantName(ctx, s.DB, sess.UserID)
	s.publishPage(quiz.ID, part, name, "question")

	return ok(c, map[string]any{
		"preview":         preview,
		"current_q":       target,
		"total":           len(order.Questions),
		"current_q_since": part.CurrentQSince.Time.Unix(),
	})
}

// --- POST /quiz/:code/page --------------------------------------------------

// ReportPage is the workspace's page beacon ({page: "question"|"preview"}).
// The pre-submit review is a client-side view, so the student's browser
// tells the server when it opens/closes — the live monitor then shows the
// murid on the review page in every timer mode (spec §6.3, §7). No DB
// write: the page is a rebuildable Live mirror.
func (s *Student) ReportPage(c *echo.Context) error {
	sess := mw.SessionFrom(c)
	ctx := c.Request().Context()
	quiz, rerr, err := quizByCode(ctx, s.DB, c.Param("code"))
	if err != nil {
		return err
	}
	if rerr != nil {
		return fail(c, rerr.Status, rerr.Code, rerr.Msg)
	}
	var body struct {
		Page string `json:"page"`
	}
	if rerr := decodeJSON(c, &body); rerr != nil {
		return fail(c, rerr.Status, rerr.Code, rerr.Msg)
	}
	switch body.Page {
	case "preview", "question":
	default:
		return fail(c, http.StatusBadRequest, ErrValidation, "Halaman tidak dikenal.")
	}

	part, found, err := latestParticipant(ctx, s.DB, quiz.ID, sess.UserID, false)
	if err != nil {
		return err
	}
	if !found {
		return fail(c, http.StatusNotFound, ErrNotFound, msgNotParticipant)
	}
	switch part.Status {
	case "dikeluarkan":
		return fail(c, http.StatusForbidden, ErrForbidden, msgRemoved)
	case "pending":
		return fail(c, http.StatusForbidden, ErrForbidden, msgAwaiting)
	case "selesai":
		return fail(c, http.StatusConflict, ErrConflict, msgAttemptDone)
	}
	if part.Status != "started" {
		return fail(c, http.StatusConflict, ErrConflict, "Mulai kuis terlebih dahulu.")
	}

	s.Live.SetPage(part.ID, body.Page)
	name, _ := participantName(ctx, s.DB, sess.UserID)
	s.publishPage(quiz.ID, part, name, body.Page)
	return ok(c, map[string]string{"page": body.Page})
}

// --- POST /quiz/:code/finish ------------------------------------------------

// scoreAndFinish closes one started attempt inside tx: score_auto over the
// FULL question count (unanswered = wrong), final_score per FinalScore (nil
// while essays are pending). updated=false means a concurrent finish won.
func (s *Student) scoreAndFinish(ctx context.Context, tx *sql.Tx, quiz quizDetail,
	part attemptRow, userID uint64,
) (float64, any, string, bool, error) {
	var correct, total int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM answers
		WHERE participant_id = ? AND is_correct = 1`, part.ID).Scan(&correct); err != nil {
		return 0, nil, "", false, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM quiz_questions
		WHERE quiz_id = ?`, quiz.ID).Scan(&total); err != nil {
		return 0, nil, "", false, err
	}
	hasEssay, err := quizHasEssay(ctx, tx, quiz.ID)
	if err != nil {
		return 0, nil, "", false, err
	}
	scoreAuto := quizengine.CentiPercent(correct, total)
	final := quizengine.FinalScore(&scoreAuto, nil, hasEssay)
	res, err := tx.ExecContext(ctx, `UPDATE participants
		SET status = 'selesai', finished_at = NOW(), score_auto = ?, final_score = ?
		WHERE id = ? AND status = 'started'`, scoreAuto, final, part.ID)
	if err != nil {
		return 0, nil, "", false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, nil, "", false, err
	}
	var name string
	if err := tx.QueryRowContext(ctx,
		`SELECT nama_lengkap FROM users WHERE id = ?`, userID).Scan(&name); err != nil {
		return 0, nil, "", false, err
	}
	return scoreAuto, final, name, n > 0, nil
}

// publishFinished fans the close record out strictly after COMMIT.
func (s *Student) publishFinished(quiz quizDetail, part attemptRow, name string,
	scoreAuto float64, final any,
) {
	s.Hub.Publish(realtime.TeacherTopicForQuizID(quiz.ID), realtime.Event{
		Type: "finished",
		Data: map[string]any{
			"participant_id": part.ID,
			"name":           name,
			"score_auto":     scoreAuto,
			"final_score":    final,
		},
	})
	s.Live.Ranker(quiz.ID).Upsert(quizengine.Entry{
		ParticipantID: part.ID,
		Name:          name,
		Score:         scoreAuto,
		Finished:      true,
		Cheating:      part.Cheating,
	})
	s.publishRank(quiz)
}

// FinishAttempt closes the attempt: score_auto over the FULL question count
// (unanswered = wrong), final_score per FinalScore (nil while essays are
// pending), idempotent on an already-selesai row (spec §6.7.2).
func (s *Student) FinishAttempt(c *echo.Context) error {
	sess := mw.SessionFrom(c)
	ctx := c.Request().Context()
	quiz, rerr, err := quizByCode(ctx, s.DB, c.Param("code"))
	if err != nil {
		return err
	}
	if rerr != nil {
		return fail(c, rerr.Status, rerr.Code, rerr.Msg)
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	part, found, err := latestParticipant(ctx, tx, quiz.ID, sess.UserID, true)
	if err != nil {
		tx.Rollback()
		return err
	}
	if !found {
		tx.Rollback()
		return fail(c, http.StatusNotFound, ErrNotFound, msgNotParticipant)
	}
	if part.Status == "dikeluarkan" {
		tx.Rollback()
		return fail(c, http.StatusForbidden, ErrForbidden, msgRemoved)
	}
	if part.Status == "pending" {
		tx.Rollback()
		return fail(c, http.StatusForbidden, ErrForbidden, msgAwaiting)
	}
	if part.Status == "selesai" { // idempotent: report the stored result
		tx.Rollback()
		return ok(c, finishState(quiz, part))
	}
	if part.Status != "started" {
		tx.Rollback()
		return fail(c, http.StatusConflict, ErrConflict, "Mulai kuis terlebih dahulu.")
	}

	scoreAuto, finalScore, name, updated, err := s.scoreAndFinish(ctx, tx, quiz, part, sess.UserID)
	if err != nil {
		tx.Rollback()
		return err
	}
	if !updated { // concurrent finish won
		tx.Rollback()
		current, _, err := latestParticipant(ctx, s.DB, quiz.ID, sess.UserID, false)
		if err != nil {
			return err
		}
		return ok(c, finishState(quiz, current))
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	// publish strictly after COMMIT
	s.publishFinished(quiz, part, name, scoreAuto, finalScore)

	part.Status = "selesai"
	part.ScoreAuto = sql.NullFloat64{Float64: scoreAuto, Valid: true}
	if f, ok := finalScore.(*float64); ok && f != nil {
		part.FinalScore = sql.NullFloat64{Float64: *f, Valid: true}
	}
	return ok(c, finishState(quiz, part))
}

func finishState(quiz quizDetail, part attemptRow) map[string]any {
	data := map[string]any{"status": part.Status}
	if part.ScoreAuto.Valid {
		data["score_auto"] = part.ScoreAuto.Float64
	} else {
		data["score_auto"] = nil
	}
	if part.FinalScore.Valid {
		data["final_score"] = part.FinalScore.Float64
	} else {
		data["final_score"] = nil
	}
	return data
}

// --- POST /quiz/:code/visibility (anti-cheat) -------------------------------

// ReportVisibility records one anti-cheat event: ENUM-validated kind, the
// 10 s same-kind collapse (quizengine.ShouldRecord), then a teacher-topic
// `cheat` push (spec §6.9).
func (s *Student) ReportVisibility(c *echo.Context) error {
	sess := mw.SessionFrom(c)
	ctx := c.Request().Context()
	quiz, rerr, err := quizByCode(ctx, s.DB, c.Param("code"))
	if err != nil {
		return err
	}
	if rerr != nil {
		return fail(c, rerr.Status, rerr.Code, rerr.Msg)
	}
	var body struct {
		Kind string `json:"kind"`
	}
	if rerr := decodeJSON(c, &body); rerr != nil {
		return fail(c, rerr.Status, rerr.Code, rerr.Msg)
	}
	switch body.Kind {
	case "blur", "minimize", "switch", "sleep":
	default:
		return fail(c, http.StatusBadRequest, ErrValidation, "Jenis peristiwa tidak dikenal.")
	}

	part, found, err := latestParticipant(ctx, s.DB, quiz.ID, sess.UserID, false)
	if err != nil {
		return err
	}
	if !found {
		return fail(c, http.StatusNotFound, ErrNotFound, msgNotParticipant)
	}
	if part.Status == "dikeluarkan" {
		return fail(c, http.StatusForbidden, ErrForbidden, msgRemoved)
	}
	quizEnded := quiz.Status == "selesai" || part.Status == "selesai"
	var lastAt sql.NullTime
	var lastKind string
	if err := s.DB.QueryRowContext(ctx,
		`SELECT created_at, kind FROM anti_cheat_events
		 WHERE participant_id = ? ORDER BY id DESC LIMIT 1`, part.ID).
		Scan(&lastAt, &lastKind); errors.Is(err, sql.ErrNoRows) {
		// none yet
	} else if err != nil {
		return err
	}
	now := time.Now().UTC()
	if !quizengine.ShouldRecord(lastAt.Time, now, body.Kind, lastKind, quizEnded) {
		return ok(c, map[string]any{"recorded": false})
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO anti_cheat_events (participant_id, kind, created_at)
		 VALUES (?, ?, ?)`, part.ID, body.Kind, now); err != nil {
		tx.Rollback()
		return err
	}
	// The push carries the participant's violation TOTAL after this insert:
	// the monitor keeps it as an absolute floor, so a snapshot generated
	// around the same time can neither double-count this row nor mask it
	// (spec §6.9 — counts only grow, rows are never removed mid-attempt).
	var count int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM anti_cheat_events WHERE participant_id = ?`,
		part.ID).Scan(&count); err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.Hub.Publish(realtime.TeacherTopicForQuizID(quiz.ID), realtime.Event{
		Type: "cheat",
		Data: map[string]any{"participant_id": part.ID, "kind": body.Kind, "count": count},
	})
	return ok(c, map[string]any{"recorded": true})
}

// --- shared helpers ---------------------------------------------------------

// publishRank pushes the ranking to the student and teacher topics when the
// setting is on (spec §6.7.3). Publish is best-effort: the DB already holds
// the truth, a dropped event only delays a repaint.
func (s *Student) publishRank(quiz quizDetail) {
	if !quiz.RankingLive {
		return
	}
	entries := s.Live.Ranker(quiz.ID).Snapshot()
	payload := realtime.Event{Type: "rank", Data: map[string]any{"ranking": entries}}
	s.Hub.Publish(realtime.TopicForQuizID(quiz.ID), payload)
	s.Hub.Publish(realtime.TeacherTopicForQuizID(quiz.ID), payload)
}

// quizHasEssay reports whether the quiz contains an essay question (its
// final_score stays nil until grading, spec §6.4).
func quizHasEssay(ctx context.Context, q qRow, quizID uint64) (bool, error) {
	var exists bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM quiz_questions qq
		JOIN questions q ON q.id = qq.question_id
		WHERE qq.quiz_id = ? AND q.type = 'essay')`, quizID).Scan(&exists)
	return exists, err
}

// studentSnapshot is the per-connection greeting on the student topic
// (spec §6.3): the classified view state, the attempt clock, the live
// ranking and the waiting-room pending count — "where do I stand right
// now" after every (re)connect. Timers never reset: ends_at is the stored
// clock, not a fresh countdown.
func studentSnapshot(ctx context.Context, db *sql.DB, live *Live, code string, userID uint64) *realtime.Event {
	quiz, rerr, err := quizByCode(ctx, db, code)
	if rerr != nil || err != nil {
		return nil
	}
	part, found, err := latestParticipant(ctx, db, quiz.ID, userID, false)
	if err != nil || !found {
		return nil
	}
	state, rerr := classifyView(quiz, part)
	if rerr != nil {
		state = "error"
	}
	var endsUnix int64
	if quiz.TimerOn {
		switch {
		case part.EndsAt.Valid:
			endsUnix = part.EndsAt.Time.Unix()
		case quiz.TimerType == "global" && part.Status == "started" &&
			quiz.Status == "berjalan" && quiz.StartedAt.Valid:
			endsUnix = quizengine.GlobalEndsAt(quiz.StartedAt.Time, quiz.TotalSeconds).Unix()
		}
	}
	current := 0
	if part.CurrentQ.Valid {
		current = int(part.CurrentQ.Int64)
	}
	data := map[string]any{
		"state":       state,
		"quiz_status": quiz.Status,
		"status":      part.Status,
		"current_q":   current,
		"ends_at":     endsUnix,
		"server_now":  time.Now().Unix(),
		"cheating":    part.Cheating,
	}
	var pending int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM participants WHERE quiz_id = ? AND status = 'pending'`,
		quiz.ID).Scan(&pending); err == nil {
		data["pending_count"] = pending
	}
	if quiz.RankingLive {
		data["ranking"] = live.Ranker(quiz.ID).Snapshot()
	}
	return &realtime.Event{Type: "snapshot", Data: data}
}

// decodeJSON reads the request body; an empty body is legal (optional
// payloads like POST /next), anything else invalid is a 400.
func decodeJSON(c *echo.Context, dst any) *respError {
	dec := json.NewDecoder(c.Request().Body)
	if err := dec.Decode(dst); err != nil && !errors.Is(err, io.EOF) {
		return errResp(http.StatusBadRequest, ErrValidation, "Badan JSON tidak valid.")
	}
	return nil
}
