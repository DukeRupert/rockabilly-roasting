package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A box is the customer's subscriptions that ship in one renewal run at one
// address: the scheduler takes everything due at a run and groups it by
// customer and address, and every next_order_at is snapped to the anchor, so
// "one run" is "one instant". Derived, never stored.

func boxSub(addr uuid.UUID, at time.Time, status SubscriptionStatus) Subscription {
	return Subscription{
		ID:                uuid.New(),
		ShippingAddressID: addr,
		NextOrderAt:       at,
		Status:            status,
	}
}

func boxIDs(b SubscriptionBox) []uuid.UUID {
	ids := make([]uuid.UUID, len(b.Members))
	for i, m := range b.Members {
		ids[i] = m.Subscription.ID
	}
	return ids
}

func TestGroupIntoBoxes(t *testing.T) {
	denver, err := time.LoadLocation("America/Denver")
	require.NoError(t, err)
	home, office := uuid.New(), uuid.New()
	// 2am Denver on the 10th, and 7am the same Denver day: one day, two runs.
	run := time.Date(2026, 10, 10, 2, 0, 0, 0, denver)
	laterSameDay := time.Date(2026, 10, 10, 7, 0, 0, 0, denver)
	nextWeek := run.AddDate(0, 0, 7)

	t.Run("same address and instant is one box", func(t *testing.T) {
		a := boxSub(home, run, SubscriptionStatusActive)
		b := boxSub(home, run.UTC(), SubscriptionStatusActive) // same instant, another zone
		boxes := GroupIntoBoxes([]Subscription{a, b}, denver)
		require.Len(t, boxes, 1)
		assert.ElementsMatch(t, []uuid.UUID{a.ID, b.ID}, boxIDs(boxes[0]))
		assert.Equal(t, home, boxes[0].Key.ShippingAddressID)
		assert.True(t, boxes[0].Key.NextOrderAt.Equal(run))
		assert.Equal(t, time.Date(2026, 10, 10, 0, 0, 0, 0, denver), boxes[0].Day, "labelled by its day in the merchant's zone")
	})

	t.Run("another address or another instant is another box", func(t *testing.T) {
		boxes := GroupIntoBoxes([]Subscription{
			boxSub(home, run, SubscriptionStatusActive),
			boxSub(office, run, SubscriptionStatusActive),
			boxSub(home, nextWeek, SubscriptionStatusActive),
		}, denver)
		assert.Len(t, boxes, 3)
	})

	t.Run("two instants on one day are two boxes", func(t *testing.T) {
		// After RENEWAL_ANCHOR_HOUR changes, existing rows keep the old hour:
		// same day, different runs, two orders.
		boxes := GroupIntoBoxes([]Subscription{
			boxSub(home, run, SubscriptionStatusActive),
			boxSub(home, laterSameDay, SubscriptionStatusActive),
		}, denver)
		require.Len(t, boxes, 2)
		assert.Equal(t, boxes[0].Day, boxes[1].Day)
	})

	t.Run("past due joins its box; paused and cancelled form none", func(t *testing.T) {
		active := boxSub(home, run, SubscriptionStatusActive)
		pastDue := boxSub(home, run, SubscriptionStatusPastDue)
		boxes := GroupIntoBoxes([]Subscription{
			active, pastDue,
			boxSub(home, run, SubscriptionStatusPaused),
			boxSub(home, run, SubscriptionStatusCancelled),
			boxSub(home, run, SubscriptionStatusExpired),
		}, denver)
		require.Len(t, boxes, 1)
		assert.ElementsMatch(t, []uuid.UUID{active.ID, pastDue.ID}, boxIDs(boxes[0]))
	})

	t.Run("ordered by instant, then address", func(t *testing.T) {
		first, second := home, office
		if office.String() < home.String() {
			first, second = office, home
		}
		boxes := GroupIntoBoxes([]Subscription{
			boxSub(home, nextWeek, SubscriptionStatusActive),
			boxSub(second, run, SubscriptionStatusActive),
			boxSub(first, run, SubscriptionStatusActive),
		}, denver)
		require.Len(t, boxes, 3)
		assert.Equal(t, first, boxes[0].Key.ShippingAddressID)
		assert.Equal(t, second, boxes[1].Key.ShippingAddressID)
		assert.True(t, boxes[2].Key.NextOrderAt.Equal(nextWeek))
	})

	t.Run("a dead-card member is in its box but ships separately", func(t *testing.T) {
		// The scheduler routes it to a solo renewal and RenewBatch drops it,
		// so it is its own order with its own shipping.
		healthy := boxSub(home, run, SubscriptionStatusActive)
		dead := boxSub(home, run, SubscriptionStatusPastDue)
		dead.Metadata = map[string]any{SubscriptionMetaDunningDeadPaymentMethods: []any{"pm_dead"}}
		require.True(t, dead.DunningHasDeadCard(), "fixture: the dead-card record is what flags it")

		boxes := GroupIntoBoxes([]Subscription{healthy, dead}, denver)
		require.Len(t, boxes, 1)
		for _, m := range boxes[0].Members {
			assert.Equal(t, m.Subscription.ID == dead.ID, m.ShipsSeparately)
		}
		assert.Equal(t, 2, boxes[0].Shipments(), "one order for the box, one for the dead-card member")
	})

	t.Run("a box of one healthy member is one shipment", func(t *testing.T) {
		boxes := GroupIntoBoxes([]Subscription{boxSub(home, run, SubscriptionStatusActive)}, denver)
		require.Len(t, boxes, 1)
		assert.Equal(t, 1, boxes[0].Shipments())
	})
}
