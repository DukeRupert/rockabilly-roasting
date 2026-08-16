package web

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/store"
)

func TestNormalizeOrderSort(t *testing.T) {
	// Known values pass through.
	assert.Equal(t, store.OrderSortTotalDesc, normalizeOrderSort("total_desc"))
	assert.Equal(t, store.OrderSortNumberAsc, normalizeOrderSort("number_asc"))
	assert.Equal(t, store.OrderSortPlacedAsc, normalizeOrderSort("placed_asc"))

	// Anything else clamps to the default rather than erroring, so a stale or
	// hand-edited URL still renders a list.
	assert.Equal(t, store.OrderSortPlacedDesc, normalizeOrderSort(""))
	assert.Equal(t, store.OrderSortPlacedDesc, normalizeOrderSort("bogus"))
	assert.Equal(t, store.OrderSortPlacedDesc, normalizeOrderSort("total DESC; DROP TABLE orders"))
}

func TestNormalizeOrderStatusParams(t *testing.T) {
	assert.Equal(t, "captured", normalizeOrderPayment("captured"))
	assert.Equal(t, "", normalizeOrderPayment("not_a_status"))
	assert.Equal(t, "delivered", normalizeOrderFulfillment("delivered"))
	assert.Equal(t, "", normalizeOrderFulfillment("' OR 1=1"))
	assert.Equal(t, "30d", normalizeOrderRange("30d"))
	assert.Equal(t, "", normalizeOrderRange("last_century"))
}

func TestParseDollarFilter(t *testing.T) {
	cents, raw := parseDollarFilter("25")
	require.NotNil(t, cents)
	assert.Equal(t, 2500, *cents)
	assert.Equal(t, "25", raw)

	// A leading $ is what people actually type.
	cents, _ = parseDollarFilter("$12.34")
	require.NotNil(t, cents)
	assert.Equal(t, 1234, *cents)

	// Rounding must not drop a cent to floating point.
	cents, _ = parseDollarFilter("19.99")
	require.NotNil(t, cents)
	assert.Equal(t, 1999, *cents)

	// Absent means unbounded.
	cents, raw = parseDollarFilter("   ")
	assert.Nil(t, cents)
	assert.Equal(t, "", raw)

	// Garbage doesn't filter, but is echoed back so the user sees what they
	// typed instead of a silently blanked field.
	cents, raw = parseDollarFilter("abc")
	assert.Nil(t, cents)
	assert.Equal(t, "abc", raw)

	// Negative totals aren't a thing.
	cents, _ = parseDollarFilter("-5")
	assert.Nil(t, cents)
}

// Day boundaries must land in merchant time, not UTC. Denver is UTC-6 in
// August, so 01:30 UTC on the 16th is still the 15th at the shop — "today"
// has to start at the shop's midnight or orders vanish hours early.
func TestOrderDateBounds_UsesMerchantTimezone(t *testing.T) {
	denver, err := time.LoadLocation("America/Denver")
	require.NoError(t, err)

	now := time.Date(2026, 8, 16, 1, 30, 0, 0, time.UTC) // = Aug 15, 19:30 Denver

	from, to := orderDateBounds("today", "", "", denver, now)
	require.NotNil(t, from)
	assert.Nil(t, to, "an open-ended preset shouldn't cap the future")

	got := from.In(denver)
	assert.Equal(t, 2026, got.Year())
	assert.Equal(t, time.August, got.Month())
	assert.Equal(t, 15, got.Day(), "should be the shop's today, not UTC's tomorrow")
	assert.Equal(t, 0, got.Hour())
	assert.Equal(t, 0, got.Minute())
}

func TestOrderDateBounds_Presets(t *testing.T) {
	utc := time.UTC
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, utc)

	from, _ := orderDateBounds("7d", "", "", utc, now)
	require.NotNil(t, from)
	// Inclusive of today, so 7 days spans the 10th through the 16th.
	assert.Equal(t, 10, from.Day())

	from, _ = orderDateBounds("30d", "", "", utc, now)
	require.NotNil(t, from)
	assert.Equal(t, time.July, from.Month())
	assert.Equal(t, 18, from.Day())

	from, _ = orderDateBounds("month", "", "", utc, now)
	require.NotNil(t, from)
	assert.Equal(t, 1, from.Day())
	assert.Equal(t, time.August, from.Month())

	// No range means no bounds at all.
	from, to := orderDateBounds("", "", "", utc, now)
	assert.Nil(t, from)
	assert.Nil(t, to)
}

// The end of a custom range must cover the whole final day, or "to: today"
// silently excludes every order placed today.
func TestOrderDateBounds_CustomCoversWholeEndDay(t *testing.T) {
	utc := time.UTC
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, utc)

	from, to := orderDateBounds("custom", "2026-08-01", "2026-08-15", utc, now)
	require.NotNil(t, from)
	require.NotNil(t, to)

	assert.Equal(t, 1, from.Day())
	assert.Equal(t, 0, from.Hour())

	assert.Equal(t, 15, to.Day(), "end bound should stay on the requested day")
	assert.Equal(t, 23, to.Hour())
	assert.Equal(t, 59, to.Minute())

	// An order placed late on the end day falls inside the range.
	lateOnEndDay := time.Date(2026, 8, 15, 23, 45, 0, 0, utc)
	assert.True(t, lateOnEndDay.Before(*to))
}

func TestOrderDateBounds_CustomIgnoresGarbage(t *testing.T) {
	utc := time.UTC
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, utc)

	from, to := orderDateBounds("custom", "not-a-date", "", utc, now)
	assert.Nil(t, from)
	assert.Nil(t, to)
}

// A nil timezone must not panic — it falls back to UTC.
func TestOrderDateBounds_NilTimezone(t *testing.T) {
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	from, _ := orderDateBounds("today", "", "", nil, now)
	require.NotNil(t, from)
	assert.Equal(t, 16, from.Day())
}

func TestMinRawDate(t *testing.T) {
	// Echoed back only under the custom range.
	assert.Equal(t, "2026-08-01", minRawDate("2026-08-01", "custom"))
	assert.Equal(t, "", minRawDate("2026-08-01", "30d"))
	assert.Equal(t, "", minRawDate("garbage", "custom"))
}

func TestApplyOrderStatusFilters(t *testing.T) {
	var f store.OrderFilter
	applyOrderStatusFilters("captured", "unfulfilled", &f)

	require.Len(t, f.PaymentStatuses, 1)
	assert.EqualValues(t, "captured", f.PaymentStatuses[0])
	require.NotNil(t, f.FulfillmentStatus)
	assert.EqualValues(t, "unfulfilled", *f.FulfillmentStatus)

	// Empty means unconstrained, not "match empty string".
	var g store.OrderFilter
	applyOrderStatusFilters("", "", &g)
	assert.Empty(t, g.PaymentStatuses)
	assert.Nil(t, g.FulfillmentStatus)
}

// The view tabs constrain `status`; the status pickers constrain payment and
// fulfillment. They must compose, not overwrite each other.
func TestOrderFiltersComposeWithViewFilter(t *testing.T) {
	var f store.OrderFilter
	applyOrderViewFilter("needs_action", &f)
	applyOrderStatusFilters("captured", "unfulfilled", &f)

	assert.NotEmpty(t, f.Statuses, "view filter should survive")
	assert.True(t, f.ExcludeUnconfirmed)
	assert.NotEmpty(t, f.PaymentStatuses, "payment filter should apply too")
	assert.NotNil(t, f.FulfillmentStatus)
}
