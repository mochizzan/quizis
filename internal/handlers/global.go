package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/labstack/echo/v5"

	"quiz/internal/cache"
	"quiz/internal/quizengine"
	"quiz/internal/realtime"
)

// disconnectSilence is how long a stream must be silent before its personal
// clock freezes (spec §6.6 — the heartbeat cadence is 15 s, so a healthy
// connection always beats inside this window).
const disconnectSilence = 15 * time.Second

// Global serves the global-timer teacher routes (spec §6.6): the live
// monitor page, roster actions, START and STOP. Every way a running quiz
// can end — STOP, the 1 s timeout, the all-finished confirmation — funnels
// through closeQuiz; the boot helpers (watchdog loop + rehydrate) live here
// too.
type Global struct {
	DB    *sql.DB
	Hub   *realtime.Hub
	Store *cache.Store
	Live  *Live

	mu          sync.Mutex
	allFinished map[uint64]bool // all_finished_pending dedupe, cleared on close
	silence     atomic.Int64    // disconnect threshold in ns; 0 = 15 s default
}

// NewGlobal builds the handler with its once-per-quiz announcement state.
func NewGlobal(db *sql.DB, hub *realtime.Hub, store *cache.Store, live *Live) *Global {
	return &Global{
		DB: db, Hub: hub, Store: store, Live: live,
		allFinished: make(map[uint64]bool),
	}
}

// makeQOrder builds the stored attempt order (question sequence + option
// permutations): seeded per (participant, quiz, attempt), written once and
// never regenerated (spec §6.7.1). Used by both start paths — per-question
// /start and the global START in Step 11.
func makeQOrder(pid, quizID uint64, attempt uint8, questions []bankQuestion, shuffleQ, shuffleO bool) (string, error) {
	ids := make([]uint64, 0, len(questions))
	counts := make(map[uint64]int, len(questions))
	for _, q := range questions {
		ids = append(ids, q.ID)
		counts[q.ID] = len(q.Options)
	}
	seed := quizengine.SeedFor(pid, quizID, uint64(attempt))
	order := quizengine.BuildOrder(seed, ids, counts, shuffleQ, shuffleO)
	b, err := json.Marshal(order)
	return string(b), err
}

// --- monitor page + snapshot ------------------------------------------------

// monitorCard is one latest-attempt student row on the live monitor (§7).
type monitorCard struct {
	ParticipantID uint64   `json:"participant_id"`
	UserID        uint64   `json:"user_id"`
	Name          string   `json:"name"`
	AttemptNo     uint8    `json:"attempt_no"`
	Status        string   `json:"status"`
	CurrentQ      int      `json:"current_q"`
	CurrentQSince int64    `json:"current_q_since"` // unix, 0 = none
	EndsAt        int64    `json:"ends_at"`         // unix, 0 = none
	FinishedAt    int64    `json:"finished_at"`     // unix, 0 = none
	ScoreAuto     *float64 `json:"score_auto"`
	FinalScore    *float64 `json:"final_score"`
	Cheating      bool     `json:"cheating"`
	Connected     bool     `json:"connected"`
	Violations    int      `json:"violations"`
	AnswerQ       uint64   `json:"answer_question_id"`  // last submitted answer
	AnswerQPos    int      `json:"answer_question_pos"` // its 1-based qorder position, 0 = unknown
	Answer        string   `json:"answer"`              // stored JSON, "" = none
	Page          string   `json:"page"`                // ""/"question"/"preview"
	Spent         []SpentQ `json:"spent"`               // per-question seconds
	ScoreText     string   `json:"-"`                   // display-only "%.2f"
	AnswerText    string   `json:"-"`                   // display-only form of Answer (SSR)
}

// monitorData is the one builder behind the SSR monitor page and the
// per-connection snapshot: quiz header, latest attempt per student,
// waiting-room count and the live ranking (spec §6.3, §7).
func monitorData(ctx context.Context, db *sql.DB, live *Live, quizID uint64) (map[string]any, error) {
	var judul, code, status, timerType string
	var timerOn int
	var startedAt sql.NullTime
	var totalSeconds int
	err := db.QueryRowContext(ctx, `SELECT judul, code, status, timer_type, timer_on,
		started_at, total_seconds FROM quizzes WHERE id = ?`, quizID).
		Scan(&judul, &code, &status, &timerType, &timerOn, &startedAt, &totalSeconds)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	// Active tracking rows only: a submitted attempt (status 'selesai')
	// leaves the cards the moment it finishes — the monitor covers every
	// murid from start until submit (spec §7). Pending/registered/removed
	// rows stay, the ranking keeps the finished scores.
	rows, err := db.QueryContext(ctx, `SELECT p.id, p.user_id, u.nama_lengkap, p.attempt_no,
		p.status, p.current_q, p.current_q_since, p.ends_at, p.finished_at,
		p.score_auto, p.final_score, p.cheating
		FROM participants p JOIN users u ON u.id = p.user_id
		WHERE p.quiz_id = ? AND p.status <> 'selesai' AND (p.user_id, p.attempt_no) IN
		  (SELECT user_id, MAX(attempt_no) FROM participants WHERE quiz_id = ? GROUP BY user_id)
		ORDER BY p.id`, quizID, quizID)
	if err != nil {
		return nil, err
	}
	cards := []monitorCard{}
	pending := 0
	for rows.Next() {
		var c monitorCard
		var curQ sql.NullInt64
		var curSince, ends, finished sql.NullTime
		var score, final sql.NullFloat64
		if err := rows.Scan(&c.ParticipantID, &c.UserID, &c.Name, &c.AttemptNo,
			&c.Status, &curQ, &curSince, &ends, &finished, &score, &final, &c.Cheating); err != nil {
			rows.Close()
			return nil, err
		}
		if curQ.Valid {
			c.CurrentQ = int(curQ.Int64)
		}
		if curSince.Valid {
			c.CurrentQSince = curSince.Time.Unix()
		}
		if ends.Valid {
			c.EndsAt = ends.Time.Unix()
		}
		if finished.Valid {
			c.FinishedAt = finished.Time.Unix()
		}
		if score.Valid {
			v := score.Float64
			c.ScoreAuto = &v
			c.ScoreText = fmt.Sprintf("%.2f", v)
		}
		if final.Valid {
			v := final.Float64
			c.FinalScore = &v
		}
		if c.Status == "pending" {
			pending++
		}
		c.Connected = live.Connected(c.ParticipantID)
		c.Page = live.Page(c.ParticipantID)
		c.Spent = live.Spent(c.ParticipantID)
		cards = append(cards, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// per-card extras: violation count and the most recent submitted answer
	for i := range cards {
		if err := db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM anti_cheat_events WHERE participant_id = ?`,
			cards[i].ParticipantID).Scan(&cards[i].Violations); err != nil {
			return nil, err
		}
		var ans sql.NullString
		var qorder sql.NullString
		err := db.QueryRowContext(ctx,
			`SELECT a.question_id, a.answer, p.qorder FROM answers a
			 JOIN participants p ON p.id = a.participant_id
			 WHERE a.participant_id = ?
			 ORDER BY a.answered_at DESC, a.id DESC LIMIT 1`, cards[i].ParticipantID).
			Scan(&cards[i].AnswerQ, &ans, &qorder)
		if errors.Is(err, sql.ErrNoRows) {
			cards[i].AnswerQ = 0
		} else if err != nil {
			return nil, err
		} else {
			cards[i].Answer = ans.String
			cards[i].AnswerText = answerDisplay(ans.String)
			// the answer is attributed to ITS question (qorder position),
			// never to the card's current question (spec §6.3)
			cards[i].AnswerQPos = answerPos(qorder, cards[i].AnswerQ)
		}
	}

	var endsUnix int64
	if timerOn != 0 && startedAt.Valid && status == "berjalan" {
		endsUnix = quizengine.GlobalEndsAt(startedAt.Time, totalSeconds).Unix()
	}
	ranking := live.Ranker(quizID).Snapshot()

	blob, err := json.Marshal(map[string]any{
		"quiz_id": quizID, "code": code, "status": status,
		"timer_type": timerType, "timer_on": timerOn != 0,
		"total_seconds": totalSeconds, "ends_at": endsUnix,
		"server_now": time.Now().Unix(), "pending": pending,
		"participants": cards, "ranking": ranking,
	})
	if err != nil {
		return nil, err
	}
	var startedUnix int64
	if startedAt.Valid {
		startedUnix = startedAt.Time.Unix()
	}
	return map[string]any{
		"Title":        "Pemantauan — " + judul,
		"ID":           quizID,
		"Judul":        judul,
		"Code":         code,
		"Status":       status,
		"TimerType":    timerType,
		"TimerOn":      timerOn != 0,
		"StartedAt":    startedUnix,
		"TotalSeconds": totalSeconds,
		"EndsUnix":     endsUnix,
		"ServerNow":    time.Now().Unix(),
		"Pending":      pending,
		"Cards":        cards,
		"Ranking":      ranking,
		"StreamURL":    fmt.Sprintf("/teacher/quiz/%d/monitor/stream", quizID),
		"MonitorData":  template.JS(blob),
	}, nil
}

// answerPos is the 1-based position of questionID in the attempt's stored
// qorder (spec §6.7.1) — the monitor labels each answer with the question it
// actually belongs to, so an advancing current_q never re-attributes it.
// 0 = the question is not in this attempt's order.
func answerPos(qorder sql.NullString, questionID uint64) int {
	order := decodeOrder(qorder)
	if order == nil {
		return 0
	}
	for i, qid := range order.Questions {
		if qid == questionID {
			return i + 1
		}
	}
	return 0
}

// answerDisplay mirrors monitor.js fmtAnswer for the server-rendered answer
// line: stored option indexes become letters, JSON strings are unquoted and
// truncated — the SSR card and the live JS card must read identically.
func answerDisplay(raw string) string {
	if len(raw) > 0 && raw[0] == '[' {
		var idx []int
		if err := json.Unmarshal([]byte(raw), &idx); err != nil {
			return raw
		}
		out := ""
		for i, n := range idx {
			if i > 0 {
				out += ", "
			}
			out += string(rune('A' + n))
		}
		return out
	}
	var text string
	if err := json.Unmarshal([]byte(raw), &text); err != nil {
		return raw
	}
	runes := []rune(text)
	if len(runes) > 60 {
		return string(runes[:57]) + "…"
	}
	return text
}

// monitorSnapshot is the per-connection greeting on the teacher topic: the
// whole monitor state for exactly this client (spec §6.3).
func monitorSnapshot(ctx context.Context, db *sql.DB, live *Live, quizID uint64) *realtime.Event {
	data, err := monitorData(ctx, db, live, quizID)
	if err != nil || data == nil {
		return nil
	}
	return &realtime.Event{Type: "snapshot", Data: data}
}

// MonitorPage is GET /teacher/quiz/:id/monitor: the server-rendered live
// monitor (waiting room + student cards); monitor.js takes over over SSE.
func (g *Global) MonitorPage(c *echo.Context) error {
	id, rerr := monitorQuizID(c)
	if rerr != nil {
		return fail(c, rerr.Status, rerr.Code, rerr.Msg)
	}
	ctx := c.Request().Context()
	data, err := monitorData(ctx, g.DB, g.Live, id)
	if err != nil {
		return err
	}
	if data == nil {
		return fail(c, http.StatusNotFound, ErrNotFound, "Kuis tidak ditemukan.")
	}
	judul, _ := data["Judul"].(string)
	data["Crumbs"] = QuizCrumbs(id, judul, "Pemantauan")
	return c.Render(http.StatusOK, "page-teacher-monitor", data)
}

// monitorQuizID parses the :id path segment.
func monitorQuizID(c *echo.Context) (uint64, *respError) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		return 0, errResp(http.StatusNotFound, ErrNotFound, "Kuis tidak ditemukan.")
	}
	return id, nil
}

// --- roster actions ---------------------------------------------------------

// afterRosterChange republishes the monitor snapshot so every open monitor
// repaints from DB truth (spec §6.2: after COMMIT, never before).
func (g *Global) afterRosterChange(ctx context.Context, quizID uint64) {
	if ev := monitorSnapshot(ctx, g.DB, g.Live, quizID); ev != nil {
		g.Hub.Publish(realtime.TeacherTopicForQuizID(quizID), *ev)
	}
}

// ParticipantAction is POST /teacher/quiz/:id/participants/:pid/action with
// {action: approve|reject|remove|cheat_toggle} (spec §6.9–§6.12).
func (g *Global) ParticipantAction(c *echo.Context) error {
	quizID, rerr := monitorQuizID(c)
	if rerr != nil {
		return fail(c, rerr.Status, rerr.Code, rerr.Msg)
	}
	pid, err := strconv.ParseUint(c.Param("pid"), 10, 64)
	if err != nil || pid == 0 {
		return fail(c, http.StatusNotFound, ErrNotFound, "Peserta tidak ditemukan.")
	}
	var body struct {
		Action string `json:"action"`
	}
	if rerr := decodeJSON(c, &body); rerr != nil {
		return fail(c, rerr.Status, rerr.Code, rerr.Msg)
	}
	ctx := c.Request().Context()
	quiz, rerr, err := quizByID(ctx, g.DB, quizID)
	if err != nil {
		return err
	}
	if rerr != nil {
		return fail(c, rerr.Status, rerr.Code, rerr.Msg)
	}

	switch body.Action {
	case "approve":
		return g.approvePending(ctx, c, quizID, pid)
	case "reject":
		return g.rejectPending(ctx, c, quizID, pid)
	case "remove":
		return g.removeParticipant(ctx, c, quiz, pid)
	case "cheat_toggle":
		return g.cheatToggle(ctx, c, quiz, pid)
	default:
		return fail(c, http.StatusBadRequest, ErrValidation, "Aksi tidak dikenal.")
	}
}

// existsParticipant distinguishes a 404 (no such row) from a state 409.
func (g *Global) existsParticipant(ctx context.Context, quizID, pid uint64) (bool, error) {
	var exists bool
	err := g.DB.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM participants WHERE id = ? AND quiz_id = ?)`,
		pid, quizID).Scan(&exists)
	return exists, err
}

const (
	msgApproveGone    = "Permintaan ini tidak dapat disetujui lagi."
	msgNotPending     = "Permintaan ini tidak lagi tertunda."
	msgAlreadyRemoved = "Murid ini sudah dikeluarkan."
	msgNotRunning     = "Kuis ini tidak sedang berjalan."
)

// approvePending flips pending → registered, but only while the quiz is
// still aktif — the same UPDATE guards both conditions, so an approve that
// races START loses cleanly (0 rows → 409, spec §6.12).
func (g *Global) approvePending(ctx context.Context, c *echo.Context, quizID, pid uint64) error {
	res, err := g.DB.ExecContext(ctx, `UPDATE participants p, quizzes q
		SET p.status = 'registered'
		WHERE p.id = ? AND p.quiz_id = ? AND q.id = p.quiz_id
		  AND p.status = 'pending' AND q.status = 'aktif'`, pid, quizID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		exists, err := g.existsParticipant(ctx, quizID, pid)
		if err != nil {
			return err
		}
		if !exists {
			return fail(c, http.StatusNotFound, ErrNotFound, "Peserta tidak ditemukan.")
		}
		return fail(c, http.StatusConflict, ErrConflict, msgApproveGone)
	}
	g.afterRosterChange(ctx, quizID)
	return ok(c, map[string]string{"status": "registered"})
}

// rejectPending flips pending → dikeluarkan (final_score stays NULL).
func (g *Global) rejectPending(ctx context.Context, c *echo.Context, quizID, pid uint64) error {
	res, err := g.DB.ExecContext(ctx,
		`UPDATE participants SET status = 'dikeluarkan', final_score = NULL
		 WHERE id = ? AND quiz_id = ? AND status = 'pending'`, pid, quizID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		exists, err := g.existsParticipant(ctx, quizID, pid)
		if err != nil {
			return err
		}
		if !exists {
			return fail(c, http.StatusNotFound, ErrNotFound, "Peserta tidak ditemukan.")
		}
		return fail(c, http.StatusConflict, ErrConflict, msgNotPending)
	}
	g.afterRosterChange(ctx, quizID)
	return ok(c, map[string]string{"status": "dikeluarkan"})
}

// removeParticipant removes a student: waiting-room rows are dropped
// outright (final_score NULL, out of ranking); a working student first gets
// their score snapshotted over the FULL question count (unanswered = wrong)
// and stays in the ranking with the removed flag (spec §6.10).
func (g *Global) removeParticipant(ctx context.Context, c *echo.Context, quiz quizDetail, pid uint64) error {
	tx, err := g.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	var status string
	var cheating bool
	var name string
	err = tx.QueryRowContext(ctx, `SELECT p.status, p.cheating, u.nama_lengkap
		FROM participants p JOIN users u ON u.id = p.user_id
		WHERE p.id = ? AND p.quiz_id = ? FOR UPDATE`, pid, quiz.ID).
		Scan(&status, &cheating, &name)
	if errors.Is(err, sql.ErrNoRows) {
		tx.Rollback()
		return fail(c, http.StatusNotFound, ErrNotFound, "Peserta tidak ditemukan.")
	}
	if err != nil {
		tx.Rollback()
		return err
	}
	if status == "dikeluarkan" {
		tx.Rollback()
		return fail(c, http.StatusConflict, ErrConflict, msgAlreadyRemoved)
	}

	var score float64
	var finalScore any
	working := status == "started"
	if working {
		total, _, hasEssay, err := quizScope(ctx, tx, quiz.ID)
		if err != nil {
			tx.Rollback()
			return err
		}
		score, finalScore, err = attemptScore(ctx, tx, pid, quiz.ID, total, hasEssay)
		if err != nil {
			tx.Rollback()
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE participants SET status = 'dikeluarkan', finished_at = NOW(),
			 score_auto = ?, final_score = ? WHERE id = ? AND status = 'started'`,
			score, finalScore, pid); err != nil {
			tx.Rollback()
			return err
		}
	} else {
		if _, err := tx.ExecContext(ctx,
			`UPDATE participants SET status = 'dikeluarkan', final_score = NULL
			 WHERE id = ? AND quiz_id = ? AND status IN ('pending','registered')`,
			pid, quiz.ID); err != nil {
			tx.Rollback()
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	if working {
		g.Live.Ranker(quiz.ID).Upsert(quizengine.Entry{
			ParticipantID: pid,
			Name:          name,
			Score:         score,
			Finished:      false,
			Removed:       true,
			Cheating:      cheating,
		})
		publishRankTo(g.Hub, g.Live, quiz.ID, quiz.RankingLive)
	}
	g.afterRosterChange(ctx, quiz.ID)
	return ok(c, map[string]string{"status": "dikeluarkan"})
}

// cheatToggle flips participants.cheating (idempotent by construction —
// every call is exactly one transition, spec §6.9).
func (g *Global) cheatToggle(ctx context.Context, c *echo.Context, quiz quizDetail, pid uint64) error {
	res, err := g.DB.ExecContext(ctx,
		`UPDATE participants SET cheating = NOT cheating
		 WHERE id = ? AND quiz_id = ?`, pid, quiz.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		exists, err := g.existsParticipant(ctx, quiz.ID, pid)
		if err != nil {
			return err
		}
		if !exists {
			return fail(c, http.StatusNotFound, ErrNotFound, "Peserta tidak ditemukan.")
		}
	}
	var cheating bool
	if err := g.DB.QueryRowContext(ctx,
		`SELECT cheating FROM participants WHERE id = ?`, pid).Scan(&cheating); err != nil {
		return err
	}
	// keep the ranking flag in step if this student already has an entry
	ranker := g.Live.Ranker(quiz.ID)
	for _, e := range ranker.Snapshot() {
		if e.ParticipantID == pid {
			e.Cheating = cheating
			ranker.Upsert(e)
			break
		}
	}
	publishRankTo(g.Hub, g.Live, quiz.ID, quiz.RankingLive)
	g.afterRosterChange(ctx, quiz.ID)
	// The student topic fans out to every murid of this quiz (spec §6.3 —
	// there is no per-participant channel), so the frame carries the target
	// participant_id plus the new flag state: the flagged workspace reacts
	// in realtime, everyone else's client can tell it is not about them.
	if cheating {
		g.Hub.Publish(realtime.TopicForQuizID(quiz.ID), realtime.Event{
			Type: "flagged",
			Data: map[string]any{"participant_id": pid, "cheating": true},
		})
	}
	return ok(c, map[string]any{"cheating": cheating})
}

// --- START ------------------------------------------------------------------

// Start is POST /teacher/quiz/:id/start (spec §6.6): one guarded flip
// aktif → berjalan, then every registered attempt gets its qorder (written
// once, here), the shared global clock and status 'started'; rows still
// pending are auto-rejected in the same transaction. Zero participants is
// legal (§8). On success it publishes `start` to both topics.
func (g *Global) Start(c *echo.Context) error {
	id, rerr := monitorQuizID(c)
	if rerr != nil {
		return fail(c, rerr.Status, rerr.Code, rerr.Msg)
	}
	ctx := c.Request().Context()
	quiz, rerr, err := quizByID(ctx, g.DB, id)
	if err != nil {
		return err
	}
	if rerr != nil {
		return fail(c, rerr.Status, rerr.Code, rerr.Msg)
	}
	if quiz.TimerType != "global" {
		// per-question and no-timer quizzes never enter berjalan: each
		// student starts in their own workspace and the teacher closes
		// with POST .../status
		msg := "Kuis ini memakai timer per pertanyaan."
		if quiz.TimerType == "tanpa_timer" {
			msg = "Kuis ini tanpa timer."
		}
		return fail(c, http.StatusConflict, ErrConflict, msg)
	}
	questions, err := loadQuestions(ctx, g.DB, quiz.ID)
	if err != nil {
		return err
	}
	if len(questions) == 0 {
		return fail(c, http.StatusBadRequest, ErrValidation, "Tambahkan setidaknya satu pertanyaan.")
	}

	tx, err := g.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE quizzes SET status = 'berjalan', started_at = NOW()
		 WHERE id = ? AND status = 'aktif'`, quiz.ID)
	if err != nil {
		tx.Rollback()
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		tx.Rollback()
		var status string
		_ = g.DB.QueryRowContext(ctx, `SELECT status FROM quizzes WHERE id = ?`, quiz.ID).Scan(&status)
		switch status {
		case "berjalan":
			return fail(c, http.StatusConflict, ErrConflict, "Kuis sudah dimulai.")
		case "selesai":
			return fail(c, http.StatusConflict, ErrConflict, "Kuis ini sudah berakhir.")
		default:
			return fail(c, http.StatusConflict, ErrConflict, "Aktifkan kuis sebelum memulainya.")
		}
	}
	var startedAt time.Time
	if err := tx.QueryRowContext(ctx,
		`SELECT started_at FROM quizzes WHERE id = ?`, quiz.ID).Scan(&startedAt); err != nil {
		tx.Rollback()
		return err
	}

	// every registered attempt: qorder once, shared clock, started
	var endsAt any
	if quiz.TimerOn {
		endsAt = startedAt.Add(time.Duration(quiz.TotalSeconds) * time.Second).
			UTC().Format("2006-01-02 15:04:05")
	}
	regRows, err := tx.QueryContext(ctx,
		`SELECT p.id, p.user_id, p.attempt_no, u.nama_lengkap
		 FROM participants p JOIN users u ON u.id = p.user_id
		 WHERE p.quiz_id = ? AND p.status = 'registered'`, quiz.ID)
	if err != nil {
		tx.Rollback()
		return err
	}
	type reg struct {
		pid, userID uint64
		attempt     uint8
		name        string
	}
	var registered []reg
	for regRows.Next() {
		var r reg
		if err := regRows.Scan(&r.pid, &r.userID, &r.attempt, &r.name); err != nil {
			regRows.Close()
			tx.Rollback()
			return err
		}
		registered = append(registered, r)
	}
	regRows.Close()
	if err := regRows.Err(); err != nil {
		tx.Rollback()
		return err
	}
	for _, r := range registered {
		orderJSON, err := makeQOrder(r.pid, quiz.ID, r.attempt, questions,
			quiz.ShuffleQuestions, quiz.ShuffleOptions)
		if err != nil {
			tx.Rollback()
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE participants SET qorder = ?, status = 'started', started_at = NOW(),
			 current_q = 1, current_q_since = NOW(), ends_at = ?
			 WHERE id = ? AND status = 'registered'`,
			orderJSON, endsAt, r.pid); err != nil {
			tx.Rollback()
			return err
		}
	}
	// settle pre-START stragglers (spec §6.12 sync)
	if _, err := tx.ExecContext(ctx,
		`UPDATE participants SET status = 'dikeluarkan', final_score = NULL
		 WHERE quiz_id = ? AND status = 'pending'`, quiz.ID); err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	// status changed → home list mirror out of date
	g.Store.Delete(cache.QuizListKey())

	// every attempt just flipped registered → started: put them in the
	// live ranking at score 0 so all started murid are visible before
	// their first answer (spec §6.7.3). Registered-but-never-started
	// rows were not flipped and stay out of the ranker (spec §8).
	for _, r := range registered {
		g.Live.Ranker(quiz.ID).Upsert(quizengine.Entry{
			ParticipantID: r.pid,
			Name:          r.name,
			Score:         0,
		})
	}

	var endsUnix int64
	if quiz.TimerOn {
		endsUnix = startedAt.Add(time.Duration(quiz.TotalSeconds) * time.Second).Unix()
	}
	payload := realtime.Event{Type: "start", Data: map[string]any{
		"started_at":    startedAt.Unix(),
		"total_seconds": quiz.TotalSeconds,
		"ends_at":       endsUnix,
	}}
	g.Hub.Publish(realtime.TeacherTopicForQuizID(quiz.ID), payload)
	g.Hub.Publish(realtime.TopicForQuizID(quiz.ID), payload)
	publishRankTo(g.Hub, g.Live, quiz.ID, quiz.RankingLive)
	return ok(c, map[string]any{
		"status":        "berjalan",
		"started_at":    startedAt.Unix(),
		"total_seconds": quiz.TotalSeconds,
		"ends_at":       endsUnix,
	})
}

// --- STOP + the shared close routine ---------------------------------------

// Stop is POST /teacher/quiz/:id/stop; {confirm:true} is mandatory so a
// stray click can never end a quiz (spec §6.6).
func (g *Global) Stop(c *echo.Context) error {
	id, rerr := monitorQuizID(c)
	if rerr != nil {
		return fail(c, rerr.Status, rerr.Code, rerr.Msg)
	}
	var body struct {
		Confirm bool `json:"confirm"`
	}
	if rerr := decodeJSON(c, &body); rerr != nil {
		return fail(c, rerr.Status, rerr.Code, rerr.Msg)
	}
	if !body.Confirm {
		return fail(c, http.StatusBadRequest, ErrValidation, "Konfirmasi wajib diisi.")
	}
	ctx := c.Request().Context()
	if _, rerr, err := quizByID(ctx, g.DB, id); err != nil {
		return err
	} else if rerr != nil {
		return fail(c, rerr.Status, rerr.Code, rerr.Msg)
	}
	// the modal confirmation (sent after all_finished_pending) is worded
	// differently on the wire than a plain early stop
	reason := "stop"
	g.mu.Lock()
	if g.allFinished[id] {
		reason = "all_finished"
	}
	g.mu.Unlock()
	closed, err := g.closeQuiz(ctx, id, reason)
	if err != nil {
		return err
	}
	if !closed {
		return fail(c, http.StatusConflict, ErrConflict, msgNotRunning)
	}
	return ok(c, map[string]string{"status": "selesai"})
}

// quizScope reads the quiz-level grading inputs: full question count,
// ranking_live and the essay presence that keeps final_score NULL.
func quizScope(ctx context.Context, q qRow, quizID uint64) (total int, rankingLive, hasEssay bool, err error) {
	if err = q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM quiz_questions WHERE quiz_id = ?`, quizID).Scan(&total); err != nil {
		return
	}
	var ranking int
	if err = q.QueryRowContext(ctx,
		`SELECT ranking_live FROM quizzes WHERE id = ?`, quizID).Scan(&ranking); err != nil {
		return
	}
	hasEssay, err = quizHasEssay(ctx, q, quizID)
	rankingLive = ranking != 0
	return
}

// attemptScore grades one participant against the FULL question count —
// unanswered = wrong — and returns (score_auto, final_score); final is nil
// while essays are pending (spec §6.7.2).
func attemptScore(ctx context.Context, q qRow, pid, quizID uint64, total int, hasEssay bool) (float64, any, error) {
	var correct int
	if err := q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM answers WHERE participant_id = ? AND is_correct = 1`,
		pid).Scan(&correct); err != nil {
		return 0, nil, err
	}
	auto := quizengine.CentiPercent(correct, total)
	return auto, quizengine.FinalScore(&auto, nil, hasEssay), nil
}

// closeQuiz is THE end-trigger for a running global quiz — STOP, the
// timeout watcher and the all-finished confirmation all land here (spec
// §6.6). Inside one transaction: every working attempt is scored over the
// full question count, then `UPDATE quizzes ... WHERE status='berjalan'` —
// its 0-rows guard means another closer won, so this one rolls everything
// back and reports closed=false without publishing. On success force_stop
// {reason} goes to both topics.
func (g *Global) closeQuiz(ctx context.Context, quizID uint64, reason string) (bool, error) {
	total, rankingLive, hasEssay, err := quizScope(ctx, g.DB, quizID)
	if err != nil {
		return false, err
	}

	type worker struct {
		pid      uint64
		name     string
		cheating bool
		score    float64
	}
	tx, err := g.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	rows, err := tx.QueryContext(ctx,
		`SELECT p.id, u.nama_lengkap, p.cheating
		 FROM participants p JOIN users u ON u.id = p.user_id
		 WHERE p.quiz_id = ? AND p.status = 'started' FOR UPDATE`, quizID)
	if err != nil {
		tx.Rollback()
		return false, err
	}
	var workers []worker
	graded := []worker{}
	for rows.Next() {
		var w worker
		if err := rows.Scan(&w.pid, &w.name, &w.cheating); err != nil {
			rows.Close()
			tx.Rollback()
			return false, err
		}
		workers = append(workers, w)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		tx.Rollback()
		return false, err
	}

	for _, w := range workers {
		score, final, err := attemptScore(ctx, tx, w.pid, quizID, total, hasEssay)
		if err != nil {
			tx.Rollback()
			return false, err
		}
		w.score = score
		if _, err := tx.ExecContext(ctx,
			`UPDATE participants SET status = 'selesai', finished_at = NOW(),
			 score_auto = ?, final_score = ? WHERE id = ? AND status = 'started'`,
			score, final, w.pid); err != nil {
			tx.Rollback()
			return false, err
		}
		graded = append(graded, w)
	}
	// registered/pending never touched a question: close as "not attempted"
	// (final_score NULL, excluded from the ranker — only started rows enter)
	if _, err := tx.ExecContext(ctx,
		`UPDATE participants SET status = 'selesai', final_score = NULL
		 WHERE quiz_id = ? AND status IN ('registered', 'pending')`, quizID); err != nil {
		tx.Rollback()
		return false, err
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE quizzes SET status = 'selesai'
		 WHERE id = ? AND status IN ('aktif', 'berjalan')`, quizID)
	if err != nil {
		tx.Rollback()
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		tx.Rollback() // someone else already closed it — undo our copies
		return false, nil
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}

	g.Store.Delete(cache.QuizListKey())
	for _, w := range graded {
		g.Live.Ranker(quizID).Upsert(quizengine.Entry{
			ParticipantID: w.pid,
			Name:          w.name,
			Score:         w.score,
			Finished:      true,
			Cheating:      w.cheating,
		})
	}
	publishRankTo(g.Hub, g.Live, quizID, rankingLive)
	payload := realtime.Event{Type: "force_stop", Data: map[string]any{"reason": reason}}
	g.Hub.Publish(realtime.TopicForQuizID(quizID), payload)
	g.Hub.Publish(realtime.TeacherTopicForQuizID(quizID), payload)

	g.mu.Lock()
	delete(g.allFinished, quizID)
	g.mu.Unlock()
	return true, nil
}

// --- watchdog ---------------------------------------------------------------

// WatchOnce is one 1 s watchdog tick (main loops it): close timed-out
// quizzes, freeze disconnected clocks, and announce all_finished_pending
// once per quiz while every working student has already finished.
func (g *Global) WatchOnce(ctx context.Context) error {
	// 1) global timeout needs no confirmation (spec §6.6)
	overdue, err := g.DB.QueryContext(ctx, `SELECT id FROM quizzes
		WHERE status = 'berjalan' AND timer_on = 1 AND started_at IS NOT NULL
		  AND NOW() >= DATE_ADD(started_at, INTERVAL total_seconds SECOND)`)
	if err != nil {
		return err
	}
	var ids []uint64
	for overdue.Next() {
		var id uint64
		if err := overdue.Scan(&id); err != nil {
			overdue.Close()
			return err
		}
		ids = append(ids, id)
	}
	overdue.Close()
	if err := overdue.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := g.closeQuiz(ctx, id, "timeout"); err != nil {
			return err
		}
	}

	// 2) disconnect freeze at the last heartbeat (spec §11.16)
	g.Live.SweepDisconnects(ctx, time.Now(), g.disconnectThreshold())

	// 3) everyone who ever started has finished → ask the teacher once
	return g.probeAllFinished(ctx)
}

// SetDisconnectSilence overrides the watchdog's disconnect threshold
// (tests shrink it instead of sleeping 15 s — same seam as
// Hub.SetHeartbeatEvery).
func (g *Global) SetDisconnectSilence(d time.Duration) {
	g.silence.Store(int64(d))
}

// disconnectThreshold is the configured silence window (default 15 s).
func (g *Global) disconnectThreshold() time.Duration {
	if n := g.silence.Load(); n > 0 {
		return time.Duration(n)
	}
	return disconnectSilence
}

// probeAllFinished publishes all_finished_pending the first time a running
// quiz's started population is entirely finished (deduped until close).
func (g *Global) probeAllFinished(ctx context.Context) error {
	rows, err := g.DB.QueryContext(ctx, `SELECT q.id
		FROM quizzes q
		WHERE q.status = 'berjalan'
		  AND EXISTS (SELECT 1 FROM participants p
		              WHERE p.quiz_id = q.id AND p.started_at IS NOT NULL)`)
	if err != nil {
		return err
	}
	var candidates []uint64
	for rows.Next() {
		var id uint64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		candidates = append(candidates, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range candidates {
		var n, finished int
		if err := g.DB.QueryRowContext(ctx,
			`SELECT COUNT(*), COALESCE(SUM(finished_at IS NOT NULL), 0)
			 FROM participants WHERE quiz_id = ? AND started_at IS NOT NULL`, id).
			Scan(&n, &finished); err != nil {
			return err
		}
		if n == 0 || n != finished {
			continue
		}
		g.mu.Lock()
		already := g.allFinished[id]
		g.allFinished[id] = true
		g.mu.Unlock()
		if !already {
			g.Hub.Publish(realtime.TeacherTopicForQuizID(id), realtime.Event{
				Type: "all_finished_pending",
				Data: map[string]any{"quiz_id": id},
			})
		}
	}
	return nil
}

// WatchLoop runs WatchOnce every second until ctx is cancelled.
func (g *Global) WatchLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := g.WatchOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
				// one bad tick must not kill the watchdog
				continue
			}
		}
	}
}

// --- boot rehydrate ---------------------------------------------------------

// Rehydrate runs once before the server accepts traffic (spec §6.3):
// attempts whose personal clock expired while the process was down are
// finished from their stored answers (unanswered = wrong), and every ranker
// is rebuilt from the answers table so the first monitor render is already
// correct. Read-through mirrors (≤ 10 s TTL) warm on first read instead of
// being preloaded here.
func (g *Global) Rehydrate(ctx context.Context) error {
	if err := g.finishExpired(ctx); err != nil {
		return err
	}
	return g.rebuildRankers(ctx)
}

// finishExpired closes started attempts with ends_at already in the past —
// one transaction per quiz so a restart never halves a closing batch.
func (g *Global) finishExpired(ctx context.Context) error {
	rows, err := g.DB.QueryContext(ctx,
		`SELECT p.id, p.quiz_id FROM participants p
		 WHERE p.status = 'started' AND p.ends_at IS NOT NULL AND p.ends_at <= NOW()`)
	if err != nil {
		return err
	}
	type exp struct{ pid, quizID uint64 }
	var expired []exp
	for rows.Next() {
		var e exp
		if err := rows.Scan(&e.pid, &e.quizID); err != nil {
			rows.Close()
			return err
		}
		expired = append(expired, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	byQuiz := map[uint64][]uint64{}
	for _, e := range expired {
		byQuiz[e.quizID] = append(byQuiz[e.quizID], e.pid)
	}
	for quizID, pids := range byQuiz {
		total, _, hasEssay, err := quizScope(ctx, g.DB, quizID)
		if err != nil {
			return err
		}
		tx, err := g.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		for _, pid := range pids {
			score, final, err := attemptScore(ctx, tx, pid, quizID, total, hasEssay)
			if err != nil {
				tx.Rollback()
				return err
			}
			if _, err := tx.ExecContext(ctx,
				`UPDATE participants SET status = 'selesai', finished_at = NOW(),
				 score_auto = ?, final_score = ?
				 WHERE id = ? AND status = 'started' AND ends_at IS NOT NULL AND ends_at <= NOW()`,
				score, final, pid); err != nil {
				tx.Rollback()
				return err
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// rebuildRankers repopulates the live leaderboards from stored answers.
// Everyone who ever started is included (started, finished and removed
// while working); registered/pending rows never had a clock and stay out
// (spec §6.7.3, §9 ranking matrix).
func (g *Global) rebuildRankers(ctx context.Context) error {
	rows, err := g.DB.QueryContext(ctx,
		`SELECT p.id, p.quiz_id, u.nama_lengkap, p.status, p.cheating,
		        (SELECT COUNT(*) FROM answers a
		         WHERE a.participant_id = p.id AND a.is_correct = 1) AS correct
		 FROM participants p JOIN users u ON u.id = p.user_id
		 WHERE p.started_at IS NOT NULL`)
	if err != nil {
		return err
	}
	type line struct {
		pid, quizID  uint64
		name, status string
		cheating     bool
		correct      int
	}
	var lines []line
	for rows.Next() {
		var l line
		if err := rows.Scan(&l.pid, &l.quizID, &l.name, &l.status, &l.cheating, &l.correct); err != nil {
			rows.Close()
			return err
		}
		lines = append(lines, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	totals := map[uint64]int{}
	for _, l := range lines {
		total, ok := totals[l.quizID]
		if !ok {
			if err := g.DB.QueryRowContext(ctx,
				`SELECT COUNT(*) FROM quiz_questions WHERE quiz_id = ?`, l.quizID).
				Scan(&total); err != nil {
				return err
			}
			totals[l.quizID] = total
		}
		score := 0.0
		if total > 0 {
			score = quizengine.CentiPercent(l.correct, total)
		}
		g.Live.Ranker(l.quizID).Upsert(quizengine.Entry{
			ParticipantID: l.pid,
			Name:          l.name,
			Score:         score,
			Finished:      l.status == "selesai",
			Removed:       l.status == "dikeluarkan",
			Cheating:      l.cheating,
		})
	}
	return nil
}

// publishRankTo pushes the live ranking to both topics when the setting is
// on (spec §6.7.3). Best-effort: the DB already holds the truth.
func publishRankTo(hub *realtime.Hub, live *Live, quizID uint64, rankingLive bool) {
	if !rankingLive {
		return
	}
	payload := realtime.Event{
		Type: "rank",
		Data: map[string]any{"ranking": live.Ranker(quizID).Snapshot()},
	}
	hub.Publish(realtime.TopicForQuizID(quizID), payload)
	hub.Publish(realtime.TeacherTopicForQuizID(quizID), payload)
}
