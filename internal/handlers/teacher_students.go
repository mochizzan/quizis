package handlers

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v5"

	"quiz/internal/cache"
	mw "quiz/internal/middleware"
)

// Manage Akun Murid (teacher dashboard): list, edit, deactivate/activate and
// delete of users rows — the CRUD behind /teacher/students. The schema has
// no foreign keys, so every integrity guard lives here: the class/major refs
// must exist, duplicates surface through the UNIQUE keys (1062 → 409), and a
// delete cascades to the student's attempts in one transaction.
//
// Session discipline: deactivation revokes every session (mirrors first,
// then rows — spec §6.2); deletion revokes BEFORE the user row disappears so
// no cookie can outlive its account.

// studentRow is one account-manager entry.
type studentRow struct {
	ID        uint64
	Username  string
	Email     string
	Nama      string
	KelasID   uint64
	JurusanID uint64
	Kelas     string
	Jurusan   string
	Aktif     bool
}

// StudentsPage renders the account manager, searchable and filterable:
// ?q= (username, email, full name) + ?status= (aktif|nonaktif) + ?page=
// combine (see list.go). The edit form lives on its own page
// (StudentEditPage) — the list is read-only plus row actions.
func (t *Teacher) StudentsPage(c *echo.Context) error {
	rows, err := t.studentList(c.Request().Context())
	if err != nil {
		return err
	}
	q := listQ(c)
	status := strings.TrimSpace(c.QueryParam("status"))
	filtered := make([]studentRow, 0, len(rows))
	for _, r := range rows {
		if status == "aktif" && !r.Aktif {
			continue
		}
		if status == "nonaktif" && r.Aktif {
			continue
		}
		if !matchSearch(q, r.Username, r.Email, r.Nama) {
			continue
		}
		filtered = append(filtered, r)
	}
	page := listPage(c)
	out, page, _ := paginate(filtered, page)
	tb := newTable(c, q, len(filtered), page)
	tb.Placeholder = "Cari username, email, atau nama…"
	return c.Render(http.StatusOK, "page-teacher-students", map[string]any{
		"Title": "Kelola akun murid", "Rows": out,
		"Table": tb, "Status": status,
	})
}

// studentList reads every account with its ref names (LEFT JOIN: a dangling
// ref renders empty instead of dropping the row).
func (t *Teacher) studentList(ctx context.Context) ([]studentRow, error) {
	rows, err := t.DB.QueryContext(ctx, `SELECT u.id, u.username, u.email, u.nama_lengkap,
		u.kelas_id, u.jurusan_id, k.nama, j.nama, u.aktif
		FROM users u
		LEFT JOIN ref_kelas k ON k.id = u.kelas_id
		LEFT JOIN ref_jurusan j ON j.id = u.jurusan_id
		ORDER BY u.nama_lengkap, u.username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []studentRow{}
	for rows.Next() {
		var r studentRow
		var kelas, jurusan sql.NullString
		if err := rows.Scan(&r.ID, &r.Username, &r.Email, &r.Nama,
			&r.KelasID, &r.JurusanID, &kelas, &jurusan, &r.Aktif); err != nil {
			return nil, err
		}
		r.Kelas, r.Jurusan = kelas.String, jurusan.String
		out = append(out, r)
	}
	return out, rows.Err()
}

// studentID parses the :id route parameter.
func studentID(c *echo.Context) (uint64, *respError) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		return 0, errResp(http.StatusBadRequest, ErrValidation, "ID tidak valid.")
	}
	return id, nil
}

// userExists reports whether the users row is present.
func userExists(ctx context.Context, db *sql.DB, id uint64) (bool, error) {
	var exists bool
	err := db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM users WHERE id = ?)`, id).Scan(&exists)
	return exists, err
}

// --- GET /teacher/students/:id/edit --------------------------------------

// StudentEditPage renders the standalone edit form for one account,
// prefilled server-side from the DB in one SELECT (the class/major refs ride
// the page for the selects). Unknown or malformed id → 404, same message as
// EditStudent. The form posts through teacher.js to the existing
// POST /teacher/students/:id/edit endpoint; on success teacher.js follows
// data-next back to the list with the stored success toast.
func (t *Teacher) StudentEditPage(c *echo.Context) error {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		return fail(c, http.StatusNotFound, ErrNotFound, "Murid tidak ditemukan.")
	}
	ctx := c.Request().Context()
	var u studentRow
	err = t.DB.QueryRowContext(ctx,
		`SELECT id, username, email, nama_lengkap, kelas_id, jurusan_id
		 FROM users WHERE id = ?`, id).
		Scan(&u.ID, &u.Username, &u.Email, &u.Nama, &u.KelasID, &u.JurusanID)
	if errors.Is(err, sql.ErrNoRows) {
		return fail(c, http.StatusNotFound, ErrNotFound, "Murid tidak ditemukan.")
	}
	if err != nil {
		return err
	}
	kelas, err := t.refsList(ctx, "kelas")
	if err != nil {
		return err
	}
	jurusan, err := t.refsList(ctx, "jurusan")
	if err != nil {
		return err
	}
	return c.Render(http.StatusOK, "page-teacher-student-edit", map[string]any{
		"Title": "Ubah akun murid", "U": u,
		"Kelas": kelas, "Jurusan": jurusan,
		"Crumbs": []Crumb{
			{Label: "Dasbor"},
			{Label: "Guru", URL: "/teacher"},
			{Label: "Kelola akun murid", URL: "/teacher/students"},
			{Label: "Ubah"},
		},
	})
}

// EditStudent updates one account: username, full name, email, class, major.
// Unknown id → 404; duplicate username/email → 409; a missing ref → 400.
func (t *Teacher) EditStudent(c *echo.Context) error {
	id, rerr := studentID(c)
	if rerr != nil {
		return fail(c, rerr.Status, rerr.Code, rerr.Msg)
	}
	ctx := c.Request().Context()
	username := strings.TrimSpace(c.FormValue("username"))
	nama := strings.TrimSpace(c.FormValue("nama_lengkap"))
	email := strings.TrimSpace(c.FormValue("email"))
	kelasStr := strings.TrimSpace(c.FormValue("kelas_id"))
	jurusanStr := strings.TrimSpace(c.FormValue("jurusan_id"))

	switch {
	case username == "" || nama == "" || email == "" || kelasStr == "" || jurusanStr == "":
		return fail(c, http.StatusBadRequest, ErrValidation, "Silakan isi semua kolom.")
	case len([]rune(username)) > 50:
		return fail(c, http.StatusBadRequest, ErrValidation,
			"Username terlalu panjang (maksimal 50 karakter).")
	case len([]rune(nama)) > 100:
		return fail(c, http.StatusBadRequest, ErrValidation,
			"Nama lengkap terlalu panjang (maksimal 100 karakter).")
	case len([]rune(email)) > 100:
		return fail(c, http.StatusBadRequest, ErrValidation,
			"Email terlalu panjang (maksimal 100 karakter).")
	}
	kelasID, err1 := strconv.ParseUint(kelasStr, 10, 16)
	jurusanID, err2 := strconv.ParseUint(jurusanStr, 10, 16)
	if err1 != nil || err2 != nil {
		return fail(c, http.StatusBadRequest, ErrValidation, "Kelas atau jurusan tidak dikenal.")
	}
	// no FKs → both refs must exist before the row can point at them
	for _, ref := range []struct {
		table string
		id    uint64
	}{{"ref_kelas", kelasID}, {"ref_jurusan", jurusanID}} {
		var exists bool
		if err := t.DB.QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM `+ref.table+` WHERE id = ?)`, ref.id).
			Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return fail(c, http.StatusBadRequest, ErrValidation, "Kelas atau jurusan tidak dikenal.")
		}
	}
	exists, err := userExists(ctx, t.DB, id)
	if err != nil {
		return err
	}
	if !exists {
		return fail(c, http.StatusNotFound, ErrNotFound, "Murid tidak ditemukan.")
	}
	if _, err := t.DB.ExecContext(ctx, `UPDATE users
		SET username = ?, email = ?, nama_lengkap = ?, kelas_id = ?, jurusan_id = ?
		WHERE id = ?`, username, email, nama, kelasID, jurusanID, id); err != nil {
		if isDuplicateKey(err) {
			return fail(c, http.StatusConflict, ErrConflict, MsgTaken)
		}
		return err
	}
	return ok(c, nil)
}

// ActivateStudent / DeactivateStudent flip users.aktif. Deactivating also
// revokes every session of that account (mirrors first, then rows), so a
// signed-in student is out immediately — not only at the next login.
func (t *Teacher) ActivateStudent(c *echo.Context) error {
	return t.setStudentActive(c, true)
}

func (t *Teacher) DeactivateStudent(c *echo.Context) error {
	return t.setStudentActive(c, false)
}

func (t *Teacher) setStudentActive(c *echo.Context, active bool) error {
	id, rerr := studentID(c)
	if rerr != nil {
		return fail(c, rerr.Status, rerr.Code, rerr.Msg)
	}
	ctx := c.Request().Context()
	exists, err := userExists(ctx, t.DB, id)
	if err != nil {
		return err
	}
	if !exists {
		return fail(c, http.StatusNotFound, ErrNotFound, "Murid tidak ditemukan.")
	}
	if _, err := t.DB.ExecContext(ctx,
		`UPDATE users SET aktif = ? WHERE id = ?`, active, id); err != nil {
		return err
	}
	if !active {
		if err := mw.RevokeUserSessions(ctx, t.DB, t.Store, id); err != nil {
			return err
		}
	}
	return ok(c, nil)
}

// DeleteStudent removes an account and everything hanging off it in ONE
// transaction: answers → anti-cheat events → attempts → reset requests →
// sessions → the user row (children first; the schema has no FKs, so the
// order IS the integrity). The user's sessions are revoked before the tx so
// mirrors never outlive their rows. Known side effect by design: the student
// disappears from every quiz result (results JOIN users).
func (t *Teacher) DeleteStudent(c *echo.Context) error {
	id, rerr := studentID(c)
	if rerr != nil {
		return fail(c, rerr.Status, rerr.Code, rerr.Msg)
	}
	ctx := c.Request().Context()
	exists, err := userExists(ctx, t.DB, id)
	if err != nil {
		return err
	}
	if !exists {
		return fail(c, http.StatusNotFound, ErrNotFound, "Murid tidak ditemukan.")
	}

	// the quizzes whose state mirrors mention this student — invalidate
	// after commit (spec §6.2: mirrors may answer faster, never newer)
	var quizIDs []uint64
	if rows, err := t.DB.QueryContext(ctx,
		`SELECT DISTINCT quiz_id FROM participants WHERE user_id = ?`, id); err != nil {
		return err
	} else {
		for rows.Next() {
			var qid uint64
			if err := rows.Scan(&qid); err != nil {
				rows.Close()
				return err
			}
			quizIDs = append(quizIDs, qid)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
	}

	// sessions outlive nothing: mirror entries first, then the rows
	if err := mw.RevokeUserSessions(ctx, t.DB, t.Store, id); err != nil {
		return err
	}

	tx, err := t.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	for _, stmt := range []string{
		`DELETE FROM answers WHERE participant_id IN
		   (SELECT id FROM participants WHERE user_id = ?)`,
		`DELETE FROM anti_cheat_events WHERE participant_id IN
		   (SELECT id FROM participants WHERE user_id = ?)`,
		`DELETE FROM participants WHERE user_id = ?`,
		`DELETE FROM password_resets WHERE user_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, stmt, id); err != nil {
			tx.Rollback()
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id); err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	t.Store.Delete(cache.HistoryKey(id))
	for _, qid := range quizIDs {
		t.Store.Delete(cache.QuizStateKey(qid))
	}
	return ok(c, nil)
}
