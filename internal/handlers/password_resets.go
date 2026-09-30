package handlers

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v5"
)

type resetRow struct {
	ID            uint64
	InputUsername string
	InputNama     string
	CreatedAt     time.Time
}

// --- GET /teacher/password-resets ----------------------------------------
// Lists pending requests, searchable and sortable: ?q= (username, name) +
// ?sort=lama (oldest first) + ?page= combine (see list.go); under the
// /teacher group + AuthTeacher.
func (a *Auth) PasswordResetsPage(c *echo.Context) error {
	rows, err := a.DB.QueryContext(c.Request().Context(),
		`SELECT id, input_username, input_nama, created_at
		 FROM password_resets WHERE status = 'pending' ORDER BY created_at, id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var list []resetRow
	for rows.Next() {
		var r resetRow
		if err := rows.Scan(&r.ID, &r.InputUsername, &r.InputNama, &r.CreatedAt); err != nil {
			return err
		}
		list = append(list, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	q := listQ(c)
	sortBy := strings.TrimSpace(c.QueryParam("sort"))
	filtered := make([]resetRow, 0, len(list))
	for _, r := range list {
		if !matchSearch(q, r.InputUsername, r.InputNama) {
			continue
		}
		filtered = append(filtered, r)
	}
	if sortBy == "lama" {
		// Terlama: reverse into a fresh slice — the fetched order is shared.
		oldest := make([]resetRow, len(filtered))
		for i, r := range filtered {
			oldest[len(filtered)-1-i] = r
		}
		filtered = oldest
	}
	page := listPage(c)
	out, page, _ := paginate(filtered, page)
	tb := newTable(c, q, len(filtered), page)
	tb.Placeholder = "Cari username atau nama…"
	return c.Render(http.StatusOK, "page-password-resets", map[string]any{
		"Title": "Permintaan ganti kata sandi", "Rows": out,
		"Table": tb, "Sort": sortBy,
	})
}

// --- POST /teacher/password-resets/:id/approve ----------------------------
// Idempotent: only a pending row flips to disetujui (0 rows → 409), and the
// user's must_change_pw=1 is set in the same transaction.
func (a *Auth) ApproveReset(c *echo.Context) error {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		return fail(c, http.StatusBadRequest, ErrValidation, "ID permintaan tidak valid.")
	}
	ctx := c.Request().Context()

	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE password_resets SET status = 'disetujui' WHERE id = ? AND status = 'pending'`, id)
	if err != nil {
		tx.Rollback()
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		tx.Rollback()
		return fail(c, http.StatusConflict, ErrConflict, "Permintaan sudah diproses.")
	}
	var userID uint64
	if err := tx.QueryRowContext(ctx, `SELECT user_id FROM password_resets WHERE id = ?`, id).
		Scan(&userID); err != nil {
		tx.Rollback()
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE users SET must_change_pw = 1 WHERE id = ?`, userID); err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return ok(c, nil)
}

// --- POST /teacher/password-resets/:id/reject -----------------------------
// pending → ditolak; 0 rows → 409 (double-click safe, one-way transitions).
func (a *Auth) RejectReset(c *echo.Context) error {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		return fail(c, http.StatusBadRequest, ErrValidation, "ID permintaan tidak valid.")
	}
	res, err := a.DB.ExecContext(c.Request().Context(),
		`UPDATE password_resets SET status = 'ditolak' WHERE id = ? AND status = 'pending'`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fail(c, http.StatusConflict, ErrConflict, "Permintaan sudah diproses.")
	}
	return ok(c, nil)
}

// pendingResetCount is the dashboard badge (spec §7 dashboard route).
func pendingResetCount(ctx context.Context, db *sql.DB) (int, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM password_resets WHERE status = 'pending'`).Scan(&n)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return n, err
}
