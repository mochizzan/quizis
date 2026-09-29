package handlers

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/labstack/echo/v5"

	"quiz/internal/analytics"
)

// --- GET /teacher/api/overview ---------------------------------------------
// Analytics overview JSON for the guru dashboard. All three params are
// optional (""/absent = no filter); malformed values → 400 VALIDATION,
// numeric-but-unknown ids just match nothing. The response is dynamic
// data: the StaticCache middleware sends Cache-Control: no-store for this
// path (it is not under a static mount).

// Overview serves the filtered overview payload.
func (t *Teacher) Overview(c *echo.Context) error {
	f, err := analytics.ParseFilter(
		c.QueryParam("kelas"), c.QueryParam("jurusan"), c.QueryParam("status"))
	if err != nil {
		return fail(c, http.StatusBadRequest, ErrValidation, "Invalid filter value.")
	}
	ov, err := t.buildOverview(c.Request().Context(), f)
	if err != nil {
		return err
	}
	return ok(c, ov)
}

// buildOverview loads the raw rows and hands them to the pure builder.
// Shared by the API endpoint and the dashboard's embedded default payload.
// No cache mirror: every number is live (the middleware policy is
// no-store, never a hard cache).
func (t *Teacher) buildOverview(ctx context.Context, f analytics.Filter) (analytics.Overview, error) {
	kelas, err := overviewRefs(ctx, t.DB, "ref_kelas")
	if err != nil {
		return analytics.Overview{}, err
	}
	jurusan, err := overviewRefs(ctx, t.DB, "ref_jurusan")
	if err != nil {
		return analytics.Overview{}, err
	}
	users, err := overviewUsers(ctx, t.DB)
	if err != nil {
		return analytics.Overview{}, err
	}
	quizzes, err := overviewQuizzes(ctx, t.DB)
	if err != nil {
		return analytics.Overview{}, err
	}
	parts, err := overviewParts(ctx, t.DB)
	if err != nil {
		return analytics.Overview{}, err
	}
	ov := analytics.Build(analytics.Input{
		Filter: f, Kelas: kelas, Jurusan: jurusan,
		Users: users, Quizzes: quizzes, Parts: parts,
	})
	applyQuizChips(&ov)
	return ov, nil
}

// applyQuizChips fills the presentation fields analytics.Build leaves
// zero — presentation stays in handlers (quizChip is the single mapping).
func applyQuizChips(ov *analytics.Overview) {
	for i := range ov.Quizzes {
		ov.Quizzes[i].Label, ov.Quizzes[i].Chip = quizChip(ov.Quizzes[i].Status)
	}
}

// overviewRefs loads one dropdown table in id order (the charts emit
// every ref row, zero included).
func overviewRefs(ctx context.Context, db *sql.DB, table string) ([]analytics.Ref, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, nama FROM `+table+` ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []analytics.Ref{}
	for rows.Next() {
		var r analytics.Ref
		if err := rows.Scan(&r.ID, &r.Nama); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// overviewUsers loads the aggregate dimensions of every users row.
func overviewUsers(ctx context.Context, db *sql.DB) ([]analytics.UserRow, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT kelas_id, jurusan_id, aktif FROM users`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []analytics.UserRow{}
	for rows.Next() {
		var u analytics.UserRow
		if err := rows.Scan(&u.KelasID, &u.JurusanID, &u.Aktif); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// overviewQuizzes loads every quiz newest-first (created_at DESC, id DESC
// breaks same-second fixture ties deterministically).
func overviewQuizzes(ctx context.Context, db *sql.DB) ([]analytics.QuizRow, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, judul, code, status FROM quizzes
		 ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []analytics.QuizRow{}
	for rows.Next() {
		var q analytics.QuizRow
		if err := rows.Scan(&q.ID, &q.Judul, &q.Code, &q.Status); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// overviewParts loads finished/unfinished participants with the user's
// dimensions denormalized (status/score filtering happens in Build).
func overviewParts(ctx context.Context, db *sql.DB) ([]analytics.PartRow, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT p.quiz_id, u.kelas_id, u.jurusan_id, p.status, p.final_score
		 FROM participants p JOIN users u ON u.id = p.user_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []analytics.PartRow{}
	for rows.Next() {
		var p analytics.PartRow
		var score sql.NullFloat64
		if err := rows.Scan(&p.QuizID, &p.KelasID, &p.JurusanID, &p.Status, &score); err != nil {
			return nil, err
		}
		if score.Valid {
			v := score.Float64
			p.FinalScore = &v
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
