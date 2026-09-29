package handlers

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"sync"
	"time"

	"quiz/internal/quizengine"
)

// Live holds the rebuildable in-memory mirrors shared by the student and
// teacher handlers: the per-quiz live rankers (spec §6.7.3) and the
// per-participant stream heartbeat registry behind the disconnect freeze
// (spec §6.6, §11.16). A ranker is progress state, never truth — it
// rebuilds from the answers table (Rehydrate); a restart starts empty.
type Live struct {
	DB *sql.DB

	mu      sync.Mutex
	rankers map[uint64]*quizengine.Ranker
	beats   map[uint64]*beat

	// pages holds which workspace page each participant is showing
	// ("question" default, "preview" = the pre-submit review); spent banks
	// the seconds a participant already spent on each left question (the
	// per-question timer monitor, spec §7). Both are rebuildable mirrors:
	// a restart starts them over, never touching DB truth.
	pages map[uint64]string
	spent map[uint64]map[int]int64
}

// SpentQ is one completed question's time-spent entry on a monitor card.
type SpentQ struct {
	Q   int   `json:"q"`
	Sec int64 `json:"sec"`
}

// beat is one participant's stream liveness: connected means the SSE
// subscription is open; last is the time of its last received heartbeat
// (the last message actually written to the socket); frozen marks a
// disconnect whose personal clock was pinned to last, with remain holding
// the still-unused time to hand back on reconnect.
type beat struct {
	connected bool
	last      time.Time
	frozen    bool
	remain    time.Duration
}

// NewLive builds an empty registry. db backs the disconnect freeze writes.
func NewLive(db *sql.DB) *Live {
	return &Live{
		DB:      db,
		rankers: make(map[uint64]*quizengine.Ranker),
		beats:   make(map[uint64]*beat),
		pages:   make(map[uint64]string),
		spent:   make(map[uint64]map[int]int64),
	}
}

// SetPage records which page the participant's workspace is showing and
// reports whether it actually changed (so a render-only reload never
// re-announces a page the monitor already shows).
func (l *Live) SetPage(pid uint64, page string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.pages[pid] == page {
		return false
	}
	l.pages[pid] = page
	return true
}

// Page returns the participant's last known page ("" = never reported).
func (l *Live) Page(pid uint64) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.pages[pid]
}

// AddSpent banks the time a participant spent on one question at the moment
// they leave it (per-question timer monitor). Ledger only: no DB column may
// change for a display concern (spec §5 schema is frozen).
func (l *Live) AddSpent(pid uint64, q int, d time.Duration) {
	sec := int64(d / time.Second)
	if sec < 0 {
		sec = 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	m := l.spent[pid]
	if m == nil {
		m = make(map[int]int64)
		l.spent[pid] = m
	}
	m[q] += sec
}

// Spent returns the participant's completed-question ledger, ascending by
// question position (the still-running question is NOT in here — its clock
// is current_q_since, which the caller ticks live).
func (l *Live) Spent(pid uint64) []SpentQ {
	l.mu.Lock()
	defer l.mu.Unlock()
	m := l.spent[pid]
	out := make([]SpentQ, 0, len(m))
	for q, sec := range m {
		out = append(out, SpentQ{Q: q, Sec: sec})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Q < out[j].Q })
	return out
}

// Ranker returns the live leaderboard for one quiz (create on first use).
func (l *Live) Ranker(quizID uint64) *quizengine.Ranker {
	l.mu.Lock()
	defer l.mu.Unlock()
	r, ok := l.rankers[quizID]
	if !ok {
		r = quizengine.NewRanker()
		l.rankers[quizID] = r
	}
	return r
}

// Connect marks a participant's stream live again. If a disconnect had
// frozen their clock, it resumes with the frozen remainder — no time is
// lost or gained (spec §11.16).
func (l *Live) Connect(pid uint64) {
	l.mu.Lock()
	b := l.beats[pid]
	if b == nil {
		b = &beat{}
		l.beats[pid] = b
	}
	b.connected = true
	b.last = time.Now()
	var resumeAt time.Time
	if b.frozen {
		resumeAt = time.Now().Add(b.remain)
		b.frozen = false
	}
	l.mu.Unlock()
	if resumeAt.IsZero() {
		return
	}
	// Only a still-open attempt gets its clock back.
	_, _ = l.DB.ExecContext(context.Background(),
		`UPDATE participants SET ends_at = ? WHERE id = ? AND status = 'started'`,
		resumeAt.UTC().Format("2006-01-02 15:04:05"), pid)
}

// Connected reports whether the participant's stream is open right now
// (the monitor's connection badge reads it).
func (l *Live) Connected(pid uint64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.beats[pid]
	return b != nil && b.connected
}

// Disconnect records that the stream closed, keeping lastSent — the moment
// of the connection's final heartbeat — as the freeze anchor.
func (l *Live) Disconnect(pid uint64, lastSent time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.beats[pid]
	if b == nil {
		b = &beat{}
		l.beats[pid] = b
	}
	b.connected = false
	b.last = lastSent
}

// SweepDisconnects freezes every known-disconnected participant whose stream
// has been silent for longer than threshold: ends_at is pinned to the LAST
// HEARTBEAT instant (never the detection time — spec §11.16) and the unused
// remainder is kept for the reconnect resume. Participants with no clock
// (timer off) or that are not working are left alone. The map lock is held
// across the few queries so a concurrent Connect cannot interleave.
// Returns the pids it froze.
func (l *Live) SweepDisconnects(ctx context.Context, now time.Time, threshold time.Duration) []uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	var frozen []uint64
	for pid, b := range l.beats {
		if b.connected || b.frozen || now.Sub(b.last) <= threshold {
			continue
		}
		var endsAt sql.NullTime
		err := l.DB.QueryRowContext(ctx,
			`SELECT ends_at FROM participants WHERE id = ? AND status = 'started'`, pid).
			Scan(&endsAt)
		if errors.Is(err, sql.ErrNoRows) || !endsAt.Valid {
			continue // not working, or no personal clock to freeze
		}
		remain := endsAt.Time.Sub(b.last)
		if remain < 0 {
			remain = 0
		}
		if _, err := l.DB.ExecContext(ctx,
			`UPDATE participants SET ends_at = ? WHERE id = ? AND status = 'started'`,
			b.last.UTC().Format("2006-01-02 15:04:05"), pid); err != nil {
			continue
		}
		b.frozen = true
		b.remain = remain
		frozen = append(frozen, pid)
	}
	return frozen
}
