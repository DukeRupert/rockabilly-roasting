package domain

import (
	"time"

	"github.com/google/uuid"
)

// SubscriptionInterval represents the billing interval for a subscription.
type SubscriptionInterval string

const (
	SubscriptionIntervalWeekly    SubscriptionInterval = "weekly"
	SubscriptionIntervalBiweekly  SubscriptionInterval = "biweekly"
	SubscriptionIntervalMonthly   SubscriptionInterval = "monthly"
	SubscriptionIntervalQuarterly SubscriptionInterval = "quarterly"
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

// SubscriptionPlan defines a recurring delivery plan.
type SubscriptionPlan struct {
	ID            uuid.UUID
	Name          string
	Interval      SubscriptionInterval
	IntervalCount int
	VariantID     uuid.UUID
	PriceSetID    uuid.UUID
	IsActive      bool
	Metadata      map[string]any
}

// Subscription represents an active customer subscription.
type Subscription struct {
	ID                 uuid.UUID
	CustomerID         uuid.UUID
	PlanID             uuid.UUID
	Status             SubscriptionStatus
	ShippingAddressID  uuid.UUID
	CurrentPeriodStart time.Time
	CurrentPeriodEnd   time.Time
	NextOrderAt        time.Time
	CancelledAt        *time.Time
	PauseUntil         *time.Time
	Metadata           map[string]any
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// SubscriptionOrder links a subscription to an order for a billing period.
type SubscriptionOrder struct {
	SubscriptionID uuid.UUID
	OrderID        uuid.UUID
	PeriodStart    time.Time
	PeriodEnd      time.Time
}
