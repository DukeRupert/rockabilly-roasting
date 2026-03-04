package ratelimit

import "time"

// Store is the interface for rate limit token bucket storage.
type Store interface {
	// Allow checks whether key has available tokens.
	// Returns (allowed, remaining tokens, time until next token).
	Allow(key string, cfg LimitConfig) (bool, float64, time.Duration)
}
