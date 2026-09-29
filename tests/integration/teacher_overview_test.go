package integration

import (
	"database/sql"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/labstack/echo/v5"

	"quiz/bootstrap"
	mw "quiz/internal/middleware"
	"quiz/web"
)

// --- payload decoding --------------------------------------------------------

type ovKV struct {
	Label string `json:"label"`
	Value int    `json:"value"`
}

type ovRefOption struct {
	ID   uint64 `json:"id"`
	Nama string `json:"nama"`
}

type ovStatusOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type ovQuizItem struct {
	ID      uint64 `json:"id"`
	Judul   string `json:"judul"`
	Code    string `json:"code"`
	Status  string `json:"status"`
	Label   string `json:"label"`
	Chip    string `json:"chip"`
	Peserta int    `json:"peserta"`
}

type ovOverview struct {
	Filters struct {
		Kelas   []ovRefOption    `json:"kelas"`
		Jurusan []ovRefOption    `json:"jurusan"`
		Status  []ovStatusOption `json:"status"`
	} `json:"filters"`
	Applied struct {
		Kelas   uint32 `json:"kelas"`
		Jurusan uint32 `json:"jurusan"`
		Status  string `json:"status"`
	} `json:"applied"`
	Summary struct {
		TotalMurid         int `json:"total_murid"`
		MuridNonaktif      int `json:"murid_nonaktif"`
		QuizAktif          int `json:"quiz_aktif"`
		QuizNonaktif       int `json:"quiz_nonaktif"`
		PesertaMengerjakan int `json:"peserta_mengerjakan"`
	} `json:"summary"`
	PerKelas   []ovKV       `json:"per_kelas"`
	PerJurusan []ovKV       `json:"per_jurusan"`
	Quizzes    []ovQuizItem `json:"quizzes"`
	Rekap      struct {
		Rata2    float64 `json:"rata2"`
		Jumlah   int     `json:"jumlah"`
		NilaiMin float64 `json:"nilai_min"`
		NilaiMax float64 `json:"nilai_max"`
		Buckets  []ovKV  `json:"buckets"`
	} `json:"rekap_nilai"`
}

// --- fixture -----------------------------------------------------------------

// overviewFixture seeds two active students in different class/major
// combinations, one inactive student, three quizzes (one per relevant
// status) and finished participants with known scores.
//
//	users:  A kelas1/jurusan1 aktif · B kelas2/jurusan2 aktif · C kelas3/jurusan1 NONAKTIF
//	quizzes (ids 1..3, listed newest-first): q1 aktif · q2 nonaktif · q3 selesai
//	parts:  q1/A 55.5 · q1/B 80 · q2/A 95.25 · q2/C 40 · q2/B registered (never counted)
func overviewFixture(t *testing.T) (*httptest.Server, *sql.DB, *http.Cookie) {
	t.Helper()
	ts, pool, _, _ := quizFixture(t)
	guru := guruLogin(t, ts)

	insert := func(username string, kelas, jurusan uint32, aktif bool) {
		t.Helper()
		if _, err := pool.Exec(
			`INSERT INTO users (username, email, password_hash, nama_lengkap,
				kelas_id, jurusan_id, aktif)
			 VALUES (?, ?, 'x', ?, ?, ?, ?)`,
			username, username+"@overview.test", username, kelas, jurusan, aktif); err != nil {
			t.Fatalf("insert user %s: %v", username, err)
		}
	}
	refID := func(table, nama string) uint32 {
		t.Helper()
		var id uint32
		if err := pool.QueryRow(
			`SELECT id FROM `+table+` WHERE nama = ? ORDER BY id LIMIT 1`, nama).
			Scan(&id); err != nil {
			t.Fatalf("ref id %s/%s: %v", table, nama, err)
		}
		return id
	}
	insert("ovA", refID("ref_kelas", "Grade 10"), refID("ref_jurusan", "Science"), true)
	insert("ovB", refID("ref_kelas", "Grade 11"), refID("ref_jurusan", "Social"), true)
	insert("ovC", refID("ref_kelas", "Grade 12"), refID("ref_jurusan", "Science"), false)

	quiz := func(code, judul, status string) uint64 {
		t.Helper()
		res, err := pool.Exec(
			`INSERT INTO quizzes (code, judul, timer_type, status)
			 VALUES (?, ?, 'global', ?)`, code, judul, status)
		if err != nil {
			t.Fatalf("insert quiz %s: %v", judul, err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatalf("quiz id: %v", err)
		}
		return uint64(id)
	}
	q1 := quiz("OvwA01", "Overview Aktif Quiz", "aktif")
	q2 := quiz("OvwA02", "Overview Nonaktif Quiz", "nonaktif")
	quiz("OvwA03", "Overview Selesai Quiz", "selesai")

	part := func(quizID, userID uint64, status string, score *float64) {
		t.Helper()
		if _, err := pool.Exec(
			`INSERT INTO participants (quiz_id, user_id, attempt_no, status, final_score)
			 VALUES (?, ?, 1, ?, ?)`, quizID, userID, status, score); err != nil {
			t.Fatalf("insert participant: %v", err)
		}
	}
	var userA, userB, userC uint64
	for u, dst := range map[string]*uint64{"ovA": &userA, "ovB": &userB, "ovC": &userC} {
		if err := pool.QueryRow(`SELECT id FROM users WHERE username = ?`, u).Scan(dst); err != nil {
			t.Fatalf("user id %s: %v", u, err)
		}
	}
	f := func(v float64) *float64 { return &v }
	part(q1, userA, "selesai", f(55.5))
	part(q1, userB, "selesai", f(80))
	part(q2, userA, "selesai", f(95.25))
	part(q2, userC, "selesai", f(40)) // inactive user: still a finished attempt
	part(q2, userB, "registered", nil)

	return ts, pool, guru
}

func overviewGET(t *testing.T, ts *httptest.Server, ck *http.Cookie, query string) ovOverview {
	t.Helper()
	resp, body := getWith(t, ts.URL+"/teacher/api/overview"+query, ck)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET overview%s = %d: %s", query, resp.StatusCode, body)
	}
	env := decodeEnv(t, body)
	if !env.OK {
		t.Fatalf("overview%s envelope ok=false: %s", query, body)
	}
	var ov ovOverview
	if err := json.Unmarshal(env.Data, &ov); err != nil {
		t.Fatalf("overview data: %v (%s)", err, body)
	}
	return ov
}

func approx(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}

func kvValues(kvs []ovKV) []int {
	out := make([]int, len(kvs))
	for i, kv := range kvs {
		out[i] = kv.Value
	}
	return out
}

func assertKV(t *testing.T, name string, kvs []ovKV, want []int) {
	t.Helper()
	got := kvValues(kvs)
	if len(got) != len(want) {
		t.Fatalf("%s labels/values = %+v, want %v", name, kvs, want)
	}
	for i, v := range want {
		if got[i] != v {
			t.Errorf("%s[%d] (%q) = %d, want %d", name, i, kvs[i].Label, got[i], v)
		}
	}
}

// kvValue returns the value for one ref label (charts are label-addressed;
// the shared DB may hold extra ref rows created by other tests).
func kvValue(t *testing.T, name string, kvs []ovKV, label string) int {
	t.Helper()
	for _, kv := range kvs {
		if kv.Label == label {
			return kv.Value
		}
	}
	t.Fatalf("%s has no row %q (%d rows)", name, label, len(kvs))
	return -1
}

// assertSeededChart pins the seeded rows by label and requires every
// extra ref row (created by other tests) to sit at zero in this fixture.
func assertSeededChart(t *testing.T, name string, kvs []ovKV,
	seeded []string, want map[string]int) {
	t.Helper()
	if len(kvs) < len(seeded) {
		t.Fatalf("%s rows = %d, want at least %d", name, len(kvs), len(seeded))
	}
	for i, label := range seeded {
		if kvs[i].Label != label {
			t.Errorf("%s[%d] = %q, want %q (seeded refs order by id)", name, i, kvs[i].Label, label)
		}
		if got := kvValue(t, name, kvs, label); got != want[label] {
			t.Errorf("%s %q = %d, want %d", name, label, got, want[label])
		}
	}
	for _, kv := range kvs[len(seeded):] {
		if kv.Value != 0 {
			t.Errorf("%s extra row %q = %d, want 0 (fixture users are only in the seeded refs)",
				name, kv.Label, kv.Value)
		}
	}
}

// --- tests -------------------------------------------------------------------

// The endpoint lives behind AuthTeacher: guests bounce to /login.
func TestTeacherOverviewRequiresGuru(t *testing.T) {
	ts, _, _, _ := quizFixture(t)
	resp, body := getWith(t, ts.URL+"/teacher/api/overview")
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/login" {
		t.Errorf("guest GET = %d %q, want 302 /login (%s)",
			resp.StatusCode, resp.Header.Get("Location"), body)
	}
}

// Envelope keys, value sanity against the fixture, label/chip from
// quizChip, and every filter changing exactly the right aggregates.
func TestTeacherOverviewEnvelopeAndFilters(t *testing.T) {
	ts, pool, guru := overviewFixture(t)

	// fixture ids (never assume auto_increment state)
	quizID := func(judul string) uint64 {
		t.Helper()
		var id uint64
		if err := pool.QueryRow(`SELECT id FROM quizzes WHERE judul = ?`, judul).
			Scan(&id); err != nil {
			t.Fatalf("quiz id %q: %v", judul, err)
		}
		return id
	}
	q1 := quizID("Overview Aktif Quiz")
	q2 := quizID("Overview Nonaktif Quiz")
	q3 := quizID("Overview Selesai Quiz")

	// the fixture's seeded reference ids (never truncated by quizFixture)
	var kelas2, jurusan1 uint32
	if err := pool.QueryRow(`SELECT id FROM ref_kelas WHERE nama = 'Grade 11'
			ORDER BY id LIMIT 1`).
		Scan(&kelas2); err != nil {
		t.Fatalf("Grade 11 id: %v", err)
	}
	if err := pool.QueryRow(`SELECT id FROM ref_jurusan WHERE nama = 'Science'
			ORDER BY id LIMIT 1`).
		Scan(&jurusan1); err != nil {
		t.Fatalf("Science id: %v", err)
	}

	t.Run("default payload", func(t *testing.T) {
		ov := overviewGET(t, ts, guru, "")

		// filter option lists (pinned contract; the shared test DB may carry
		// extra ref rows other tests created — the seeded refs come first by id)
		var kelasNames, jurusanNames []string
		for _, r := range ov.Filters.Kelas {
			kelasNames = append(kelasNames, r.Nama)
		}
		for _, r := range ov.Filters.Jurusan {
			jurusanNames = append(jurusanNames, r.Nama)
		}
		if len(kelasNames) < 3 || kelasNames[0] != "Grade 10" ||
			kelasNames[1] != "Grade 11" || kelasNames[2] != "Grade 12" {
			t.Errorf("filters.kelas = %v", kelasNames)
		}
		if len(jurusanNames) < 4 || jurusanNames[0] != "Science" ||
			jurusanNames[1] != "Social" || jurusanNames[2] != "Language" ||
			jurusanNames[3] != "Vocational" {
			t.Errorf("filters.jurusan = %v", jurusanNames)
		}
		wantStatus := []ovStatusOption{
			{Value: "", Label: "All"},
			{Value: "aktif", Label: "Active"},
			{Value: "berjalan", Label: "Running"},
			{Value: "selesai", Label: "Finished"},
			{Value: "nonaktif", Label: "Not active"},
		}
		if len(ov.Filters.Status) != len(wantStatus) {
			t.Fatalf("filters.status = %+v", ov.Filters.Status)
		}
		for i, want := range wantStatus {
			if ov.Filters.Status[i] != want {
				t.Errorf("filters.status[%d] = %+v, want %+v", i, ov.Filters.Status[i], want)
			}
		}
		if ov.Applied.Kelas != 0 || ov.Applied.Jurusan != 0 || ov.Applied.Status != "" {
			t.Errorf("applied = %+v, want zero values", ov.Applied)
		}

		// summary: 2 active + 1 inactive student; 1 running vs 2 rest; 4 finished
		if ov.Summary.TotalMurid != 2 || ov.Summary.MuridNonaktif != 1 {
			t.Errorf("murid total/nonaktif = %d/%d, want 2/1",
				ov.Summary.TotalMurid, ov.Summary.MuridNonaktif)
		}
		if ov.Summary.QuizAktif != 1 || ov.Summary.QuizNonaktif != 2 {
			t.Errorf("quiz aktif/nonaktif = %d/%d, want 1/2",
				ov.Summary.QuizAktif, ov.Summary.QuizNonaktif)
		}
		if ov.Summary.PesertaMengerjakan != 4 {
			t.Errorf("peserta_mengerjakan = %d, want 4", ov.Summary.PesertaMengerjakan)
		}

		// charts: every ref row emitted, active students only
		assertSeededChart(t, "per_kelas", ov.PerKelas,
			[]string{"Grade 10", "Grade 11", "Grade 12"},
			map[string]int{"Grade 10": 1, "Grade 11": 1, "Grade 12": 0})
		assertSeededChart(t, "per_jurusan", ov.PerJurusan,
			[]string{"Science", "Social", "Language", "Vocational"},
			map[string]int{"Science": 1, "Social": 1, "Language": 0, "Vocational": 0})

		// quiz list: newest first (created_at DESC, id DESC tiebreak),
		// label/chip via quizChip
		if len(ov.Quizzes) != 3 {
			t.Fatalf("quizzes len = %d: %+v", len(ov.Quizzes), ov.Quizzes)
		}
		wantIDs := []uint64{q3, q2, q1}
		for i, id := range wantIDs {
			if ov.Quizzes[i].ID != id {
				t.Errorf("quizzes[%d].id = %d, want %d (created_at DESC)", i, ov.Quizzes[i].ID, id)
			}
		}
		byID := map[uint64]ovQuizItem{}
		for _, q := range ov.Quizzes {
			byID[q.ID] = q
		}
		if q := byID[q1]; q.Label != "Active" || q.Chip != "badge bg-ok" || q.Peserta != 2 {
			t.Errorf("aktif quiz label/chip/peserta = %q/%q/%d", q.Label, q.Chip, q.Peserta)
		}
		if q := byID[q2]; q.Label != "Not active" || q.Chip != "badge text-bg-secondary" ||
			q.Peserta != 2 {
			t.Errorf("nonaktif quiz label/chip/peserta = %q/%q/%d", q.Label, q.Chip, q.Peserta)
		}
		if q := byID[q3]; q.Label != "Finished" || q.Chip != "badge border text-secondary" ||
			q.Peserta != 0 {
			t.Errorf("selesai quiz label/chip/peserta = %q/%q/%d", q.Label, q.Chip, q.Peserta)
		}

		// rekap over {55.5, 80, 95.25, 40}
		if ov.Rekap.Jumlah != 4 {
			t.Errorf("jumlah = %d, want 4", ov.Rekap.Jumlah)
		}
		approx(t, "rata2", ov.Rekap.Rata2, 67.69)
		approx(t, "nilai_min", ov.Rekap.NilaiMin, 40)
		approx(t, "nilai_max", ov.Rekap.NilaiMax, 95.25)
		if len(ov.Rekap.Buckets) != 5 {
			t.Fatalf("buckets = %+v", ov.Rekap.Buckets)
		}
		wantBuckets := []int{0, 0, 2, 0, 2} // 55.5+40 → 40–59; 80+95.25 → 80–100
		assertKV(t, "buckets", ov.Rekap.Buckets, wantBuckets)
		if ov.Rekap.Buckets[0].Label != "0–19" || ov.Rekap.Buckets[4].Label != "80–100" {
			t.Errorf("bucket labels = %+v", ov.Rekap.Buckets)
		}
	})

	t.Run("kelas filter narrows users charts, counts and rekap", func(t *testing.T) {
		ov := overviewGET(t, ts, guru, "?kelas="+url.QueryEscape(strconv.Itoa(int(kelas2))))
		if ov.Applied.Kelas != kelas2 {
			t.Errorf("applied.kelas = %d, want %d", ov.Applied.Kelas, kelas2)
		}
		if ov.Summary.TotalMurid != 1 || ov.Summary.MuridNonaktif != 0 {
			t.Errorf("murid = %d/%d, want 1/0", ov.Summary.TotalMurid, ov.Summary.MuridNonaktif)
		}
		assertSeededChart(t, "per_kelas", ov.PerKelas,
			[]string{"Grade 10", "Grade 11", "Grade 12"},
			map[string]int{"Grade 10": 0, "Grade 11": 1, "Grade 12": 0}) // only Grade 11
		assertSeededChart(t, "per_jurusan", ov.PerJurusan,
			[]string{"Science", "Social", "Language", "Vocational"},
			map[string]int{"Science": 0, "Social": 1, "Language": 0, "Vocational": 0})
		// quiz universe untouched by a user filter
		if ov.Summary.QuizAktif != 1 || ov.Summary.QuizNonaktif != 2 || len(ov.Quizzes) != 3 {
			t.Errorf("quiz universe changed under kelas filter: aktif/nonaktif/list = %d/%d/%d",
				ov.Summary.QuizAktif, ov.Summary.QuizNonaktif, len(ov.Quizzes))
		}
		if ov.Summary.PesertaMengerjakan != 1 {
			t.Errorf("peserta = %d, want 1 (only B's finished attempt)", ov.Summary.PesertaMengerjakan)
		}
		byID := map[uint64]int{}
		for _, q := range ov.Quizzes {
			byID[q.ID] = q.Peserta
		}
		if byID[q1] != 1 || byID[q2] != 0 {
			t.Errorf("per-quiz peserta = %+v, want aktif=1 nonaktif=0", byID)
		}
		if ov.Rekap.Jumlah != 1 {
			t.Errorf("jumlah = %d, want 1", ov.Rekap.Jumlah)
		}
		approx(t, "rata2", ov.Rekap.Rata2, 80)
		approx(t, "nilai_min", ov.Rekap.NilaiMin, 80)
		approx(t, "nilai_max", ov.Rekap.NilaiMax, 80)
	})

	t.Run("jurusan filter cross-filters the per-kelas chart", func(t *testing.T) {
		ov := overviewGET(t, ts, guru, "?jurusan="+url.QueryEscape(strconv.Itoa(int(jurusan1))))
		// A active + C inactive share Science
		if ov.Summary.TotalMurid != 1 || ov.Summary.MuridNonaktif != 1 {
			t.Errorf("murid = %d/%d, want 1/1", ov.Summary.TotalMurid, ov.Summary.MuridNonaktif)
		}
		assertSeededChart(t, "per_kelas", ov.PerKelas,
			[]string{"Grade 10", "Grade 11", "Grade 12"},
			map[string]int{"Grade 10": 1, "Grade 11": 0, "Grade 12": 0}) // Grade 11 drops out
		assertSeededChart(t, "per_jurusan", ov.PerJurusan,
			[]string{"Science", "Social", "Language", "Vocational"},
			map[string]int{"Science": 1, "Social": 0, "Language": 0, "Vocational": 0})
		if ov.Summary.PesertaMengerjakan != 3 {
			t.Errorf("peserta = %d, want 3 (A×2 + C×1)", ov.Summary.PesertaMengerjakan)
		}
		if ov.Rekap.Jumlah != 3 {
			t.Errorf("jumlah = %d, want 3", ov.Rekap.Jumlah)
		}
		approx(t, "rata2", ov.Rekap.Rata2, 63.58) // (55.5+95.25+40)/3
	})

	t.Run("status filter narrows only the quiz universe", func(t *testing.T) {
		ov := overviewGET(t, ts, guru, "?status=aktif")
		if ov.Applied.Status != "aktif" {
			t.Errorf("applied.status = %q", ov.Applied.Status)
		}
		if ov.Summary.QuizAktif != 1 || ov.Summary.QuizNonaktif != 0 {
			t.Errorf("quiz aktif/nonaktif = %d/%d, want 1/0",
				ov.Summary.QuizAktif, ov.Summary.QuizNonaktif)
		}
		if len(ov.Quizzes) != 1 || ov.Quizzes[0].ID != q1 {
			t.Fatalf("list = %+v, want only the aktif quiz", ov.Quizzes)
		}
		// users charts never see the status filter
		if ov.Summary.TotalMurid != 2 || ov.Summary.MuridNonaktif != 1 {
			t.Errorf("murid = %d/%d, want 2/1", ov.Summary.TotalMurid, ov.Summary.MuridNonaktif)
		}
		// peserta/rekap follow the quiz join: only q1's two finished attempts
		if ov.Summary.PesertaMengerjakan != 2 {
			t.Errorf("peserta = %d, want 2", ov.Summary.PesertaMengerjakan)
		}
		if ov.Rekap.Jumlah != 2 {
			t.Errorf("jumlah = %d, want 2", ov.Rekap.Jumlah)
		}
		approx(t, "rata2", ov.Rekap.Rata2, 67.75) // (55.5+80)/2
	})

	t.Run("unknown numeric id is a normal empty-ish result", func(t *testing.T) {
		ov := overviewGET(t, ts, guru, "?kelas=999")
		if ov.Summary.TotalMurid != 0 || ov.Summary.PesertaMengerjakan != 0 ||
			ov.Rekap.Jumlah != 0 {
			t.Errorf("unknown-id summary = %+v, want zeros", ov.Summary)
		}
		assertSeededChart(t, "per_kelas", ov.PerKelas,
			[]string{"Grade 10", "Grade 11", "Grade 12"},
			map[string]int{"Grade 10": 0, "Grade 11": 0, "Grade 12": 0})
		if len(ov.Quizzes) != 3 {
			t.Errorf("quiz list = %d rows, want the full status universe", len(ov.Quizzes))
		}
	})
}

// Malformed params are 400 VALIDATION; the envelope contract is exact.
func TestTeacherOverviewValidation(t *testing.T) {
	ts, _, guru := overviewFixture(t)
	for _, query := range []string{
		"?kelas=abc", "?jurusan=1.5", "?jurusan=-2", "?status=running", "?status=AKTIF",
	} {
		resp, body := getWith(t, ts.URL+"/teacher/api/overview"+query, guru)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("GET overview%s = %d, want 400 (%s)", query, resp.StatusCode, body)
		}
		env := decodeEnv(t, body)
		if env.OK {
			t.Errorf("GET overview%s: ok = true, want false", query)
		}
		if env.Error != "VALIDATION" {
			t.Errorf("GET overview%s error = %q, want VALIDATION", query, env.Error)
		}
	}
}

// The API path is not under a static mount, so the StaticCache middleware
// sends the exact "everything else" branch header before next().
func TestTeacherOverviewCacheControlNoStore(t *testing.T) {
	// same mounts as production (cmd/server staticMounts) — the point is the
	// API path is NOT under any of them
	mounts := []mw.StaticMount{
		{Path: "/assets", FS: echo.MustSubFS(web.FS, "vendor")},
		{Path: "/css", FS: echo.MustSubFS(web.FS, "css")},
		{Path: "/js", FS: echo.MustSubFS(web.FS, "js")},
		{Path: "/bootstrap", FS: bootstrap.FS},
	}
	e := echo.New()
	e.Use(mw.StaticCache(mounts, map[string]string{}))
	e.GET("/teacher/api/overview", func(c *echo.Context) error {
		return c.JSON(http.StatusOK, map[string]any{"ok": true})
	})
	req := httptest.NewRequest(http.MethodGet, "/teacher/api/overview", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want %q (assets.go non-mount branch)", got, "no-store")
	}
}
