package integration

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"quiz/internal/quizengine"
)

// --- helpers ---------------------------------------------------------------

// shuffledAttempt carries the fixture context for the shuffled per_soal
// server-truth contracts.
type shuffledAttempt struct {
	Code      string
	QuizID    uint64
	IDs       []uint64 // question ids in seq order
	Teks      []string // question texts in seq order
	PID       uint64   // pinned participant id (stable shuffle seed)
	AnswerIdx int      // ORIGINAL option index the murid submits
}

// shuffledPersoalFixture builds an ACTIVE per_soal quiz with question AND
// option shuffle (plus ranking_live), joins the murid and starts the attempt.
// It pins the participant id to a seed whose option permutation for the FIRST
// question moves original index 2 off its identity slot, so the Bug-1 pin
// (display letter computed from qorder) discriminates against naive
// index→letter rendering on every run — participants.id is PK-only (no FKs,
// spec §5), safe to re-key before the attempt starts.
func shuffledPersoalFixture(t *testing.T, ts *httptest.Server, pool *sql.DB,
	ck, st *http.Cookie, title string, questions []string,
) shuffledAttempt {
	t.Helper()
	form := quizForm(title)
	form.Set("timer_type", "per_soal")
	form.Set("per_question_seconds", "60")
	form.Set("join_mode", "open")
	form.Set("shuffle_questions", "1")
	form.Set("shuffle_options", "1")
	form.Set("ranking_live", "1")
	quizID := createQuiz(t, ts, ck, form)

	ids := make([]uint64, 0, len(questions))
	for _, teks := range questions {
		ids = append(ids, addQuestion(t, ts, pool, ck, teks, "pg",
			[]string{"Alpha", "Beta", "Gamma", "Delta"}, []int{0}))
	}
	compose(t, ts, ck, quizID, ids...)
	if resp, body := setStatus(t, ts, ck, quizID, "aktif"); resp.StatusCode != http.StatusOK || !decodeEnv(t, body).OK {
		t.Fatalf("activate = %d: %s", resp.StatusCode, body)
	}
	code := joinCode(t, pool, quizID)
	if resp, body := postJoin(t, ts, code, st); resp.StatusCode != http.StatusOK {
		t.Fatalf("join = %d: %s", resp.StatusCode, body)
	}

	// candidate seeds mirror makeQOrder exactly (seq-ordered ids, option
	// counts from the composed questions, attempt 1)
	counts := make(map[uint64]int, len(ids))
	for _, id := range ids {
		counts[id] = 4
	}
	answerIdx := 2 // identity letter would be "C"
	var pinned uint64
	for cand := uint64(900001); cand < 901000; cand++ {
		o := quizengine.BuildOrder(quizengine.SeedFor(cand, quizID, 1), ids, counts, true, true)
		if quizengine.DisplayLetters(&o, ids[0], []int{answerIdx}) != "C" {
			pinned = cand
			break
		}
	}
	if pinned == 0 {
		t.Fatal("no pinned participant id with a non-identity display letter")
	}
	pid, _, _ := participantRowFor(t, pool, quizID, userOf(t, ts, st))
	if _, err := pool.Exec(`UPDATE participants SET id = ? WHERE id = ?`, pinned, pid); err != nil {
		t.Fatalf("pin participant id: %v", err)
	}

	startAttempt(t, ts, code, st)
	return shuffledAttempt{
		Code: code, QuizID: quizID, IDs: ids, Teks: questions,
		PID: pinned, AnswerIdx: answerIdx,
	}
}

// answerFrame is the SSE `answer` payload published to the teacher topic
// (design C3).
type answerFrame struct {
	Type string `json:"type"`
	Data struct {
		AnswerDisplay string `json:"answer_display"`
		QTeks         string `json:"q_teks"`
		QuestionPos   int    `json:"question_pos"`
	} `json:"data"`
}

// readAnswerFrame drains monitor frames until the `answer` event arrives
// (rank/heartbeat siblings are skipped); fails when the stream ends first.
func readAnswerFrame(t *testing.T, conn *sseConn) answerFrame {
	t.Helper()
	var frame answerFrame
	for range 8 {
		ev, data, _, ok := conn.readEvent(3 * time.Second)
		if !ok {
			break
		}
		if ev != "answer" {
			continue
		}
		if err := json.Unmarshal([]byte(data), &frame); err != nil {
			t.Fatalf("answer frame JSON: %v (%s)", err, data)
		}
		break
	}
	if frame.Type != "answer" {
		t.Fatal("no `answer` frame reached the monitor stream")
	}
	return frame
}

// contractMonitorCard mirrors the monitor-data card fields the server-truth
// contracts pin (design C3/C4/C12).
type contractMonitorCard struct {
	ParticipantID     uint64 `json:"participant_id"`
	AnswerDisplay     string `json:"answer_display"`
	AnswerQTeks       string `json:"answer_q_teks"`
	AnswerQuestionPos int    `json:"answer_question_pos"`
	CurrentQTeks      string `json:"current_q_teks"`
}

// contractMonitorBlob is the monitor-data script payload (design C4/C12).
type contractMonitorBlob struct {
	RankingLive  bool                  `json:"ranking_live"`
	Participants []contractMonitorCard `json:"participants"`
}

// monitorContractBlobOf decodes the id="monitor-data" JSON blob out of a
// rendered monitor page (same wire shape the SSE snapshot carries).
func monitorContractBlobOf(t *testing.T, body string) contractMonitorBlob {
	t.Helper()
	const openTag = `<script type="application/json" id="monitor-data">`
	_, rest, ok := strings.Cut(body, openTag)
	if !ok {
		t.Fatal("monitor-data blob missing from monitor page")
	}
	payload, _, ok := strings.Cut(rest, "</script>")
	if !ok {
		t.Fatal("monitor-data blob not closed")
	}
	var blob contractMonitorBlob
	if err := json.Unmarshal([]byte(payload), &blob); err != nil {
		t.Fatalf("monitor-data JSON: %v (%s)", err, payload)
	}
	return blob
}

func contractCardOf(b contractMonitorBlob, pid uint64) *contractMonitorCard {
	for i := range b.Participants {
		if b.Participants[i].ParticipantID == pid {
			return &b.Participants[i]
		}
	}
	return nil
}

// wsContractBlob carries the C6 timer anchors out of the SSR ws-data script.
type wsContractBlob struct {
	PerQuestionSeconds int   `json:"per_question_seconds"`
	QSince             int64 `json:"q_since"`
	Current            int   `json:"current"`
	Questions          []struct {
		ID uint64 `json:"id"`
	} `json:"questions"`
}

func wsContractBlobOf(t *testing.T, body string) wsContractBlob {
	t.Helper()
	const openTag = `<script type="application/json" id="ws-data">`
	_, rest, ok := strings.Cut(body, openTag)
	if !ok {
		t.Fatal("ws-data blob missing from workspace page")
	}
	payload, _, ok := strings.Cut(rest, "</script>")
	if !ok {
		t.Fatal("ws-data blob not closed")
	}
	var blob wsContractBlob
	if err := json.Unmarshal([]byte(payload), &blob); err != nil {
		t.Fatalf("ws-data JSON: %v (%s)", err, payload)
	}
	return blob
}

// --- contracts -------------------------------------------------------------

// TestShuffledAnswerDisplayContract pins the Bug-1 fix end-to-end (design
// C3/C12): the letter shown for a submitted answer comes from THIS murid's
// qorder option permutation — on the live SSE `answer` frame AND on the
// monitor snapshot — never from the raw stored index. A subscriber is
// available here, so the SSE path is asserted directly (not via snapshot
// fallback).
func TestShuffledAnswerDisplayContract(t *testing.T) {
	ts, pool, _, _ := quizFixture(t)
	ck := guruLogin(t, ts)
	st := studentCookie(t, pool, "contract-display")
	fx := shuffledPersoalFixture(t, ts, pool, ck, st, "Shuffled Display Contract",
		[]string{"Pertanyaan kontrak satu.", "Pertanyaan kontrak dua.", "Pertanyaan kontrak tiga."})

	order := storedQOrder(t, pool, fx.PID)
	if len(order.Questions) != len(fx.IDs) {
		t.Fatalf("qorder carries %d questions, want %d", len(order.Questions), len(fx.IDs))
	}
	want := quizengine.DisplayLetters(&order, fx.IDs[0], []int{fx.AnswerIdx})
	if want == "C" {
		t.Fatalf("fixture option perm %v renders identity %q — pin drifted",
			order.Options[fx.IDs[0]], want)
	}
	wantPos := 0
	for i, qid := range order.Questions {
		if qid == fx.IDs[0] {
			wantPos = i + 1
			break
		}
	}
	if wantPos == 0 {
		t.Fatalf("answered question %d missing from qorder %v", fx.IDs[0], order.Questions)
	}

	// live monitor subscriber BEFORE the submit → the `answer` frame must
	// carry answer_display + q_teks (design C3)
	conn := openSSE(t, ts.URL+fmt.Sprintf("/teacher/quiz/%d/monitor/stream", fx.QuizID), ck)
	defer conn.resp.Body.Close()
	if ev, _, _, ok := conn.readEvent(3 * time.Second); !ok || ev != "snapshot" {
		t.Fatalf("teacher greeting = %q ok=%v, want snapshot", ev, ok)
	}

	resp, body := answerPost(t, ts, fx.Code, st, fx.IDs[0], []int{fx.AnswerIdx})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("answer = %d: %s", resp.StatusCode, body)
	}

	frame := readAnswerFrame(t, conn)
	if frame.Data.AnswerDisplay != want {
		t.Errorf("sse answer_display = %q, want qorder letter %q", frame.Data.AnswerDisplay, want)
	}
	if frame.Data.QTeks != fx.Teks[0] {
		t.Errorf("sse q_teks = %q, want %q", frame.Data.QTeks, fx.Teks[0])
	}
	if frame.Data.QuestionPos != wantPos {
		t.Errorf("sse question_pos = %d, want %d", frame.Data.QuestionPos, wantPos)
	}

	// monitor snapshot (design C4/C12): answer_display, answer_q_teks and
	// ranking_live ride the monitor-data blob
	resp, body = getWith(t, fmt.Sprintf("%s/teacher/quiz/%d/monitor", ts.URL, fx.QuizID), ck)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("monitor = %d: %s", resp.StatusCode, body)
	}
	blob := monitorContractBlobOf(t, body)
	if !blob.RankingLive {
		t.Error("monitor blob ranking_live = false, want true")
	}
	card := contractCardOf(blob, fx.PID)
	if card == nil {
		t.Fatalf("participant %d missing from monitor blob", fx.PID)
	}
	if card.AnswerDisplay != want {
		t.Errorf("monitor answer_display = %q, want qorder letter %q", card.AnswerDisplay, want)
	}
	if card.AnswerQTeks != fx.Teks[0] {
		t.Errorf("monitor answer_q_teks = %q, want %q", card.AnswerQTeks, fx.Teks[0])
	}
	if card.AnswerQuestionPos != wantPos {
		t.Errorf("monitor answer_question_pos = %d, want %d", card.AnswerQuestionPos, wantPos)
	}
}

// TestPerQuestionTimerAnchorPayloads pins the design C6/C7 wire fields: the
// SSR ws blob carries per_question_seconds + q_since, and /next answers with
// current_q_since. (Backward /next → 409 is pinned separately by
// TestNextNavigationAndFreeModeGuard — design C7.)
func TestPerQuestionTimerAnchorPayloads(t *testing.T) {
	ts, pool, _, _ := quizFixture(t)
	ck := guruLogin(t, ts)
	st := studentCookie(t, pool, "contract-timers")

	form := quizForm("Timer Anchors")
	form.Set("timer_type", "per_soal")
	form.Set("per_question_seconds", "60")
	quizID := createQuiz(t, ts, ck, form)
	q1 := addQuestion(t, ts, pool, ck, "Anchor one.", "pg", []string{"One", "Two"}, []int{0})
	q2 := addQuestion(t, ts, pool, ck, "Anchor two.", "pg", []string{"One", "Two"}, []int{0})
	compose(t, ts, ck, quizID, q1, q2)
	if resp, body := setStatus(t, ts, ck, quizID, "aktif"); resp.StatusCode != http.StatusOK || !decodeEnv(t, body).OK {
		t.Fatalf("activate = %d: %s", resp.StatusCode, body)
	}
	code := joinCode(t, pool, quizID)
	if resp, body := postJoin(t, ts, code, st); resp.StatusCode != http.StatusOK {
		t.Fatalf("join = %d: %s", resp.StatusCode, body)
	}
	startAttempt(t, ts, code, st)

	// SSR ws blob: per_question_seconds + q_since (design C6)
	resp, body := getWith(t, ts.URL+"/quiz/"+code, st)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("workspace = %d", resp.StatusCode)
	}
	blob := wsContractBlobOf(t, body)
	if blob.PerQuestionSeconds != 60 {
		t.Errorf("per_question_seconds = %d, want 60", blob.PerQuestionSeconds)
	}
	if blob.QSince <= 0 {
		t.Errorf("q_since = %d, want unix > 0", blob.QSince)
	}
	firstQSince := blob.QSince

	// /next response carries current_q_since (design C7)
	resp, body = postJSON(t, ts.URL+"/quiz/"+code+"/next", nil, st)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("next = %d: %s", resp.StatusCode, body)
	}
	env := decodeEnv(t, body)
	if !env.OK {
		t.Fatalf("next envelope: %s", body)
	}
	var nd struct {
		CurrentQ      int   `json:"current_q"`
		CurrentQSince int64 `json:"current_q_since"`
	}
	if err := json.Unmarshal(env.Data, &nd); err != nil {
		t.Fatalf("next data %q: %v", env.Data, err)
	}
	if nd.CurrentQ != 2 {
		t.Errorf("current_q = %d, want 2", nd.CurrentQ)
	}
	if nd.CurrentQSince <= 0 {
		t.Errorf("current_q_since = %d, want unix > 0", nd.CurrentQSince)
	}

	// the refreshed page resumes from the NEW anchor (monotonic)
	resp, body = getWith(t, ts.URL+"/quiz/"+code, st)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("workspace after next = %d", resp.StatusCode)
	}
	if got := wsContractBlobOf(t, body); got.QSince < firstQSince {
		t.Errorf("q_since after next = %d, want >= %d", got.QSince, firstQSince)
	}
}

// TestCorruptQOrderDegradesToIdentity pins the corrupt-qorder fallback
// (design C12): an undecodable participants.qorder is logged by decodeOrder
// and BOTH surfaces degrade consistently to identity/seq order — the monitor
// renders the stored answer with identity letters (no position attribution,
// no current-question text) and the student workspace renders the questions
// in seq order. No panic, no divergent shuffle.
func TestCorruptQOrderDegradesToIdentity(t *testing.T) {
	ts, pool, _, _ := quizFixture(t)
	ck := guruLogin(t, ts)
	st := studentCookie(t, pool, "contract-corrupt")
	fx := shuffledPersoalFixture(t, ts, pool, ck, st, "Korder rusak",
		[]string{"Korup satu.", "Korup dua.", "Korup tiga."})

	// submit the first question's answer while the qorder is still valid —
	// stored original index fx.AnswerIdx renders a NON-identity letter under
	// the shuffled permutation (fixture-pinned), so the identity assertion
	// below discriminates against both shuffled and broken rendering
	if resp, body := answerPost(t, ts, fx.Code, st, fx.IDs[0],
		[]int{fx.AnswerIdx}); resp.StatusCode != http.StatusOK {
		t.Fatalf("answer = %d: %s", resp.StatusCode, body)
	}

	// NULL (row predates the column / start path wrote none) and
	// undecodable (valid JSON — the JSON column enforces JSON_VALID — that
	// does not unmarshal into an Order) both hit decodeOrder's fallback
	for _, c := range []struct {
		name string
		stmt string
	}{
		{"null qorder", `UPDATE participants SET qorder = NULL WHERE id = ?`},
		{
			"undecodable qorder",
			`UPDATE participants SET qorder = '{"questions":"corrupt"}' WHERE id = ?`,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := pool.Exec(c.stmt, fx.PID); err != nil {
				t.Fatalf("set qorder: %v", err)
			}

			t.Run("monitor degrades to identity without panicking", func(t *testing.T) {
				resp, body := getWith(t,
					fmt.Sprintf("%s/teacher/quiz/%d/monitor", ts.URL, fx.QuizID), ck)
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("monitor = %d: %s", resp.StatusCode, body)
				}
				blob := monitorContractBlobOf(t, body)
				card := contractCardOf(blob, fx.PID)
				if card == nil {
					t.Fatalf("participant %d missing from monitor blob", fx.PID)
				}
				// stored [2] → identity letter "C" (the shuffled display
				// differs — pinned by TestShuffledAnswerDisplayContract)
				if card.AnswerDisplay != "C" {
					t.Errorf("answer_display = %q, want identity %q",
						card.AnswerDisplay, "C")
				}
				// no decodable order → no qorder position / no current text
				if card.AnswerQuestionPos != 0 {
					t.Errorf("answer_question_pos = %d, want 0 (no order to attribute)",
						card.AnswerQuestionPos)
				}
				if card.CurrentQTeks != "" {
					t.Errorf("current_q_teks = %q, want empty (no order to read)",
						card.CurrentQTeks)
				}
				// the answer text itself still comes from the questions JOIN
				if card.AnswerQTeks != fx.Teks[0] {
					t.Errorf("answer_q_teks = %q, want %q", card.AnswerQTeks, fx.Teks[0])
				}
			})

			t.Run("student workspace degrades to seq order without panicking", func(t *testing.T) {
				resp, body := getWith(t, ts.URL+"/quiz/"+fx.Code, st)
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("workspace = %d: %s", resp.StatusCode, body)
				}
				blob := wsContractBlobOf(t, body)
				if len(blob.Questions) != len(fx.IDs) {
					t.Fatalf("questions = %d, want %d", len(blob.Questions), len(fx.IDs))
				}
				for i, q := range blob.Questions {
					if q.ID != fx.IDs[i] {
						t.Fatalf("question %d = id %d, want seq id %d (identity order)",
							i+1, q.ID, fx.IDs[i])
					}
				}
				if blob.Current < 1 || blob.Current > len(fx.IDs) {
					t.Errorf("current = %d, want 1..%d", blob.Current, len(fx.IDs))
				}
			})
		})
	}
}

// contractSnapshotEnvelope is the greeting `snapshot` frame on the teacher
// topic (design C4): monitorData's template-keyed map under the hub's
// {"type","data"} envelope.
type contractSnapshotEnvelope struct {
	Type string `json:"type"`
	Data struct {
		RankingLive bool                  `json:"RankingLive"`
		Cards       []contractMonitorCard `json:"Cards"`
	} `json:"data"`
}

// TestMonitorSnapshotFieldsAcrossReconnect pins design C4 on the LIVE
// greeting path: every connection (first subscribe AND reconnect) is greeted
// by a snapshot carrying answer_display, answer_q_teks, current_q_teks and
// ranking_live — the same fields the SSR monitor-data blob carries.
func TestMonitorSnapshotFieldsAcrossReconnect(t *testing.T) {
	ts, pool, _, _ := quizFixture(t)
	ck := guruLogin(t, ts)
	st := studentCookie(t, pool, "contract-reconnect")
	fx := shuffledPersoalFixture(t, ts, pool, ck, st, "Snapshot reconnect",
		[]string{"Snapshot satu.", "Snapshot dua.", "Snapshot tiga."})

	if resp, body := answerPost(t, ts, fx.Code, st, fx.IDs[0],
		[]int{fx.AnswerIdx}); resp.StatusCode != http.StatusOK {
		t.Fatalf("answer = %d: %s", resp.StatusCode, body)
	}
	order := storedQOrder(t, pool, fx.PID)
	wantDisplay := quizengine.DisplayLetters(&order, fx.IDs[0], []int{fx.AnswerIdx})
	// current_q = 1 → the FIRST question of this murid's shuffled order,
	// whose text is not necessarily the first question's (seq order)
	posOf := map[uint64]int{}
	for i, qid := range fx.IDs {
		posOf[qid] = i
	}
	firstPos, ok := posOf[order.Questions[0]]
	if !ok {
		t.Fatalf("shuffled first question %d missing from seq ids %v",
			order.Questions[0], fx.IDs)
	}
	wantCurrentTeaks := fx.Teks[firstPos]

	readGreeting := func(t *testing.T, conn *sseConn) contractSnapshotEnvelope {
		t.Helper()
		ev, data, _, ok := conn.readEvent(3 * time.Second)
		if !ok || ev != "snapshot" {
			t.Fatalf("greeting = %q ok=%v, want snapshot", ev, ok)
		}
		var env contractSnapshotEnvelope
		if err := json.Unmarshal([]byte(data), &env); err != nil {
			t.Fatalf("greeting JSON: %v (%s)", err, data)
		}
		if env.Type != "snapshot" {
			t.Fatalf("greeting envelope type = %q, want snapshot", env.Type)
		}
		return env
	}
	assertFields := func(t *testing.T, env contractSnapshotEnvelope) {
		t.Helper()
		if !env.Data.RankingLive {
			t.Error("snapshot ranking_live = false, want true")
		}
		var card *contractMonitorCard
		for i := range env.Data.Cards {
			if env.Data.Cards[i].ParticipantID == fx.PID {
				card = &env.Data.Cards[i]
				break
			}
		}
		if card == nil {
			t.Fatalf("participant %d missing from greeting snapshot", fx.PID)
		}
		if card.AnswerDisplay != wantDisplay {
			t.Errorf("snapshot answer_display = %q, want qorder letter %q",
				card.AnswerDisplay, wantDisplay)
		}
		if card.AnswerQTeks != fx.Teks[0] {
			t.Errorf("snapshot answer_q_teks = %q, want %q", card.AnswerQTeks, fx.Teks[0])
		}
		if card.CurrentQTeks != wantCurrentTeaks {
			t.Errorf("snapshot current_q_teks = %q, want %q", card.CurrentQTeks, wantCurrentTeaks)
		}
	}

	url := ts.URL + fmt.Sprintf("/teacher/quiz/%d/monitor/stream", fx.QuizID)
	conn := openSSE(t, url, ck)
	assertFields(t, readGreeting(t, conn))
	conn.resp.Body.Close() // disconnect

	// reconnect → a fresh greeting must carry the same fields
	conn2 := openSSE(t, url, ck)
	assertFields(t, readGreeting(t, conn2))
	conn2.resp.Body.Close()
}

// TestMonitorEmptyStates pins monitorData + SSR on the sparse shapes
// (design C4): a quiz nobody joined renders its empty states, and an
// essay-only quiz (zero MCQ) renders the card with NO answer line until an
// essay is written — then the line carries the essay text itself.
func TestMonitorEmptyStates(t *testing.T) {
	ts, pool, _, _ := quizFixture(t)
	ck := guruLogin(t, ts)

	t.Run("zero participants", func(t *testing.T) {
		quizID := createQuiz(t, ts, ck, quizForm("Monitor tanpa murid"))
		resp, body := getWith(t,
			fmt.Sprintf("%s/teacher/quiz/%d/monitor", ts.URL, quizID), ck)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("monitor = %d: %s", resp.StatusCode, body)
		}
		if !contains(body, "Belum ada yang bergabung.") {
			t.Error("zero-participant monitor missing the cards empty state")
		}
		if !contains(body, "Tidak ada yang menunggu.") {
			t.Error("zero-participant monitor missing the waiting empty state")
		}
	})

	t.Run("essay-only quiz, zero answers until written", func(t *testing.T) {
		quizID := createQuiz(t, ts, ck, quizForm("Esai saja"))
		es := addQuestion(t, ts, pool, ck, "Esai monitornya.", "essay", nil, nil)
		compose(t, ts, ck, quizID, es)
		if resp, body := setStatus(t, ts, ck, quizID, "aktif"); resp.StatusCode != http.StatusOK ||
			!decodeEnv(t, body).OK {
			t.Fatalf("activate = %d: %s", resp.StatusCode, body)
		}
		code := joinCode(t, pool, quizID)
		st := studentCookie(t, pool, "monitor-essay-only")
		if resp, body := postJoin(t, ts, code, st); resp.StatusCode != http.StatusOK {
			t.Fatalf("join = %d: %s", resp.StatusCode, body)
		}
		startGlobal(t, ts, ck, quizID)

		monitor := fmt.Sprintf("%s/teacher/quiz/%d/monitor", ts.URL, quizID)
		resp, body := getWith(t, monitor, ck)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("monitor = %d: %s", resp.StatusCode, body)
		}
		if !contains(body, "data-pid=") {
			t.Fatalf("started attempt missing from the monitor cards")
		}
		// zero answers → the answer line must stay hidden ({{if .Answer}})
		if contains(body, "data-answer-line") {
			t.Fatalf("answer line rendered with zero answers: %s", body)
		}

		if resp, body := answerPost(t, ts, code, st, es, "Esai terjawab."); resp.StatusCode != http.StatusOK {
			t.Fatalf("essay answer = %d: %s", resp.StatusCode, body)
		}
		resp, body = getWith(t, monitor, ck)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("monitor after answer = %d: %s", resp.StatusCode, body)
		}
		if !contains(body, "data-answer-line") {
			t.Fatalf("answer line missing after the essay answer")
		}
		if !contains(body, "Esai terjawab.") {
			t.Fatalf("answer line missing the essay text: %s", body)
		}
	})
}
