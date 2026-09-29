// Package cache is the in-memory read-through mirror (spec §6.2).
//
// Golden rule: the cache may answer faster, never newer than the DB, never
// something the DB doesn't have. Every TTL is listed in keys.go; mutations
// delete mirror entries after COMMIT and before any SSE broadcast.
package cache

import (
	"strings"
	"sync"
	"time"
)

type entry struct {
	val     any
	expires time.Time // zero = no expiry
}

// Store is a concurrency-safe TTL key/value mirror. Tests inject a fake
// clock via NewWithClock.
type Store struct {
	mu    sync.RWMutex
	items map[string]entry
	now   func() time.Time
}

// New returns a Store using the wall clock.
func New() *Store { return NewWithClock(time.Now) }

// NewWithClock returns a Store driven by now (test seam).
func NewWithClock(now func() time.Time) *Store {
	return &Store{items: make(map[string]entry), now: now}
}

// Get returns the value for key. An expired entry is deleted and reported as
// a miss.
func (s *Store) Get(key string) (any, bool) {
	s.mu.RLock()
	e, ok := s.items[key]
	s.mu.RUnlock()
	if !ok {
		return nil, false
	}
	if !e.expires.IsZero() && !s.now().Before(e.expires) {
		s.Delete(key)
		return nil, false
	}
	return e.val, true
}

// Set stores val under key with ttl. ttl <= 0 stores without expiry.
func (s *Store) Set(key string, val any, ttl time.Duration) {
	var exp time.Time
	if ttl > 0 {
		exp = s.now().Add(ttl)
	}
	s.mu.Lock()
	s.items[key] = entry{val: val, expires: exp}
	s.mu.Unlock()
}

// Delete removes key immediately — revocation must not wait for the TTL.
func (s *Store) Delete(key string) {
	s.mu.Lock()
	delete(s.items, key)
	s.mu.Unlock()
}

// Has reports raw map presence for a key, ignoring expiry.
// Test seam: Store.Get lazily deletes expired entries on read, so tests
// that assert proactive expiry need raw presence without a read.
func (s *Store) Has(key string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.items[key]
	return ok
}

// DeletePrefix removes every key with the given prefix (e.g. "quizstate:7:").
func (s *Store) DeletePrefix(prefix string) {
	s.mu.Lock()
	for k := range s.items {
		if strings.HasPrefix(k, prefix) {
			delete(s.items, k)
		}
	}
	s.mu.Unlock()
}
