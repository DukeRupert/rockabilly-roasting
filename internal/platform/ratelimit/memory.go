package ratelimit

import (
	"sync"
	"time"
)

type bucket struct {
	tokens   float64
	lastSeen time.Time
}

// InMemoryStore is an in-process token bucket rate limit store.
type InMemoryStore struct {
	mu      sync.Mutex
	buckets map[string]*bucket
}

// NewInMemoryStore creates a new in-memory rate limit store.
func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{
		buckets: make(map[string]*bucket),
	}
}

// Allow checks whether key has available tokens using the token bucket algorithm.
func (s *InMemoryStore) Allow(key string, cfg LimitConfig) (bool, float64, time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	b, ok := s.buckets[key]
	if !ok {
		b = &bucket{tokens: cfg.Capacity, lastSeen: now}
		s.buckets[key] = b
	}

	// Refill tokens based on elapsed time
	elapsed := now.Sub(b.lastSeen).Seconds()
	b.tokens += elapsed * cfg.Rate
	if b.tokens > cfg.Capacity {
		b.tokens = cfg.Capacity
	}
	b.lastSeen = now

	if b.tokens >= 1 {
		b.tokens--
		return true, b.tokens, 0
	}

	// Calculate time until next token
	wait := time.Duration((1 - b.tokens) / cfg.Rate * float64(time.Second))
	return false, b.tokens, wait
}
