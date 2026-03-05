package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/platform/payments"
	"github.com/dukerupert/hiri/internal/store"
)

// CheckoutService orchestrates the checkout flow: cart validation, payment, and order creation.
type CheckoutService struct {
	orders   *store.OrderStore
	customers *store.CustomerStore
	discounts *store.DiscountStore
	payments  payments.Provider
	audit     *audit.AuditWriter
	metrics   *metrics.Registry
}

// NewCheckoutService creates a new CheckoutService.
func NewCheckoutService(
	orders *store.OrderStore,
	customers *store.CustomerStore,
	discounts *store.DiscountStore,
	payments payments.Provider,
	audit *audit.AuditWriter,
	metrics *metrics.Registry,
) *CheckoutService {
	return &CheckoutService{
		orders:    orders,
		customers: customers,
		discounts: discounts,
		payments:  payments,
		audit:     audit,
		metrics:   metrics,
	}
}

// CartItem represents an item in the checkout cart with pre-resolved pricing.
type CartItem struct {
	VariantID uuid.UUID
	Quantity  int
	UnitPrice int
}

// PlaceOrderParams holds all input needed to place an order.
type PlaceOrderParams struct {
	CustomerID        uuid.UUID
	Items             []CartItem
	ShippingAddressID uuid.UUID
	BillingAddressID  uuid.UUID
	CurrencyCode      string
	CouponCode        *string
	ShippingCents     int
	TaxCents          int
	Notes             *string
	Metadata          map[string]any
}

// PlaceOrder creates a new order from the given parameters within the provided transaction.
func (s *CheckoutService) PlaceOrder(ctx context.Context, tx pgx.Tx, p PlaceOrderParams, actor Actor) (*domain.Order, error) {
	if len(p.Items) == 0 {
		return nil, ErrCartEmpty
	}

	// Verify customer exists.
	_, err := s.customers.GetByID(ctx, tx, p.CustomerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCustomerNotFound
		}
		return nil, fmt.Errorf("get customer: %w", err)
	}

	// Calculate subtotal.
	subtotal := 0
	for _, item := range p.Items {
		subtotal += item.UnitPrice * item.Quantity
	}

	// Apply coupon/discount if provided.
	var appliedDiscount *domain.Discount
	var coupon *domain.CouponCode
	discountAmount := 0

	if p.CouponCode != nil {
		coupon, err = s.discounts.GetCouponCodeByCode(ctx, tx, *p.CouponCode)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrCouponNotFound
			}
			return nil, fmt.Errorf("get coupon code: %w", err)
		}

		if coupon.RedeemedAt != nil {
			return nil, ErrCouponAlreadyUsed
		}

		appliedDiscount, err = s.discounts.GetByID(ctx, tx, coupon.DiscountID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrDiscountNotFound
			}
			return nil, fmt.Errorf("get discount: %w", err)
		}

		if !appliedDiscount.Active {
			return nil, ErrDiscountNotActive
		}

		now := time.Now()
		if appliedDiscount.ExpiresAt != nil && now.After(*appliedDiscount.ExpiresAt) {
			return nil, ErrDiscountExpired
		}

		if appliedDiscount.MinimumOrderCents != nil && subtotal < *appliedDiscount.MinimumOrderCents {
			return nil, ErrMinimumOrderNotMet
		}

		discountAmount = calculateDiscount(appliedDiscount, subtotal)
	}

	total := subtotal - discountAmount + p.ShippingCents + p.TaxCents

	orderNumber := fmt.Sprintf("ORD-%d", time.Now().UnixMilli())
	customerID := p.CustomerID

	order, err := s.orders.CreateOrder(ctx, tx, store.CreateOrderParams{
		Number:            orderNumber,
		CustomerID:        &customerID,
		Status:            domain.OrderStatusPending,
		PaymentStatus:     domain.PaymentStatusAwaiting,
		FulfillmentStatus: domain.FulfillmentStatusUnfulfilled,
		CurrencyCode:      p.CurrencyCode,
		Subtotal:          subtotal,
		DiscountTotal:     discountAmount,
		ShippingTotal:     p.ShippingCents,
		TaxTotal:          p.TaxCents,
		Total:             total,
		ShippingAddressID: p.ShippingAddressID,
		BillingAddressID:  p.BillingAddressID,
		Notes:             p.Notes,
		Metadata:          p.Metadata,
		PlacedAt:          time.Now(),
	})
	if err != nil {
		return nil, fmt.Errorf("create order: %w", err)
	}

	// Create line items.
	for _, item := range p.Items {
		lineSubtotal := item.UnitPrice * item.Quantity
		_, err := s.orders.CreateLineItem(ctx, tx, store.CreateLineItemParams{
			OrderID:   order.ID,
			VariantID: item.VariantID,
			Quantity:  item.Quantity,
			UnitPrice: item.UnitPrice,
			Subtotal:  lineSubtotal,
			Total:     lineSubtotal,
		})
		if err != nil {
			return nil, fmt.Errorf("create line item: %w", err)
		}
	}

	// Create discount adjustment and mark coupon redeemed.
	if appliedDiscount != nil && coupon != nil {
		_, err := s.orders.CreateAdjustment(ctx, tx, store.CreateAdjustmentParams{
			OrderID:    order.ID,
			Label:      appliedDiscount.Name,
			Amount:     -discountAmount,
			SourceType: "discount",
			SourceID:   appliedDiscount.ID,
		})
		if err != nil {
			return nil, fmt.Errorf("create discount adjustment: %w", err)
		}

		if err := s.discounts.MarkCouponCodeRedeemed(ctx, tx, coupon.ID, &p.CustomerID); err != nil {
			return nil, fmt.Errorf("mark coupon redeemed: %w", err)
		}
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditOrderCreated,
		ResourceType: "order",
		ResourceID:   order.ID,
		After:        order,
	}); err != nil {
		return nil, fmt.Errorf("audit order created: %w", err)
	}

	return order, nil
}

// ApplyCoupon validates a coupon code and returns its associated discount for preview.
func (s *CheckoutService) ApplyCoupon(ctx context.Context, tx pgx.Tx, code string) (*domain.Discount, error) {
	coupon, err := s.discounts.GetCouponCodeByCode(ctx, tx, code)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCouponNotFound
		}
		return nil, fmt.Errorf("get coupon code: %w", err)
	}

	if coupon.RedeemedAt != nil {
		return nil, ErrCouponAlreadyUsed
	}

	discount, err := s.discounts.GetByID(ctx, tx, coupon.DiscountID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrDiscountNotFound
		}
		return nil, fmt.Errorf("get discount: %w", err)
	}

	if !discount.Active {
		return nil, ErrDiscountNotActive
	}

	now := time.Now()
	if discount.ExpiresAt != nil && now.After(*discount.ExpiresAt) {
		return nil, ErrDiscountExpired
	}

	return discount, nil
}

// calculateDiscount computes the discount amount based on discount type.
func calculateDiscount(d *domain.Discount, subtotal int) int {
	switch d.Type {
	case domain.DiscountTypePercentage:
		return subtotal * d.Value / 100
	case domain.DiscountTypeFixedAmount:
		if d.Value > subtotal {
			return subtotal
		}
		return d.Value
	default:
		return 0
	}
}
