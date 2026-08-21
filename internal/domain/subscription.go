package domain

import (
	"time"

	"github.com/google/uuid"
)

// SubscriptionInterval represents the billing interval for a subscription.
type SubscriptionInterval string

const (
	SubscriptionIntervalEvery2Minutes SubscriptionInterval = "every_2_minutes" // dev/testing only
	SubscriptionIntervalEvery7Days    SubscriptionInterval = "every_7_days"
	SubscriptionIntervalEvery14Days   SubscriptionInterval = "every_14_days"
	SubscriptionIntervalEvery21Days   SubscriptionInterval = "every_21_days"
	SubscriptionIntervalEvery30Days   SubscriptionInterval = "every_30_days"
	SubscriptionIntervalEvery60Days   SubscriptionInterval = "every_60_days"
	SubscriptionIntervalEvery90Days   SubscriptionInterval = "every_90_days"
)

// SubscriptionStatus represents the lifecycle state of a subscription.
type SubscriptionStatus string

const (
	SubscriptionStatusActive    SubscriptionStatus = "active"
	SubscriptionStatusPaused    SubscriptionStatus = "paused"
	SubscriptionStatusPastDue   SubscriptionStatus = "past_due"
	SubscriptionStatusCancelled SubscriptionStatus = "cancelled"
	SubscriptionStatusExpired   SubscriptionStatus = "expired"
)

// SubscriptionPlan defines a recurring delivery cadence (decoupled from products).
type SubscriptionPlan struct {
	ID            uuid.UUID
	Name          string
	Interval      SubscriptionInterval
	IntervalCount int
	DiscountPct   int
	IsActive      bool
	Metadata      map[string]any
}

// Subscription represents an active customer subscription.
type Subscription struct {
	ID                 uuid.UUID
	CustomerID         uuid.UUID
	PlanID             uuid.UUID
	VariantID          uuid.UUID
	Quantity           int
	Status             SubscriptionStatus
	ShippingAddressID      uuid.UUID
	StripePaymentMethodID  *string
	CurrentPeriodStart time.Time
	CurrentPeriodEnd   time.Time
	NextOrderAt        time.Time
	EndsAt             *time.Time
	CancelledAt        *time.Time
	PauseUntil         *time.Time
	Metadata           map[string]any
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// SubscriptionMaxSkipIntervals caps how many upcoming shipments a subscriber
// (or staff on their behalf) can skip in one request. Six keeps a weekly
// subscriber inside a month and a half and a monthly subscriber inside half a
// year — long enough for a trip or a full pantry, short enough that a forgotten
// subscription resurfaces rather than quietly going dormant. Anything longer is
// a pause, not a skip.
const SubscriptionMaxSkipIntervals = 6

// SubscriptionMaxSkipDays caps the date form of a skip: any restart day up to
// 60 days out. Beyond that the customer wants a pause — an open-ended gap is a
// different intent, and we'd rather they tell us so.
const SubscriptionMaxSkipDays = 60

// SubscriptionMetaSkipUndo is the metadata key holding the schedule a skip
// replaced, so a mistaken skip can be put back exactly as it was rather than
// re-derived from a cadence that may since have changed.
const SubscriptionMetaSkipUndo = "skip_undo"

// SkipUndo is the schedule snapshot taken when a subscription was skipped.
// AppliedNextOrderAt records what the skip set next_order_at to: an undo is
// only offered while the subscription still sits on that date, so any later
// change (a renewal, a resume, a plan swap, a second skip) retires the undo on
// its own without anyone having to remember to clear it.
type SkipUndo struct {
	PeriodEnd          time.Time
	NextOrderAt        time.Time
	AppliedNextOrderAt time.Time
}

// Metadata renders the snapshot for the jsonb column. Times are RFC3339 so they
// survive the round-trip through jsonb as strings.
func (u SkipUndo) Metadata() map[string]any {
	return map[string]any{
		"period_end":            u.PeriodEnd.Format(time.RFC3339Nano),
		"next_order_at":         u.NextOrderAt.Format(time.RFC3339Nano),
		"applied_next_order_at": u.AppliedNextOrderAt.Format(time.RFC3339Nano),
	}
}

// SkipUndo reads back the snapshot written by the last skip. ok is false when
// there is none, or when the stored shape is not what we wrote (an old row, or
// hand-edited metadata) — an unreadable snapshot must read as "nothing to
// undo", never as a partially-restored schedule.
func (s *Subscription) SkipUndo() (SkipUndo, bool) {
	if s.Metadata == nil {
		return SkipUndo{}, false
	}
	raw, _ := s.Metadata[SubscriptionMetaSkipUndo].(map[string]any)
	if raw == nil {
		return SkipUndo{}, false
	}
	parse := func(key string) (time.Time, bool) {
		str, _ := raw[key].(string)
		t, err := time.Parse(time.RFC3339Nano, str)
		return t, err == nil
	}
	periodEnd, okEnd := parse("period_end")
	nextOrder, okNext := parse("next_order_at")
	applied, okApplied := parse("applied_next_order_at")
	if !okEnd || !okNext || !okApplied {
		return SkipUndo{}, false
	}
	return SkipUndo{PeriodEnd: periodEnd, NextOrderAt: nextOrder, AppliedNextOrderAt: applied}, true
}

// SubscriptionMetaShippingGrandfathered is the metadata key marking a
// subscription whose renewals keep free shipping (it predates, or was manually
// exempted from, the shipping-on-renewal policy). Migration 054 seeds it on
// pre-existing subscriptions; staff toggle it per subscription thereafter.
const SubscriptionMetaShippingGrandfathered = "shipping_grandfathered"

// ShippingGrandfathered reports whether this subscription's renewals should
// waive the shipping charge. jsonb true round-trips through Metadata as a Go
// bool; any other shape reads as false (charge shipping normally).
func (s *Subscription) ShippingGrandfathered() bool {
	if s.Metadata == nil {
		return false
	}
	v, _ := s.Metadata[SubscriptionMetaShippingGrandfathered].(bool)
	return v
}

// SubscriptionDelta is the net change in the active subscription base on one
// calendar day (merchant timezone): +1 for each subscription created that day,
// -1 for each that was cancelled or expired. Days with no change are omitted —
// the caller carries a running total forward to reconstruct the active count
// over time.
type SubscriptionDelta struct {
	Date time.Time
	Net  int
}

// SubscriptionOrder links a subscription to an order for a billing period.
type SubscriptionOrder struct {
	SubscriptionID uuid.UUID
	OrderID        uuid.UUID
	PeriodStart    time.Time
	PeriodEnd      time.Time
}
