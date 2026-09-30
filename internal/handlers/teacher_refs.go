package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"html/template"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v5"

	"quiz/internal/analytics"
	"quiz/internal/cache"
	"quiz/internal/uploads"
)

// Teacher serves the /teacher group: dashboard, reference dropdowns, the
// question bank and quiz CRUD (spec §7). Mutation protocol: write the DB
// inside a transaction, COMMIT, delete the affected mirror entries
// (spec §6.2 — the cache may be faster, never newer), then return the
// envelope.
type Teacher struct {
	DB    *sql.DB
	Store *cache.Store

	// NewCode produces join codes; overridable in tests to force a UNIQUE
	// collision (nil → quizengine.NewJoinCode).
	NewCode func() (string, error)

	// UploadsRoot is the chunked-image upload storage root; zero →
	// uploads.DefaultRoot ("data/uploads").
	UploadsRoot string

	// Uploads is the session store; nil → built once from UploadsRoot + DB
	// on the first upload/media request (lazy — fixtures that never hit
	// those routes touch no disk).
	Uploads     *uploads.Store
	uploadsOnce sync.Once
}

// quizListRow is one dashboard/quiz-list entry (chip precomputed so the
// cached value renders directly).
type quizListRow struct {
	ID        uint64
	Judul     string
	Code      string
	Status    string
	Label     string
	Chip      string
	TimerType string
	JoinMode  string
	CreatedAt time.Time
}

// --- GET /teacher (dashboard) ---------------------------------------------

// Dashboard serves the analytics overview shell: the pending password-
// reset badge plus the default-filter overview embedded as a JSON blob
// (the client re-filters through GET /teacher/api/overview). Search +
// status filter + pagination are client-side now; the management list
// stays on /teacher/quiz (spec §7).
func (t *Teacher) Dashboard(c *echo.Context) error {
	pending, err := pendingResetCount(c.Request().Context(), t.DB)
	if err != nil {
		return err
	}
	ov, err := t.buildOverview(c.Request().Context(), analytics.Filter{})
	if err != nil {
		return err
	}
	blob, err := json.Marshal(ov)
	if err != nil {
		return err
	}
	return c.Render(http.StatusOK, "page-teacher-dashboard", map[string]any{
		"Title": "Dasbor", "Pending": pending,
		"OverviewJSON": template.JS(blob),
	})
}

// listQuizzes reads the shared quizlist mirror (TTL 10 s, invalidated by
// every quiz mutation). Returned slices are shared with the cache — callers
// read only.
func (t *Teacher) listQuizzes(ctx context.Context) ([]quizListRow, error) {
	return quizListMirror(ctx, t.DB, t.Store)
}

// quizListMirror is the one reader behind cache.QuizListKey(): the teacher
// dashboard and the student home list both come from it (spec §6.2).
func quizListMirror(ctx context.Context, db *sql.DB, store *cache.Store) ([]quizListRow, error) {
	if v, ok := store.Get(cache.QuizListKey()); ok {
		if list, ok := v.([]quizListRow); ok {
			return list, nil
		}
	}
	rows, err := db.QueryContext(ctx,
		`SELECT id, judul, code, status, timer_type, join_mode, created_at
		 FROM quizzes ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []quizListRow{}
	for rows.Next() {
		var r quizListRow
		if err := rows.Scan(&r.ID, &r.Judul, &r.Code, &r.Status, &r.TimerType, &r.JoinMode, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.Label, r.Chip = quizChip(r.Status)
		list = append(list, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	store.Set(cache.QuizListKey(), list, cache.TTLQuizList)
	return list, nil
}

// --- reference dropdowns (classes & majors) -------------------------------

type refRow struct {
	ID   uint64
	Nama string
}

// refsList reads one reference table through its 60 s mirror — shared by
// the teacher refs pages and the student profile form.
func refsList(ctx context.Context, db *sql.DB, store *cache.Store, kind string) ([]refRow, error) {
	if v, ok := store.Get(cache.RefsKey(kind)); ok {
		if list, ok := v.([]refRow); ok {
			return list, nil
		}
	}
	table := "ref_kelas"
	if kind == "jurusan" {
		table = "ref_jurusan"
	}
	rows, err := db.QueryContext(ctx, `SELECT id, nama FROM `+table+` ORDER BY nama`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []refRow{}
	for rows.Next() {
		var r refRow
		if err := rows.Scan(&r.ID, &r.Nama); err != nil {
			return nil, err
		}
		list = append(list, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	store.Set(cache.RefsKey(kind), list, cache.TTLRefs)
	return list, nil
}

// refsList reads one reference table through its 60 s mirror.
func (t *Teacher) refsList(ctx context.Context, kind string) ([]refRow, error) {
	return refsList(ctx, t.DB, t.Store, kind)
}

// ClassesPage renders the class dropdown manager.
func (t *Teacher) ClassesPage(c *echo.Context) error {
	return t.refsPage(c, "kelas", "Kelas")
}

// MajorsPage renders the major dropdown manager.
func (t *Teacher) MajorsPage(c *echo.Context) error {
	return t.refsPage(c, "jurusan", "Jurusan")
}

// --- GET /teacher/classes/new, /teacher/majors/new ------------------------

// NewClassPage renders the standalone create form for a class (spec §7).
// The form posts through the shared JSON endpoint; on success teacher.js
// follows data-next back to the list with the stored success toast.
func (t *Teacher) NewClassPage(c *echo.Context) error {
	return t.refNewPage(c, "kelas", "Kelas baru", "Nama kelas")
}

// NewMajorPage renders the same create form for a major.
func (t *Teacher) NewMajorPage(c *echo.Context) error {
	return t.refNewPage(c, "jurusan", "Jurusan baru", "Nama jurusan")
}

func (t *Teacher) refNewPage(c *echo.Context, kind, title, nameLabel string) error {
	section := "Kelas"
	if kind == "jurusan" {
		section = "Jurusan"
	}
	list := "/teacher/" + refPath(kind)
	return c.Render(http.StatusOK, "page-teacher-ref-new", map[string]any{
		"Title": title, "Kind": kind, "NameLabel": nameLabel,
		"Action": list,
		"Crumbs": []Crumb{
			{Label: "Dasbor"},
			{Label: "Guru", URL: "/teacher"},
			{Label: section, URL: list},
			{Label: title},
		},
	})
}

// refPath maps the cache/table kind to its route segment (kelas→classes,
// jurusan→majors — the DB keeps Indonesian names, the UI English).
func refPath(kind string) string {
	if kind == "jurusan" {
		return "majors"
	}
	return "classes"
}

func (t *Teacher) refsPage(c *echo.Context, kind, title string) error {
	rows, err := t.refsList(c.Request().Context(), kind)
	if err != nil {
		return err
	}
	q := listQ(c)
	sortBy := strings.TrimSpace(c.QueryParam("sort"))
	filtered := make([]refRow, 0, len(rows))
	for _, r := range rows {
		if matchSearch(q, r.Nama) {
			filtered = append(filtered, r)
		}
	}
	if sortBy == "baru" {
		// Terbaru = newest first. filtered is a fresh slice built above,
		// so reordering it never touches the shared cache mirror.
		sort.SliceStable(filtered, func(i, j int) bool { return filtered[i].ID > filtered[j].ID })
	}
	page := listPage(c)
	pageRows, page, _ := paginate(filtered, page)
	tb := newTable(c, q, len(filtered), page)
	tb.Placeholder = "Cari nama…"
	return c.Render(http.StatusOK, "page-teacher-refs", map[string]any{
		"Title": title, "Kind": kind, "Rows": pageRows,
		"Action": "/teacher/" + refPath(kind),
		"Table":  tb, "Sort": sortBy,
	})
}

// CreateClass / CreateMajor insert a reference row.
func (t *Teacher) CreateClass(c *echo.Context) error { return t.createRef(c, "kelas") }
func (t *Teacher) CreateMajor(c *echo.Context) error { return t.createRef(c, "jurusan") }

func (t *Teacher) createRef(c *echo.Context, kind string) error {
	nama := strings.TrimSpace(c.FormValue("nama"))
	if nama == "" {
		return fail(c, http.StatusBadRequest, ErrValidation, "Nama wajib diisi.")
	}
	if len([]rune(nama)) > 50 {
		return fail(c, http.StatusBadRequest, ErrValidation, "Nama terlalu panjang (maksimal 50 karakter).")
	}
	table := "ref_kelas"
	if kind == "jurusan" {
		table = "ref_jurusan"
	}
	tx, err := t.DB.BeginTx(c.Request().Context(), nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(c.Request().Context(),
		`INSERT INTO `+table+` (nama) VALUES (?)`, nama); err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	t.Store.Delete(cache.RefsKey(kind))
	return ok(c, nil)
}

// DeleteClass / DeleteMajor drop a reference row unless users reference it
// (spec CRUD: "Delete class/major in use → 409").
func (t *Teacher) DeleteClass(c *echo.Context) error { return t.deleteRef(c, "kelas") }
func (t *Teacher) DeleteMajor(c *echo.Context) error { return t.deleteRef(c, "jurusan") }

func (t *Teacher) deleteRef(c *echo.Context, kind string) error {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		return fail(c, http.StatusBadRequest, ErrValidation, "ID tidak valid.")
	}
	table, usersCol := "ref_kelas", "kelas_id"
	if kind == "jurusan" {
		table, usersCol = "ref_jurusan", "jurusan_id"
	}
	// One guarded statement: the in-use check and the delete cannot race
	// (schema has no FKs, so integrity lives here).
	res, err := t.DB.ExecContext(c.Request().Context(),
		`DELETE FROM `+table+` WHERE id = ? AND NOT EXISTS
		 (SELECT 1 FROM users WHERE `+usersCol+` = `+table+`.id)`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		// in use, or gone — classify
		var exists bool
		if err := t.DB.QueryRowContext(c.Request().Context(),
			`SELECT EXISTS(SELECT 1 FROM `+table+` WHERE id = ?)`, id).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return fail(c, http.StatusConflict, ErrConflict,
				"Masih dipakai murid — penghapusan diblokir.")
		}
		noun := "Kelas"
		if kind == "jurusan" {
			noun = "Jurusan"
		}
		return fail(c, http.StatusNotFound, ErrNotFound, noun+" tidak ditemukan.")
	}
	t.Store.Delete(cache.RefsKey(kind))
	return ok(c, nil)
}
