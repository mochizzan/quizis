package integration

import (
	"database/sql"
	"fmt"
	"net/http"
	"testing"

	"quiz/internal/quizengine"
)

// TestEssayWeightAndQuestionGrouping pins contract C11 end to end on a
// 5-question quiz (2 pg + 3 essays, the last essay left unanswered):
//   - the grading page renders one card per essay question, grouped,
//   - essay_score = SUM(graded)/total — each save adds exactly score/total,
//     so an essay graded 100 contributes quizengine.CentiPercent(1, total)
//     (the weight of one correct MCQ),
//   - final_score stays NULL until every essay ANSWER of the quiz holds a
//     score (results keep "Menunggu penilaian"), then finalizes.
func TestEssayWeightAndQuestionGrouping(t *testing.T) {
	ts, _, pool := globalFixture(t)
	ck := guruLogin(t, ts)

	quizID := createQuiz(t, ts, ck, quizForm("Bobot esai"))
	pgOK := addQuestion(t, ts, pool, ck, "Pilihan ganda benar", "pg",
		[]string{"A", "B"}, []int{0})
	pgBad := addQuestion(t, ts, pool, ck, "Pilihan ganda salah", "pg",
		[]string{"A", "B"}, []int{0})
	es1 := addQuestion(t, ts, pool, ck, "Esai satu.", "essay", nil, nil)
	es2 := addQuestion(t, ts, pool, ck, "Esai dua.", "essay", nil, nil)
	es3 := addQuestion(t, ts, pool, ck, "Esai tiga.", "essay", nil, nil)
	compose(t, ts, ck, quizID, pgOK, pgBad, es1, es2, es3)
	if resp, body := setStatus(t, ts, ck, quizID, "aktif"); resp.StatusCode != http.StatusOK ||
		!decodeEnv(t, body).OK {
		t.Fatalf("activate = %d: %s", resp.StatusCode, body)
	}
	code := joinCode(t, pool, quizID)
	st := studentCookie(t, pool, "essay-weight-student")
	if resp, body := postJoin(t, ts, code, st); resp.StatusCode != http.StatusOK {
		t.Fatalf("join = %d: %s", resp.StatusCode, body)
	}
	startGlobal(t, ts, ck, quizID)
	// 1 correct pg (auto = CentiPercent(1,5)), 1 wrong pg, two essays
	// written, the third essay left unanswered (no answers row at all)
	for _, step := range []struct {
		qid uint64
		ans any
	}{
		{pgOK, []int{0}},
		{pgBad, []int{1}},
		{es1, "Jawaban satu."},
		{es2, "Jawaban dua."},
	} {
		if resp, body := answerPost(t, ts, code, st, step.qid, step.ans); resp.StatusCode != http.StatusOK {
			t.Fatalf("answer qid %d = %d: %s", step.qid, resp.StatusCode, body)
		}
	}
	if resp, body := postJSON(t, ts.URL+"/quiz/"+code+"/finish", nil, st); resp.StatusCode != http.StatusOK {
		t.Fatalf("finish = %d: %s", resp.StatusCode, body)
	}

	const total = 5
	mcqOne := quizengine.CentiPercent(1, total) // 20.00 — one correct MCQ
	resultsURL := fmt.Sprintf("%s/teacher/quiz/%d/results", ts.URL, quizID)
	gradingURL := fmt.Sprintf("%s/teacher/quiz/%d/grading", ts.URL, quizID)

	// the graded essay answer ids in question order (es3 unanswered → no row)
	rows, err := pool.Query(`SELECT qq.seq, a.id FROM answers a
		JOIN participants p ON p.id = a.participant_id
		JOIN quiz_questions qq ON qq.quiz_id = p.quiz_id AND qq.question_id = a.question_id
		WHERE p.quiz_id = ? AND a.is_correct IS NULL
		ORDER BY qq.seq`, quizID)
	if err != nil {
		t.Fatalf("essay answer ids: %v", err)
	}
	var ids []uint64
	for rows.Next() {
		var seq int
		var id uint64
		if err := rows.Scan(&seq, &id); err != nil {
			rows.Close()
			t.Fatalf("essay answer scan: %v", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatalf("essay answers: %v", err)
	}
	rows.Close()
	if len(ids) != 2 {
		t.Fatalf("essay answers = %d, want 2 (es3 unanswered has no row)", len(ids))
	}
	ans1, ans2 := ids[0], ids[1]

	readScores := func(t *testing.T) (essay, final sql.NullFloat64) {
		t.Helper()
		if err := pool.QueryRow(`SELECT essay_score, final_score FROM participants
			WHERE quiz_id = ? AND attempt_no = 1`, quizID).Scan(&essay, &final); err != nil {
			t.Fatalf("participant scores: %v", err)
		}
		return
	}
	grade := func(t *testing.T, answerID uint64, score any) (*http.Response, string) {
		t.Helper()
		return postJSON(t, fmt.Sprintf("%s/%d", gradingURL, answerID),
			map[string]any{"score": score}, ck)
	}

	t.Run("finish leaves the final pending", func(t *testing.T) {
		var auto sql.NullFloat64
		if err := pool.QueryRow(`SELECT score_auto FROM participants
			WHERE quiz_id = ? AND attempt_no = 1`, quizID).Scan(&auto); err != nil {
			t.Fatalf("score_auto: %v", err)
		}
		if !auto.Valid || auto.Float64 != mcqOne {
			t.Fatalf("score_auto = %v, want %v", auto.Float64, mcqOne)
		}
		essay, final := readScores(t)
		if essay.Valid || final.Valid {
			t.Fatalf("essay_score=%v final_score=%v, want both NULL before grading",
				essay, final)
		}
		resp, body := getWith(t, resultsURL, ck)
		if resp.StatusCode != http.StatusOK || !contains(body, "Menunggu penilaian") {
			t.Fatalf("results = %d, awaiting marker %v",
				resp.StatusCode, contains(body, "Menunggu penilaian"))
		}
	})

	t.Run("grading page groups answers per essay question", func(t *testing.T) {
		resp, body := getWith(t, gradingURL, ck)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("grading page = %d: %s", resp.StatusCode, body)
		}
		// one card per essay question — es1/es2/es3 hold seq 3/4/5; the
		// unanswered es3 card still renders its own empty state
		for _, want := range []string{
			"Pertanyaan 3", "Pertanyaan 4", "Pertanyaan 5",
			"Jawaban satu.", "Jawaban dua.",
			"Belum ada jawaban untuk pertanyaan ini.",
		} {
			if !contains(body, want) {
				t.Fatalf("grading page missing %q", want)
			}
		}
		// SAME endpoint + fetch attributes on every per-student form
		wantAction := fmt.Sprintf(`action="/teacher/quiz/%d/grading/%d" data-fetch data-json="true"`,
			quizID, ans1)
		if !contains(body, wantAction) {
			t.Fatalf("grading form action missing %q", wantAction)
		}
		// pg questions never render on the grading page (their texts only
		// live in the question-form/quiz-detail templates, never in chrome)
		if contains(body, "Pilihan ganda") {
			t.Fatalf("grading page renders a non-essay question")
		}
		// the page-level empty state must not fire while questions exist
		if contains(body, "Tidak ada yang dinilai") {
			t.Fatalf("page-level empty state rendered with essay questions present")
		}
	})

	t.Run("partial grading adds exactly score/total, final stays NULL", func(t *testing.T) {
		if resp, body := grade(t, ans1, 100); resp.StatusCode != http.StatusOK {
			t.Fatalf("grade es1 = %d: %s", resp.StatusCode, body)
		}
		essay, final := readScores(t)
		// 100/5 == CentiPercent(1,5): one essay at 100 weighs one correct MCQ
		if !essay.Valid || essay.Float64 != mcqOne {
			t.Fatalf("essay_score = %v, want %v (100/total == CentiPercent(1,total))",
				essay.Float64, mcqOne)
		}
		if final.Valid {
			t.Fatalf("final_score = %v, want NULL while es2 is ungraded", final.Float64)
		}
		resp, body := getWith(t, resultsURL, ck)
		if resp.StatusCode != http.StatusOK || !contains(body, "Menunggu penilaian") {
			t.Fatalf("results = %d, awaiting marker %v",
				resp.StatusCode, contains(body, "Menunggu penilaian"))
		}
	})

	t.Run("invalid scores are rejected", func(t *testing.T) {
		cases := []struct {
			name  string
			score any
			msg   string
		}{
			{"empty", "", "Nilai wajib diisi."},
			{"null", nil, "Nilai wajib diisi."},
			{"below zero", -1, "Nilai harus antara 0 dan 100."},
			{"above 100", 101, "Nilai harus antara 0 dan 100."},
			{"not a number", "abc", "Nilai harus antara 0 dan 100."},
			{"NaN", "NaN", "Nilai harus antara 0 dan 100."},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				resp, body := grade(t, ans2, c.score)
				assertFail(t, resp, body, http.StatusBadRequest, "VALIDATION", c.msg)
			})
		}
		// absent key (no score field at all) — the same nil branch as null
		t.Run("missing", func(t *testing.T) {
			resp, body := postJSON(t, fmt.Sprintf("%s/%d", gradingURL, ans2),
				map[string]any{}, ck)
			assertFail(t, resp, body, http.StatusBadRequest, "VALIDATION",
				"Nilai wajib diisi.")
		})
		// rejected saves leave the partial state untouched
		essay, final := readScores(t)
		if !essay.Valid || essay.Float64 != mcqOne || final.Valid {
			t.Fatalf("after rejects: essay_score=%v final_score=%v, want %v/NULL",
				essay, final, mcqOne)
		}
	})

	t.Run("all essays graded finalizes the final", func(t *testing.T) {
		if resp, body := grade(t, ans2, 100); resp.StatusCode != http.StatusOK {
			t.Fatalf("grade es2 = %d: %s", resp.StatusCode, body)
		}
		essay, final := readScores(t)
		// 200/total — the second save added exactly score/total again
		if !essay.Valid || essay.Float64 != 2*mcqOne {
			t.Fatalf("essay_score = %v, want %v (200/total)", essay.Float64, 2*mcqOne)
		}
		// final = auto (1/5) + essays (2×100/5) = 3/5 of the exam: an
		// essay-100 and a correct MCQ carry identical weight
		if !final.Valid || final.Float64 != quizengine.CentiPercent(3, total) {
			t.Fatalf("final_score = %v, want %v", final.Float64,
				quizengine.CentiPercent(3, total))
		}
		resp, body := getWith(t, resultsURL, ck)
		if resp.StatusCode != http.StatusOK || !contains(body, "60.00") {
			t.Fatalf("results = %d: %s", resp.StatusCode, body)
		}
		if contains(body, "Menunggu penilaian") {
			t.Fatalf("awaiting marker still present after all essays graded")
		}
	})
}

// TestGradingWithoutEssayQuestions pins the no-essay edge of the grading
// surface: a quiz with zero essay questions renders the page-level empty
// state without panicking, and GradeAnswer on such a quiz misses the
// is_correct IS NULL filter → 404 (the pg answer's is_correct is set).
func TestGradingWithoutEssayQuestions(t *testing.T) {
	ts, _, pool := globalFixture(t)
	ck := guruLogin(t, ts)

	quizID := createQuiz(t, ts, ck, quizForm("Tanpa esai"))
	pg := addQuestion(t, ts, pool, ck, "Pilihan ganda tunggal", "pg",
		[]string{"A", "B"}, []int{0})
	compose(t, ts, ck, quizID, pg)
	if resp, body := setStatus(t, ts, ck, quizID, "aktif"); resp.StatusCode != http.StatusOK ||
		!decodeEnv(t, body).OK {
		t.Fatalf("activate = %d: %s", resp.StatusCode, body)
	}
	code := joinCode(t, pool, quizID)
	st := studentCookie(t, pool, "grading-no-essay")
	if resp, body := postJoin(t, ts, code, st); resp.StatusCode != http.StatusOK {
		t.Fatalf("join = %d: %s", resp.StatusCode, body)
	}
	startGlobal(t, ts, ck, quizID)
	if resp, body := answerPost(t, ts, code, st, pg, []int{0}); resp.StatusCode != http.StatusOK {
		t.Fatalf("answer = %d: %s", resp.StatusCode, body)
	}

	gradingURL := fmt.Sprintf("%s/teacher/quiz/%d/grading", ts.URL, quizID)

	t.Run("page-level empty state, no panic", func(t *testing.T) {
		resp, body := getWith(t, gradingURL, ck)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("grading page = %d: %s", resp.StatusCode, body)
		}
		if !contains(body, "Tidak ada yang dinilai — belum ada jawaban esai.") {
			t.Fatalf("page-level empty state missing (body %d bytes)", len(body))
		}
	})

	t.Run("GradeAnswer misses the essay filter", func(t *testing.T) {
		var answerID uint64
		if err := pool.QueryRow(`SELECT a.id FROM answers a
			JOIN participants p ON p.id = a.participant_id
			WHERE p.quiz_id = ?`, quizID).Scan(&answerID); err != nil {
			t.Fatalf("pg answer id: %v", err)
		}
		// the pg answer carries is_correct — outside the is_correct IS NULL
		// grading scope → not found, scores untouched
		resp, body := postJSON(t, fmt.Sprintf("%s/%d", gradingURL, answerID),
			map[string]any{"score": 100}, ck)
		assertFail(t, resp, body, http.StatusNotFound, "NOT_FOUND",
			"Jawaban esai tidak ditemukan.")
	})
}
