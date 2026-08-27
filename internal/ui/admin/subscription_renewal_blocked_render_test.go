package admin

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
)

// The page always showed ends_at — it just showed it as a mild "Ended on" line
// sitting next to an Active badge, which read as a harmless historical note.
// Three subscriptions went unbilled for months in front of that exact display.
// These assert the contradiction now announces its consequence, and that the
// quiet version still covers the case where nothing is wrong.
func TestSubscriptionShow_RenewalBlockedBanner(t *testing.T) {
	tz := time.UTC
	now := time.Now().In(tz)

	render := func(t *testing.T, status domain.SubscriptionStatus, endsAt *time.Time) string {
		t.Helper()
		var buf bytes.Buffer
		require.NoError(t, SubscriptionShowContent(SubscriptionShowProps{
			Subscription: &domain.Subscription{
				ID:                 uuid.New(),
				Status:             status,
				EndsAt:             endsAt,
				CurrentPeriodStart: now,
				CurrentPeriodEnd:   now.AddDate(0, 0, 30),
				NextOrderAt:        now.AddDate(0, 0, 30),
				CreatedAt:          now,
			},
			Plan:       &domain.SubscriptionPlan{Name: "Monthly", Interval: domain.SubscriptionIntervalEvery30Days, IntervalCount: 1},
			Customer:   &domain.Customer{ID: uuid.New(), FirstName: "Ada", LastName: "Byron", Email: "ada@example.com"},
			MerchantTZ: tz,
		}).Render(context.Background(), &buf))
		return buf.String()
	}

	t.Run("active with a past end date says it will never bill again", func(t *testing.T) {
		past := now.AddDate(0, 0, -30)
		html := render(t, domain.SubscriptionStatusActive, &past)

		assert.Contains(t, html, "Not renewing")
		assert.Contains(t, html, "will never be billed again")
		// Says what to do about it — the earlier display named the data and left
		// the reader to infer the consequence, which nobody did.
		assert.Contains(t, html, "It cannot be both.")
		// Loud, not decorative.
		assert.Contains(t, html, "badge-red")
	})

	t.Run("expired with a past end date keeps the quiet note", func(t *testing.T) {
		past := now.AddDate(0, 0, -30)
		html := render(t, domain.SubscriptionStatusExpired, &past)

		assert.NotContains(t, html, "Not renewing",
			"status and ends_at agree here; there is no contradiction to shout about")
		assert.Contains(t, html, "Ended")
	})

	t.Run("active with a future end date is not flagged", func(t *testing.T) {
		future := now.AddDate(0, 0, 30)
		html := render(t, domain.SubscriptionStatusActive, &future)

		assert.NotContains(t, html, "Not renewing",
			"a fixed-term subscription still renewing towards its end is legitimate")
	})

	t.Run("active with no end date is not flagged", func(t *testing.T) {
		html := render(t, domain.SubscriptionStatusActive, nil)
		assert.NotContains(t, html, "Not renewing")
		assert.NotContains(t, html, "Ended")
	})
}
