package unit

import (
	"testing"

	"quiz/internal/quizengine"
)

func ids(entries []quizengine.Entry) []uint64 {
	out := make([]uint64, len(entries))
	for i, e := range entries {
		out[i] = e.ParticipantID
	}
	return out
}

func equalIDs(got []quizengine.Entry, want ...uint64) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i].ParticipantID != want[i] {
			return false
		}
	}
	return true
}

func TestSnapshotSortsByScoreDesc(t *testing.T) {
	r := quizengine.NewRanker()
	r.Upsert(quizengine.Entry{ParticipantID: 1, Score: 50})
	r.Upsert(quizengine.Entry{ParticipantID: 2, Score: 90})
	r.Upsert(quizengine.Entry{ParticipantID: 3, Score: 10})

	snap := r.Snapshot()
	if !equalIDs(snap, 2, 1, 3) {
		t.Errorf("order = %v, want [2 1 3] by score desc", ids(snap))
	}
	for i, e := range snap {
		if e.Rank != i+1 {
			t.Errorf("rank of entry %d = %d, want %d", e.ParticipantID, e.Rank, i+1)
		}
	}
}

func TestTieBreakFinishedFirst(t *testing.T) {
	r := quizengine.NewRanker()
	r.Upsert(quizengine.Entry{ParticipantID: 1, Score: 60, Finished: false})
	r.Upsert(quizengine.Entry{ParticipantID: 2, Score: 60, Finished: true})

	if !equalIDs(r.Snapshot(), 2, 1) {
		t.Errorf("tie must rank the finished participant first, got %v", ids(r.Snapshot()))
	}
}

func TestTieBreakParticipantIDAsc(t *testing.T) {
	r := quizengine.NewRanker()
	// same score, both finished → lower participant ID first (deterministic,
	// independent of input order)
	r.Upsert(quizengine.Entry{ParticipantID: 5, Score: 70, Finished: true})
	r.Upsert(quizengine.Entry{ParticipantID: 3, Score: 70, Finished: true})
	r.Upsert(quizengine.Entry{ParticipantID: 4, Score: 70, Finished: true})

	if !equalIDs(r.Snapshot(), 3, 4, 5) {
		t.Errorf("tie order = %v, want [3 4 5] by ID asc", ids(r.Snapshot()))
	}
}

func TestRemovedStaysInRanking(t *testing.T) {
	r := quizengine.NewRanker()
	r.Upsert(quizengine.Entry{ParticipantID: 1, Score: 40})
	r.Upsert(quizengine.Entry{ParticipantID: 2, Score: 95, Removed: true})
	r.Upsert(quizengine.Entry{ParticipantID: 3, Score: 20})

	snap := r.Snapshot()
	if !equalIDs(snap, 2, 1, 3) {
		t.Errorf("removed participant must stay in the ranking (spec §6.10), got %v", ids(snap))
	}
	if !snap[0].Removed {
		t.Error("Removed flag must survive the snapshot")
	}
}

func TestUpsertReplacesAndCheatingIsPreserved(t *testing.T) {
	r := quizengine.NewRanker()
	r.Upsert(quizengine.Entry{ParticipantID: 7, Name: "A", Score: 10})
	r.Upsert(quizengine.Entry{ParticipantID: 7, Name: "A", Score: 55, Cheating: true})

	snap := r.Snapshot()
	if len(snap) != 1 || snap[0].Score != 55 || !snap[0].Cheating {
		t.Errorf("upsert must replace in place, got %+v", snap)
	}
	// Cheating is a badge, not a sort key
	r2 := quizengine.NewRanker()
	r2.Upsert(quizengine.Entry{ParticipantID: 1, Score: 80, Cheating: true})
	r2.Upsert(quizengine.Entry{ParticipantID: 2, Score: 50})
	if !equalIDs(r2.Snapshot(), 1, 2) {
		t.Error("cheating flag must not affect ranking order")
	}
}

func TestRemoveDeletesEntry(t *testing.T) {
	r := quizengine.NewRanker()
	r.Upsert(quizengine.Entry{ParticipantID: 1, Score: 10})
	r.Upsert(quizengine.Entry{ParticipantID: 2, Score: 20})
	r.Remove(1)
	if snap := r.Snapshot(); !equalIDs(snap, 2) {
		t.Errorf("after Remove: %v, want [2]", ids(snap))
	}
}

func TestSnapshotIsACopy(t *testing.T) {
	r := quizengine.NewRanker()
	r.Upsert(quizengine.Entry{ParticipantID: 1, Score: 30})
	snap := r.Snapshot()
	snap[0].Score = 0 // consumer mutates the copy
	if s := r.Snapshot(); s[0].Score != 30 {
		t.Error("snapshot must be a copy — internal state leaked")
	}
}
