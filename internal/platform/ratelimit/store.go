package ratelimit

import (
	"context"
	"time"
)

// Store is the interface for rate limit storage.
// Implementations use a sliding window algorithm: "no more than limit
// attempts in the last window duration."
type Store interface {
	// Allow checks and increments the counter for key.
	// Returns whether the request is allowed, remaining attempts, and when
	// the oldest tracked attempt expires (reset time).
	Allow(ctx context.Context, key string, limit int, window time.Duration) (allowed bool, remaining int, resetAt time.Time, err error)

	// Reset clears the counter for key — used after successful login.
	Reset(ctx context.Context, key string) error
}
