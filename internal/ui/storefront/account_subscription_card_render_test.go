package storefront

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
)

// Rendering the card and reading the markup, because the defect this guards
// against is a call site, not a helper. formatDateIn can be perfectly correct
// and the card still print UTC — that is exactly the state this branch found,
// and a unit test on the helper alone would have gone on passing through it.

func renderSubscriptionCard(t *testing.T, row AccountSubscriptionRow, tz *time.Location, resumeOn time.Time) string {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, accountSubscriptionCard(row, tz, resumeOn).Render(context.Background(), &buf))
	return buf.String()
}

// The subscribe page previews the first renewal from the server clock, so an
// evening visitor was shown tomorrow's date. Same class as the card, different
// props struct — which is how it survived the first sweep.
func TestSubscribePageRendersNextChargeInTheMerchantZone(t *testing.T) {
	la, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)

	var buf bytes.Buffer
	require.NoError(t, SubscribeContent(SubscribePageProps{
		Plan:         &domain.SubscriptionPlan{Name: "Weekly", Interval: domain.SubscriptionIntervalEvery7Days, IntervalCount: 1},
		Quantity:     1,
		ProductTitle: "Switchblade Espresso",
		NextChargeAt: time.Date(2027, 3, 12, 22, 0, 0, 0, la).UTC(),
		MerchantTZ:   la,
	}).Render(context.Background(), &buf))

	assert.Contains(t, buf.String(), "Mar 12, 2027")
	assert.NotContains(t, buf.String(), "Mar 13, 2027")
}

// Every date on the card is the merchant's date, whatever zone the value
// carries. next_order_at arrives from pgx in the database session zone (UTC on
// the server) while the resume preview is built merchant-local, so the card is
// the one place where both kinds meet.
func TestSubscriptionCardRendersDatesInTheMerchantZone(t *testing.T) {
	la, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)

	// 10pm in Los Angeles is already the next day in UTC. Every assertion below
	// turns on that: at the 2am renewal anchor the two zones agree and none of
	// this could fail, which is precisely why the bug shipped.
	evening := time.Date(2027, 3, 12, 22, 0, 0, 0, la)
	plan := &domain.SubscriptionPlan{Name: "Weekly", Interval: domain.SubscriptionIntervalEvery7Days}

	t.Run("active card names the booked date", func(t *testing.T) {
		row := AccountSubscriptionRow{
			Subscription: domain.Subscription{
				Status:      domain.SubscriptionStatusActive,
				Quantity:    1,
				NextOrderAt: evening.UTC(), // as pgx hands it over
			},
			Plan: plan,
		}
		out := renderSubscriptionCard(t, row, la, time.Time{})
		assert.Contains(t, out, "Mar 12, 2027")
		assert.NotContains(t, out, "Mar 13, 2027")
	})

	t.Run("paused card names the resume date", func(t *testing.T) {
		row := AccountSubscriptionRow{
			Subscription: domain.Subscription{
				Status:      domain.SubscriptionStatusPaused,
				Quantity:    1,
				NextOrderAt: evening.UTC(),
			},
			Plan: plan,
		}
		// The advisory preview is merchant-located already; it must not be
		// re-formatted into some other zone on the way out.
		out := renderSubscriptionCard(t, row, la, evening)
		assert.Contains(t, out, "Mar 12, 2027")
		assert.NotContains(t, out, "Mar 13, 2027")
	})

	t.Run("past-due card names the retry date", func(t *testing.T) {
		row := AccountSubscriptionRow{
			Subscription: domain.Subscription{
				Status:      domain.SubscriptionStatusPastDue,
				Quantity:    1,
				NextOrderAt: evening.UTC(),
			},
			Plan: plan,
		}
		out := renderSubscriptionCard(t, row, la, time.Time{})
		assert.Contains(t, out, "Mar 12, 2027")
		assert.NotContains(t, out, "Mar 13, 2027")
	})

	t.Run("cancelled card names the cancellation date", func(t *testing.T) {
		cancelled := evening.UTC()
		row := AccountSubscriptionRow{
			Subscription: domain.Subscription{
				Status:      domain.SubscriptionStatusCancelled,
				Quantity:    1,
				NextOrderAt: evening.UTC(),
				CancelledAt: &cancelled,
			},
			Plan: plan,
		}
		out := renderSubscriptionCard(t, row, la, time.Time{})
		assert.Contains(t, out, "Mar 12, 2027")
		assert.NotContains(t, out, "Mar 13, 2027")
	})
}
