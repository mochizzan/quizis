package cachetest

import (
	"sync"
	"testing"
	"time"

	"quiz/internal/cache"
)

func TestGetMissOnEmpty(t *testing.T) {
	s := cache.New()
	if v, ok := s.Get("nope"); ok {
		t.Errorf("Get on empty store = (%v, true), want miss", v)
	}
}

func TestSetGetAndTTLExpiry(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s := cache.NewWithClock(func() time.Time { return now })

	s.Set("k", "v", 3*time.Second)
	if v, ok := s.Get("k"); !ok || v.(string) != "v" {
		t.Fatalf("Get before expiry = (%v, %v), want (v, true)", v, ok)
	}

	now = now.Add(2 * time.Second)
	if _, ok := s.Get("k"); !ok {
		t.Error("Get at 2s of 3s TTL = miss, want hit")
	}

	now = now.Add(1 * time.Second) // exactly at expiry → miss
	if v, ok := s.Get("k"); ok {
		t.Errorf("Get at expiry = (%v, true), want miss", v)
	}

	// expired entry must be gone, not just hidden
	if s.Has("k") {
		t.Error("expired entry still present after Get")
	}
}

func TestDeleteRevokesImmediately(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s := cache.NewWithClock(func() time.Time { return now })

	s.Set("session:abc", "uid", cache.TTLSession)
	s.Delete("session:abc")
	if _, ok := s.Get("session:abc"); ok {
		t.Error("Get after Delete = hit; revocation must not wait for TTL")
	}

	// TTL has not advanced at all — proves the miss is from Delete, not expiry
	if _, ok := s.Get("session:abc"); ok {
		t.Error("second Get after Delete = hit")
	}
}

func TestDeletePrefix(t *testing.T) {
	s := cache.New()
	s.Set("quizstate:7:a", 1, 0)
	s.Set("quizstate:7:b", 2, 0)
	s.Set("quizstate:8:c", 3, 0)
	s.Set("session:x", 4, 0)

	s.DeletePrefix("quizstate:7:")
	for _, k := range []string{"quizstate:7:a", "quizstate:7:b"} {
		if _, ok := s.Get(k); ok {
			t.Errorf("%s survived DeletePrefix", k)
		}
	}
	for _, k := range []string{"quizstate:8:c", "session:x"} {
		if _, ok := s.Get(k); !ok {
			t.Errorf("%s wrongly removed by DeletePrefix", k)
		}
	}
}

func TestZeroTTLNeverExpires(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s := cache.NewWithClock(func() time.Time { return now })
	s.Set("cfg", 42, 0)
	now = now.Add(100 * time.Hour)
	if v, ok := s.Get("cfg"); !ok || v.(int) != 42 {
		t.Errorf("Get after 100h = (%v, %v), want (42, true)", v, ok)
	}
}

func TestConcurrentAccess(t *testing.T) {
	s := cache.New()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				s.Set(cache.SessionKey(string(rune('a'+n))+strconvItoa(j)), n, cache.TTLSession)
			}
		}(i)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				s.Get(cache.SessionKey(string(rune('a'+n)) + strconvItoa(j)))
				s.DeletePrefix("session:")
			}
		}(i)
	}
	wg.Wait()
}

// strconvItoa avoids importing strconv just for the race test.
func strconvItoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
