package ratelimit

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemoryStore_Allow(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore(time.Hour) // long cleanup interval for tests
	defer s.Stop()

	t.Run("allows requests under limit", func(t *testing.T) {
		allowed, remaining, _, err := s.Allow(ctx, "test:1", 3, time.Minute)
		require.NoError(t, err)
		assert.True(t, allowed)
		assert.Equal(t, 2, remaining)
	})

	t.Run("blocks when limit reached", func(t *testing.T) {
		key := "test:block"
		for i := 0; i < 5; i++ {
			s.Allow(ctx, key, 5, time.Minute) //nolint:errcheck
		}

		allowed, remaining, _, err := s.Allow(ctx, key, 5, time.Minute)
		require.NoError(t, err)
		assert.False(t, allowed)
		assert.Equal(t, 0, remaining)
	})

	t.Run("window expiry allows new requests", func(t *testing.T) {
		key := "test:expire"
		window := 50 * time.Millisecond

		for i := 0; i < 2; i++ {
			s.Allow(ctx, key, 2, window) //nolint:errcheck
		}

		// Should be blocked.
		allowed, _, _, _ := s.Allow(ctx, key, 2, window)
		assert.False(t, allowed)

		// Wait for window to expire.
		time.Sleep(60 * time.Millisecond)

		// Should be allowed again.
		allowed, remaining, _, err := s.Allow(ctx, key, 2, window)
		require.NoError(t, err)
		assert.True(t, allowed)
		assert.Equal(t, 1, remaining)
	})

	t.Run("reset clears counter", func(t *testing.T) {
		key := "test:reset"
		for i := 0; i < 3; i++ {
			s.Allow(ctx, key, 3, time.Minute) //nolint:errcheck
		}

		// Blocked.
		allowed, _, _, _ := s.Allow(ctx, key, 3, time.Minute)
		assert.False(t, allowed)

		// Reset.
		err := s.Reset(ctx, key)
		require.NoError(t, err)

		// Allowed again.
		allowed, remaining, _, err := s.Allow(ctx, key, 3, time.Minute)
		require.NoError(t, err)
		assert.True(t, allowed)
		assert.Equal(t, 2, remaining)
	})

	t.Run("resetAt reflects oldest attempt expiry when blocked", func(t *testing.T) {
		key := "test:resetat"
		window := time.Minute

		before := time.Now()
		for i := 0; i < 2; i++ {
			s.Allow(ctx, key, 2, window) //nolint:errcheck
		}

		_, _, resetAt, _ := s.Allow(ctx, key, 2, window)
		// resetAt should be roughly now + window (oldest attempt was ~now).
		assert.WithinDuration(t, before.Add(window), resetAt, 100*time.Millisecond)
	})
}

func TestMemoryStore_ConcurrentAccess(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore(time.Hour)
	defer s.Stop()

	done := make(chan struct{})
	for i := 0; i < 100; i++ {
		go func() {
			s.Allow(ctx, "concurrent", 1000, time.Minute) //nolint:errcheck
			done <- struct{}{}
		}()
	}
	for i := 0; i < 100; i++ {
		<-done
	}
}
