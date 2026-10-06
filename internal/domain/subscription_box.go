package domain

import (
	"sort"
	"time"

	"github.com/google/uuid"
)

// SubscriptionBoxKey identifies a box: one shipping address and one renewal
// instant.
//
// The instant, not the calendar day. The scheduler takes everything due at a
// run and groups it by customer and address, and every next_order_at is snapped
// to the renewal anchor, so "ships together" is "same instant". Two
// subscriptions can share a day and not a run — after RENEWAL_ANCHOR_HOUR
// changes, existing rows keep the old hour — and those ship as two orders.
type SubscriptionBoxKey struct {
	ShippingAddressID uuid.UUID
	NextOrderAt       time.Time
}

// SubscriptionBoxMember is one subscription in a box.
type SubscriptionBoxMember struct {
	Subscription Subscription
	// ShipsSeparately is set for a member carrying a dead-card record. The
	// scheduler routes it to a solo renewal and a batch drops it, so it is its
	// own order with its own shipping, even though it is due with the box.
	ShipsSeparately bool
}

// SubscriptionBox is the customer's subscriptions that renew in one run at one
// address — one order, one shipping charge. Derived from the subscriptions
// every time it is needed, never stored: there is no box_id, and a skip on one
// member simply moves it to another box.
type SubscriptionBox struct {
	Key SubscriptionBoxKey
	// Day is the box's ship day in the merchant's zone, at midnight. It labels
	// the box; it is not the key.
	Day     time.Time
	Members []SubscriptionBoxMember
}

// Shipments is how many orders this box actually becomes: one for the members
// that batch, and one more for each member that ships separately.
func (b SubscriptionBox) Shipments() int {
	n := 0
	together := false
	for _, m := range b.Members {
		if m.ShipsSeparately {
			n++
		} else {
			together = true
		}
	}
	if together {
		n++
	}
	return n
}

// GroupIntoBoxes groups subscriptions into the boxes they ship in, ordered by
// instant and then by address.
//
// Active and past-due subscriptions form boxes; a past-due one is retried at
// its next_order_at and is batched with whatever is due then. Paused,
// cancelled and expired subscriptions have no upcoming run and form none.
func GroupIntoBoxes(subs []Subscription, tz *time.Location) []SubscriptionBox {
	if tz == nil {
		tz = time.Local
	}
	type mapKey struct {
		addr uuid.UUID
		at   int64
	}
	index := map[mapKey]int{}
	var boxes []SubscriptionBox
	for _, sub := range subs {
		if sub.Status != SubscriptionStatusActive && sub.Status != SubscriptionStatusPastDue {
			continue
		}
		k := mapKey{addr: sub.ShippingAddressID, at: sub.NextOrderAt.UnixNano()}
		i, ok := index[k]
		if !ok {
			local := sub.NextOrderAt.In(tz)
			i = len(boxes)
			index[k] = i
			boxes = append(boxes, SubscriptionBox{
				Key: SubscriptionBoxKey{ShippingAddressID: sub.ShippingAddressID, NextOrderAt: sub.NextOrderAt},
				Day: time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, tz),
			})
		}
		boxes[i].Members = append(boxes[i].Members, SubscriptionBoxMember{
			Subscription:    sub,
			ShipsSeparately: sub.DunningHasDeadCard(),
		})
	}
	sort.SliceStable(boxes, func(i, j int) bool {
		a, b := boxes[i].Key, boxes[j].Key
		if !a.NextOrderAt.Equal(b.NextOrderAt) {
			return a.NextOrderAt.Before(b.NextOrderAt)
		}
		return a.ShippingAddressID.String() < b.ShippingAddressID.String()
	})
	return boxes
}

// SubscriptionIntervalDays is a plan's cadence in days, for customer-facing
// copy and date arithmetic that has to agree with it. Zero for the dev-only
// two-minute interval, which never appears in customer comms.
func SubscriptionIntervalDays(interval SubscriptionInterval, count int) int {
	if count < 1 {
		count = 1
	}
	switch interval {
	case SubscriptionIntervalEvery7Days:
		return 7 * count
	case SubscriptionIntervalEvery14Days:
		return 14 * count
	case SubscriptionIntervalEvery21Days:
		return 21 * count
	case SubscriptionIntervalEvery30Days:
		return 30 * count
	case SubscriptionIntervalEvery60Days:
		return 60 * count
	case SubscriptionIntervalEvery90Days:
		return 90 * count
	default:
		return 0
	}
}
