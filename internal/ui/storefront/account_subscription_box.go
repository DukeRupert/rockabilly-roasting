package storefront

import (
	"time"

	"github.com/google/uuid"

	"github.com/dukerupert/hiri/internal/domain"
)

// AccountSubscriptionBox is one box on the account page: the subscriptions
// that ship together in one run at one address, each still its own card.
type AccountSubscriptionBox struct {
	Key  domain.SubscriptionBoxKey
	Day  time.Time
	Rows []AccountSubscriptionRow
	// SkipDefault is the restart day the whole-box skip form opens on: the
	// box's day plus the shortest member cadence, which is the skip a customer
	// most often means ("not this one, the next"). Kept inside the window the
	// service accepts. Empty when no day in that window exists, and the form
	// is not offered.
	SkipDefault          string
	SkipMin, SkipMax     string
	CanSkip, CanPauseAll bool
}

// BuildAccountSubscriptionsProps sorts a customer's subscription rows into the
// boxes they ship in and the rows that ship in none (paused, cancelled,
// expired), and decides the separate-charges notice. The grouping is
// domain.GroupIntoBoxes, the same rule SubscriptionService.SkipBox finds a
// box's members by.
func BuildAccountSubscriptionsProps(customer *domain.Customer, rows []AccountSubscriptionRow, tz *time.Location) AccountSubscriptionsProps {
	props := AccountSubscriptionsProps{Customer: customer, MerchantTZ: tz}

	byID := make(map[uuid.UUID]AccountSubscriptionRow, len(rows))
	subs := make([]domain.Subscription, len(rows))
	for i, row := range rows {
		byID[row.Subscription.ID] = row
		subs[i] = row.Subscription
	}

	boxed := map[uuid.UUID]bool{}
	for _, box := range domain.GroupIntoBoxes(subs, tz) {
		b := AccountSubscriptionBox{Key: box.Key, Day: box.Day, CanSkip: true, CanPauseAll: true}
		shortest := 0
		for _, m := range box.Members {
			row := byID[m.Subscription.ID]
			row.ShipsSeparately = m.ShipsSeparately
			b.Rows = append(b.Rows, row)
			boxed[m.Subscription.ID] = true
			// The service refuses the whole box when any member cannot go, so
			// the page does not offer what would bounce.
			if m.Subscription.Status != domain.SubscriptionStatusActive {
				b.CanSkip, b.CanPauseAll = false, false
			}
			if row.Plan != nil {
				if days := domain.SubscriptionIntervalDays(row.Plan.Interval, row.Plan.IntervalCount); days > 0 && (shortest == 0 || days < shortest) {
					shortest = days
				}
			}
		}
		b.SkipMin, b.SkipMax, b.SkipDefault = boxSkipWindow(box.Key.NextOrderAt, box.Day, shortest, tz)
		if b.SkipDefault == "" {
			b.CanSkip = false
		}
		props.Boxes = append(props.Boxes, b)
	}
	for _, row := range rows {
		if !boxed[row.Subscription.ID] {
			props.Rows = append(props.Rows, row)
		}
	}

	props.SeparateChargesNotice, props.CanLineUpBySkipping = separateCharges(props.Boxes)
	return props
}

// boxSkipWindow is the whole-box skip form's bounds and the day it opens on.
// The bounds are the per-row form's, from the box's instant; the default is the
// box's day plus the shortest cadence, pulled back inside the bounds.
func boxSkipWindow(nextOrderAt, day time.Time, shortestDays int, tz *time.Location) (lo, hi, def string) {
	lo, hi, ok := skipRestartBounds(domain.Subscription{NextOrderAt: nextOrderAt}, tz)
	if !ok {
		return "", "", ""
	}
	if shortestDays < 1 {
		shortestDays = 1
	}
	def = day.AddDate(0, 0, shortestDays).Format("2006-01-02")
	if def < lo {
		def = lo
	}
	if def > hi {
		def = hi
	}
	return lo, hi, def
}

// separateCharges reports whether any address has more than one shipment
// coming — two boxes, or a member that ships on its own — and so more than one
// shipping charge; and whether a skip could line them up, which it can only if
// every item there is on the same plan. A weekly and a monthly drift apart
// again whatever day they are moved to.
func separateCharges(boxes []AccountSubscriptionBox) (notice, canLineUp bool) {
	type perAddress struct {
		shipments int
		plans     map[uuid.UUID]bool
	}
	addrs := map[uuid.UUID]*perAddress{}
	for _, b := range boxes {
		a := addrs[b.Key.ShippingAddressID]
		if a == nil {
			a = &perAddress{plans: map[uuid.UUID]bool{}}
			addrs[b.Key.ShippingAddressID] = a
		}
		together := false
		for _, row := range b.Rows {
			a.plans[row.Subscription.PlanID] = true
			if row.ShipsSeparately {
				a.shipments++
			} else {
				together = true
			}
		}
		if together {
			a.shipments++
		}
	}
	canLineUp = true
	for _, a := range addrs {
		if a.shipments > 1 {
			notice = true
			if len(a.plans) > 1 {
				canLineUp = false
			}
		}
	}
	return notice, notice && canLineUp
}
