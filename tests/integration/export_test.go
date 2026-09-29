package integration

import (
	"database/sql"
	"encoding/csv"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"quiz/internal/handlers"
)

// exportHeaders must mirror the csv tags in internal/handlers/export.go.
var exportHeaders = []string{"Rank", "Name", "Username", "Class", "Major",
	"Score", "Status", "Cheating", "Attempts"}

func TestResultsExportCSV(t *testing.T) {
	ts, _, pool := globalFixture(t)
	ck := guruLogin(t, ts)
	quizID, code, qids := linearQuiz(t, ts, pool, ck, "CSV export", 1)
	st := studentCookie(t, pool, "csv-student")
	if resp, body := postJoin(t, ts, code, st); resp.StatusCode != http.StatusOK {
		t.Fatalf("join = %d: %s", resp.StatusCode, body)
	}
	startGlobal(t, ts, ck, quizID)
	if resp, body := answerPost(t, ts, code, st, qids[0], []int{0}); resp.StatusCode != http.StatusOK {
		t.Fatalf("answer = %d: %s", resp.StatusCode, body)
	}
	if resp, body := postJSON(t, ts.URL+"/quiz/"+code+"/finish", nil, st); resp.StatusCode != http.StatusOK {
		t.Fatalf("finish = %d: %s", resp.StatusCode, body)
	}
	// spreadsheet formula payload as the student's name
	if _, err := pool.Exec(`UPDATE users SET nama_lengkap = '=SUM(A1)' WHERE id = ?`,
		userOf(t, ts, st)); err != nil {
		t.Fatalf("rename: %v", err)
	}

	// no format param → csv (plan Step 14)
	resp, body := getWith(t, fmt.Sprintf("%s/teacher/quiz/%d/results/export", ts.URL, quizID), ck)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("export = %d: %s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/csv; charset=utf-8" {
		t.Fatalf("content type = %q", ct)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, "quiz-"+code+".csv") {
		t.Fatalf("content disposition = %q", cd)
	}
	// [PIN UPDATE, format-mandated] the CSV now starts with a UTF-8 BOM and
	// carries a question-text row between header and students — strip the
	// BOM before parsing, expect header + question row + 1 student.
	if !strings.HasPrefix(body, "\ufeff") {
		t.Fatalf("csv BOM missing, prefix = %q", body)
	}
	records, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(body, "\ufeff"))).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("csv rows = %d, want header + question text + 1 student", len(records))
	}
	wantHeaders := append(append([]string{}, exportHeaders...), "Q1 Answer", "Q1 Score")
	if got := strings.Join(records[0], ","); got != strings.Join(wantHeaders, ",") {
		t.Fatalf("csv header = %s", got)
	}
	// row 2 = full question text under the Answer column, Score column blank
	if records[1][0] != "Question text" || records[1][9] != "Q1 pick A" || records[1][10] != "" {
		t.Fatalf("question row = %q", records[1])
	}
	row := records[2]
	if row[1] != "'=SUM(A1)" {
		t.Fatalf("injection name = %q, want %q", row[1], "'=SUM(A1)")
	}
	if row[0] != "1" || row[6] != "finished" || row[7] != "no" || row[8] != "1" {
		t.Fatalf("csv row = %q", row)
	}
	score, err := strconv.ParseFloat(row[5], 64)
	if err != nil || score != 100 {
		t.Fatalf("csv score = %q (%v)", row[5], err)
	}
	// additive pins: the appended Q pair — answered option 0 → "A", correct
	// on a 1-question quiz → 100/1 = 100
	if row[9] != "A" || row[10] != "100" {
		t.Fatalf("csv q pair = %q / %q", row[9], row[10])
	}
}

func TestResultsExportXLSX(t *testing.T) {
	ts, _, pool := globalFixture(t)
	ck := guruLogin(t, ts)
	quizID, code, qids := linearQuiz(t, ts, pool, ck, "XLSX export", 1)
	st := studentCookie(t, pool, "xlsx-student")
	if resp, body := postJoin(t, ts, code, st); resp.StatusCode != http.StatusOK {
		t.Fatalf("join = %d: %s", resp.StatusCode, body)
	}
	startGlobal(t, ts, ck, quizID)
	if resp, body := answerPost(t, ts, code, st, qids[0], []int{0}); resp.StatusCode != http.StatusOK {
		t.Fatalf("answer = %d: %s", resp.StatusCode, body)
	}
	if resp, body := postJSON(t, ts.URL+"/quiz/"+code+"/finish", nil, st); resp.StatusCode != http.StatusOK {
		t.Fatalf("finish = %d: %s", resp.StatusCode, body)
	}
	if _, err := pool.Exec(`UPDATE users SET nama_lengkap = '=SUM(A1)' WHERE id = ?`,
		userOf(t, ts, st)); err != nil {
		t.Fatalf("rename: %v", err)
	}

	respXLSX, bodyXLSX := getWith(t,
		fmt.Sprintf("%s/teacher/quiz/%d/results/export?format=xlsx", ts.URL, quizID), ck)
	if respXLSX.StatusCode != http.StatusOK {
		t.Fatalf("xlsx export = %d", respXLSX.StatusCode)
	}
	if ct := respXLSX.Header.Get("Content-Type"); !strings.Contains(ct, "spreadsheetml") {
		t.Fatalf("xlsx content type = %q", ct)
	}
	respCSV, bodyCSV := getWith(t,
		fmt.Sprintf("%s/teacher/quiz/%d/results/export?format=csv", ts.URL, quizID), ck)
	if respCSV.StatusCode != http.StatusOK {
		t.Fatalf("csv export = %d", respCSV.StatusCode)
	}
	// [PIN UPDATE, format-mandated] CSV side: strip the new UTF-8 BOM; the
	// student row moved to index 2 (row 2 is the question-text row).
	if !strings.HasPrefix(bodyCSV, "\ufeff") {
		t.Fatalf("csv BOM missing, prefix = %q", bodyCSV)
	}
	csvRecords, err := csv.NewReader(
		strings.NewReader(strings.TrimPrefix(bodyCSV, "\ufeff"))).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}

	path := filepath.Join(t.TempDir(), "export.xlsx")
	if err := os.WriteFile(path, []byte(bodyXLSX), 0o600); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatalf("reopen xlsx: %v", err)
	}
	defer f.Close()
	sheet := f.GetSheetName(0)
	rows, err := f.GetRows(sheet)
	if err != nil {
		t.Fatalf("read rows: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("xlsx rows = %d, want header + 1", len(rows))
	}
	// [PIN UPDATE, format-mandated] Murid row 1 follows the fixed workbook
	// contract: No | Username | Nama | … (the CSV keeps the legacy nine).
	wantHeaders := []string{"No", "Username", "Nama", "Kelas", "Jurusan",
		"Status", "Attempts", "Cheating", "Q1 Answer", "Q1 Score", "Total Score"}
	for i, want := range wantHeaders {
		if rows[0][i] != want {
			t.Fatalf("xlsx header[%d] = %q, want %q", i, rows[0][i], want)
		}
	}
	// the name cell is a literal STRING, never a formula
	// [PIN UPDATE] Nama moved from column B to column C (No|Username|Nama)
	axis, _ := excelize.CoordinatesToCellName(3, 2)
	cellType, err := f.GetCellType(sheet, axis)
	if err != nil {
		t.Fatalf("cell type: %v", err)
	}
	if cellType != excelize.CellTypeSharedString || cellType == excelize.CellTypeFormula {
		t.Fatalf("name cell type = %v, want a plain string cell", cellType)
	}
	if rows[1][2] != "'=SUM(A1)" {
		t.Fatalf("xlsx name = %q, want %q", rows[1][2], "'=SUM(A1)")
	}
	// identical values to the CSV row — [PIN UPDATE] via the new column
	// mapping (sheet No|Username|Nama|… ↔ CSV Rank|Name|Username|… plus the
	// appended Q pairs); every sheet cell still checked against its CSV twin.
	csvRow := csvRecords[2]
	csvColOfSheet := []int{0, 2, 1, 3, 4, 6, 8, 7, 9, 10, 5}
	if len(csvRow) != len(csvColOfSheet) {
		t.Fatalf("csv cols = %d, want %d", len(csvRow), len(csvColOfSheet))
	}
	for i, ci := range csvColOfSheet {
		if i == 9 || i == 10 { // Q1 Score / Total Score: numeric compare
			xl, err1 := strconv.ParseFloat(rows[1][i], 64)
			cv, err2 := strconv.ParseFloat(csvRow[ci], 64)
			if err1 != nil || err2 != nil || xl != cv {
				t.Fatalf("score mismatch: xlsx %q vs csv %q (csv col %d)", rows[1][i], csvRow[ci], ci)
			}
			continue
		}
		if rows[1][i] != csvRow[ci] {
			t.Fatalf("cell %d mismatch: xlsx %q vs csv %q (csv col %d)", i, rows[1][i], csvRow[ci], ci)
		}
	}
}

func TestExportInvalidFormatAndEmpty(t *testing.T) {
	ts, _, pool := globalFixture(t)
	ck := guruLogin(t, ts)
	form := quizForm("Empty export")
	quizID := createQuiz(t, ts, ck, form)
	q := addQuestion(t, ts, pool, ck, "EQ", "pg", []string{"A", "B"}, []int{0})
	compose(t, ts, ck, quizID, q)
	if resp, body := setStatus(t, ts, ck, quizID, "aktif"); resp.StatusCode != http.StatusOK || !decodeEnv(t, body).OK {
		t.Fatalf("activate = %d: %s", resp.StatusCode, body)
	}

	resp, body := getWith(t,
		fmt.Sprintf("%s/teacher/quiz/%d/results/export?format=bogus", ts.URL, quizID), ck)
	assertFail(t, resp, body, http.StatusBadRequest, handlers.ErrValidation, "Invalid export format.")

	// no participants → header-only file in both formats
	resp, body = getWith(t,
		fmt.Sprintf("%s/teacher/quiz/%d/results/export?format=csv", ts.URL, quizID), ck)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("empty csv = %d", resp.StatusCode)
	}
	records, err := csv.NewReader(strings.NewReader(body)).ReadAll()
	if err != nil {
		t.Fatalf("parse empty csv: %v", err)
	}
	// [PIN UPDATE, format-mandated] row 2 is always the question-text row —
	// with no participants the file is header + question text, not header only
	if len(records) != 2 || records[1][0] != "Question text" {
		t.Fatalf("empty csv rows = %d (%v), want header + question text", len(records), err)
	}
	resp, body = getWith(t,
		fmt.Sprintf("%s/teacher/quiz/%d/results/export?format=xlsx", ts.URL, quizID), ck)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("empty xlsx = %d", resp.StatusCode)
	}
	path := filepath.Join(t.TempDir(), "empty.xlsx")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatalf("reopen empty xlsx: %v", err)
	}
	defer f.Close()
	rows, err := f.GetRows(f.GetSheetName(0))
	if err != nil || len(rows) != 1 {
		t.Fatalf("empty xlsx rows = %d (%v), want header only", len(rows), err)
	}
}

// TestEssayGradingFinalizes pins spec §6.11: finish leaves final_score NULL
// ("Awaiting grading"), grading an essay stores the score (is_correct stays
// NULL), essay_score becomes the graded mean scaled to the essay share, and
// final_score = score_auto + essay_score — idempotently.
func TestEssayGradingFinalizes(t *testing.T) {
	ts, _, pool := globalFixture(t)
	ck := guruLogin(t, ts)

	form := quizForm("Grading Flow")
	form.Set("timer_type", "per_soal")
	form.Set("per_question_seconds", "60")
	quizID := createQuiz(t, ts, ck, form)
	pg := addQuestion(t, ts, pool, ck, "PG one", "pg", []string{"A", "B"}, []int{0})
	essay := addQuestion(t, ts, pool, ck, "Explain the thing.", "essay", nil, nil)
	compose(t, ts, ck, quizID, pg, essay)
	if resp, body := setStatus(t, ts, ck, quizID, "aktif"); resp.StatusCode != http.StatusOK || !decodeEnv(t, body).OK {
		t.Fatalf("activate = %d: %s", resp.StatusCode, body)
	}
	code := joinCode(t, pool, quizID)
	st := studentCookie(t, pool, "grade-student")
	if resp, body := postJoin(t, ts, code, st); resp.StatusCode != http.StatusOK {
		t.Fatalf("join = %d: %s", resp.StatusCode, body)
	}
	startAttempt(t, ts, code, st)
	if resp, body := answerPost(t, ts, code, st, pg, []int{0}); resp.StatusCode != http.StatusOK {
		t.Fatalf("pg answer = %d: %s", resp.StatusCode, body)
	}
	if resp, body := answerPost(t, ts, code, st, essay, "Typed my answer."); resp.StatusCode != http.StatusOK {
		t.Fatalf("essay answer = %d: %s", resp.StatusCode, body)
	}
	if resp, body := postJSON(t, ts.URL+"/quiz/"+code+"/finish", nil, st); resp.StatusCode != http.StatusOK {
		t.Fatalf("finish = %d: %s", resp.StatusCode, body)
	}

	resultsURL := fmt.Sprintf("%s/teacher/quiz/%d/results", ts.URL, quizID)
	gradingURL := fmt.Sprintf("%s/teacher/quiz/%d/grading", ts.URL, quizID)

	// finished with essays → final_score NULL → "Awaiting grading"
	resp, body := getWith(t, resultsURL, ck)
	if resp.StatusCode != http.StatusOK || !contains(body, "Awaiting grading") {
		t.Fatalf("results = %d, awaiting marker %v", resp.StatusCode, contains(body, "Awaiting grading"))
	}
	resp, body = getWith(t, gradingURL, ck)
	if resp.StatusCode != http.StatusOK || !contains(body, "Typed my answer.") {
		t.Fatalf("grading list = %d: %s", resp.StatusCode, body)
	}

	var answerID uint64
	if err := pool.QueryRow(`SELECT a.id FROM answers a
		JOIN participants p ON p.id = a.participant_id
		WHERE p.quiz_id = ? AND a.is_correct IS NULL`, quizID).Scan(&answerID); err != nil {
		t.Fatalf("essay answer id: %v", err)
	}
	grade := func(score any) (*http.Response, string) {
		t.Helper()
		return postJSON(t, fmt.Sprintf("%s/%d", gradingURL, answerID),
			map[string]any{"score": score}, ck)
	}

	// grade 80 on one essay of a 2-question quiz → essay_score = 80×1/2 = 40
	// → final = 50 (auto) + 40 = 90
	if resp, body := grade(80); resp.StatusCode != http.StatusOK {
		t.Fatalf("grade = %d: %s", resp.StatusCode, body)
	}
	assertGraded := func() {
		t.Helper()
		var storedScore, essayScore, finalScore sql.NullFloat64
		var correct sql.NullInt64
		if err := pool.QueryRow(`SELECT score, is_correct FROM answers WHERE id = ?`, answerID).
			Scan(&storedScore, &correct); err != nil {
			t.Fatalf("answer read: %v", err)
		}
		if !storedScore.Valid || storedScore.Float64 != 80 || correct.Valid {
			t.Fatalf("answer = score %v correct %v, want 80/NULL", storedScore.Float64, correct.Valid)
		}
		if err := pool.QueryRow(`SELECT essay_score, final_score FROM participants
			WHERE quiz_id = ? AND attempt_no = 1`, quizID).
			Scan(&essayScore, &finalScore); err != nil {
			t.Fatalf("participant read: %v", err)
		}
		if !essayScore.Valid || essayScore.Float64 != 40 {
			t.Fatalf("essay_score = %v, want 40.00", essayScore.Float64)
		}
		if !finalScore.Valid || finalScore.Float64 != 90 {
			t.Fatalf("final_score = %v, want 90.00", finalScore.Float64)
		}
	}
	assertGraded()

	// idempotent: same score again → same state
	if resp, body := grade(80); resp.StatusCode != http.StatusOK {
		t.Fatalf("re-grade = %d: %s", resp.StatusCode, body)
	}
	assertGraded()

	// out-of-range score → 400
	resp, body = grade(150)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("grade 150 = %d: %s", resp.StatusCode, body)
	}
	if env := decodeEnv(t, body); env.Message != "Score must be between 0 and 100." {
		t.Fatalf("grade 150 message = %q", env.Message)
	}

	// results now shows the finalized score, no awaiting marker
	resp, body = getWith(t, resultsURL, ck)
	if resp.StatusCode != http.StatusOK || !contains(body, "90.00") {
		t.Fatalf("results after grading = %d: %s", resp.StatusCode, body)
	}
	if contains(body, "Awaiting grading") {
		t.Fatalf("awaiting marker still present after grading")
	}

	// data-json serializes form fields as strings: {"score":"85.5"} must
	// grade exactly like the JSON number 85.5 (200 envelope, DB row 85.5)
	resp, body = grade("85.5")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("grade string score = %d: %s", resp.StatusCode, body)
	}
	if gradeEnv := decodeEnv(t, body); !gradeEnv.OK ||
		!strings.Contains(string(gradeEnv.Data), `"answer_id":`) ||
		!strings.Contains(string(gradeEnv.Data), `"score":85.5`) {
		t.Fatalf("string-score envelope data = %s", gradeEnv.Data)
	}
	var stringStored float64
	if err := pool.QueryRow(`SELECT score FROM answers WHERE id = ?`, answerID).
		Scan(&stringStored); err != nil {
		t.Fatalf("stored string score: %v", err)
	}
	if stringStored != 85.5 {
		t.Fatalf("stored score = %v, want 85.5", stringStored)
	}

	// the JSON number form still works after the change
	if resp, body := grade(90); resp.StatusCode != http.StatusOK {
		t.Fatalf("grade number score = %d: %s", resp.StatusCode, body)
	}

	// absent / null score → 400 VALIDATION "Score is required."
	resp, body = postJSON(t, fmt.Sprintf("%s/%d", gradingURL, answerID), map[string]any{}, ck)
	assertFail(t, resp, body, http.StatusBadRequest, "VALIDATION", "Score is required.")
	resp, body = postJSON(t, fmt.Sprintf("%s/%d", gradingURL, answerID),
		map[string]any{"score": nil}, ck)
	assertFail(t, resp, body, http.StatusBadRequest, "VALIDATION", "Score is required.")

	// strict parse: NaN would slip both bounds checks (500 instead of 400)
	// and "85abc" used to grade as 85 — both are the range error now
	resp, body = grade("NaN")
	assertFail(t, resp, body, http.StatusBadRequest, "VALIDATION",
		"Score must be between 0 and 100.")
	resp, body = grade("85abc")
	assertFail(t, resp, body, http.StatusBadRequest, "VALIDATION",
		"Score must be between 0 and 100.")
}
