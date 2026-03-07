package ratelimit

import (
	"context"
	"sync"
	"time"
)

type entry struct {
	mu       sync.Mutex
	attempts []time.Time
}

// MemoryStore is an in-process sliding window rate limit store.
// State is ephemeral — counters reset on process restart, which is acceptable
// for a single-server deployment.
type MemoryStore struct {
	mu      sync.RWMutex
	entries map[string]*entry
	stop    chan struct{}
}

// NewMemoryStore creates a new in-memory store and starts a background
// goroutine that evicts expired entries every cleanupInterval.
func NewMemoryStore(cleanupInterval time.Duration) *MemoryStore {
	s := &MemoryStore{
		entries: make(map[string]*entry),
		stop:    make(chan struct{}),
	}
	go s.cleanup(cleanupInterval)
	return s
}

// Allow implements Store using a sliding window algorithm.
func (s *MemoryStore) Allow(_ context.Context, key string, limit int, window time.Duration) (bool, int, time.Time, error) {
	now := time.Now()
	cutoff := now.Add(-window)
	resetAt := now.Add(window)

	s.mu.Lock()
	e, ok := s.entries[key]
	if !ok {
		e = &entry{}
		s.entries[key] = e
	}
	s.mu.Unlock()

	e.mu.Lock()
	defer e.mu.Unlock()

	// Evict attempts outside the window.
	valid := e.attempts[:0]
	for _, t := range e.attempts {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}
	e.attempts = valid

	if len(e.attempts) >= limit {
		// Calculate when the oldest attempt expires.
		if len(e.attempts) > 0 {
			resetAt = e.attempts[0].Add(window)
		}
		return false, 0, resetAt, nil
	}

	e.attempts = append(e.attempts, now)
	remaining := limit - len(e.attempts)
	return true, remaining, resetAt, nil
}

// Reset clears the counter for key.
func (s *MemoryStore) Reset(_ context.Context, key string) error {
	s.mu.Lock()
	delete(s.entries, key)
	s.mu.Unlock()
	return nil
}

// Stop signals the cleanup goroutine to exit.
func (s *MemoryStore) Stop() {
	close(s.stop)
}

// cleanup periodically removes entries whose newest attempt is older than
// a generous threshold (10 minutes). This prevents unbounded memory growth.
func (s *MemoryStore) cleanup(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case now := <-ticker.C:
			threshold := now.Add(-10 * time.Minute)
			s.mu.Lock()
			for key, e := range s.entries {
				e.mu.Lock()
				if len(e.attempts) == 0 || e.attempts[len(e.attempts)-1].Before(threshold) {
					delete(s.entries, key)
				}
				e.mu.Unlock()
			}
			s.mu.Unlock()
		}
	}
}
