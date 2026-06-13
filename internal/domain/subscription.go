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
