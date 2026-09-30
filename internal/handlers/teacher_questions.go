package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v5"

	"quiz/internal/cache"
	"quiz/internal/uploads"
)

// errInvalidLength marks an unknown ?length= bucket.
var errInvalidLength = errors.New("invalid length filter")

// lengthBucket is the single source of truth for the bank filter
// (spec §6.8: short <80, medium 80–200, long >200 — characters, not
// bytes). Both the SQL clause and the server-side compose re-check derive
// from these bounds, so they cannot drift apart.
type lengthBucket struct {
	min, max int // inclusive; 0 = unbounded
}

func bucketFor(length string) (lengthBucket, error) {
	switch length {
	case "":
		return lengthBucket{}, nil
	case "short":
		return lengthBucket{max: 79}, nil
	case "medium":
		return lengthBucket{min: 80, max: 200}, nil
	case "long":
		return lengthBucket{min: 201}, nil
	default:
		return lengthBucket{}, errInvalidLength
	}
}

// clause renders the bucket as a CHAR_LENGTH WHERE fragment ("" = all).
func (b lengthBucket) clause() string {
	switch {
	case b.min == 0 && b.max == 0:
		return ""
	case b.max != 0:
		if b.min != 0 {
			return "WHERE CHAR_LENGTH(teks) BETWEEN " + strconv.Itoa(b.min) + " AND " + strconv.Itoa(b.max)
		}
		return "WHERE CHAR_LENGTH(teks) < " + strconv.Itoa(b.max+1)
	default:
		return "WHERE CHAR_LENGTH(teks) > " + strconv.Itoa(b.min-1)
	}
}

// includes is the Go-side twin of clause(), used to re-validate the client's
// declared filter when composing (spec §6.8).
func (b lengthBucket) includes(n int) bool {
	if b.min != 0 && n < b.min {
		return false
	}
	if b.max != 0 && n > b.max {
		return false
	}
	return true
}

// bankRow is one question-bank entry (raw JSON kept for the edit form).
type bankRow struct {
	ID        uint64
	Teks      string
	Type      string
	Options   string
	Correct   string
	TeksLen   int
	CreatedAt time.Time
}

// listBank reads the bank through its 10 s mirror (spec §6.2), filtered by
// length bucket ("" = all).
func (t *Teacher) listBank(ctx context.Context, length string) ([]bankRow, error) {
	if v, ok := t.Store.Get(cache.BankKey(length)); ok {
		if list, ok := v.([]bankRow); ok {
			return list, nil
		}
	}
	bucket, err := bucketFor(length)
	if err != nil {
		return nil, err
	}
	rows, err := t.DB.QueryContext(ctx,
		`SELECT id, teks, type, COALESCE(options, ''), COALESCE(correct, ''),
		        CHAR_LENGTH(teks), created_at
		 FROM questions `+bucket.clause()+` ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []bankRow{}
	for rows.Next() {
		var r bankRow
		if err := rows.Scan(&r.ID, &r.Teks, &r.Type, &r.Options, &r.Correct,
			&r.TeksLen, &r.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	t.Store.Set(cache.BankKey(length), list, cache.TTLBank)
	return list, nil
}

// QuestionsPage renders the bank with the length filter. ?q= searches the
// question text and COMBINES with ?length= and ?page= (see list.go): the
// bucket still comes from the list fetch, search and paging narrow it in
// memory.
func (t *Teacher) QuestionsPage(c *echo.Context) error {
	length := c.QueryParam("length")
	list, err := t.listBank(c.Request().Context(), length)
	if err != nil {
		if errors.Is(err, errInvalidLength) {
			return fail(c, http.StatusBadRequest, ErrValidation, "Filter panjang tidak valid.")
		}
		return err
	}
	search := listQ(c)
	filtered := make([]bankRow, 0, len(list))
	for _, r := range list {
		if !matchSearch(search, r.Teks) {
			continue
		}
		filtered = append(filtered, r)
	}
	page := listPage(c)
	rows, page, _ := paginate(filtered, page)
	tb := newTable(c, search, len(filtered), page)
	tb.Placeholder = "Cari teks pertanyaan…"
	return c.Render(http.StatusOK, "page-teacher-questions", map[string]any{
		"Title": "Bank pertanyaan", "Rows": rows, "Length": length, "Table": tb,
	})
}

// --- GET /teacher/questions/new -------------------------------------------

// QuestionNewPage renders the standalone create form (spec §7). The form
// posts through the shared JSON endpoint; on success teacher.js follows
// data-next back to the list with the stored success toast.
func (t *Teacher) QuestionNewPage(c *echo.Context) error {
	return c.Render(http.StatusOK, "page-teacher-question-new", map[string]any{
		"Title":      "Pertanyaan baru",
		"Q":          bankRow{Type: "pg"},
		"FormAction": "/teacher/questions",
		"Crumbs": []Crumb{
			{Label: "Dasbor"},
			{Label: "Guru", URL: "/teacher"},
			{Label: "Bank pertanyaan", URL: "/teacher/questions"},
			{Label: "Pertanyaan baru"},
		},
	})
}

// --- GET /teacher/questions/:id/edit --------------------------------------

// QuestionEditPage renders the standalone edit form for one bank row,
// prefilled server-side from the DB (the raw options/correct JSON rides the
// page for the option-row script). Unknown or malformed id → 404.
func (t *Teacher) QuestionEditPage(c *echo.Context) error {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		return fail(c, http.StatusNotFound, ErrNotFound, "Pertanyaan tidak ditemukan.")
	}
	var q bankRow
	err = t.DB.QueryRowContext(c.Request().Context(),
		`SELECT id, teks, type, COALESCE(options, ''), COALESCE(correct, ''),
		        CHAR_LENGTH(teks), created_at
		 FROM questions WHERE id = ?`, id).
		Scan(&q.ID, &q.Teks, &q.Type, &q.Options, &q.Correct, &q.TeksLen, &q.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return fail(c, http.StatusNotFound, ErrNotFound, "Pertanyaan tidak ditemukan.")
	}
	if err != nil {
		return err
	}
	return c.Render(http.StatusOK, "page-teacher-question-edit", map[string]any{
		"Title":      "Ubah pertanyaan",
		"Q":          q,
		"FormAction": "/teacher/questions/" + strconv.FormatUint(id, 10) + "/edit",
		"Crumbs": []Crumb{
			{Label: "Dasbor"},
			{Label: "Guru", URL: "/teacher"},
			{Label: "Bank pertanyaan", URL: "/teacher/questions"},
			{Label: "Ubah pertanyaan"},
		},
	})
}

// questionInput validates and encodes the shared add/edit form. Returns a
// non-empty errMsg for the 400 branch (spec CRUD: PG exactly 1 correct,
// multi ≥2 options and ≥1 correct, <2 options → 400).
func questionInput(c *echo.Context) (teks, qtype, options, correct, errMsg string) {
	teks = strings.TrimSpace(c.FormValue("teks"))
	if teks == "" {
		return "", "", "", "", "Teks pertanyaan wajib diisi."
	}
	qtype = c.FormValue("type")
	if err := c.Request().ParseForm(); err != nil {
		return "", "", "", "", "Badan formulir tidak valid."
	}
	form := c.Request().Form

	switch qtype {
	case "pg", "multi":
		raw := form["options[]"]
		if len(raw) < 2 {
			return "", "", "", "", "Tambahkan setidaknya dua pilihan."
		}
		opts := make([]string, len(raw))
		for i, o := range raw {
			opts[i] = strings.TrimSpace(o)
			if opts[i] == "" {
				return "", "", "", "", "Pilihan tidak boleh kosong."
			}
		}
		var idxs []int
		for _, s := range form["correct[]"] {
			n, err := strconv.Atoi(s)
			if err != nil {
				return "", "", "", "", "Pemilihan jawaban benar tidak valid."
			}
			idxs = append(idxs, n)
		}
		if qtype == "pg" && len(idxs) != 1 {
			return "", "", "", "", "Pilih tepat satu jawaban benar."
		}
		if qtype == "multi" && len(idxs) < 1 {
			return "", "", "", "", "Pilih setidaknya satu jawaban benar."
		}
		for _, n := range idxs {
			if n < 0 || n >= len(opts) {
				return "", "", "", "", "Pemilihan jawaban benar tidak valid."
			}
		}
		optJSON, err := json.Marshal(opts)
		if err != nil {
			return "", "", "", "", "Badan formulir tidak valid."
		}
		// schema §5: pg correct is a scalar index, multi is an index array
		var corrJSON []byte
		if qtype == "pg" {
			corrJSON, err = json.Marshal(idxs[0])
		} else {
			corrJSON, err = json.Marshal(idxs)
		}
		if err != nil {
			return "", "", "", "", "Badan formulir tidak valid."
		}
		return teks, qtype, string(optJSON), string(corrJSON), ""

	case "essay":
		// essay key text is optional and never auto-graded (schema §5)
		key := strings.TrimSpace(c.FormValue("essay_key"))
		if key == "" {
			return teks, qtype, "", "", ""
		}
		keyJSON, err := json.Marshal(key)
		if err != nil {
			return "", "", "", "", "Badan formulir tidak valid."
		}
		return teks, qtype, "", string(keyJSON), ""

	default:
		return "", "", "", "", "Jenis pertanyaan tidak valid."
	}
}

// CreateQuestion adds a bank question (POST /teacher/questions).
func (t *Teacher) CreateQuestion(c *echo.Context) error {
	teks, qtype, options, correct, errMsg := questionInput(c)
	if errMsg != "" {
		return fail(c, http.StatusBadRequest, ErrValidation, errMsg)
	}
	// optional image — the hidden field carries the path Complete returned.
	// VerifyLink re-derives every row value from server state (format
	// regex, meta.json, on-disk size, sha256 re-hash, once-only attach);
	// the form only names the directory (spec §6.14).
	var img uploads.Result
	imgRef := strings.TrimSpace(c.FormValue("image"))
	if imgRef != "" {
		var err error
		if img, err = t.uploads().VerifyLink(imgRef); err != nil {
			return uploadFail(c, err)
		}
	}
	tx, err := t.DB.BeginTx(c.Request().Context(), nil)
	if err != nil {
		return err
	}
	var opt, corr any
	if options != "" {
		opt = options
	}
	if correct != "" {
		corr = correct
	}
	res, err := tx.ExecContext(c.Request().Context(),
		`INSERT INTO questions (teks, type, options, correct) VALUES (?, ?, ?, ?)`,
		teks, qtype, opt, corr)
	if err != nil {
		tx.Rollback()
		return err
	}
	if imgRef != "" {
		// same transaction: a question either gets its image or nothing
		qid, err := res.LastInsertId()
		if err != nil {
			tx.Rollback()
			return err
		}
		if _, err := tx.ExecContext(c.Request().Context(),
			`INSERT INTO question_images (question_id, filename, path, byte_size, mime_type, sha256)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			uint64(qid), path.Base(img.Path), img.Path, img.Size, img.MIME, img.SHA256); err != nil {
			tx.Rollback()
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	t.Store.DeletePrefix(cache.BankKey(""))
	return ok(c, nil)
}

// EditQuestion updates a bank question in place (POST /teacher/questions/:id/edit).
func (t *Teacher) EditQuestion(c *echo.Context) error {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		return fail(c, http.StatusBadRequest, ErrValidation, "ID pertanyaan tidak valid.")
	}
	teks, qtype, options, correct, errMsg := questionInput(c)
	if errMsg != "" {
		return fail(c, http.StatusBadRequest, ErrValidation, errMsg)
	}
	ctx := c.Request().Context()
	var opt, corr any
	if options != "" {
		opt = options
	}
	if correct != "" {
		corr = correct
	}
	res, err := t.DB.ExecContext(ctx,
		`UPDATE questions SET teks = ?, type = ?, options = ?, correct = ? WHERE id = ?`,
		teks, qtype, opt, corr, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// MySQL reports 0 for an unchanged row too — classify before 404.
		var exists bool
		if err := t.DB.QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM questions WHERE id = ?)`, id).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return fail(c, http.StatusNotFound, ErrNotFound, "Pertanyaan tidak ditemukan.")
		}
	}
	t.Store.DeletePrefix(cache.BankKey(""))
	return ok(c, nil)
}

// DeleteQuestion drops an unreferenced question; referenced → 409 (spec CRUD).
func (t *Teacher) DeleteQuestion(c *echo.Context) error {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		return fail(c, http.StatusBadRequest, ErrValidation, "ID pertanyaan tidak valid.")
	}
	ctx := c.Request().Context()
	// integrity without FKs: read the image row first so its files can
	// follow once the rows are gone
	var imgPath string
	err = t.DB.QueryRowContext(ctx,
		`SELECT path FROM question_images WHERE question_id = ?`, id).Scan(&imgPath)
	linked := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	tx, err := t.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx,
		`DELETE FROM questions WHERE id = ? AND NOT EXISTS
		 (SELECT 1 FROM quiz_questions WHERE question_id = questions.id)`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var exists bool
		if err := tx.QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM questions WHERE id = ?)`, id).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return fail(c, http.StatusNotFound, ErrNotFound, "Pertanyaan tidak ditemukan.")
		}
		return fail(c, http.StatusConflict, ErrConflict,
			"Pertanyaan dipakai di kuis — hapus dari kuis terlebih dahulu.")
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM question_images WHERE question_id = ?`, id); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if linked && uploads.ValidRefPath(imgPath) {
		// files go after COMMIT; a missed RemoveAll is reclaimed by the
		// 24h sweep — the row is already gone, so the dir is unreferenced
		dir := filepath.Join(t.uploads().Root(), filepath.FromSlash(path.Dir(imgPath)))
		if err := os.RemoveAll(dir); err != nil {
			c.Logger().Error("question image removal failed", "err", err)
		}
	}
	t.Store.DeletePrefix(cache.BankKey(""))
	return ok(c, nil)
}
