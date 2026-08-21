package web

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
)

func TestNormalizeSubscriptionSort(t *testing.T) {
	// Known values pass through.
	assert.Equal(t, store.SubscriptionSortCustomerAsc, normalizeSubscriptionSort("customer_asc"))
	assert.Equal(t, store.SubscriptionSortNextOrderAsc, normalizeSubscriptionSort("next_order_asc"))
	assert.Equal(t, store.SubscriptionSortCreatedAsc, normalizeSubscriptionSort("created_asc"))

	// Anything else clamps to the default rather than erroring, so a stale or
	// hand-edited URL still renders a list — and no raw identifier can reach
	// the ORDER BY.
	assert.Equal(t, store.SubscriptionSortCreatedDesc, normalizeSubscriptionSort(""))
	assert.Equal(t, store.SubscriptionSortCreatedDesc, normalizeSubscriptionSort("bogus"))
	assert.Equal(t, store.SubscriptionSortCreatedDesc, normalizeSubscriptionSort("created_at DESC; DROP TABLE subscriptions"))
}

func TestNormalizeSubscriptionParams(t *testing.T) {
	assert.Equal(t, "past_due", normalizeSubscriptionStatus("past_due"))
	assert.Equal(t, "", normalizeSubscriptionStatus("not_a_status"))
	assert.Equal(t, "", normalizeSubscriptionStatus("' OR 1=1"))

	assert.Equal(t, "overdue", normalizeSubscriptionDue("overdue"))
	assert.Equal(t, "custom", normalizeSubscriptionDue("custom"))
	assert.Equal(t, "", normalizeSubscriptionDue("next_century"))

	id := uuid.New()
	assert.Equal(t, id.String(), normalizeUUIDParam(id.String()))
	assert.Equal(t, "", normalizeUUIDParam("not-a-uuid"))
	assert.Equal(t, "", normalizeUUIDParam(""))
}

func TestApplySubscriptionFilters(t *testing.T) {
	planID, productID := uuid.New(), uuid.New()

	var f store.SubscriptionFilter
	applySubscriptionFilters("paused", planID.String(), productID.String(), &f)
	require.NotNil(t, f.Status)
	assert.Equal(t, domain.SubscriptionStatusPaused, *f.Status)
	require.NotNil(t, f.PlanID)
	assert.Equal(t, planID, *f.PlanID)
	require.NotNil(t, f.ProductID)
	assert.Equal(t, productID, *f.ProductID)

	// Re-applying with everything cleared must reset the filter, not leave the
	// previous run's pointers behind — the pill counts reuse one filter value.
	applySubscriptionFilters("", "", "", &f)
	assert.Nil(t, f.Status)
	assert.Nil(t, f.PlanID)
	assert.Nil(t, f.ProductID)
}

// Date boundaries are computed in the merchant's timezone: a staffer in Denver
// looking at a Los Angeles shop must see the shop's "today", not their own.
func TestSubscriptionDueBounds_UsesMerchantTimezone(t *testing.T) {
	la, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)

	// 01:30 UTC on the 16th is still 18:30 on the 15th in Los Angeles.
	now := time.Date(2026, 8, 16, 1, 30, 0, 0, time.UTC)
	from, to := subscriptionDueBounds("today", "", "", la, now)
	require.NotNil(t, from)
	require.NotNil(t, to)
	assert.Equal(t, 2026, from.Year())
	assert.Equal(t, 15, from.In(la).Day(), "the shop's today, not UTC's tomorrow")
	assert.Equal(t, 15, to.In(la).Day())
}

func TestSubscriptionDueBounds_Presets(t *testing.T) {
	tz := time.UTC
	now := time.Date(2026, 8, 16, 14, 0, 0, 0, tz)
	startOfToday := time.Date(2026, 8, 16, 0, 0, 0, 0, tz)

	// Overdue looks backward and is bounded strictly before today starts.
	from, to := subscriptionDueBounds("overdue", "", "", tz, now)
	assert.Nil(t, from)
	require.NotNil(t, to)
	assert.True(t, to.Before(startOfToday))
	assert.Equal(t, startOfToday.Add(-time.Nanosecond), *to)

	// The forward presets all start at today and cover whole days.
	for _, tc := range []struct {
		key     string
		lastDay time.Time
	}{
		{"today", startOfToday},
		{"7d", startOfToday.AddDate(0, 0, 6)},
		{"30d", startOfToday.AddDate(0, 0, 29)},
	} {
		from, to := subscriptionDueBounds(tc.key, "", "", tz, now)
		require.NotNil(t, from, tc.key)
		require.NotNil(t, to, tc.key)
		assert.Equal(t, startOfToday, *from, tc.key)
		assert.Equal(t, tc.lastDay.AddDate(0, 0, 1).Add(-time.Nanosecond), *to, tc.key)
	}

	// No preset means no bounds at all.
	from, to = subscriptionDueBounds("", "", "", tz, now)
	assert.Nil(t, from)
	assert.Nil(t, to)
}

// "To: today" must include everything due later today, not stop at midnight.
func TestSubscriptionDueBounds_CustomCoversWholeEndDay(t *testing.T) {
	tz := time.UTC
	now := time.Date(2026, 8, 16, 14, 0, 0, 0, tz)

	from, to := subscriptionDueBounds("custom", "2026-08-10", "2026-08-16", tz, now)
	require.NotNil(t, from)
	require.NotNil(t, to)
	assert.Equal(t, time.Date(2026, 8, 10, 0, 0, 0, 0, tz), *from)
	assert.True(t, to.After(time.Date(2026, 8, 16, 23, 59, 0, 0, tz)))
	assert.Equal(t, 16, to.Day())
}

// A half-filled or malformed custom range narrows on the half that parses
// rather than erroring the page.
func TestSubscriptionDueBounds_CustomIgnoresGarbage(t *testing.T) {
	tz := time.UTC
	now := time.Date(2026, 8, 16, 14, 0, 0, 0, tz)

	from, to := subscriptionDueBounds("custom", "yesterday", "2026-08-16", tz, now)
	assert.Nil(t, from)
	assert.NotNil(t, to)

	from, to = subscriptionDueBounds("custom", "", "", tz, now)
	assert.Nil(t, from)
	assert.Nil(t, to)
}

func TestSubscriptionDueBounds_NilTimezone(t *testing.T) {
	now := time.Date(2026, 8, 16, 14, 0, 0, 0, time.UTC)
	from, _ := subscriptionDueBounds("today", "", "", nil, now)
	require.NotNil(t, from)
	assert.Equal(t, time.UTC, from.Location())
}
