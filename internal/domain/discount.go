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

// AppliedDiscount represents the result of evaluating a discount against a cart.
type AppliedDiscount struct {
	Discount     *Discount
	CouponCode   *CouponCode
	AmountCents  int
	FreeShipping bool
}
