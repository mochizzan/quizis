package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"quiz/internal/quizengine"
)

// rankEntry mirrors one row of the `rank` SSE event payload (the
// quizengine.Entry JSON contract, spec §6.7.3).
type rankEntry struct {
	ParticipantID uint64  `json:"participant_id"`
	Name          string  `json:"name"`
	Score         float64 `json:"score"`
	Finished      bool    `json:"finished"`
}

// waitRankEvent drains frames until the next `rank` event and returns its
// ranking payload — the push that repaints an open monitor/leaderboard
// without a reload.
func waitRankEvent(t *testing.T, conn *sseConn) []rankEntry {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		ev, data, _, ok := conn.readEvent(time.Until(deadline))
		if !ok {
			break
		}
		if ev != "rank" {
			continue
		}
		// the frame carries the {"type","data"} envelope
		var msg struct {
			Type string `json:"type"`
			Data struct {
				Ranking []rankEntry `json:"ranking"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(data), &msg); err != nil {
			t.Fatalf("rank payload %q: %v", data, err)
		}
		if msg.Type != "rank" {
			t.Fatalf("frame type = %q, want rank", msg.Type)
		}
		return msg.Data.Ranking
	}
	t.Fatal("no `rank` event arrived on the open stream")
	return nil
}

// assertRanker pins the live ranker: every started pid is present at
// score 0 / not finished / with a name, every never-started pid is absent
// (spec §8), and nothing else sneaked in.
func assertRanker(t *testing.T, ranker *quizengine.Ranker, present, absent []uint64) {
	t.Helper()
	entries := ranker.Snapshot()
	byID := map[uint64]quizengine.Entry{}
	for _, e := range entries {
		byID[e.ParticipantID] = e
	}
	if len(entries) != len(present) {
		t.Errorf("ranking has %d entries (%+v), want exactly %d started", len(entries), entries, len(present))
	}
	for _, pid := range present {
		e, ok := byID[pid]
		if !ok {
			t.Errorf("started participant %d missing from the ranking: %+v", pid, entries)
			continue
		}
		if e.Score != 0 || e.Finished {
			t.Errorf("fresh entry %d = %+v, want score 0 / not finished", pid, e)
		}
		if e.Name == "" {
			t.Errorf("entry %d carries no name — the monitor cannot show the murid", pid)
		}
	}
	for _, pid := range absent {
		if _, ok := byID[pid]; ok {
			t.Errorf("never-started participant %d must stay out of the ranking (spec §8)", pid)
		}
	}
}

// assertRankEventIDs checks the pushed `rank` payload carries exactly the
// started pids.
func assertRankEventIDs(t *testing.T, ranking []rankEntry, present, absent []uint64) {
	t.Helper()
	byID := map[uint64]rankEntry{}
	for _, e := range ranking {
		byID[e.ParticipantID] = e
	}
	if len(ranking) != len(present) {
		t.Errorf("rank event carries %d entries (%+v), want exactly %d started", len(ranking), ranking, len(present))
	}
	for _, pid := range present {
		if e, ok := byID[pid]; !ok {
			t.Errorf("rank event missing started participant %d: %+v", pid, ranking)
		} else if e.Score != 0 || e.Finished || e.Name == "" {
			t.Errorf("rank entry %d = %+v, want score 0 / not finished / named", pid, e)
		}
	}
	for _, pid := range absent {
		if _, ok := byID[pid]; ok {
			t.Errorf("rank event leaks never-started participant %d: %+v", pid, ranking)
		}
	}
}

// The moment an attempt flips registered → started the murid must appear in
// the live ranking at score 0 (before any answer), the open monitor must get
// the `rank` push, and students who never started must stay out (spec §6.7.3,
// §8). Covers both start paths: the teacher's global START and the murid's
// per-question attempt start.
func TestStartPutsEveryStartedMuridInRanking(t *testing.T) {
	ts, globals, pool := globalFixture(t)
	ck := guruLogin(t, ts)

	t.Run("global timer", func(t *testing.T) {
		form := quizForm("Rank Global Start")
		form.Set("join_mode", "approve")
		form.Set("ranking_live", "1")
		quizID := createQuiz(t, ts, ck, form)
		q := addQuestion(t, ts, pool, ck, "Pick A", "pg", []string{"A", "B"}, []int{0})
		compose(t, ts, ck, quizID, q)
		if resp, body := setStatus(t, ts, ck, quizID, "aktif"); resp.StatusCode != http.StatusOK || !decodeEnv(t, body).OK {
			t.Fatalf("activate = %d: %s", resp.StatusCode, body)
		}
		code := joinCode(t, pool, quizID)

		join := func(username string) uint64 {
			t.Helper()
			st := studentCookie(t, pool, username)
			if resp, body := postJoin(t, ts, code, st); resp.StatusCode != http.StatusOK {
				t.Fatalf("join = %d: %s", resp.StatusCode, body)
			} else if d := decodeJoin(t, body); d.Status != "pending" {
				t.Fatalf("join status = %q, want pending (approve mode)", d.Status)
			}
			pid, _, _ := participantRowFor(t, pool, quizID, userOf(t, ts, st))
			return pid
		}
		approve := func(pid uint64) {
			t.Helper()
			resp, body := postJSON(t,
				fmt.Sprintf("%s/teacher/quiz/%d/participants/%d/action", ts.URL, quizID, pid),
				map[string]string{"action": "approve"}, ck)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("approve %d = %d: %s", pid, resp.StatusCode, body)
			}
		}
		pidA, pidB := join("rank-gstart-a"), join("rank-gstart-b")
		approve(pidA)
		approve(pidB)
		// C stays pending → auto-rejected at START, never started
		pidC := join("rank-gstart-c")

		// monitor open BEFORE the start — it must learn the ranking live
		conn := openSSE(t, ts.URL+fmt.Sprintf("/teacher/quiz/%d/monitor/stream", quizID), ck)
		defer conn.resp.Body.Close()
		if ev, _, _, ok := conn.readEvent(3 * time.Second); !ok || ev != "snapshot" {
			t.Fatalf("monitor greeting = %q (ok=%v), want snapshot", ev, ok)
		}

		startGlobal(t, ts, ck, quizID)

		// both registered murid now started with zero answers → BOTH are
		// visible immediately; the never-started pending murid stays out
		assertRanker(t, globals.Live.Ranker(quizID), []uint64{pidA, pidB}, []uint64{pidC})
		assertRankEventIDs(t, waitRankEvent(t, conn), []uint64{pidA, pidB}, []uint64{pidC})
	})

	t.Run("per-question", func(t *testing.T) {
		form := quizForm("Rank PerQuestion Start")
		form.Set("timer_type", "per_soal")
		form.Set("ranking_live", "1")
		quizID := createQuiz(t, ts, ck, form)
		q := addQuestion(t, ts, pool, ck, "Pick A", "pg", []string{"A", "B"}, []int{0})
		compose(t, ts, ck, quizID, q)
		if resp, body := setStatus(t, ts, ck, quizID, "aktif"); resp.StatusCode != http.StatusOK || !decodeEnv(t, body).OK {
			t.Fatalf("activate = %d: %s", resp.StatusCode, body)
		}
		code := joinCode(t, pool, quizID)

		join := func(username string) (*http.Cookie, uint64) {
			t.Helper()
			st := studentCookie(t, pool, username)
			if resp, body := postJoin(t, ts, code, st); resp.StatusCode != http.StatusOK {
				t.Fatalf("join = %d: %s", resp.StatusCode, body)
			} else if d := decodeJoin(t, body); d.Status != "registered" {
				t.Fatalf("join status = %q, want registered", d.Status)
			}
			pid, _, _ := participantRowFor(t, pool, quizID, userOf(t, ts, st))
			return st, pid
		}
		stA, pidA := join("rank-pq-a")
		stB, pidB := join("rank-pq-b")

		conn := openSSE(t, ts.URL+fmt.Sprintf("/teacher/quiz/%d/monitor/stream", quizID), ck)
		defer conn.resp.Body.Close()
		if ev, _, _, ok := conn.readEvent(3 * time.Second); !ok || ev != "snapshot" {
			t.Fatalf("monitor greeting = %q (ok=%v), want snapshot", ev, ok)
		}

		// A starts: A appears at score 0, B (registered, never started) stays out
		startAttempt(t, ts, code, stA)
		assertRanker(t, globals.Live.Ranker(quizID), []uint64{pidA}, []uint64{pidB})
		assertRankEventIDs(t, waitRankEvent(t, conn), []uint64{pidA}, []uint64{pidB})

		// B starts too: now BOTH started murid are ranked
		startAttempt(t, ts, code, stB)
		assertRanker(t, globals.Live.Ranker(quizID), []uint64{pidA, pidB}, nil)
		assertRankEventIDs(t, waitRankEvent(t, conn), []uint64{pidA, pidB}, nil)
	})
}
