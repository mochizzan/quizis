package integration

import (
	"bytes"
	"database/sql"
	"encoding/csv"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/xuri/excelize/v2"
)

// exportFixture builds the workbook fixture: a 3-question quiz (pg keyed
// scalar 0, multi keyed [0,2], one essay whose TEXT starts with "=" for the
// injection-guard pin), one finished+scored student and one joined-but-
// never-attempted student, on two distinct classes.
func exportFixture(t *testing.T) (*httptest.Server, *http.Cookie, *sql.DB, uint64, string) {
	t.Helper()
	ts, _, pool := globalFixture(t)
	ck := guruLogin(t, ts)
	quizID := createQuiz(t, ts, ck, quizForm("Sheets export"))
	pg := addQuestion(t, ts, pool, ck, "Pick A", "pg", []string{"A", "B"}, []int{0})
	multi := addQuestion(t, ts, pool, ck, "Pick A and C", "multi",
		[]string{"A", "B", "C"}, []int{0, 2})
	essay := addQuestion(t, ts, pool, ck, "=SUM(A1)", "essay", nil, nil)
	compose(t, ts, ck, quizID, pg, multi, essay)
	if resp, body := setStatus(t, ts, ck, quizID, "aktif"); resp.StatusCode != http.StatusOK ||
		!decodeEnv(t, body).OK {
		t.Fatalf("activate = %d: %s", resp.StatusCode, body)
	}
	code := joinCode(t, pool, quizID)

	st1 := studentCookie(t, pool, "sheets-student-one")
	st2 := studentCookie(t, pool, "sheets-student-two")
	for i, st := range []*http.Cookie{st1, st2} {
		if resp, body := postJoin(t, ts, code, st); resp.StatusCode != http.StatusOK {
			t.Fatalf("join %d = %d: %s", i, resp.StatusCode, body)
		}
	}
	startGlobal(t, ts, ck, quizID)
	// st1 answers the pg correctly and writes the essay; the MULTI is left
	// UNANSWERED — on the finished (scored) attempt its Q Score must be 0
	// (unanswered = wrong in the engine). The essay is NOT graded here:
	// each test grades it via gradeEssay to pin both the ungraded (blank
	// cell, Total = score_auto) and graded (80/total contribution,
	// Total = final) states of the uniform Q Score rule.
	steps := []struct {
		qid uint64
		ans any
	}{
		{pg, []int{0}},
		{essay, "Typed my essay."},
	}
	for _, s := range steps {
		if resp, body := answerPost(t, ts, code, st1, s.qid, s.ans); resp.StatusCode != http.StatusOK {
			t.Fatalf("answer = %d: %s", resp.StatusCode, body)
		}
	}
	if resp, body := postJSON(t, ts.URL+"/quiz/"+code+"/finish", nil, st1); resp.StatusCode != http.StatusOK {
		t.Fatalf("finish = %d: %s", resp.StatusCode, body)
	}
	// distinct classes for the Rekap per kelas block — ref tables are never
	// truncated by the fixtures, so seed fresh rows and reference them by id
	for _, s := range []struct{ username, kelas string }{
		{"sheets-student-one", "Kelas Satu"},
		{"sheets-student-two", "Kelas Dua"},
	} {
		res, err := pool.Exec(`INSERT INTO ref_kelas (nama) VALUES (?)`, s.kelas)
		if err != nil {
			t.Fatalf("seed kelas: %v", err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatalf("kelas id: %v", err)
		}
		if _, err := pool.Exec(`UPDATE users SET kelas_id = ? WHERE username = ?`,
			id, s.username); err != nil {
			t.Fatalf("assign kelas: %v", err)
		}
	}
	return ts, ck, pool, quizID, code
}

// gradeEssay scores the fixture's single essay (the only answers row with
// is_correct IS NULL): GradeAnswer stores the raw 0–100 score and computes
// final_score = score_auto + score/total.
func gradeEssay(t *testing.T, ts *httptest.Server, pool *sql.DB,
	ck *http.Cookie, quizID uint64, score float64,
) {
	t.Helper()
	var answerID uint64
	if err := pool.QueryRow(`SELECT a.id FROM answers a
		JOIN participants p ON p.id = a.participant_id
		WHERE p.quiz_id = ? AND a.is_correct IS NULL`, quizID).Scan(&answerID); err != nil {
		t.Fatalf("essay answer id: %v", err)
	}
	if resp, body := postJSON(t,
		fmt.Sprintf("%s/teacher/quiz/%d/grading/%d", ts.URL, quizID, answerID),
		map[string]any{"score": score}, ck); resp.StatusCode != http.StatusOK {
		t.Fatalf("grade = %d: %s", resp.StatusCode, body)
	}
}

// openExport reopens an XLSX response body with RAW cell values so numeric
// pins compare against the stored floats (format strings only decorate).
func openExport(t *testing.T, body string) *excelize.File {
	t.Helper()
	path := filepath.Join(t.TempDir(), "export.xlsx")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	f, err := excelize.OpenFile(path, excelize.Options{RawCellValue: true})
	if err != nil {
		t.Fatalf("reopen xlsx: %v", err)
	}
	return f
}

// assertSheetRow0 pins a sheet's header row cell by cell.
func assertSheetRow0(t *testing.T, f *excelize.File, sheet string, want []string) {
	t.Helper()
	rows, err := f.GetRows(sheet)
	if err != nil || len(rows) < 1 {
		t.Fatalf("%s rows = %d (%v)", sheet, len(rows), err)
	}
	if len(rows[0]) != len(want) {
		t.Fatalf("%s header has %d cells, want %d: %q", sheet, len(rows[0]), len(want), rows[0])
	}
	for i, h := range want {
		if rows[0][i] != h {
			t.Fatalf("%s header[%d] = %q, want %q", sheet, i, rows[0][i], h)
		}
	}
}

// cellFloat reads a raw numeric cell.
func cellFloat(t *testing.T, f *excelize.File, sheet, cell string) float64 {
	t.Helper()
	raw, err := f.GetCellValue(sheet, cell)
	if err != nil {
		t.Fatalf("read %s!%s: %v", sheet, cell, err)
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		t.Fatalf("%s!%s = %q, want a number (%v)", sheet, cell, raw, err)
	}
	return v
}

func approxTol(t *testing.T, what string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Fatalf("%s = %.10f, want %.10f (±%g)", what, got, want, tol)
	}
}

// qScoreSum sums a Murid row's Q Score cells for the uniform-unit
// invariant Σ Q Score ≈ Total Score (blank cells — no attempt or an
// ungraded essay — count as 0). The fixture's three questions put the
// score columns at J, L, N.
func qScoreSum(t *testing.T, f *excelize.File, row int) float64 {
	t.Helper()
	sum := 0.0
	for _, col := range []string{"J", "L", "N"} {
		raw, err := f.GetCellValue("Murid", fmt.Sprintf("%s%d", col, row))
		if err != nil {
			t.Fatalf("read Murid %s%d: %v", col, row, err)
		}
		if raw == "" {
			continue
		}
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			t.Fatalf("Murid %s%d = %q, want a number (%v)", col, row, raw, err)
		}
		sum += v
	}
	return sum
}

// TestExportWorkbookSheets pins the fixed 3-sheet workbook contract: sheet
// list/order, both header rows, frozen first row + primary fill, the
// Rekapitulasi formula blocks (every metric a formula, cross-sheet refs),
// the Pertanyaan TOTAL row, and the uniform Q Score rule — every cell a
// share of the 100-point final (Σ Q Score ≈ Total Score) in both the
// essay-ungraded and essay-graded states.
func TestExportWorkbookSheets(t *testing.T) {
	ts, ck, pool, quizID, code := exportFixture(t)

	resp, body := getWith(t,
		fmt.Sprintf("%s/teacher/quiz/%d/results/export?format=xlsx", ts.URL, quizID), ck)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("xlsx export = %d: %s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "spreadsheetml") {
		t.Fatalf("content type = %q", ct)
	}
	f := openExport(t, body)
	defer f.Close()

	// (1) exactly the three sheets, in order, no default leftover
	wantSheets := []string{"Murid", "Pertanyaan", "Rekapitulasi"}
	if got := strings.Join(f.GetSheetList(), ","); got != strings.Join(wantSheets, ",") {
		t.Fatalf("sheet list = %q, want %q", got, wantSheets)
	}

	// (2) header cells on both data sheets (verbatim contract)
	assertSheetRow0(t, f, "Murid", []string{
		"No", "Username", "Nama", "Kelas", "Jurusan", "Status", "Percobaan",
		"Kecurangan", "Q1 Jawaban", "Q1 Nilai", "Q2 Jawaban", "Q2 Nilai",
		"Q3 Jawaban", "Q3 Nilai", "Total Nilai",
	})
	assertSheetRow0(t, f, "Pertanyaan", []string{
		"No", "Teks", "Tipe", "Kunci", "Nilai Maks", "Benar", "Salah",
		"Tidak Dijawab", "% Benar", "% Salah", "% Kosong", "Paling Sering Dipilih",
	})

	// (3) frozen first row on every sheet
	for _, s := range wantSheets {
		p, err := f.GetPanes(s)
		if err != nil {
			t.Fatalf("panes %s: %v", s, err)
		}
		if !p.Freeze || p.YSplit != 1 || p.TopLeftCell != "A2" {
			t.Fatalf("panes %s = freeze=%v ySplit=%d topLeft=%q, want frozen ySplit=1 A2",
				s, p.Freeze, p.YSplit, p.TopLeftCell)
		}
	}

	// (4) Murid!A1 carries the styled primary fill
	styleID, err := f.GetCellStyle("Murid", "A1")
	if err != nil || styleID == 0 {
		t.Fatalf("Murid!A1 style = %d (%v), want a non-default style", styleID, err)
	}
	if f.Styles == nil || f.Styles.CellXfs == nil || styleID >= len(f.Styles.CellXfs.Xf) {
		t.Fatalf("styles not loaded (style id %d)", styleID)
	}
	xf := f.Styles.CellXfs.Xf[styleID]
	if xf.FillID == nil || f.Styles.Fills == nil ||
		*xf.FillID < 0 || *xf.FillID >= len(f.Styles.Fills.Fill) {
		t.Fatalf("Murid!A1 has no fill (style %d)", styleID)
	}
	fill := f.Styles.Fills.Fill[*xf.FillID]
	colors := ""
	if fill != nil && fill.PatternFill != nil {
		if fill.PatternFill.FgColor != nil {
			colors += fill.PatternFill.FgColor.RGB
		}
		if fill.PatternFill.BgColor != nil {
			colors += fill.PatternFill.BgColor.RGB
		}
	}
	if fill == nil || fill.PatternFill == nil || fill.PatternFill.PatternType != "solid" ||
		!strings.Contains(colors, "2C5EAD") {
		t.Fatalf("Murid!A1 fill = %#v colors %q, want solid primary 2C5EAD", fill, colors)
	}

	// (5) Rekapitulasi: every metric cell is a formula, cross-sheet refs
	rekRows, err := f.GetRows("Rekapitulasi")
	if err != nil || len(rekRows) == 0 {
		t.Fatalf("rekap rows: %d (%v)", len(rekRows), err)
	}
	sectionRow := func(label string) int {
		for i, r := range rekRows {
			if len(r) > 0 && r[0] == label {
				return i + 1
			}
		}
		return 0
	}
	checkMetric := func(cell string, want ...string) string {
		t.Helper()
		fml, err := f.GetCellFormula("Rekapitulasi", cell)
		if err != nil || fml == "" {
			t.Fatalf("Rekapitulasi!%s formula = %q (%v), want a formula — static numbers are forbidden here",
				cell, fml, err)
		}
		ct, _ := f.GetCellType("Rekapitulasi", cell)
		if ct != excelize.CellTypeFormula {
			t.Fatalf("Rekapitulasi!%s type = %v, want a formula cell", cell, ct)
		}
		for _, w := range want {
			if !strings.Contains(fml, w) {
				t.Fatalf("Rekapitulasi!%s = %q, want it to contain %q", cell, fml, w)
			}
		}
		return fml
	}
	ringkasan := sectionRow("Ringkasan")
	if ringkasan == 0 {
		t.Fatalf("Ringkasan section missing, rows = %q", rekRows)
	}
	jumlahRow := ringkasan + 1
	formulaCount := 0
	for i := 0; i < 7; i++ { // Jumlah peserta … Terendah
		checkMetric(fmt.Sprintf("B%d", jumlahRow+i), "Murid!")
		formulaCount++
	}
	if got, _ := f.GetCellFormula("Rekapitulasi", fmt.Sprintf("B%d", jumlahRow)); !strings.Contains(got, "$B$2") {
		t.Fatalf("Jumlah peserta = %q, want COUNTA over Murid!$B$2", got)
	}
	distribusi := sectionRow("Distribusi nilai")
	if distribusi == 0 {
		t.Fatalf("Distribusi nilai section missing")
	}
	for i := 1; i <= 5; i++ { // five bins: count + percent of Jumlah peserta
		checkMetric(fmt.Sprintf("B%d", distribusi+i), "COUNTIFS", "Murid!")
		percent := checkMetric(fmt.Sprintf("C%d", distribusi+i),
			fmt.Sprintf("$B$%d", jumlahRow))
		if !strings.Contains(percent, "/") {
			t.Fatalf("bin percent %q must divide by the total-count cell", percent)
		}
		formulaCount += 2
	}
	kelasRow := sectionRow("Rekap per kelas")
	if kelasRow == 0 {
		t.Fatalf("Rekap per kelas section missing")
	}
	for i, wantClass := range []string{"Kelas Satu", "Kelas Dua"} {
		r := kelasRow + 1 + i
		if got, _ := f.GetCellValue("Rekapitulasi", fmt.Sprintf("A%d", r)); got != wantClass {
			t.Fatalf("kelas label row %d = %q, want %q", r, got, wantClass)
		}
		checkMetric(fmt.Sprintf("B%d", r), "COUNTIF(Murid!$D$")
		checkMetric(fmt.Sprintf("C%d", r), "AVERAGEIF(Murid!$D$", "Murid!")
		formulaCount += 2
	}
	pertanyaanSec := sectionRow("Rekap per pertanyaan")
	if pertanyaanSec == 0 {
		t.Fatalf("Rekap per pertanyaan section missing")
	}
	checkMetric(fmt.Sprintf("A%d", pertanyaanSec+1), "Pertanyaan!") // ≥1 Pertanyaan! ref
	checkMetric(fmt.Sprintf("B%d", pertanyaanSec+1), "Pertanyaan!")
	checkMetric(fmt.Sprintf("C%d", pertanyaanSec+1), "AVERAGE(Murid!")
	checkMetric(fmt.Sprintf("D%d", pertanyaanSec+1), "Pertanyaan!")
	formulaCount += 4
	if formulaCount < 4 {
		t.Fatalf("formula cells = %d, want at least 4", formulaCount)
	}
	// title row: static text, styled
	if got, _ := f.GetCellValue("Rekapitulasi", "A1"); got != fmt.Sprintf("Rekapitulasi — Sheets export (%s)", code) {
		t.Fatalf("rekap title = %q", got)
	}

	// (6) Pertanyaan TOTAL row: =SUM of the shares (equals 100) + content
	totalFml, err := f.GetCellFormula("Pertanyaan", "E5")
	if err != nil || totalFml != "=SUM(E2:E4)" {
		t.Fatalf("total formula = %q (%v), want =SUM(E2:E4)", totalFml, err)
	}
	sum := 0.0
	for r := 2; r <= 4; r++ {
		sum += cellFloat(t, f, "Pertanyaan", fmt.Sprintf("E%d", r))
	}
	approxTol(t, "SUM of Nilai Maks", sum, 100, 1e-9)
	// engine key semantics: pg scalar → letter, multi array → letters, essay → —
	for cell, want := range map[string]string{"D2": "A", "D3": "A, C", "D4": "—"} {
		if got, _ := f.GetCellValue("Pertanyaan", cell); got != want {
			t.Fatalf("Kunci %s = %q, want %q", cell, got, want)
		}
	}
	if got, _ := f.GetCellValue("Pertanyaan", "B4"); got != "'=SUM(A1)" {
		t.Fatalf("Teks B4 = %q, want the guarded question text", got)
	}
	if got, _ := f.GetCellValue("Pertanyaan", "C4"); got != "essay" {
		t.Fatalf("Tipe C4 = %q, want essay", got)
	}
	approxTol(t, "Nilai Maks E2", cellFloat(t, f, "Pertanyaan", "E2"), 100.0/3.0, 1e-9)
	if got := cellFloat(t, f, "Pertanyaan", "F2"); got != 1 {
		t.Fatalf("Benar F2 = %v, want 1", got)
	}
	if got := cellFloat(t, f, "Pertanyaan", "H2"); got != 1 {
		t.Fatalf("Tidak Dijawab H2 = %v, want 1", got)
	}
	approxTol(t, "% Benar I2", cellFloat(t, f, "Pertanyaan", "I2"), 50, 1e-9)
	if got, _ := f.GetCellValue("Pertanyaan", "L2"); got != "A" {
		t.Fatalf("Paling Sering Dipilih L2 = %q, want A", got)
	}

	// (7) Murid rows: finished student first (score desc), not-attempted
	// second — Q Score is UNIFORM: every cell holds that question's
	// contribution to the 100-point final (the points rolling into
	// score_auto/final_score), so the cells sum to Total Score within the
	// engine's 2-decimal rounding.
	var auto, final sql.NullFloat64
	if err := pool.QueryRow(`SELECT p.score_auto, p.final_score FROM participants p
		JOIN users u ON u.id = p.user_id
		WHERE p.quiz_id = ? AND u.username = ?`, quizID, "sheets-student-one").
		Scan(&auto, &final); err != nil {
		t.Fatalf("attempt scores: %v", err)
	}
	if got, _ := f.GetCellValue("Murid", "B2"); got != "sheets-student-one" {
		t.Fatalf("Murid B2 = %q, want the scored student first", got)
	}
	if got, _ := f.GetCellValue("Murid", "F2"); got != "finished" {
		t.Fatalf("Murid F2 = %q", got)
	}
	if got, _ := f.GetCellValue("Murid", "I2"); got != "A" {
		t.Fatalf("Q1 Answer = %q, want A", got)
	}
	if got, _ := f.GetCellValue("Murid", "K2"); got != "" {
		t.Fatalf("Q2 Answer = %q, want blank (multi never answered)", got)
	}
	if got, _ := f.GetCellValue("Murid", "M2"); got != "Typed my essay." {
		t.Fatalf("Q3 Answer = %q", got)
	}
	// phase 1 — essay still ungraded: 100/total for the correct answer,
	// 0 for the unanswered multi (scored attempt — unanswered = wrong),
	// blank for the ungraded essay; Total falls back to score_auto
	if !auto.Valid || final.Valid {
		t.Fatalf("pre-grade scores: auto=%v final=%v, want auto set and final NULL",
			auto, final)
	}
	approxTol(t, "score_auto", auto.Float64, 33.33, 1e-9)
	approxTol(t, "Q1 Score (answered correct)", cellFloat(t, f, "Murid", "J2"), 100.0/3.0, 1e-9)
	approxTol(t, "Q2 Score (unanswered, scored)", cellFloat(t, f, "Murid", "L2"), 0, 1e-9)
	if got, _ := f.GetCellValue("Murid", "N2"); got != "" {
		t.Fatalf("Q3 Score = %q, want blank (essay ungraded)", got)
	}
	approxTol(t, "Total Score = score_auto", cellFloat(t, f, "Murid", "O2"), auto.Float64, 1e-9)
	approxTol(t, "Σ Q Score ≈ Total (essay pending)", qScoreSum(t, f, 2),
		cellFloat(t, f, "Murid", "O2"), 0.02)

	if got, _ := f.GetCellValue("Murid", "B3"); got != "sheets-student-two" {
		t.Fatalf("Murid B3 = %q, want the untouched student", got)
	}
	if got, _ := f.GetCellValue("Murid", "A3"); got != "0" {
		t.Fatalf("Murid A3 (Rank) = %q, want 0", got)
	}
	if got, _ := f.GetCellValue("Murid", "F3"); got != "not-attempted" {
		t.Fatalf("Murid F3 = %q", got)
	}
	// no attempt → all Q cells AND the Total Score stay EMPTY (not 0, not —)
	for _, cell := range []string{"I3", "J3", "K3", "L3", "M3", "N3", "O3"} {
		if got, _ := f.GetCellValue("Murid", cell); got != "" {
			t.Fatalf("Murid %s = %q, want empty", cell, got)
		}
	}

	// column widths (Teks generous; Q columns capped ~12)
	if w, err := f.GetColWidth("Pertanyaan", "B"); err != nil || w != 60 {
		t.Fatalf("Pertanyaan!B width = %v (%v), want 60", w, err)
	}
	if w, err := f.GetColWidth("Murid", "I"); err != nil || w > 12 {
		t.Fatalf("Murid!Q answer width = %v (%v), want <= 12", w, err)
	}

	// (8) phase 2 — grade the essay 80: its cell becomes the contribution
	// 80/total (NOT the raw 80), Total = final = 33.33 + 26.67 = 60, and
	// the row still sums to Total Score
	gradeEssay(t, ts, pool, ck, quizID, 80)
	resp, body = getWith(t,
		fmt.Sprintf("%s/teacher/quiz/%d/results/export?format=xlsx", ts.URL, quizID), ck)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("xlsx re-export = %d: %s", resp.StatusCode, body)
	}
	f2 := openExport(t, body)
	defer f2.Close()
	if err := pool.QueryRow(`SELECT p.final_score FROM participants p
		JOIN users u ON u.id = p.user_id
		WHERE p.quiz_id = ? AND u.username = ?`, quizID, "sheets-student-one").
		Scan(&final); err != nil {
		t.Fatalf("final score: %v", err)
	}
	approxTol(t, "final after grading", final.Float64, 60, 1e-9)
	approxTol(t, "Q3 Score (graded contribution)",
		cellFloat(t, f2, "Murid", "N2"), 80.0/3.0, 1e-9)
	approxTol(t, "Total Score = final", cellFloat(t, f2, "Murid", "O2"), final.Float64, 1e-9)
	approxTol(t, "Σ Q Score ≈ Total (graded)", qScoreSum(t, f2, 2),
		cellFloat(t, f2, "Murid", "O2"), 0.02)
}

// TestExportCSVQuestionColumns pins the CSV contract: UTF-8 BOM, the legacy
// nine headers first (values untouched), Q<i> Answer/Score pairs after them
// with the uniform contribution values (Σ Q Score ≈ Total Score), the
// question-text row, one row per student, and the injection guard on a
// question text that starts with "=".
func TestExportCSVQuestionColumns(t *testing.T) {
	ts, ck, pool, quizID, _ := exportFixture(t)

	resp, body := getWith(t,
		fmt.Sprintf("%s/teacher/quiz/%d/results/export", ts.URL, quizID), ck)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("csv export = %d: %s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/csv; charset=utf-8" {
		t.Fatalf("content type = %q", ct)
	}
	raw := []byte(body)
	if !bytes.HasPrefix(raw, []byte{0xEF, 0xBB, 0xBF}) {
		t.Fatalf("BOM missing, first bytes = %v", raw[:minInt(4, len(raw))])
	}
	if !utf8.Valid(raw) {
		t.Fatalf("csv body is not valid UTF-8")
	}
	records := parseCSVRecords(t, body)
	// 1 header + 1 question-text row + 2 students
	if len(records) != 4 {
		t.Fatalf("csv rows = %d, want 2 + 2 students", len(records))
	}

	want := append(append([]string{}, exportHeaders...),
		"Q1 Jawaban", "Q1 Nilai", "Q2 Jawaban", "Q2 Nilai", "Q3 Jawaban", "Q3 Nilai")
	if len(records[0]) != len(want) {
		t.Fatalf("header cols = %d, want %d", len(records[0]), len(want))
	}
	// the legacy nine stay byte-identical and first
	if got := strings.Join(records[0][:9], ","); got != strings.Join(exportHeaders, ",") {
		t.Fatalf("legacy header prefix = %s", got)
	}
	for i, h := range want {
		if records[0][i] != h {
			t.Fatalf("header[%d] = %q, want %q", i, records[0][i], h)
		}
	}

	// row 2: question text under each Answer column, Score columns blank
	qrow := records[1]
	if qrow[0] != "Teks pertanyaan" {
		t.Fatalf("row 2 first cell = %q", qrow[0])
	}
	for _, tc := range []struct {
		idx  int
		want string
	}{{9, "Pick A"}, {11, "Pick A and C"}, {13, "'=SUM(A1)"}} {
		if qrow[tc.idx] != tc.want {
			t.Fatalf("question text col %d = %q, want %q", tc.idx, qrow[tc.idx], tc.want)
		}
	}
	for _, sc := range []int{10, 12, 14} {
		if qrow[sc] != "" {
			t.Fatalf("question row score col %d = %q, want blank", sc, qrow[sc])
		}
	}

	// student rows (scored first), legacy nine values unchanged — phase 1
	// (essay ungraded): uniform contributions, 100/total for the correct
	// answer, "0" for the unanswered multi (scored attempt), blank for the
	// ungraded essay; legacy Score falls back to score_auto
	var auto, final sql.NullFloat64
	if err := pool.QueryRow(`SELECT p.score_auto, p.final_score FROM participants p
		JOIN users u ON u.id = p.user_id
		WHERE p.quiz_id = ? AND u.username = ?`, quizID, "sheets-student-one").
		Scan(&auto, &final); err != nil {
		t.Fatalf("attempt scores: %v", err)
	}
	if !auto.Valid || final.Valid {
		t.Fatalf("pre-grade scores: auto=%v final=%v, want auto set and final NULL",
			auto, final)
	}
	row := records[2]
	if row[0] != "1" || row[6] != "finished" || row[7] != "no" || row[8] != "1" {
		t.Fatalf("scored csv row = %q", row)
	}
	if row[2] != "sheets-student-one" {
		t.Fatalf("scored csv username = %q", row[2])
	}
	score, err := strconv.ParseFloat(row[5], 64)
	if err != nil || math.Abs(score-auto.Float64) > 1e-9 {
		t.Fatalf("csv score = %q auto %v (%v)", row[5], auto, err)
	}
	if row[9] != "A" || row[11] != "" || row[13] != "Typed my essay." {
		t.Fatalf("csv answers = %q / %q / %q", row[9], row[11], row[13])
	}
	q1, err := strconv.ParseFloat(row[10], 64)
	if err != nil || math.Abs(q1-100.0/3.0) > 1e-9 {
		t.Fatalf("csv Q1 score = %q (%v)", row[10], err)
	}
	if row[12] != "0" {
		t.Fatalf("csv Q2 score = %q, want 0 (unanswered on a scored attempt)", row[12])
	}
	if row[14] != "" {
		t.Fatalf("csv Q3 score = %q, want blank (essay ungraded)", row[14])
	}
	approxTol(t, "Σ Q Score ≈ Total (essay pending)", csvQSum(t, row), score, 0.02)

	untouched := records[3]
	if untouched[0] != "0" || untouched[6] != "not-attempted" || untouched[5] != "0" {
		t.Fatalf("untouched csv row = %q", untouched)
	}
	for _, i := range []int{9, 10, 11, 12, 13, 14} {
		if untouched[i] != "" {
			t.Fatalf("untouched csv col %d = %q, want blank", i, untouched[i])
		}
	}

	// phase 2 — grade the essay 80: Q3 becomes the contribution 80/total,
	// legacy Score = final = 60, and the row still sums to Total
	gradeEssay(t, ts, pool, ck, quizID, 80)
	resp, body = getWith(t,
		fmt.Sprintf("%s/teacher/quiz/%d/results/export", ts.URL, quizID), ck)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("csv re-export = %d: %s", resp.StatusCode, body)
	}
	records = parseCSVRecords(t, body)
	if len(records) != 4 {
		t.Fatalf("graded csv rows = %d, want 4", len(records))
	}
	graded := records[2]
	if err := pool.QueryRow(`SELECT p.final_score FROM participants p
		JOIN users u ON u.id = p.user_id
		WHERE p.quiz_id = ? AND u.username = ?`, quizID, "sheets-student-one").
		Scan(&final); err != nil {
		t.Fatalf("final score: %v", err)
	}
	approxTol(t, "final after grading", final.Float64, 60, 1e-9)
	gscore, err := strconv.ParseFloat(graded[5], 64)
	if err != nil || math.Abs(gscore-final.Float64) > 1e-9 {
		t.Fatalf("graded csv score = %q final %v (%v)", graded[5], final, err)
	}
	if graded[9] != "A" || graded[11] != "" || graded[13] != "Typed my essay." {
		t.Fatalf("graded csv answers = %q / %q / %q", graded[9], graded[11], graded[13])
	}
	q3, err := strconv.ParseFloat(graded[14], 64)
	if err != nil || math.Abs(q3-80.0/3.0) > 1e-9 {
		t.Fatalf("graded csv Q3 score = %q (%v), want 80/3", graded[14], err)
	}
	if graded[12] != "0" {
		t.Fatalf("graded csv Q2 score = %q, want 0", graded[12])
	}
	approxTol(t, "Σ Q Score ≈ Total (graded)", csvQSum(t, graded), gscore, 0.02)
}

// parseCSVRecords parses an export CSV response body (BOM-stripped).
func parseCSVRecords(t *testing.T, body string) [][]string {
	t.Helper()
	records, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(body, "\ufeff"))).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	return records
}

// csvQSum sums a CSV student row's Q Score columns (the even 0-based
// indexes after the legacy nine: 10, 12, …; blank cells count as 0) for
// the uniform-unit invariant Σ Q Score ≈ Total Score.
func csvQSum(t *testing.T, row []string) float64 {
	t.Helper()
	sum := 0.0
	for i := 10; i < len(row); i += 2 {
		if row[i] == "" {
			continue
		}
		v, err := strconv.ParseFloat(row[i], 64)
		if err != nil {
			t.Fatalf("csv Q score col %d = %q (%v)", i, row[i], err)
		}
		sum += v
	}
	return sum
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
