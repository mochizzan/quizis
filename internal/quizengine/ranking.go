package quizengine

import "sync"

// Entry is one participant's row in the live ranking. The JSON tags are the
// SSE `rank` event contract (spec §6.7.3).
type Entry struct {
	ParticipantID uint64  `json:"participant_id"`
	Name          string  `json:"name"`
	Score         float64 `json:"score"`
	Finished      bool    `json:"finished"`
	Removed       bool    `json:"removed"` // removed (dikeluarkan) stays in the ranking (spec §6.10)
	Cheating      bool    `json:"cheating"`
	Rank          int     `json:"rank"` // filled by Snapshot
}

// Ranker is the live leaderboard: one entry per participant, updated on
// every answer/finish/removal. Participants registered but never started
// are never inserted (spec §8 — caller's responsibility).
type Ranker struct {
	mu   sync.RWMutex
	byID map[uint64]*Entry
}

func NewRanker() *Ranker {
	return &Ranker{byID: make(map[uint64]*Entry)}
}

// Upsert inserts or replaces the entry for e.ParticipantID.
func (r *Ranker) Upsert(e Entry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := e
	r.byID[e.ParticipantID] = &cp
}

// Remove drops the entry entirely (the Removed flag is different: it keeps
// the participant visible in the ranking).
func (r *Ranker) Remove(id uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.byID, id)
}

// Snapshot returns the ranking copy sorted by score desc → finished first →
// participant ID asc, with Rank numbered from 1.
func (r *Ranker) Snapshot() []Entry {
	r.mu.RLock()
	entries := make([]Entry, 0, len(r.byID))
	for _, e := range r.byID {
		entries = append(entries, *e)
	}
	r.mu.RUnlock()

	sortRanking(entries)
	for i := range entries {
		entries[i].Rank = i + 1
	}
	return entries
}

// sortRanking orders: Score desc → Finished first → ParticipantID asc.
// Hand-written three-key sort (plan: deterministic, no generics needed).
func sortRanking(entries []Entry) {
	// insertion sort — rankings are small (classroom-sized) and this keeps
	// ties stable without pulling in sort.Slice's non-stable swap.
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0 && rankingLess(entries[j], entries[j-1]); j-- {
			entries[j], entries[j-1] = entries[j-1], entries[j]
		}
	}
}

func rankingLess(a, b Entry) bool {
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	if a.Finished != b.Finished {
		return a.Finished
	}
	return a.ParticipantID < b.ParticipantID
}
