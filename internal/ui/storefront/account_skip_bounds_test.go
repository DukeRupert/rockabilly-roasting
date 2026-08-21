package storefront

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/dukerupert/hiri/internal/domain"
)

// The date picker must only offer days the service will accept. Its lower
// bound is the shipment already scheduled — not merely tomorrow — or a
// subscriber mid-cycle is shown weeks of days that all bounce with an error
// page instead of a skip.
func TestSkipRestartBounds(t *testing.T) {
	denver, err := time.LoadLocation("America/Denver")
	assert.NoError(t, err)
	now := time.Now().In(denver)

	t.Run("opens the day after the next order", func(t *testing.T) {
		sub := domain.Subscription{NextOrderAt: now.AddDate(0, 0, 28)}
		min, max, ok := skipRestartBounds(sub, denver)
		assert.True(t, ok)
		assert.Equal(t, now.AddDate(0, 0, 29).Format("2006-01-02"), min)
		assert.Equal(t, now.AddDate(0, 0, domain.SubscriptionMaxSkipDays).Format("2006-01-02"), max)
	})

	t.Run("never opens before tomorrow", func(t *testing.T) {
		sub := domain.Subscription{NextOrderAt: now.AddDate(0, 0, -5)}
		min, _, ok := skipRestartBounds(sub, denver)
		assert.True(t, ok)
		assert.Equal(t, now.AddDate(0, 0, 1).Format("2006-01-02"), min)
	})

	t.Run("no window when the next order is past the ceiling", func(t *testing.T) {
		sub := domain.Subscription{NextOrderAt: now.AddDate(0, 0, domain.SubscriptionMaxSkipDays+1)}
		_, _, ok := skipRestartBounds(sub, denver)
		assert.False(t, ok, "picker must be hidden rather than offering only rejectable days")
	})

	t.Run("nil timezone falls back rather than panicking", func(t *testing.T) {
		sub := domain.Subscription{NextOrderAt: now.AddDate(0, 0, 3)}
		assert.NotPanics(t, func() { skipRestartBounds(sub, nil) })
	})
}
