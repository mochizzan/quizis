package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v5"
)

// --- POST /teacher/quiz/:id/status -----------------------------------------

// SetQuizStatus is the ONLY status route (spec §7): activation (aktif /
// nonaktif, unchanged Step 8 semantics via activateQuiz) and the
// per-question close (selesai, spec §6.5). It lives with the per-question
// lifecycle because closing runs the same closeQuiz routine as STOP and the
// timeout watchdog.
//
// While students are still working, the unconfirmed close answers 409 with
// {"working":N} so the client can show the confirmation modal; the confirmed
// close (form or JSON {status:"selesai", confirm:true}) closes with
// reason="teacher_close".
func (g *Global) SetQuizStatus(c *echo.Context) error {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		return fail(c, http.StatusBadRequest, ErrValidation, "ID kuis tidak valid.")
	}
	want, confirm := c.FormValue("status"), false
	if want == "" {
		var body struct {
			Status  string `json:"status"`
			Confirm bool   `json:"confirm"`
		}
		if err := json.NewDecoder(c.Request().Body).Decode(&body); err != nil {
			return fail(c, http.StatusBadRequest, ErrValidation, "Status tidak valid.")
		}
		want, confirm = body.Status, body.Confirm
	}
	switch want {
	case "selesai":
		return g.closeWithModal(c, id, confirm)
	case "aktif", "nonaktif":
		return activateQuiz(c, g.DB, g.Store, id, want)
	default:
		return fail(c, http.StatusBadRequest, ErrValidation, "Status tidak valid.")
	}
}

// closeWithModal moves an active quiz to selesai (never back to nonaktif).
// Registered/pending rows close as "not attempted": selesai + final_score
// NULL, excluded from the ranker (only started rows ever enter it).
func (g *Global) closeWithModal(c *echo.Context, quizID uint64, confirm bool) error {
	ctx := c.Request().Context()
	var working int
	if err := g.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM participants WHERE quiz_id = ? AND status = 'started'`,
		quizID).Scan(&working); err != nil {
		return err
	}
	if working > 0 && !confirm {
		return failData(c, http.StatusConflict, ErrConflict,
			fmt.Sprintf("%d murid masih mengerjakan — tetap tutup?", working),
			map[string]any{"working": working})
	}
	closed, err := g.closeQuiz(ctx, quizID, "teacher_close")
	if err != nil {
		return err
	}
	if !closed {
		var exists bool
		if err := g.DB.QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM quizzes WHERE id = ?)`, quizID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return fail(c, http.StatusNotFound, ErrNotFound, "Kuis tidak ditemukan.")
		}
		return fail(c, http.StatusConflict, ErrConflict, "Kuis ini tidak sedang berjalan.")
	}
	return ok(c, map[string]any{"status": "selesai"})
}
