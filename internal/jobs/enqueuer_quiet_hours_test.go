package jobs

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// deferToSendHour is pure, so the quiet-hours policy needs no database or clock.
func TestDeferToSendHour(t *testing.T) {
	la, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)
	const sendHour = 8

	t.Run("pre-dawn renewal is held until 8am", func(t *testing.T) {
		now := time.Date(2026, 7, 17, 2, 0, 0, 0, la) // the renewal batch hour
		send, deferred := deferToSendHour(now, la, sendHour)
		require.True(t, deferred)
		assert.True(t, send.Equal(time.Date(2026, 7, 17, 8, 0, 0, 0, la)), "got %s", send)
		assert.True(t, send.After(now), "deferral must be in the future")
	})

	t.Run("daytime renewal sends immediately", func(t *testing.T) {
		now := time.Date(2026, 7, 17, 11, 0, 0, 0, la) // e.g. manual admin renewal
		_, deferred := deferToSendHour(now, la, sendHour)
		assert.False(t, deferred)
	})

	t.Run("exactly send hour sends immediately", func(t *testing.T) {
		now := time.Date(2026, 7, 17, 8, 0, 0, 0, la)
		_, deferred := deferToSendHour(now, la, sendHour)
		assert.False(t, deferred)
	})

	t.Run("uses merchant-local wall clock, not UTC", func(t *testing.T) {
		// 2026-07-17 09:00 UTC == 02:00 PDT, which is pre-dawn locally.
		now := time.Date(2026, 7, 17, 9, 0, 0, 0, time.UTC)
		send, deferred := deferToSendHour(now, la, sendHour)
		require.True(t, deferred)
		assert.True(t, send.Equal(time.Date(2026, 7, 17, 8, 0, 0, 0, la)), "got %s", send)
	})
}
