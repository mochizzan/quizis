package handlers

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v5"

	mw "quiz/internal/middleware"
	"quiz/internal/realtime"
)

// Streams mounts the two SSE endpoints (spec §6.3): the per-quiz student
// stream and the teacher monitor stream. Auth is checked before the upgrade;
// after it, events flow through the hub. Each connection opens with one
// per-client snapshot (never fanned out) and reports its participant's
// connect/disconnect to the heartbeat registry behind the freeze.
type Streams struct {
	DB   *sql.DB
	Hub  *realtime.Hub
	Live *Live
}

// StudentStream is GET /quiz/:code/stream: require a murid session FIRST
// (an anonymous probe must not learn whether a code exists — 401 either
// way), then resolve the code, then require an existing non-removed
// membership, then subscribe to the student topic.
func (s *Streams) StudentStream(c *echo.Context) error {
	sess := mw.SessionFrom(c)
	if sess == nil || sess.Role != "murid" {
		return fail(c, http.StatusUnauthorized, ErrUnauthenticated, msgSignInStream)
	}
	code := c.Param("code")
	var quizID uint64
	err := s.DB.QueryRowContext(c.Request().Context(),
		`SELECT id FROM quizzes WHERE code = ?`, code).Scan(&quizID)
	if errors.Is(err, sql.ErrNoRows) {
		return fail(c, http.StatusNotFound, ErrInvalidCode, MsgInvalidCode)
	}
	if err != nil {
		return err
	}
	var member bool
	if err := s.DB.QueryRowContext(c.Request().Context(),
		`SELECT EXISTS(SELECT 1 FROM participants
		  WHERE quiz_id = ? AND user_id = ? AND status <> 'dikeluarkan')`,
		quizID, sess.UserID).Scan(&member); err != nil {
		return err
	}
	if !member {
		return fail(c, http.StatusForbidden, ErrForbidden,
			"You are not a participant of this quiz.")
	}
	// the latest attempt owns this connection (join never creates a row
	// after a removed one, so it is the one the membership check saw)
	var pid uint64
	if err := s.DB.QueryRowContext(c.Request().Context(),
		`SELECT id FROM participants WHERE quiz_id = ? AND user_id = ?
		 ORDER BY attempt_no DESC LIMIT 1`, quizID, sess.UserID).Scan(&pid); err != nil {
		return err
	}
	ctx := c.Request().Context()
	return s.Hub.Handler(
		realtime.TopicForQuizID(quizID), requireSession,
		realtime.WithLifecycle(
			func() {
				s.Live.Connect(pid)
				s.announcePresence(quizID, pid, sess.UserID)
			},
			func(lastSent time.Time) {
				s.Live.Disconnect(pid, lastSent)
				s.announcePresence(quizID, pid, sess.UserID)
			},
		),
		realtime.WithGreeting(func() *realtime.Event {
			return studentSnapshot(ctx, s.DB, s.Live, code, sess.UserID)
		}),
	)(c)
}

// announcePresence republishes this participant's card state to the monitor
// topic whenever their stream (re)opens or closes — the refresh /
// leave-and-return path behind the live monitor (spec §6.3, §7). Workspace
// renders only announce CHANGED pages, so a reconnect that keeps the same
// page was invisible: the teacher never saw the murid leave or come back.
// A submitted attempt is skipped — its `finished` event already took the
// card off the monitor and it must not come back. onLeave runs after the
// request is gone, so the reads use the background context (same seam the
// freeze resume in Live.Connect uses).
func (s *Streams) announcePresence(quizID, pid, userID uint64) {
	ctx := context.Background()
	part, err := scanParticipant(func(dest ...any) error {
		return s.DB.QueryRowContext(ctx,
			`SELECT `+attemptCols+` FROM participants WHERE id = ?`, pid).Scan(dest...)
	})
	if err != nil || part.Status == "selesai" {
		return // gone, or already submitted — no card to restore
	}
	name, _ := participantName(ctx, s.DB, userID)
	s.Hub.Publish(realtime.TeacherTopicForQuizID(quizID),
		pageEvent(s.Live, part, name, s.Live.Page(pid)))
}

// TeacherMonitorStream is GET /teacher/quiz/:id/monitor/stream (the route
// carries AuthTeacher): subscribe to the quiz's teacher topic.
func (s *Streams) TeacherMonitorStream(c *echo.Context) error {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		return fail(c, http.StatusNotFound, ErrNotFound, "Quiz not found.")
	}
	var exists bool
	if err := s.DB.QueryRowContext(c.Request().Context(),
		`SELECT EXISTS(SELECT 1 FROM quizzes WHERE id = ?)`, id).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return fail(c, http.StatusNotFound, ErrNotFound, "Quiz not found.")
	}
	ctx := c.Request().Context()
	return s.Hub.Handler(
		realtime.TeacherTopicForQuizID(id), requireSession,
		realtime.WithGreeting(func() *realtime.Event {
			return monitorSnapshot(ctx, s.DB, s.Live, id)
		}),
	)(c)
}

const msgSignInStream = "Sign in to open this stream."

// requireSession is the SSE auth gate: the session cookie must still resolve.
// Re-checked after the handler pre-checks so a revoked session cannot open
// a stream between the two.
func requireSession(c *echo.Context) error {
	if mw.SessionFrom(c) == nil {
		return fail(c, http.StatusUnauthorized, ErrUnauthenticated, msgSignInStream)
	}
	return nil
}
