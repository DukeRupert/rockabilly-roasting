package admin

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
)

// The skip panel submits two different actions from one form, distinguished by
// the value the pressed button carries. That wiring is invisible to the
// compiler — only the markup shows whether both buttons are still there and
// still named, so render it and read it back.
func TestSubscriptionShow_SkipPanel(t *testing.T) {
	tz := time.UTC
	now := time.Now().In(tz)
	subID := uuid.New()

	render := func(t *testing.T, status domain.SubscriptionStatus) string {
		t.Helper()
		var buf bytes.Buffer
		require.NoError(t, SubscriptionShowContent(SubscriptionShowProps{
			Subscription: &domain.Subscription{
				ID:                 subID,
				Status:             status,
				CurrentPeriodStart: now,
				CurrentPeriodEnd:   now.AddDate(0, 0, 30),
				NextOrderAt:        now.AddDate(0, 0, 30),
				CreatedAt:          now,
			},
			Plan:       &domain.SubscriptionPlan{Name: "Monthly", Interval: domain.SubscriptionIntervalEvery30Days, IntervalCount: 1},
			Customer:   &domain.Customer{ID: uuid.New(), FirstName: "Ada", LastName: "Byron", Email: "ada@example.com"},
			MerchantTZ: tz,
		}).Render(context.Background(), &buf))
		if out := os.Getenv("SKIP_PANEL_HTML_OUT"); out != "" {
			require.NoError(t, os.WriteFile(out, buf.Bytes(), 0o644))
		}
		return buf.String()
	}

	t.Run("active subscriptions get both skip forms", func(t *testing.T) {
		html := render(t, domain.SubscriptionStatusActive)
		assert.Contains(t, html, "/admin/subscriptions/"+subID.String()+"/skip")
		assert.Contains(t, html, `<input type="hidden" name="skip_mode" value="intervals">`)
		assert.Contains(t, html, `<input type="hidden" name="skip_mode" value="date">`)
		assert.Contains(t, html, `name="intervals"`)
		assert.Contains(t, html, `name="resume_on"`)
		// Two separate forms, so Enter in the date field can't submit the
		// shipment-count action.
		assert.Equal(t, 2, strings.Count(html, "/skip\""), "each skip mode needs its own form")
		// The picker is bounded by the same window the service enforces: it
		// opens after the order already scheduled (30 days out here), not
		// merely tomorrow, and closes at the ceiling.
		assert.Contains(t, html, `min="`+now.AddDate(0, 0, 31).Format("2006-01-02")+`"`)
		assert.Contains(t, html, `max="`+now.AddDate(0, 0, domain.SubscriptionMaxSkipDays).Format("2006-01-02")+`"`)
	})

	t.Run("no restart picker when the next order is past the ceiling", func(t *testing.T) {
		var buf bytes.Buffer
		require.NoError(t, SubscriptionShowContent(SubscriptionShowProps{
			Subscription: &domain.Subscription{
				ID:                 subID,
				Status:             domain.SubscriptionStatusActive,
				CurrentPeriodStart: now,
				CurrentPeriodEnd:   now.AddDate(0, 0, 90),
				NextOrderAt:        now.AddDate(0, 0, 90),
				CreatedAt:          now,
			},
			Plan:       &domain.SubscriptionPlan{Name: "Quarterly", Interval: domain.SubscriptionIntervalEvery90Days, IntervalCount: 1},
			Customer:   &domain.Customer{ID: uuid.New(), FirstName: "Ada", LastName: "Byron", Email: "ada@example.com"},
			MerchantTZ: tz,
		}).Render(context.Background(), &buf))
		html := buf.String()
		assert.NotContains(t, html, `name="resume_on"`)
		assert.Contains(t, html, "skip by shipment count instead")
	})

	t.Run("paused subscriptions cannot be skipped", func(t *testing.T) {
		html := render(t, domain.SubscriptionStatusPaused)
		assert.NotContains(t, html, "/skip")
	})
}
