package integration

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"quiz/internal/handlers"
)

// riwayatQuiz builds an ACTIVE per-question 2-pg quiz with the given
// review settings; options are Alpha/Bravo/Charlie with Charlie correct.
func riwayatQuiz(t *testing.T, ts *httptest.Server, pool *sql.DB, ck *http.Cookie,
	title, review string, showScore, rankingLive bool,
) (uint64, string) {
	t.Helper()
	form := quizForm(title)
	form.Set("timer_type", "per_soal")
	form.Set("per_question_seconds", "60")
	form.Set("question_review", review)
	if showScore {
		form.Set("show_final_score", "1")
	} else {
		form.Set("show_final_score", "0")
	}
	if rankingLive {
		form.Set("ranking_live", "1")
	} else {
		form.Set("ranking_live", "0")
	}
	quizID := createQuiz(t, ts, ck, form)
	q1 := addQuestion(t, ts, pool, ck, "Riwayat one", "pg",
		[]string{"Alpha", "Bravo", "Charlie"}, []int{2})
	q2 := addQuestion(t, ts, pool, ck, "Riwayat two", "pg",
		[]string{"Alpha", "Bravo", "Charlie"}, []int{2})
	compose(t, ts, ck, quizID, q1, q2)
	if resp, body := setStatus(t, ts, ck, quizID, "aktif"); resp.StatusCode != http.StatusOK ||
		!decodeEnv(t, body).OK {
		t.Fatalf("activate = %d: %s", resp.StatusCode, body)
	}
	return quizID, joinCode(t, pool, quizID)
}

// TestHistorySettingsMatrix walks the spec §9 matrix: 3 review levels ×
// score on/off × ranking on/off = 12 rendered cases.
func TestHistorySettingsMatrix(t *testing.T) {
	ts, _, pool := globalFixture(t)
	ck := guruLogin(t, ts)

	type testCase struct {
		level     string
		showScore bool
		ranking   bool
	}
	var cases []testCase
	for _, level := range []string{"none", "text", "full"} {
		for _, sc := range []bool{false, true} {
			for _, rk := range []bool{false, true} {
				cases = append(cases, testCase{level, sc, rk})
			}
		}
	}
	if len(cases) != 12 {
		t.Fatalf("matrix size = %d, want 12", len(cases))
	}

	for i, c := range cases {
		name := fmt.Sprintf("%s/s%d/r%d", c.level, b2i(c.showScore), b2i(c.ranking))
		t.Run(name, func(t *testing.T) {
			quizID, code := riwayatQuiz(t, ts, pool, ck,
				fmt.Sprintf("Matrix %02d", i), c.level, c.showScore, c.ranking)
			// a fresh student per case: the column gates are "any row of this
			// list", so a shared list would leak neighbouring cases' columns
			st := studentCookie(t, pool, fmt.Sprintf("riwayat-matrix-%02d", i))
			uid := userOf(t, ts, st)
			if resp, body := postJoin(t, ts, code, st); resp.StatusCode != http.StatusOK {
				t.Fatalf("join = %d: %s", resp.StatusCode, body)
			}
			startAttempt(t, ts, code, st)
			// both answers WRONG → own answer is Bravo (Charlie is the key),
			// score 0.00
			answers := quizQuestionIDs(t, pool, quizID)
			for _, qid := range answers {
				if resp, body := answerPost(t, ts, code, st, qid, []int{1}); resp.StatusCode != http.StatusOK {
					t.Fatalf("answer = %d: %s", resp.StatusCode, body)
				}
			}
			if resp, body := postJSON(t, ts.URL+"/quiz/"+code+"/finish", nil, st); resp.StatusCode != http.StatusOK {
				t.Fatalf("finish = %d: %s", resp.StatusCode, body)
			}
			pid, _, _ := participantRowFor(t, pool, quizID, uid)

			// --- list ------------------------------------------------------
			resp, body := getWith(t, ts.URL+"/history", st)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("history = %d", resp.StatusCode)
			}
			if !contains(body, fmt.Sprintf("Matrix %02d", i)) {
				t.Fatalf("quiz title missing from history: %s", body)
			}
			if c.showScore {
				if !contains(body, "0.00") || !contains(body, `<th class="text-end">Nilai</th>`) {
					t.Fatalf("score column missing (show_final_score=1)")
				}
			} else if contains(body, "0.00") || contains(body, `<th class="text-end">Nilai</th>`) {
				t.Fatalf("score column shown despite show_final_score=0")
			}
			if c.ranking {
				if !contains(body, `<th class="text-end">Peringkat</th>`) || !contains(body, "#1") {
					t.Fatalf("rank column missing (ranking_live=1): %s", body)
				}
			} else if contains(body, `<th class="text-end">Peringkat</th>`) || contains(body, "#1") {
				t.Fatalf("rank column shown despite ranking_live=0")
			}

			// --- detail ----------------------------------------------------
			resp, body = getWith(t, ts.URL+"/history/"+strconv.FormatUint(pid, 10), st)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("detail = %d: %s", resp.StatusCode, body)
			}
			switch c.level {
			case "none":
				if contains(body, "Bravo") || contains(body, "Charlie") {
					t.Fatalf("none level leaked question content")
				}
				if !contains(body, "Tinjauan pertanyaan tidak aktif untuk kuis ini.") {
					t.Fatalf("none level missing its empty state")
				}
			case "text":
				if !contains(body, "Bravo") {
					t.Fatalf("text level missing own answer")
				}
				if contains(body, "Charlie") {
					t.Fatalf("text level leaked the answer key")
				}
			case "full":
				if !contains(body, "Bravo") || !contains(body, "Charlie") {
					t.Fatalf("full level missing answer or key")
				}
			}
			if c.showScore && !contains(body, "0.00") {
				t.Fatalf("detail score missing (show_final_score=1)")
			}
			if !c.showScore && contains(body, `small">Nilai<`) {
				t.Fatalf("detail leaked the score header (show_final_score=0)")
			}
			if c.ranking && !contains(body, "#1") {
				t.Fatalf("detail rank missing (ranking_live=1)")
			}
			if !c.ranking && contains(body, "#1") {
				t.Fatalf("detail leaked the rank (ranking_live=0)")
			}
		})
	}
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// quizQuestionIDs lists a quiz's question ids in bank order.
func quizQuestionIDs(t *testing.T, pool *sql.DB, quizID uint64) []uint64 {
	t.Helper()
	rows, err := pool.Query(`SELECT question_id FROM quiz_questions
		WHERE quiz_id = ? ORDER BY seq`, quizID)
	if err != nil {
		t.Fatalf("questions: %v", err)
	}
	defer rows.Close()
	var ids []uint64
	for rows.Next() {
		var id uint64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		ids = append(ids, id)
	}
	return ids
}

// TestHistoryOwnership: another student's attempt detail is 403 (spec §7).
func TestHistoryOwnership(t *testing.T) {
	ts, _, pool := globalFixture(t)
	ck := guruLogin(t, ts)
	owner := studentCookie(t, pool, "riwayat-owner")
	quizID, code := riwayatQuiz(t, ts, pool, ck, "Ownership", "full", true, true)
	if resp, body := postJoin(t, ts, code, owner); resp.StatusCode != http.StatusOK {
		t.Fatalf("join = %d: %s", resp.StatusCode, body)
	}
	startAttempt(t, ts, code, owner)
	if resp, body := postJSON(t, ts.URL+"/quiz/"+code+"/finish", nil, owner); resp.StatusCode != http.StatusOK {
		t.Fatalf("finish = %d: %s", resp.StatusCode, body)
	}
	pid, _, _ := participantRowFor(t, pool, quizID, userOf(t, ts, owner))

	stranger := studentCookie(t, pool, "riwayat-stranger")
	resp, body := getWith(t, ts.URL+"/history/"+strconv.FormatUint(pid, 10), stranger)
	assertFail(t, resp, body, http.StatusForbidden, handlers.ErrForbidden,
		"Anda tidak diizinkan melihat percobaan ini.")

	resp, body = getWith(t, ts.URL+"/history/"+strconv.FormatUint(pid, 10), owner)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("owner detail = %d: %s", resp.StatusCode, body)
	}
}

// TestHistoryMultiAttempt: all attempts listed newest first; the rank uses
// the highest score (spec §11.10); each detail shows its own attempt score.
func TestHistoryMultiAttempt(t *testing.T) {
	ts, _, pool := globalFixture(t)
	ck := guruLogin(t, ts)
	st := studentCookie(t, pool, "riwayat-multi")
	uid := userOf(t, ts, st)

	form := quizForm("Multi Attempt")
	form.Set("timer_type", "per_soal")
	form.Set("per_question_seconds", "60")
	form.Set("max_attempts", "2")
	form.Set("ranking_live", "1")
	quizID := createQuiz(t, ts, ck, form)
	qid := addQuestion(t, ts, pool, ck, "Multi one", "pg",
		[]string{"Alpha", "Bravo", "Charlie"}, []int{2})
	compose(t, ts, ck, quizID, qid)
	if resp, body := setStatus(t, ts, ck, quizID, "aktif"); resp.StatusCode != http.StatusOK ||
		!decodeEnv(t, body).OK {
		t.Fatalf("activate = %d: %s", resp.StatusCode, body)
	}
	code := joinCode(t, pool, quizID)

	// attempt 1: wrong answer → 0
	if resp, body := postJoin(t, ts, code, st); resp.StatusCode != http.StatusOK {
		t.Fatalf("join 1 = %d: %s", resp.StatusCode, body)
	}
	startAttempt(t, ts, code, st)
	if resp, body := answerPost(t, ts, code, st, qid, []int{1}); resp.StatusCode != http.StatusOK {
		t.Fatalf("answer 1 = %d: %s", resp.StatusCode, body)
	}
	if resp, body := postJSON(t, ts.URL+"/quiz/"+code+"/finish", nil, st); resp.StatusCode != http.StatusOK {
		t.Fatalf("finish 1 = %d: %s", resp.StatusCode, body)
	}

	// attempt 2: correct answer → 100 (rank comes from this best score)
	if resp, body := postJoin(t, ts, code, st); resp.StatusCode != http.StatusOK {
		t.Fatalf("join 2 = %d: %s", resp.StatusCode, body)
	}
	startAttempt(t, ts, code, st)
	if resp, body := answerPost(t, ts, code, st, qid, []int{2}); resp.StatusCode != http.StatusOK {
		t.Fatalf("answer 2 = %d: %s", resp.StatusCode, body)
	}
	if resp, body := postJSON(t, ts.URL+"/quiz/"+code+"/finish", nil, st); resp.StatusCode != http.StatusOK {
		t.Fatalf("finish 2 = %d: %s", resp.StatusCode, body)
	}

	var pid1, pid2 uint64
	if err := pool.QueryRow(`SELECT id FROM participants
		WHERE quiz_id = ? AND user_id = ? AND attempt_no = 1`, quizID, uid).Scan(&pid1); err != nil {
		t.Fatalf("pid1: %v", err)
	}
	if err := pool.QueryRow(`SELECT id FROM participants
		WHERE quiz_id = ? AND user_id = ? AND attempt_no = 2`, quizID, uid).Scan(&pid2); err != nil {
		t.Fatalf("pid2: %v", err)
	}

	resp, body := getWith(t, ts.URL+"/history", st)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("history = %d", resp.StatusCode)
	}
	link1 := "/history/" + strconv.FormatUint(pid1, 10)
	link2 := "/history/" + strconv.FormatUint(pid2, 10)
	if n := strings.Count(body, "/history/"); n != 2 {
		t.Fatalf("attempt links = %d, want 2: %s", n, body)
	}
	i1, i2 := strings.Index(body, link1), strings.Index(body, link2)
	if i1 < 0 || i2 < 0 {
		t.Fatalf("missing attempt links: %s", body)
	}
	if i2 > i1 {
		t.Fatalf("attempt 2 (%d) must be listed before attempt 1 (%d)", i2, i1)
	}
	if !contains(body, "#1") {
		t.Fatalf("rank must use the highest score (100 → #1): %s", body)
	}

	// per-attempt scores on the detail pages
	resp, body = getWith(t, ts.URL+link1, st)
	if resp.StatusCode != http.StatusOK || !contains(body, "0.00") {
		t.Fatalf("attempt 1 detail = %d: %s", resp.StatusCode, body)
	}
	resp, body = getWith(t, ts.URL+link2, st)
	if resp.StatusCode != http.StatusOK || !contains(body, "100.00") {
		t.Fatalf("attempt 2 detail = %d: %s", resp.StatusCode, body)
	}
	if !contains(body, "#1") {
		t.Fatalf("attempt 2 detail must show rank #1: %s", body)
	}
}

// TestProfileEdit: the three editable fields save (and validate); username
// and email never move (spec §7).
func TestProfileEdit(t *testing.T) {
	ts, _, pool := globalFixture(t)
	st := studentCookie(t, pool, "riwayat-profile")
	uid := userOf(t, ts, st)

	var username, email string
	if err := pool.QueryRow(`SELECT username, email FROM users WHERE id = ?`, uid).
		Scan(&username, &email); err != nil {
		t.Fatalf("user read: %v", err)
	}
	var kelasID, jurusanID uint64
	if err := pool.QueryRow(`SELECT id FROM ref_kelas ORDER BY id LIMIT 1`).Scan(&kelasID); err != nil {
		t.Fatalf("kelas ref: %v", err)
	}
	if err := pool.QueryRow(`SELECT id FROM ref_jurusan ORDER BY id LIMIT 1`).Scan(&jurusanID); err != nil {
		t.Fatalf("jurusan ref: %v", err)
	}

	resp, body := getWith(t, ts.URL+"/profile", st)
	if resp.StatusCode != http.StatusOK || !contains(body, username) {
		t.Fatalf("profile page = %d: %s", resp.StatusCode, body)
	}

	values := url.Values{}
	values.Set("nama", "Riwayat Renamed")
	values.Set("kelas_id", strconv.FormatUint(kelasID, 10))
	values.Set("jurusan_id", strconv.FormatUint(jurusanID, 10))
	resp, body = postForm(t, ts.URL+"/profile/edit", values, st)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("edit = %d: %s", resp.StatusCode, body)
	}
	if loc := resp.Header.Get("Location"); loc != "/profile?saved=1" {
		t.Fatalf("redirect = %q", loc)
	}

	resp, body = getWith(t, ts.URL+"/profile?saved=1", st)
	if resp.StatusCode != http.StatusOK || !contains(body, "Riwayat Renamed") {
		t.Fatalf("profile after edit = %d: %s", resp.StatusCode, body)
	}
	if !contains(body, "Profil berhasil disimpan.") {
		t.Fatalf("saved confirmation missing: %s", body)
	}
	var nama string
	var kelasAfter, jurusanAfter uint64
	var userAfter, emailAfter string
	if err := pool.QueryRow(`SELECT nama_lengkap, kelas_id, jurusan_id, username, email
		FROM users WHERE id = ?`, uid).
		Scan(&nama, &kelasAfter, &jurusanAfter, &userAfter, &emailAfter); err != nil {
		t.Fatalf("user reread: %v", err)
	}
	if nama != "Riwayat Renamed" || kelasAfter != kelasID || jurusanAfter != jurusanID {
		t.Fatalf("db row = %q/%d/%d", nama, kelasAfter, jurusanAfter)
	}
	if userAfter != username || emailAfter != email {
		t.Fatalf("immutable fields moved: %s/%s → %s/%s", username, email, userAfter, emailAfter)
	}

	// validation: unknown class, empty name
	values.Set("kelas_id", "65535")
	resp, body = postForm(t, ts.URL+"/profile/edit", values, st)
	assertFail(t, resp, body, http.StatusBadRequest, handlers.ErrValidation,
		"Kelas atau jurusan tidak valid.")
	values.Set("kelas_id", strconv.FormatUint(kelasID, 10))
	values.Set("nama", "   ")
	resp, body = postForm(t, ts.URL+"/profile/edit", values, st)
	assertFail(t, resp, body, http.StatusBadRequest, handlers.ErrValidation,
		"Nama lengkap wajib diisi.")
}
