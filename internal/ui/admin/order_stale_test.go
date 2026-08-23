package admin

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/dukerupert/hiri/internal/domain"
)

// orderIsStale drives the rust leading-edge flag on four surfaces — the
// dashboard queues, the order list, and both fulfillment lists. Its whole job
// is to be believed, so the interesting cases are the ones where a flat age
// threshold would cry wolf: wholesale orders working through a weekly cycle,
// and local deliveries waiting on a run day that has not arrived yet.

// Denver is the merchant's real zone and sits far enough west that a Postgres
// `date` read as an instant lands on the wrong side of local midnight — the
// exact bug the calendar-day comparison exists to avoid.
var denver = mustLoad("America/Denver")

func mustLoad(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

func staleOrder(ch domain.OrderChannel, placedAgo time.Duration) domain.Order {
	return domain.Order{
		Channel:       ch,
		Status:        domain.OrderStatusConfirmed,
		PaymentStatus: domain.PaymentStatusCaptured,
		PlacedAt:      time.Now().Add(-placedAgo),
	}
}

// Retail keeps the original two-day threshold: it ships on demand, so an order
// sitting that long means someone forgot.
func TestOrderIsStale_RetailAtFortyEightHours(t *testing.T) {
	now := time.Now()

	assert.False(t, orderIsStale(staleOrder(domain.OrderChannelRetail, 47*time.Hour), denver, now))
	assert.True(t, orderIsStale(staleOrder(domain.OrderChannelRetail, 49*time.Hour), denver, now))
}

// Wholesale runs a weekly cycle against a Friday cutoff, so three days in the
// queue is the process working. Flagging it at 48h painted the whole wholesale
// queue rust every week, which is how staff learn to ignore the colour.
func TestOrderIsStale_WholesaleToleratesTheWeeklyCycle(t *testing.T) {
	now := time.Now()

	for _, age := range []time.Duration{49 * time.Hour, 3 * 24 * time.Hour, 6 * 24 * time.Hour} {
		assert.Falsef(t, orderIsStale(staleOrder(domain.OrderChannelWholesale, age), denver, now),
			"wholesale order aged %s should not be stale inside one ordering cycle", age)
		// The same age on the retail side is overdue — that is the whole point
		// of splitting the threshold.
		assert.Truef(t, orderIsStale(staleOrder(domain.OrderChannelRetail, age), denver, now),
			"retail order aged %s should be stale", age)
	}

	// Still unfulfilled when the customer is asked to place their next order.
	assert.True(t, orderIsStale(staleOrder(domain.OrderChannelWholesale, 8*24*time.Hour), denver, now))
}

// A local-delivery order is judged against the run it was promised, not its
// age. This is the case a flat threshold gets exactly backwards: an order
// placed Thursday for Monday's van is fine on Saturday.
func TestOrderIsStale_LocalDeliveryUsesTheRunDate(t *testing.T) {
	saturday := time.Date(2026, 8, 22, 14, 0, 0, 0, denver)
	monday := time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC) // a Postgres `date`

	o := staleOrder(domain.OrderChannelRetail, 4*24*time.Hour) // old enough to trip the age rule
	o.ScheduledDeliveryDate = &monday

	assert.False(t, orderIsStale(o, denver, saturday),
		"an order waiting on Monday's van is not late on Saturday")

	// Its own delivery day: still in play, right up to local midnight.
	assert.False(t, orderIsStale(o, denver, time.Date(2026, 8, 24, 23, 59, 0, 0, denver)))

	// The van has left and it is still sitting.
	assert.True(t, orderIsStale(o, denver, time.Date(2026, 8, 25, 0, 1, 0, 0, denver)))
}

// The run date is a calendar date, so it must not be compared as an instant.
// Read as one, midnight UTC is 6pm the previous day in Denver — which would
// call an order stale in the middle of the afternoon it was due to go out.
func TestOrderIsStale_RunDateIsNotAnInstant(t *testing.T) {
	runDay := time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)

	o := staleOrder(domain.OrderChannelRetail, 4*24*time.Hour)
	o.ScheduledDeliveryDate = &runDay

	// 6:01pm Denver on the delivery day is past midnight UTC of the next day.
	assert.False(t, orderIsStale(o, denver, time.Date(2026, 8, 24, 18, 1, 0, 0, denver)),
		"an order due out today is not overdue at 6pm today")

	// A nil zone must fall back rather than panic.
	assert.NotPanics(t, func() { orderIsStale(o, nil, time.Now()) })
}

// Nothing terminal can be overdue, and a pending intent is waiting on the
// customer — except once its charge has actually failed, which is ours to chase.
func TestOrderNeedsHands(t *testing.T) {
	actionable := []domain.OrderStatus{
		domain.OrderStatusConfirmed, domain.OrderStatusProcessing, domain.OrderStatusOnHold,
	}
	for _, st := range actionable {
		assert.Truef(t, orderNeedsHands(domain.Order{Status: st}), "%s should be actionable", st)
	}

	terminal := []domain.OrderStatus{
		domain.OrderStatusComplete, domain.OrderStatusCancelled, domain.OrderStatusRefunded,
	}
	for _, st := range terminal {
		assert.Falsef(t, orderNeedsHands(domain.Order{Status: st}), "%s should never be stale", st)
		// Not even a failed charge resurrects a cancelled order.
		assert.Falsef(t, orderNeedsHands(domain.Order{Status: st, PaymentStatus: domain.PaymentStatusFailed}),
			"%s should stay terminal even with a failed payment", st)
	}

	pending := domain.Order{Status: domain.OrderStatusPending, PaymentStatus: domain.PaymentStatusAwaiting}
	assert.False(t, orderNeedsHands(pending), "an unfinished checkout is waiting on the customer")

	pending.PaymentStatus = domain.PaymentStatusFailed
	assert.True(t, orderNeedsHands(pending), "a failed charge is ours to chase")
}

func TestDayNumber(t *testing.T) {
	assert.Equal(t, 20260824, dayNumber(2026, time.August, 24))
	// Ordering has to hold across month and year boundaries.
	assert.Greater(t, dayNumber(2026, time.September, 1), dayNumber(2026, time.August, 31))
	assert.Greater(t, dayNumber(2027, time.January, 1), dayNumber(2026, time.December, 31))
}
