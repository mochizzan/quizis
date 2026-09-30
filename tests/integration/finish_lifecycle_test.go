package integration

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"quiz/internal/cache"
	"quiz/internal/handlers"
)

// finishEnv is what every finish-lifecycle setup hands to the table: the
// server, the pool, the teacher session and the quiz under test.
type finishEnv struct {
	ts   *httptest.Server
	pool *sql.DB
	ck   *http.Cookie
	qid  uint64
}

// buildFinishQuiz creates a quiz with one composed pg question (global
// timer, spec §6 defaults) and activates it unless wantActive is false.
func buildFinishQuiz(t *testing.T, ts *httptest.Server, pool *sql.DB, ck *http.Cookie, wantActive bool) uint64 {
	t.Helper()
	qid := createQuiz(t, ts, ck, quizForm("Finish lifecycle"))
	q := addQuestion(t, ts, pool, ck, "Pick A", "pg", []string{"A", "B"}, []int{0})
	compose(t, ts, ck, qid, q)
	if wantActive {
		resp, body := setStatus(t, ts, ck, qid, "aktif")
		if resp.StatusCode != http.StatusOK || !decodeEnv(t, body).OK {
			t.Fatalf("activate = %d: %s", resp.StatusCode, body)
		}
	}
	return qid
}

// closeFinishOK JSON-closes the quiz and asserts the success envelope
// {status:"selesai"} — used by setups that must reach selesai first.
func closeFinishOK(t *testing.T, e *finishEnv) {
	t.Helper()
	resp, body := postJSON(t,
		fmt.Sprintf("%s/teacher/quiz/%d/status", e.ts.URL, e.qid),
		map[string]any{"status": "selesai"}, e.ck)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("setup close = %d, want 200: %s", resp.StatusCode, body)
	}
	var data struct {
		Status string `json:"status"`
	}
	env := decodeEnv(t, body)
	if !env.OK || json.Unmarshal(env.Data, &data) != nil || data.Status != "selesai" {
		t.Fatalf("setup close envelope = %s", body)
	}
}

// finishActiveFixture: zero-participant active quiz on the full quiz fixture.
func finishActiveFixture(t *testing.T) *finishEnv {
	t.Helper()
	ts, pool, _, _ := quizFixture(t)
	ck := guruLogin(t, ts)
	return &finishEnv{ts: ts, pool: pool, ck: ck, qid: buildFinishQuiz(t, ts, pool, ck, true)}
}

// finishClosedFixture: same quiz, already closed once (zero-worker close →
// selesai) — the setup for every "again" / "from selesai" case.
func finishClosedFixture(t *testing.T) *finishEnv {
	t.Helper()
	e := finishActiveFixture(t)
	closeFinishOK(t, e)
	return e
}

// TestFinishLifecycle drives the finish/status edge cases of plan §5
// (E2–E4) through POST /teacher/quiz/:id/status and /start: closing with
// zero participants, closing twice, closing a nonaktif quiz, every illegal
// transition out of the terminal selesai state, and closing a running
// (berjalan) quiz through the status endpoint the table's finish action
// calls.
func TestFinishLifecycle(t *testing.T) {
	cases := []struct {
		name       string
		setup      func(t *testing.T) *finishEnv
		target     string // "status" → POST /status, "start" → POST /start
		payload    any
		wantStatus int
		wantMsg    string // exact failure message; "" → expect 200 success
		wantDB     string // quizzes.status afterwards
	}{
		{
			// E3: nobody joined — close succeeds outright.
			name:       "finish with zero participants",
			setup:      finishActiveFixture,
			target:     "status",
			payload:    map[string]any{"status": "selesai"},
			wantStatus: http.StatusOK,
			wantDB:     "selesai",
		},
		{
			// E2: plain double finish (zero-worker close, no per-question
			// close, no concurrent STOP) still loses with 409.
			name:       "double finish after zero-worker close",
			setup:      finishClosedFixture,
			target:     "status",
			payload:    map[string]any{"status": "selesai"},
			wantStatus: http.StatusConflict,
			wantMsg:    "Kuis ini tidak sedang berjalan.",
			wantDB:     "selesai",
		},
		{
			// E3: a quiz that never ran cannot be finished.
			name: "finish while nonaktif",
			setup: func(t *testing.T) *finishEnv {
				ts, pool, _, _ := quizFixture(t)
				ck := guruLogin(t, ts)
				return &finishEnv{
					ts: ts, pool: pool, ck: ck,
					qid: buildFinishQuiz(t, ts, pool, ck, false),
				}
			},
			target:     "status",
			payload:    map[string]any{"status": "selesai"},
			wantStatus: http.StatusConflict,
			wantMsg:    "Kuis ini tidak sedang berjalan.",
			wantDB:     "nonaktif",
		},
		{
			// E4a: selesai is terminal — no reactivation.
			name:       "activate finished quiz",
			setup:      finishClosedFixture,
			target:     "status",
			payload:    map[string]any{"status": "aktif"},
			wantStatus: http.StatusConflict,
			wantMsg:    "Kuis sudah berakhir.",
			wantDB:     "selesai",
		},
		{
			// E4b: selesai is terminal — no deactivation.
			name:       "deactivate finished quiz",
			setup:      finishClosedFixture,
			target:     "status",
			payload:    map[string]any{"status": "nonaktif"},
			wantStatus: http.StatusConflict,
			wantMsg:    "Kuis sudah berakhir.",
			wantDB:     "selesai",
		},
		{
			// E4c: Start checks timer type first, so this must be a
			// global-timer quiz (quizForm default).
			name:       "start finished global-timer quiz",
			setup:      finishClosedFixture,
			target:     "start",
			payload:    nil,
			wantStatus: http.StatusConflict,
			wantMsg:    "Kuis ini sudah berakhir.",
			wantDB:     "selesai",
		},
		{
			// The endpoint the table's finish action calls for a running
			// quiz: start (zero participants is legal, spec §8) then close
			// through /status, not /stop.
			name: "close running quiz via status",
			setup: func(t *testing.T) *finishEnv {
				ts, _, pool := globalFixture(t)
				ck := guruLogin(t, ts)
				qid, _, _ := linearQuiz(t, ts, pool, ck, "Running close", 1)
				startGlobal(t, ts, ck, qid)
				if got := quizStatus(t, pool, qid); got != "berjalan" {
					t.Fatalf("quiz status after start = %q, want berjalan", got)
				}
				return &finishEnv{ts: ts, pool: pool, ck: ck, qid: qid}
			},
			target:     "status",
			payload:    map[string]any{"status": "selesai"},
			wantStatus: http.StatusOK,
			wantDB:     "selesai",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := tc.setup(t)
			var url string
			switch tc.target {
			case "status":
				url = fmt.Sprintf("%s/teacher/quiz/%d/status", e.ts.URL, e.qid)
			case "start":
				url = fmt.Sprintf("%s/teacher/quiz/%d/start", e.ts.URL, e.qid)
			default:
				t.Fatalf("unknown target %q", tc.target)
			}
			resp, body := postJSON(t, url, tc.payload, e.ck)

			if tc.wantMsg == "" {
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("POST /%s = %d, want 200: %s", tc.target, resp.StatusCode, body)
				}
				env := decodeEnv(t, body)
				if !env.OK {
					t.Fatalf("success envelope expected: %s", body)
				}
				var data struct {
					Status string `json:"status"`
				}
				if err := json.Unmarshal(env.Data, &data); err != nil || data.Status != "selesai" {
					t.Fatalf("data = %s (%v), want status selesai", env.Data, err)
				}
			} else {
				assertFail(t, resp, body, tc.wantStatus, handlers.ErrConflict, tc.wantMsg)
			}
			if got := quizStatus(t, e.pool, e.qid); got != tc.wantDB {
				t.Fatalf("quiz status = %q, want %q", got, tc.wantDB)
			}
		})
	}
}

// TestFinishLifecycleListCacheInvalidated pins the server half of E7: the
// close commit must drop the home-list mirror so the next render shows the
// Finished chip instead of the stale row.
func TestFinishLifecycleListCacheInvalidated(t *testing.T) {
	ts, pool, store, _ := quizFixture(t)
	ck := guruLogin(t, ts)
	qid := buildFinishQuiz(t, ts, pool, ck, true)

	store.Set(cache.QuizListKey(), "primed-by-test", 0)
	if !store.Has(cache.QuizListKey()) {
		t.Fatal("list mirror did not prime")
	}

	resp, body := postJSON(t,
		fmt.Sprintf("%s/teacher/quiz/%d/status", ts.URL, qid),
		map[string]any{"status": "selesai"}, ck)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("close = %d, want 200: %s", resp.StatusCode, body)
	}
	if store.Has(cache.QuizListKey()) {
		t.Fatal("list mirror survived the close commit")
	}
	if got := quizStatus(t, pool, qid); got != "selesai" {
		t.Fatalf("quiz status = %q, want selesai", got)
	}
}
