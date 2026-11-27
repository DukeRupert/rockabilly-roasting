package domain

import (
	"time"

	"github.com/google/uuid"
)

// DiscountType represents the kind of discount applied.
type DiscountType string

const (
	DiscountTypePercentage   DiscountType = "percentage"
	DiscountTypeFixedAmount  DiscountType = "fixed_amount"
	DiscountTypeFreeShipping DiscountType = "free_shipping"
)

// CouponUsageLimit says how many times a discount's codes can be redeemed.
type CouponUsageLimit string

const (
	// CouponUsageSingleUse: each code is spent by the first paid order that
	// uses it, whoever placed it.
	CouponUsageSingleUse CouponUsageLimit = "single_use"
	// CouponUsageOncePerCustomer: a code anyone can use, each customer once.
	// The limit is per discount, not per code, so a customer cannot use the
	// same offer twice through two of its codes.
	CouponUsageOncePerCustomer CouponUsageLimit = "once_per_customer"
)

// Valid reports whether l is one of the defined limits.
func (l CouponUsageLimit) Valid() bool {
	return l == CouponUsageSingleUse || l == CouponUsageOncePerCustomer
}

// DiscountEligibility says who may use a discount at all.
type DiscountEligibility string

const (
	DiscountEligibilityAnyone DiscountEligibility = "anyone"
	// DiscountEligibilitySubscribers limits a discount to signed-in customers
	// holding an active subscription (not paused, past due, cancelled or
	// expired), on an order
	// for themselves.
	DiscountEligibilitySubscribers DiscountEligibility = "subscribers"
)

// Valid reports whether e is one of the defined eligibilities.
func (e DiscountEligibility) Valid() bool {
	return e == DiscountEligibilityAnyone || e == DiscountEligibilitySubscribers
}

// Discount represents a discount rule.
type Discount struct {
	ID                uuid.UUID
	Name              string
	Description       *string
	Type              DiscountType
	Value             int
	MinimumOrderCents *int
	StartsAt          *time.Time
	ExpiresAt         *time.Time
	Active            bool
	UsageLimit        CouponUsageLimit
	Eligibility       DiscountEligibility
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// CouponCode represents a redeemable coupon tied to a discount.
type CouponCode struct {
	ID                uuid.UUID
	DiscountID        uuid.UUID
	Code              string
	CustomerID        *uuid.UUID
	RedeemedAt        *time.Time
	RedeemedBy        *uuid.UUID
	RedeemedByOrderID *uuid.UUID
	CreatedAt         time.Time
}

// CouponRedemption is one paid order's use of a once-per-customer discount.
// Single-use codes record their redemption on CouponCode instead.
type CouponRedemption struct {
	ID           uuid.UUID
	DiscountID   uuid.UUID
	CouponCodeID uuid.UUID
	CustomerID   uuid.UUID
	OrderID      uuid.UUID
	RedeemedAt   time.Time
}

// AppliedDiscount represents the result of evaluating a discount against a cart.
type AppliedDiscount struct {
	Discount     *Discount
	CouponCode   *CouponCode
	AmountCents  int
	FreeShipping bool
}
