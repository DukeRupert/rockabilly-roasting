package jobs_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/jobs"
)

// The maintenance sweep books real customer visits, so when it runs is a
// promise the module's docs make in as many words ("overnight"). These pin the
// two ways a wall-clock schedule goes wrong: firing twice after a restart, and
// sliding an hour across a DST boundary.
func TestDailySchedule(t *testing.T) {
	denver, err := time.LoadLocation("America/Denver")
	require.NoError(t, err)
	s := jobs.NewDailySchedule(3, 0, denver)

	t.Run("later today", func(t *testing.T) {
		got := s.Next(time.Date(2026, time.March, 10, 0, 30, 0, 0, denver))
		assert.Equal(t, time.Date(2026, time.March, 10, 3, 0, 0, 0, denver), got)
	})

	t.Run("already gone, so tomorrow", func(t *testing.T) {
		// The restart case. A deploy at 3pm must not fire a sweep that already
		// ran at 2am — that is a second pass at opening tickets.
		got := s.Next(time.Date(2026, time.March, 10, 15, 0, 0, 0, denver))
		assert.Equal(t, time.Date(2026, time.March, 11, 3, 0, 0, 0, denver), got)
	})

	t.Run("exactly on the hour rolls forward", func(t *testing.T) {
		at := time.Date(2026, time.March, 10, 3, 0, 0, 0, denver)
		assert.Equal(t, time.Date(2026, time.March, 11, 3, 0, 0, 0, denver), s.Next(at))
	})

	t.Run("stays 03:00 local across the spring DST jump", func(t *testing.T) {
		// 8 March 2026 is the US spring-forward — 02:00 does not exist that
		// day, which is why the sweep is scheduled at 03:00 and not the more
		// obvious 02:00. A fixed 24h interval would drift by an hour here and
		// stay drifted; the whole point of this type is that it does not.
		got := s.Next(time.Date(2026, time.March, 7, 12, 0, 0, 0, denver))
		assert.Equal(t, 3, got.In(denver).Hour(), "got %s", got.In(denver).Format(time.RFC3339))
		assert.Equal(t, 8, got.In(denver).Day())

		got = s.Next(time.Date(2026, time.March, 9, 12, 0, 0, 0, denver))
		assert.Equal(t, 3, got.In(denver).Hour(), "got %s", got.In(denver).Format(time.RFC3339))
	})

	t.Run("stays 03:00 local across the autumn fall-back", func(t *testing.T) {
		// 1 November 2026 repeats 01:00–02:00. 03:00 happens once.
		got := s.Next(time.Date(2026, time.October, 31, 12, 0, 0, 0, denver))
		assert.Equal(t, 3, got.In(denver).Hour(), "got %s", got.In(denver).Format(time.RFC3339))

		got = s.Next(time.Date(2026, time.November, 1, 12, 0, 0, 0, denver))
		assert.Equal(t, 3, got.In(denver).Hour(), "got %s", got.In(denver).Format(time.RFC3339))
		assert.Equal(t, 2, got.In(denver).Day(), "and does not repeat the changeover day")
	})

	t.Run("every step lands on the configured hour for a full year", func(t *testing.T) {
		// Both changeovers, walked rather than sampled: a schedule that skips a
		// day or fires twice shows up here and nowhere else.
		at := time.Date(2026, time.January, 1, 12, 0, 0, 0, denver)
		prev := at
		for i := 0; i < 365; i++ {
			next := s.Next(prev)
			assert.True(t, next.After(prev), "step %d went backwards: %s", i, next)
			assert.Equal(t, 3, next.In(denver).Hour(), "step %d landed at %s", i, next.In(denver))
			prev = next
		}
		assert.Equal(t, 2027, prev.In(denver).Year(), "365 steps should be about a year")
	})

	t.Run("a nil location is UTC, not a panic", func(t *testing.T) {
		u := jobs.NewDailySchedule(3, 0, nil)
		got := u.Next(time.Date(2026, time.March, 10, 0, 0, 0, 0, time.UTC))
		assert.Equal(t, time.Date(2026, time.March, 10, 3, 0, 0, 0, time.UTC), got)
	})
}
