package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/skip2/go-qrcode"

	"quiz/internal/cache"
	"quiz/internal/quizengine"
)

// joinCodeRetries is the bounded UNIQUE-collision retry budget for
// quiz creation (spec §7: regenerate with bounded retry, else 500).
const joinCodeRetries = 5

// quizRow is a display-ready quiz (plain strings so templates stay simple).
type quizRow struct {
	ID                 uint64
	Code               string
	Judul              string
	Deskripsi          string
	TimerType          string
	Status             string
	StatusLabel        string
	Chip               string
	JoinMode           string
	TotalSeconds       uint32
	PerQuestionSeconds uint16
	ShuffleOptions     bool
	ShuffleQuestions   bool
	ShowCorrectWrong   bool
	ShowFinalScore     bool
	RankingLive        bool
	MaxAttempts        uint8
	QuestionReview     string
	CreatedAt          time.Time
}

// quizChip maps status → (Indonesian label, badge classes). Monochrome + the
// two accents only (spec §11.20): "bg-ok" is #067647, the rest are grays.
func quizChip(status string) (label, class string) {
	switch status {
	case "nonaktif":
		return "Tidak aktif", "badge text-bg-secondary"
	case "aktif":
		return "Aktif", "badge bg-ok"
	case "berjalan":
		return "Berjalan", "badge text-bg-dark"
	case "selesai":
		return "Selesai", "badge border text-secondary"
	}
	return status, "badge text-bg-secondary"
}

func withChip(r *quizRow) {
	r.StatusLabel, r.Chip = quizChip(r.Status)
}

// participantRow is one roster/results line.
type participantRow struct {
	ID        uint64
	Username  string
	Nama      string
	Status    string
	StatusLbl string
	AttemptNo uint8
	Score     float64 // formatted for display when HasScore
	HasScore  bool
}

func participantStatusLabel(s string) string {
	switch s {
	case "pending":
		return "Menunggu persetujuan"
	case "registered":
		return "Terdaftar"
	case "started":
		return "Berlangsung"
	case "selesai":
		return "Selesai"
	case "dikeluarkan":
		return "Dikeluarkan"
	}
	return s
}

// quizSettings is the validated settings form (all fields of spec §6.7).
type quizSettings struct {
	Judul              string
	Deskripsi          string // empty → NULL
	TimerType          string
	TimerOn            bool // derived from TimerType, never a form field
	TotalSeconds       uint32
	PerQuestionSeconds uint16
	JoinMode           string
	ShuffleOptions     bool
	ShuffleQuestions   bool
	ShowCorrectWrong   bool
	ShowFinalScore     bool
	RankingLive        bool
	MaxAttempts        uint8
	QuestionReview     string
}

func formOnOff(c *echo.Context, name string) bool { return c.FormValue(name) == "1" }

func formUint(c *echo.Context, name string, bits int) (uint64, bool) {
	v, err := strconv.ParseUint(c.FormValue(name), 10, bits)
	if err != nil {
		return 0, false
	}
	return v, true
}

// parseQuizSettings validates the settings form. The returned string is a
// non-empty 400 message. Spec-forced invariants applied here, at every
// settings write: timer_on is derived from timer_type (never read from the
// form — tanpa_timer derives 0 and its timer seconds are zeroed), and
// sequential order (shuffle_questions=0) forces shuffle_options=0 (§6.7).
func parseQuizSettings(c *echo.Context) (quizSettings, string) {
	var s quizSettings
	s.Judul = strings.TrimSpace(c.FormValue("judul"))
	if s.Judul == "" {
		return s, "Judul wajib diisi."
	}
	if len([]rune(s.Judul)) > 150 {
		return s, "Judul terlalu panjang (maksimal 150 karakter)."
	}
	s.Deskripsi = strings.TrimSpace(c.FormValue("deskripsi"))

	s.TimerType = c.FormValue("timer_type")
	switch s.TimerType {
	case "global", "per_soal", "tanpa_timer":
	default:
		return s, "Jenis timer tidak valid."
	}
	s.JoinMode = c.FormValue("join_mode")
	if s.JoinMode != "open" && s.JoinMode != "approve" {
		return s, "Mode bergabung tidak valid."
	}
	s.QuestionReview = c.FormValue("question_review")
	switch s.QuestionReview {
	case "none", "text", "full":
	default:
		return s, "Pengaturan tinjauan tidak valid."
	}

	// timer_on derives from timer_type: the form no longer sends it, so a
	// stray timer_on value is ignored silently.
	s.TimerOn = s.TimerType != "tanpa_timer"
	s.ShuffleQuestions = formOnOff(c, "shuffle_questions")
	s.ShuffleOptions = formOnOff(c, "shuffle_options")
	s.ShowCorrectWrong = formOnOff(c, "show_correct_wrong")
	s.ShowFinalScore = formOnOff(c, "show_final_score")
	s.RankingLive = formOnOff(c, "ranking_live")

	if s.TimerType == "tanpa_timer" {
		// no countdown: the seconds fields are hidden (still submitted,
		// ignored) and stored as zero
		s.TotalSeconds, s.PerQuestionSeconds = 0, 0
	} else {
		v, ok := formUint(c, "total_seconds", 32)
		if !ok {
			return s, "Total detik harus berupa angka."
		}
		s.TotalSeconds = uint32(v)
		if v, ok = formUint(c, "per_question_seconds", 16); !ok {
			return s, "Detik per pertanyaan harus berupa angka."
		}
		s.PerQuestionSeconds = uint16(v)
	}
	v, ok := formUint(c, "max_attempts", 8)
	if !ok {
		return s, "Jumlah percobaan harus berupa angka."
	}
	if v < 1 {
		return s, "Jumlah percobaan setidaknya 1."
	}
	s.MaxAttempts = uint8(v)

	// server-side forces (spec §6.7 / §9)
	if !s.ShuffleQuestions {
		s.ShuffleOptions = false
	}
	// guard states that would break START (a 0-second global deadline
	// would trip the timeout watcher immediately); tanpa_timer carries no
	// timer seconds, so neither check applies to it
	if s.TimerType == "global" && s.TotalSeconds < 1 {
		return s, "Isi total waktu dalam detik."
	}
	if s.TimerType == "per_soal" && s.PerQuestionSeconds < 1 {
		return s, "Isi detik per pertanyaan."
	}
	return s, ""
}

// deskripsiVal maps "" → NULL for the TEXT NULL column.
func deskripsiVal(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// --- GET /teacher/quiz (list) ----------------------------------------------

// QuizListPage renders the quiz list with status chips, searchable and
// filterable: ?q= + ?status= + ?page= combine (see list.go), and the delete
// action lives on every row (modal confirm — never a browser confirm()).
func (t *Teacher) QuizListPage(c *echo.Context) error {
	quizzes, err := t.listQuizzes(c.Request().Context())
	if err != nil {
		return err
	}
	q := listQ(c)
	status := strings.TrimSpace(c.QueryParam("status"))
	filtered := make([]quizListRow, 0, len(quizzes))
	for _, z := range quizzes {
		if status != "" && z.Status != status {
			continue
		}
		if !matchSearch(q, z.Judul, z.Code) {
			continue
		}
		filtered = append(filtered, z)
	}
	page := listPage(c)
	rows, page, _ := paginate(filtered, page)
	tb := newTable(c, q, len(filtered), page)
	tb.Placeholder = "Cari judul atau kode…"
	return c.Render(http.StatusOK, "page-teacher-quiz-list", map[string]any{
		"Title": "Kuis", "Quizzes": rows, "Table": tb, "Status": status,
	})
}

// --- GET|POST /teacher/quiz/new -------------------------------------------

// QuizNewPage renders the create form (defaults = spec column defaults;
// timer_on derives from the default timer_type "global", so the form is
// valid out of the box).
func (t *Teacher) QuizNewPage(c *echo.Context) error {
	q := quizRow{
		TimerType:          "global",
		Status:             "nonaktif",
		JoinMode:           "open",
		TotalSeconds:       600,
		PerQuestionSeconds: 300,
		ShowCorrectWrong:   true,
		ShowFinalScore:     true,
		MaxAttempts:        1,
		QuestionReview:     "none",
	}
	withChip(&q)
	return c.Render(http.StatusOK, "page-teacher-quiz-new", map[string]any{
		"Title": "Kuis baru", "Quiz": q,
	})
}

// CreateQuiz inserts a nonaktif quiz with a fresh join code, retrying on
// UNIQUE collision up to joinCodeRetries times (spec §7).
func (t *Teacher) CreateQuiz(c *echo.Context) error {
	s, errMsg := parseQuizSettings(c)
	if errMsg != "" {
		return fail(c, http.StatusBadRequest, ErrValidation, errMsg)
	}
	codeGen := t.NewCode
	if codeGen == nil {
		codeGen = quizengine.NewJoinCode
	}
	ctx := c.Request().Context()
	tx, err := t.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	var id int64
	created := false
	for attempt := 0; attempt <= joinCodeRetries; attempt++ {
		code, err := codeGen()
		if err != nil {
			tx.Rollback()
			return fail(c, http.StatusInternalServerError, ErrServer, "Tidak dapat membuat kuis.")
		}
		res, err := tx.ExecContext(ctx,
			`INSERT INTO quizzes (code, judul, deskripsi, timer_type, timer_on, status,
				join_mode, total_seconds, per_question_seconds, shuffle_options,
				shuffle_questions, show_correct_wrong, show_final_score, ranking_live,
				max_attempts, question_review)
			 VALUES (?, ?, ?, ?, ?, 'nonaktif', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			code, s.Judul, deskripsiVal(s.Deskripsi), s.TimerType, s.TimerOn,
			s.JoinMode, s.TotalSeconds, s.PerQuestionSeconds, s.ShuffleOptions,
			s.ShuffleQuestions, s.ShowCorrectWrong, s.ShowFinalScore, s.RankingLive,
			s.MaxAttempts, s.QuestionReview)
		if err != nil {
			if isDuplicateKey(err) {
				continue // UNIQUE code collision → bounded retry
			}
			tx.Rollback()
			return err
		}
		id, _ = res.LastInsertId()
		created = true
		break
	}
	if !created {
		tx.Rollback()
		return fail(c, http.StatusInternalServerError, ErrServer,
			"Tidak dapat membuat kode gabung — silakan coba lagi.")
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	t.Store.Delete(cache.QuizListKey())
	return ok(c, map[string]any{"id": id})
}

// requestScheme derives the scheme for share links and QR payloads: the
// first X-Forwarded-Proto value when it names http/https (TLS-terminating
// proxy), else https behind local TLS, else plain http — the local default
// stays byte-identical.
func requestScheme(r *http.Request) string {
	proto := r.Header.Get("X-Forwarded-Proto")
	if i := strings.IndexByte(proto, ','); i >= 0 {
		proto = proto[:i]
	}
	proto = strings.ToLower(strings.TrimSpace(proto))
	if proto == "http" || proto == "https" {
		return proto
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

// --- GET /teacher/quiz/:id (detail with tabs) ------------------------------

// QuizDetailPage renders the Questions | Settings | Participants tabs
// (spec §7).
func (t *Teacher) QuizDetailPage(c *echo.Context) error {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		return c.String(http.StatusNotFound, "Tidak ditemukan.")
	}
	ctx := c.Request().Context()
	q, err := t.quizByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return c.String(http.StatusNotFound, "Tidak ditemukan.")
		}
		return err
	}
	composed, err := t.composedQuestions(ctx, id)
	if err != nil {
		return err
	}
	length := c.QueryParam("length")
	bank, err := t.listBank(ctx, length)
	if err != nil {
		if errors.Is(err, errInvalidLength) {
			return c.String(http.StatusBadRequest, "Filter panjang tidak valid.")
		}
		return err
	}
	roster, err := t.roster(ctx, id)
	if err != nil {
		return err
	}
	composedIDs := make(map[uint64]bool, len(composed))
	for _, cq := range composed {
		composedIDs[cq.ID] = true
	}
	// Search-bars → SSR conversion (task: unified server-side search): the
	// composed-questions tab reads ?qq= and the bank modal reads ?bq=, each
	// narrowing ONLY its own list in memory over the fetched rows — the
	// list.go matchSearch policy shared with ?q= below. Empty/absent = no
	// filter. Fresh slices: the bank list is a shared cache mirror.
	qq := strings.TrimSpace(c.QueryParam("qq"))
	questions := make([]composedRow, 0, len(composed))
	for _, cq := range composed {
		if !matchSearch(qq, cq.Teks) {
			continue
		}
		questions = append(questions, cq)
	}
	bq := strings.TrimSpace(c.QueryParam("bq"))
	bankFiltered := make([]bankRow, 0, len(bank))
	for _, b := range bank {
		if !matchSearch(bq, b.Teks) {
			continue
		}
		bankFiltered = append(bankFiltered, b)
	}
	// Participants table only: ?q= (username / full name) + ?status= +
	// ?page= combine (see list.go). The bank listing and the results page
	// still see the full roster — only the roster table is narrowed, and
	// the rows handed to it are a paginated copy.
	search := listQ(c)
	status := strings.TrimSpace(c.QueryParam("status"))
	filtered := make([]participantRow, 0, len(roster))
	for _, p := range roster {
		if status != "" && p.Status != status {
			continue
		}
		if !matchSearch(search, p.Username, p.Nama) {
			continue
		}
		filtered = append(filtered, p)
	}
	page := listPage(c)
	rows, page, _ := paginate(filtered, page)
	tb := newTable(c, search, len(filtered), page)
	tb.Placeholder = "Cari peserta…"
	return c.Render(http.StatusOK, "page-teacher-quiz-detail", map[string]any{
		"Title": q.Judul, "Quiz": q, "Questions": questions, "Bank": bankFiltered,
		// BankEmpty reports the UNFILTERED bank size so the template can
		// tell "bank is empty" apart from "search matched nothing"; the Q
		// keys echo the active search back into the forms.
		"BankEmpty": len(bank) == 0, "QuestionQ": qq, "BankQ": bq,
		"Length": length, "Composed": composedIDs, "Participants": filtered,
		"ParticipantRows": rows, "Table": tb, "Status": status,
		"JoinURL": requestScheme(c.Request()) + "://" + c.Request().Host + "/quiz/" + q.Code,
		"Crumbs":  QuizCrumbs(q.ID, q.Judul, ""),
	})
}

func (t *Teacher) quizByID(ctx context.Context, id uint64) (quizRow, error) {
	var q quizRow
	err := t.DB.QueryRowContext(ctx,
		`SELECT id, code, judul, COALESCE(deskripsi, ''), timer_type,
		        status, join_mode, total_seconds, per_question_seconds,
		        shuffle_options, shuffle_questions, show_correct_wrong,
		        show_final_score, ranking_live, max_attempts, question_review, created_at
		 FROM quizzes WHERE id = ?`, id).
		Scan(&q.ID, &q.Code, &q.Judul, &q.Deskripsi, &q.TimerType,
			&q.Status, &q.JoinMode, &q.TotalSeconds, &q.PerQuestionSeconds,
			&q.ShuffleOptions, &q.ShuffleQuestions, &q.ShowCorrectWrong,
			&q.ShowFinalScore, &q.RankingLive, &q.MaxAttempts, &q.QuestionReview,
			&q.CreatedAt)
	if err != nil {
		return q, err
	}
	withChip(&q)
	return q, nil
}

type composedRow struct {
	ID   uint64
	Seq  uint16
	Teks string
	Type string
}

func (t *Teacher) composedQuestions(ctx context.Context, quizID uint64) ([]composedRow, error) {
	rows, err := t.DB.QueryContext(ctx,
		`SELECT q.id, qq.seq, q.teks, q.type
		 FROM quiz_questions qq JOIN questions q ON q.id = qq.question_id
		 WHERE qq.quiz_id = ? ORDER BY qq.seq, qq.id`, quizID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []composedRow{}
	for rows.Next() {
		var r composedRow
		if err := rows.Scan(&r.ID, &r.Seq, &r.Teks, &r.Type); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (t *Teacher) roster(ctx context.Context, quizID uint64) ([]participantRow, error) {
	rows, err := t.DB.QueryContext(ctx,
		`SELECT p.id, u.username, u.nama_lengkap, p.status, p.attempt_no,
		        p.final_score, p.score_auto
		 FROM participants p JOIN users u ON u.id = p.user_id
		 WHERE p.quiz_id = ? ORDER BY p.id`, quizID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []participantRow{}
	for rows.Next() {
		var r participantRow
		var final, auto sql.NullFloat64
		if err := rows.Scan(&r.ID, &r.Username, &r.Nama, &r.Status, &r.AttemptNo,
			&final, &auto); err != nil {
			return nil, err
		}
		r.StatusLbl = participantStatusLabel(r.Status)
		switch {
		case final.Valid:
			r.Score, r.HasScore = final.Float64, true
		case auto.Valid && r.Status == "selesai":
			r.Score, r.HasScore = auto.Float64, true
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// --- POST /teacher/quiz/:id/edit -------------------------------------------

// EditQuiz saves settings. Guard: only while nonaktif AND zero participants
// — 0 rows affected classifies to 409 with the locked message (spec CRUD
// two-tab race).
func (t *Teacher) EditQuiz(c *echo.Context) error {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		return fail(c, http.StatusBadRequest, ErrValidation, "ID kuis tidak valid.")
	}
	s, errMsg := parseQuizSettings(c)
	if errMsg != "" {
		return fail(c, http.StatusBadRequest, ErrValidation, errMsg)
	}
	ctx := c.Request().Context()
	res, err := t.DB.ExecContext(ctx,
		`UPDATE quizzes SET judul = ?, deskripsi = ?, timer_type = ?, timer_on = ?,
			total_seconds = ?, per_question_seconds = ?, join_mode = ?,
			shuffle_options = ?, shuffle_questions = ?, show_correct_wrong = ?,
			show_final_score = ?, ranking_live = ?, max_attempts = ?, question_review = ?
		 WHERE id = ? AND status = 'nonaktif'
		   AND NOT EXISTS (SELECT 1 FROM participants WHERE quiz_id = quizzes.id)`,
		s.Judul, deskripsiVal(s.Deskripsi), s.TimerType, s.TimerOn, s.TotalSeconds,
		s.PerQuestionSeconds, s.JoinMode, s.ShuffleOptions, s.ShuffleQuestions,
		s.ShowCorrectWrong, s.ShowFinalScore, s.RankingLive, s.MaxAttempts,
		s.QuestionReview, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// classify: locked vs missing vs unchanged-row no-op
		var status string
		err := t.DB.QueryRowContext(ctx, `SELECT status FROM quizzes WHERE id = ?`, id).Scan(&status)
		if errors.Is(err, sql.ErrNoRows) {
			return fail(c, http.StatusNotFound, ErrNotFound, "Kuis tidak ditemukan.")
		}
		if err != nil {
			return err
		}
		if status != "nonaktif" {
			return lockedConflict(c)
		}
		var parts bool
		if err := t.DB.QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM participants WHERE quiz_id = ?)`, id).Scan(&parts); err != nil {
			return err
		}
		if parts {
			return lockedConflict(c)
		}
		// values identical — an effective no-op, still success
	}
	t.Store.Delete(cache.QuizListKey())
	t.Store.Delete(cache.QuizSetKey(id))
	t.Store.Delete(cache.QuizStateKey(id))
	return ok(c, nil)
}

// lockedConflict is the exact spec message for the edit guard.
func lockedConflict(c *echo.Context) error {
	return fail(c, http.StatusConflict, ErrConflict,
		"Kuis memiliki peserta atau sedang aktif — pengubahan dikunci.")
}

// --- POST /teacher/quiz/:id/questions (compose) ----------------------------

// composeForm decodes a compose body into url.Values for all three
// encodings the endpoint accepts: application/json (the bank modal sends
// {"question_ids":[12,15],"length":""} and never seq_* fields), the
// browser's multipart/form-data (FormData — ParseForm never reads multipart
// bodies, which made every browser submit fail with 400) and the legacy
// application/x-www-form-urlencoded form (question_ids[] + seq_<id>).
// Branching MUST key off Content-Type: a bare ParseMultipartForm on a
// urlencoded body parses it and then returns ErrNotMultipart. Returns a
// non-empty 400 message when the body cannot be parsed.
func composeForm(c *echo.Context) (url.Values, string) {
	ct := c.Request().Header.Get("Content-Type")
	switch {
	case strings.HasPrefix(ct, "application/json"):
		dec := json.NewDecoder(c.Request().Body)
		dec.UseNumber() // keep ids exact — float64 would mangle large ids
		var payload struct {
			QuestionIDs []any  `json:"question_ids"`
			Length      string `json:"length"`
		}
		if err := dec.Decode(&payload); err != nil {
			return nil, "Badan permintaan tidak valid."
		}
		form := url.Values{"length": {payload.Length}}
		for _, v := range payload.QuestionIDs {
			// numbers and strings pass through verbatim; any other JSON
			// value stringifies to something non-numeric and is rejected
			// downstream as an invalid question id
			form.Add("question_ids[]", fmt.Sprintf("%v", v))
		}
		return form, ""
	case strings.HasPrefix(ct, "multipart/form-data"):
		if err := c.Request().ParseMultipartForm(32 << 20); err != nil {
			return nil, "Badan formulir tidak valid."
		}
		return c.Request().Form, ""
	default:
		if err := c.Request().ParseForm(); err != nil {
			return nil, "Badan formulir tidak valid."
		}
		return c.Request().Form, ""
	}
}

// ComposeQuestions picks bank questions into the quiz. The body may be
// JSON, multipart or urlencoded (composeForm above); seq_<id> fields are
// optional — a question without one is appended after MAX(seq) of the quiz,
// sequential in arrival order. When the client declares a length= filter
// the same CHAR_LENGTH filter is re-applied server-side. Duplicates — in
// the submission or already composed — return 409 (spec CRUD / §6.8).
// The quiz must exist (404): the schema has no FKs, so without the check
// this would insert orphan quiz_questions rows.
func (t *Teacher) ComposeQuestions(c *echo.Context) error {
	quizID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || quizID == 0 {
		return fail(c, http.StatusBadRequest, ErrValidation, "ID kuis tidak valid.")
	}
	ctx := c.Request().Context()
	// missing quiz → 404 before anything is parsed or inserted (no FKs)
	var exists bool
	if err := t.DB.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM quizzes WHERE id = ?)`, quizID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return fail(c, http.StatusNotFound, ErrNotFound, "Kuis tidak ditemukan.")
	}
	form, errMsg := composeForm(c)
	if errMsg != "" {
		return fail(c, http.StatusBadRequest, ErrValidation, errMsg)
	}
	rawIDs := form["question_ids[]"]
	if len(rawIDs) == 0 {
		return fail(c, http.StatusBadRequest, ErrValidation, "Pilih setidaknya satu pertanyaan.")
	}
	bucket, werr := bucketFor(form.Get("length"))
	if werr != nil {
		return fail(c, http.StatusBadRequest, ErrValidation, "Filter panjang tidak valid.")
	}

	ids := make([]uint64, 0, len(rawIDs))
	seqs := make([]uint16, 0, len(rawIDs))
	hasSeq := make([]bool, 0, len(rawIDs))
	seen := make(map[uint64]bool, len(rawIDs))
	for _, raw := range rawIDs {
		qid, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || qid == 0 {
			return fail(c, http.StatusBadRequest, ErrValidation, "ID pertanyaan tidak valid.")
		}
		if seen[qid] {
			return fail(c, http.StatusConflict, ErrConflict, "Pertanyaan sudah ada di kuis ini.")
		}
		seen[qid] = true
		ids = append(ids, qid)
		// seq_<id> is optional: missing/empty appends after MAX(seq)
		rawSeq := form.Get("seq_" + strconv.FormatUint(qid, 10))
		if rawSeq == "" {
			seqs = append(seqs, 0)
			hasSeq = append(hasSeq, false)
			continue
		}
		seq, err := strconv.ParseUint(rawSeq, 10, 16)
		if err != nil {
			return fail(c, http.StatusBadRequest, ErrValidation, "Setiap pertanyaan memerlukan nomor urut.")
		}
		seqs = append(seqs, uint16(seq))
		hasSeq = append(hasSeq, true)
	}

	// server-side re-application of the client's length filter: every id
	// must exist AND sit in the declared bucket
	marks := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := t.DB.QueryContext(ctx,
		`SELECT id, CHAR_LENGTH(teks) FROM questions WHERE id IN (`+marks+`)`, args...)
	if err != nil {
		return err
	}
	found := make(map[uint64]int, len(ids))
	for rows.Next() {
		var qid uint64
		var ln int
		if err := rows.Scan(&qid, &ln); err != nil {
			rows.Close()
			return err
		}
		found[qid] = ln
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, id := range ids {
		ln, ok := found[id]
		if !ok {
			return fail(c, http.StatusBadRequest, ErrValidation, "Satu atau beberapa pertanyaan sudah tidak ada.")
		}
		if !bucket.includes(ln) {
			return fail(c, http.StatusBadRequest, ErrValidation,
				"Pertanyaan terpilih berada di luar filter panjang yang dipilih.")
		}
	}

	// questions without an explicit seq append after MAX(seq) of this quiz,
	// sequential in arrival order (MAX+1, MAX+2, …)
	needAppend := false
	for _, h := range hasSeq {
		if !h {
			needAppend = true
			break
		}
	}
	if needAppend {
		var maxSeq uint16
		if err := t.DB.QueryRowContext(ctx,
			`SELECT COALESCE(MAX(seq), 0) FROM quiz_questions WHERE quiz_id = ?`,
			quizID).Scan(&maxSeq); err != nil {
			return err
		}
		for i, h := range hasSeq {
			if !h {
				maxSeq++
				seqs[i] = maxSeq
			}
		}
	}

	tx, err := t.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	for i, id := range ids {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO quiz_questions (quiz_id, question_id, seq) VALUES (?, ?, ?)`,
			quizID, id, seqs[i]); err != nil {
			if isDuplicateKey(err) {
				tx.Rollback()
				return fail(c, http.StatusConflict, ErrConflict, "Pertanyaan sudah ada di kuis ini.")
			}
			tx.Rollback()
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	t.Store.Delete(cache.QuizSetKey(quizID))
	t.Store.Delete(cache.QuizStateKey(quizID))
	return ok(c, nil)
}

// --- POST /teacher/quiz/:id/questions/:qid/delete --------------------------

// RemoveQuestion drops one composed question. Guard: only while the quiz is
// nonaktif — deactivation requires zero participants, so a nonaktif quiz
// never holds attempts and removal is safe. The guarded DELETE plus the
// rows-affected classify mirror EditQuiz: missing quiz → 404, activated →
// 409 locked, not composed → 404.
func (t *Teacher) RemoveQuestion(c *echo.Context) error {
	quizID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || quizID == 0 {
		return fail(c, http.StatusBadRequest, ErrValidation, "ID kuis tidak valid.")
	}
	qid, err := strconv.ParseUint(c.Param("qid"), 10, 64)
	if err != nil || qid == 0 {
		return fail(c, http.StatusBadRequest, ErrValidation, "ID pertanyaan tidak valid.")
	}
	ctx := c.Request().Context()
	res, err := t.DB.ExecContext(ctx,
		`DELETE FROM quiz_questions WHERE quiz_id = ? AND question_id = ?
		   AND EXISTS (SELECT 1 FROM quizzes WHERE id = ? AND status = 'nonaktif')`,
		quizID, qid, quizID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// classify: missing quiz vs locked status vs not composed
		var status string
		err := t.DB.QueryRowContext(ctx, `SELECT status FROM quizzes WHERE id = ?`, quizID).Scan(&status)
		if errors.Is(err, sql.ErrNoRows) {
			return fail(c, http.StatusNotFound, ErrNotFound, "Kuis tidak ditemukan.")
		}
		if err != nil {
			return err
		}
		if status != "nonaktif" {
			return lockedConflict(c)
		}
		return fail(c, http.StatusNotFound, ErrNotFound, "Pertanyaan tidak ada di kuis ini.")
	}
	t.Store.Delete(cache.QuizSetKey(quizID))
	t.Store.Delete(cache.QuizStateKey(quizID))
	return ok(c, nil)
}

// --- POST /teacher/quiz/:id/questions/reorder ------------------------------

// ReorderQuestions rewrites the composed sequence from an explicit order —
// {"question_ids":[…]} (the manage page's up/down buttons) or a
// question_ids[] form. The payload MUST be exactly the quiz's composed set:
// an empty order, a duplicate, a foreign or a missing id are 400s; an
// unknown quiz is a 404. One transaction does everything — the quiz row is
// locked FOR UPDATE (status guard + serialization against a concurrent
// compose/delete), the composed set is read under the same lock, and seq is
// renumbered 1..N — so the stored order is contiguous and written atomically.
//
// This order is what loadQuestions hands every future attempt (ORDER BY
// seq); a RUNNING attempt's qorder snapshot is never rewritten (spec §6.8).
// The status guard mirrors RemoveQuestion: only a nonaktif quiz may be
// reordered, so a live attempt can never see its question order change.
func (t *Teacher) ReorderQuestions(c *echo.Context) error {
	quizID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || quizID == 0 {
		return fail(c, http.StatusBadRequest, ErrValidation, "ID kuis tidak valid.")
	}
	form, errMsg := composeForm(c)
	if errMsg != "" {
		return fail(c, http.StatusBadRequest, ErrValidation, errMsg)
	}
	rawIDs := form["question_ids[]"]
	if len(rawIDs) == 0 {
		return fail(c, http.StatusBadRequest, ErrValidation, "Pilih setidaknya satu pertanyaan.")
	}
	ids := make([]uint64, 0, len(rawIDs))
	seen := make(map[uint64]bool, len(rawIDs))
	for _, raw := range rawIDs {
		qid, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || qid == 0 {
			return fail(c, http.StatusBadRequest, ErrValidation, "ID pertanyaan tidak valid.")
		}
		if seen[qid] {
			return fail(c, http.StatusBadRequest, ErrValidation,
				"Pertanyaan ganda dalam urutan.")
		}
		seen[qid] = true
		ids = append(ids, qid)
	}

	ctx := c.Request().Context()
	tx, err := t.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	// quiz row first: missing → 404, and the status check runs under the
	// row lock so it cannot race activation
	var status string
	if err := tx.QueryRowContext(ctx,
		`SELECT status FROM quizzes WHERE id = ? FOR UPDATE`, quizID).Scan(&status); err != nil {
		tx.Rollback()
		if errors.Is(err, sql.ErrNoRows) {
			return fail(c, http.StatusNotFound, ErrNotFound, "Kuis tidak ditemukan.")
		}
		return err
	}
	if status != "nonaktif" {
		tx.Rollback()
		return lockedConflict(c)
	}

	// the composed set, locked: the payload must be that exact set
	rows, err := tx.QueryContext(ctx,
		`SELECT question_id FROM quiz_questions WHERE quiz_id = ? ORDER BY seq FOR UPDATE`,
		quizID)
	if err != nil {
		tx.Rollback()
		return err
	}
	composed := map[uint64]bool{}
	current := 0
	for rows.Next() {
		var qid uint64
		if err := rows.Scan(&qid); err != nil {
			rows.Close()
			tx.Rollback()
			return err
		}
		composed[qid] = true
		current++
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		tx.Rollback()
		return err
	}
	mismatch := current != len(ids)
	if !mismatch {
		for _, qid := range ids {
			if !composed[qid] {
				mismatch = true
				break
			}
		}
	}
	if mismatch {
		tx.Rollback()
		return fail(c, http.StatusBadRequest, ErrValidation,
			"Urutan pertanyaan tidak cocok dengan kuis ini.")
	}

	// renumber 1..N: idx_qq_order is a plain (non-unique) index, so the
	// in-place rewrite never trips a duplicate key against itself
	for i, qid := range ids {
		if _, err := tx.ExecContext(ctx,
			`UPDATE quiz_questions SET seq = ? WHERE quiz_id = ? AND question_id = ?`,
			i+1, quizID, qid); err != nil {
			tx.Rollback()
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	t.Store.Delete(cache.QuizSetKey(quizID))
	t.Store.Delete(cache.QuizStateKey(quizID))
	return ok(c, map[string]any{"question_ids": ids})
}

// --- GET /teacher/quiz/:id/qr ----------------------------------------------

// QuizQR renders the join-URL QR code as PNG (spec §7 join via QR).
func (t *Teacher) QuizQR(c *echo.Context) error {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		return fail(c, http.StatusNotFound, ErrNotFound, "Kuis tidak ditemukan.")
	}
	var code string
	if err := t.DB.QueryRowContext(c.Request().Context(),
		`SELECT code FROM quizzes WHERE id = ?`, id).Scan(&code); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fail(c, http.StatusNotFound, ErrNotFound, "Kuis tidak ditemukan.")
		}
		return err
	}
	joinURL := requestScheme(c.Request()) + "://" + c.Request().Host + "/quiz/" + code
	png, err := qrcode.Encode(joinURL, qrcode.Medium, 320)
	if err != nil {
		return err
	}
	return c.Blob(http.StatusOK, "image/png", png)
}

// --- POST /teacher/quiz/:id/participants/add -------------------------------

// AddParticipant manually adds a student (spec §6.12): unknown user → 404,
// existing participant → 409, else registered (open) / pending (approve).
func (t *Teacher) AddParticipant(c *echo.Context) error {
	quizID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		return fail(c, http.StatusBadRequest, ErrValidation, "ID kuis tidak valid.")
	}
	var payload struct {
		Username string `json:"username"`
	}
	if err := c.Bind(&payload); err != nil {
		return fail(c, http.StatusBadRequest, ErrValidation, "Badan permintaan tidak valid.")
	}
	username := strings.TrimSpace(payload.Username)
	if username == "" {
		return fail(c, http.StatusBadRequest, ErrValidation, "Username wajib diisi.")
	}
	ctx := c.Request().Context()
	tx, err := t.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	var joinMode string
	if err := tx.QueryRowContext(ctx,
		`SELECT join_mode FROM quizzes WHERE id = ?`, quizID).Scan(&joinMode); err != nil {
		tx.Rollback()
		if errors.Is(err, sql.ErrNoRows) {
			return fail(c, http.StatusNotFound, ErrNotFound, "Kuis tidak ditemukan.")
		}
		return err
	}
	var userID uint64
	if err := tx.QueryRowContext(ctx,
		`SELECT id FROM users WHERE username = ?`, username).Scan(&userID); err != nil {
		tx.Rollback()
		if errors.Is(err, sql.ErrNoRows) {
			return fail(c, http.StatusNotFound, ErrNotFound, "Murid tidak ditemukan.")
		}
		return err
	}
	var exists bool
	if err := tx.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM participants WHERE quiz_id = ? AND user_id = ?)`,
		quizID, userID).Scan(&exists); err != nil {
		tx.Rollback()
		return err
	}
	if exists {
		tx.Rollback()
		return fail(c, http.StatusConflict, ErrConflict, "Sudah menjadi peserta.")
	}
	status := "registered"
	if joinMode == "approve" {
		status = "pending"
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO participants (quiz_id, user_id, attempt_no, status) VALUES (?, ?, 1, ?)`,
		quizID, userID, status); err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	t.Store.Delete(cache.QuizStateKey(quizID))
	return ok(c, nil)
}

// --- activation half of POST /teacher/quiz/:id/status ----------------------

// activateQuiz is the nonaktif/aktif half of the status route (spec §7):
// aktif requires ≥1 question (400), applies the server-side forces, and
// flips only from nonaktif; nonaktif requires zero participants (409
// otherwise). The selesai close lives in personal.go (Global.SetQuizStatus).
func activateQuiz(c *echo.Context, db *sql.DB, store *cache.Store, id uint64, want string) error {
	ctx := c.Request().Context()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	classifyNoop := func() error { // concurrent transition: first wins, loser no-ops
		tx.Rollback()
		var exists bool
		if err := db.QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM quizzes WHERE id = ?)`, id).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return fail(c, http.StatusNotFound, ErrNotFound, "Kuis tidak ditemukan.")
		}
		return ok(c, nil)
	}

	switch want {
	case "aktif":
		var n int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM quiz_questions WHERE quiz_id = ?`, id).Scan(&n); err != nil {
			tx.Rollback()
			return err
		}
		if n == 0 {
			tx.Rollback()
			return fail(c, http.StatusBadRequest, ErrValidation, "Tambahkan setidaknya satu pertanyaan.")
		}
		var status string
		var shuffleQ, shuffleO, timerOn bool
		var total uint32
		err := tx.QueryRowContext(ctx,
			`SELECT status, shuffle_questions, shuffle_options, timer_on, total_seconds
			 FROM quizzes WHERE id = ? FOR UPDATE`, id).
			Scan(&status, &shuffleQ, &shuffleO, &timerOn, &total)
		if errors.Is(err, sql.ErrNoRows) {
			tx.Rollback()
			return fail(c, http.StatusNotFound, ErrNotFound, "Kuis tidak ditemukan.")
		}
		if err != nil {
			tx.Rollback()
			return err
		}
		switch status {
		case "nonaktif":
			// fall through to the guarded flip
		case "aktif", "berjalan":
			return classifyNoop()
		case "selesai":
			tx.Rollback()
			return fail(c, http.StatusConflict, ErrConflict, "Kuis sudah berakhir.")
		}
		// server-side forces at activation (spec §6.7 / §9)
		if !shuffleQ {
			shuffleO = false
		}
		if !timerOn {
			total = 0
		}
		res, err := tx.ExecContext(ctx,
			`UPDATE quizzes SET status = 'aktif', shuffle_options = ?, total_seconds = ?
			 WHERE id = ? AND status = 'nonaktif'`, shuffleO, total, id)
		if err != nil {
			tx.Rollback()
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return classifyNoop()
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		store.Delete(cache.QuizListKey())
		store.Delete(cache.QuizSetKey(id))
		store.Delete(cache.QuizStateKey(id))
		return ok(c, nil)

	case "nonaktif":
		var status string
		var parts bool
		err := tx.QueryRowContext(ctx,
			`SELECT status, EXISTS(SELECT 1 FROM participants WHERE quiz_id = quizzes.id)
			 FROM quizzes WHERE id = ? FOR UPDATE`, id).Scan(&status, &parts)
		if errors.Is(err, sql.ErrNoRows) {
			tx.Rollback()
			return fail(c, http.StatusNotFound, ErrNotFound, "Kuis tidak ditemukan.")
		}
		if err != nil {
			tx.Rollback()
			return err
		}
		switch status {
		case "nonaktif":
			return classifyNoop() // already there — idempotent success
		case "berjalan":
			tx.Rollback()
			return fail(c, http.StatusConflict, ErrConflict, "Hentikan kuis sebelum menonaktifkan.")
		case "selesai":
			tx.Rollback()
			return fail(c, http.StatusConflict, ErrConflict, "Kuis sudah berakhir.")
		}
		if parts {
			tx.Rollback()
			return fail(c, http.StatusConflict, ErrConflict,
				"Kuis memiliki peserta — penonaktifan dikunci.")
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE quizzes SET status = 'nonaktif' WHERE id = ? AND status = 'aktif'`, id); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		store.Delete(cache.QuizListKey())
		store.Delete(cache.QuizSetKey(id))
		store.Delete(cache.QuizStateKey(id))
		return ok(c, nil)

	default:
		tx.Rollback()
		return fail(c, http.StatusBadRequest, ErrValidation, "Status tidak valid.")
	}
}

// --- POST /teacher/quiz/:id/delete -----------------------------------------

// DeleteQuiz drops a quiz with no participants (spec CRUD: "Delete quiz
// with attempts → 409"). Question rows go in the same transaction.
func (t *Teacher) DeleteQuiz(c *echo.Context) error {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		return fail(c, http.StatusBadRequest, ErrValidation, "ID kuis tidak valid.")
	}
	ctx := c.Request().Context()
	tx, err := t.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx,
		`DELETE FROM quizzes WHERE id = ?
		 AND NOT EXISTS (SELECT 1 FROM participants WHERE quiz_id = quizzes.id)`, id)
	if err != nil {
		tx.Rollback()
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		tx.Rollback()
		var exists bool
		if err := t.DB.QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM quizzes WHERE id = ?)`, id).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return fail(c, http.StatusNotFound, ErrNotFound, "Kuis tidak ditemukan.")
		}
		return fail(c, http.StatusConflict, ErrConflict,
			"Kuis memiliki peserta — penghapusan dikunci.")
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM quiz_questions WHERE quiz_id = ?`, id); err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	t.Store.Delete(cache.QuizListKey())
	t.Store.Delete(cache.QuizSetKey(id))
	t.Store.Delete(cache.QuizStateKey(id))
	return ok(c, nil)
}
