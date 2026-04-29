package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/platform/payments"
	"github.com/dukerupert/hiri/internal/platform/tax"
	"github.com/dukerupert/hiri/internal/store"
)

// CheckoutService orchestrates the checkout flow: cart validation, payment, and order creation.
type CheckoutService struct {
	orders    *store.OrderStore
	customers *store.CustomerStore
	discounts *store.DiscountStore
	settings  *store.SettingsStore
	shipping  *store.ShippingStore
	payments  payments.Provider
	audit     *audit.AuditWriter
	metrics   *metrics.Registry
}

// NewCheckoutService creates a new CheckoutService.
func NewCheckoutService(
	orders *store.OrderStore,
	customers *store.CustomerStore,
	discounts *store.DiscountStore,
	settings *store.SettingsStore,
	shipping *store.ShippingStore,
	payments payments.Provider,
	audit *audit.AuditWriter,
	metrics *metrics.Registry,
) *CheckoutService {
	return &CheckoutService{
		orders:    orders,
		customers: customers,
		discounts: discounts,
		settings:  settings,
		shipping:  shipping,
		payments:  payments,
		audit:     audit,
		metrics:   metrics,
	}
}

// GetShippingConfig returns the merchant's shipping configuration so callers
// can render rate, threshold, and local-zone messaging.
func (s *CheckoutService) GetShippingConfig(ctx context.Context, tx pgx.Tx) (*domain.ShippingConfig, error) {
	return s.shipping.GetConfig(ctx, tx)
}

// UpdateShippingConfig persists an edited shipping configuration and records
// the change in the audit log inside the caller's transaction.
func (s *CheckoutService) UpdateShippingConfig(ctx context.Context, tx pgx.Tx, cfg domain.ShippingConfig, actor Actor) error {
	before, err := s.shipping.GetConfig(ctx, tx)
	if err != nil {
		return fmt.Errorf("load current shipping config: %w", err)
	}
	if err := s.shipping.UpdateConfig(ctx, tx, cfg); err != nil {
		return err
	}
	return s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditShippingConfigUpdated,
		ResourceType: "shipping_config",
		ResourceID:   uuid.Nil,
		After: map[string]any{
			"flat_rate_cents":         cfg.FlatRateCents,
			"free_shipping_threshold": cfg.FreeShippingThreshold,
			"local_zip_codes":         cfg.LocalZipCodes,
		},
		Metadata: map[string]any{
			"before": map[string]any{
				"flat_rate_cents":         before.FlatRateCents,
				"free_shipping_threshold": before.FreeShippingThreshold,
				"local_zip_codes":         before.LocalZipCodes,
			},
		},
	})
}

// CalculateShipping returns the shipping cost in cents for a given subtotal
// and destination zip, using the merchant's shipping configuration.
func (s *CheckoutService) CalculateShipping(ctx context.Context, tx pgx.Tx, subtotalCents int, shipToZip string) (int, *domain.ShippingConfig, error) {
	cfg, err := s.GetShippingConfig(ctx, tx)
	if err != nil {
		return 0, nil, fmt.Errorf("get shipping config: %w", err)
	}
	return cfg.Calculate(subtotalCents, shipToZip), cfg, nil
}

// taxCalculatorForConfig returns the appropriate TaxCalculator for the given config and customer type.
// B2B (wholesale) always gets NoneCalculator regardless of store config.
func taxCalculatorForConfig(cfg *domain.TaxConfig, isWholesale bool) tax.TaxCalculator {
	if isWholesale {
		return &tax.NoneCalculator{}
	}
	switch cfg.Mode {
	case domain.TaxModeFlatRate:
		// Single-nexus WA merchant. If a second nexus state is added,
		// promote Jurisdiction to a store_settings column.
		return &tax.FlatRateCalculator{
			Rate:         cfg.Rate,
			Label:        cfg.Label,
			Jurisdiction: "WA",
		}
	case domain.TaxModeStripeTax:
		// Stripe Tax not yet implemented — fall back to none.
		return &tax.NoneCalculator{}
	default:
		return &tax.NoneCalculator{}
	}
}

// CalculateTax computes tax for the given line items using the store's tax configuration.
// shippingState is the 2-letter state code of the ship-to address; pass "" if unknown
// (flat-rate with a Jurisdiction will return zero in that case).
func (s *CheckoutService) CalculateTax(ctx context.Context, tx pgx.Tx, items []domain.TaxLineItem, customerExempt, isWholesale bool, shippingState string) (*domain.TaxResult, error) {
	cfg, err := s.settings.GetTaxConfig(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("get tax config: %w", err)
	}

	calculator := taxCalculatorForConfig(cfg, isWholesale)
	result, err := calculator.Calculate(ctx, tax.TaxOrder{
		CustomerExempt: customerExempt,
		ShippingState:  shippingState,
		LineItems:      items,
	})
	if err != nil {
		return nil, fmt.Errorf("calculate tax: %w", err)
	}

	return &result, nil
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
	SubscriptionID    *uuid.UUID
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

	orderNumber := generateOrderNumber()
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
		SubscriptionID:    p.SubscriptionID,
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

	// Create discount adjustment and atomically redeem coupon.
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

		_, err = s.discounts.RedeemCouponCode(ctx, tx, coupon.ID, &p.CustomerID, order.ID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrCouponAlreadyRedeemed
			}
			return nil, fmt.Errorf("redeem coupon code: %w", err)
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

// GetCouponCodeByID returns a coupon code by ID.
func (s *CheckoutService) GetCouponCodeByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.CouponCode, error) {
	coupon, err := s.discounts.GetCouponCodeByID(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCouponNotFound
		}
		return nil, fmt.Errorf("get coupon code: %w", err)
	}
	return coupon, nil
}

// GetCouponCodeByCode returns a coupon code by its code string.
func (s *CheckoutService) GetCouponCodeByCode(ctx context.Context, tx pgx.Tx, code string) (*domain.CouponCode, error) {
	coupon, err := s.discounts.GetCouponCodeByCode(ctx, tx, code)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCouponNotFound
		}
		return nil, fmt.Errorf("get coupon code: %w", err)
	}
	return coupon, nil
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

// ManualOrderItem is one line item for a manually-entered order.
type ManualOrderItem struct {
	VariantID uuid.UUID
	Quantity  int
	UnitPrice int
}

// CreateManualOrderParams holds all inputs for an admin-created order.
// Totals are admin-supplied (no cart, no tax/shipping calc, no coupon).
type CreateManualOrderParams struct {
	CustomerID            uuid.UUID
	Items                 []ManualOrderItem
	ShippingAddressID     uuid.UUID
	BillingAddressID      uuid.UUID
	CurrencyCode          string
	Subtotal              int
	DiscountTotal         int
	ShippingTotal         int
	TaxTotal              int
	Total                 int
	Status                domain.OrderStatus
	PaymentStatus         domain.PaymentStatus
	StripePaymentIntentID *string
	Notes                 *string
}

// CreateManualOrder creates an order from admin input, bypassing the cart and
// coupon flow. Used for reconciliation (e.g. payments processed by Stripe but
// missing from Hiri) and phone/email orders. Totals are accepted as-given;
// the admin is the source of truth.
func (s *CheckoutService) CreateManualOrder(ctx context.Context, tx pgx.Tx, p CreateManualOrderParams, actor Actor) (*domain.Order, error) {
	if len(p.Items) == 0 {
		return nil, ErrCartEmpty
	}
	if _, err := s.customers.GetByID(ctx, tx, p.CustomerID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCustomerNotFound
		}
		return nil, fmt.Errorf("get customer: %w", err)
	}

	customerID := p.CustomerID
	order, err := s.orders.CreateOrder(ctx, tx, store.CreateOrderParams{
		Number:                generateOrderNumber(),
		CustomerID:            &customerID,
		Status:                p.Status,
		PaymentStatus:         p.PaymentStatus,
		FulfillmentStatus:     domain.FulfillmentStatusUnfulfilled,
		CurrencyCode:          p.CurrencyCode,
		Subtotal:              p.Subtotal,
		DiscountTotal:         p.DiscountTotal,
		ShippingTotal:         p.ShippingTotal,
		TaxTotal:              p.TaxTotal,
		Total:                 p.Total,
		ShippingAddressID:     p.ShippingAddressID,
		BillingAddressID:      p.BillingAddressID,
		Notes:                 p.Notes,
		Metadata:              map[string]any{"manual": true},
		PlacedAt:              time.Now(),
	})
	if err != nil {
		return nil, fmt.Errorf("create order: %w", err)
	}

	if p.StripePaymentIntentID != nil && *p.StripePaymentIntentID != "" {
		if _, err := s.orders.UpdateOrderStripePaymentIntentID(ctx, tx, order.ID, *p.StripePaymentIntentID); err != nil {
			return nil, fmt.Errorf("set stripe payment intent id: %w", err)
		}
	}

	for _, item := range p.Items {
		lineSubtotal := item.UnitPrice * item.Quantity
		if _, err := s.orders.CreateLineItem(ctx, tx, store.CreateLineItemParams{
			OrderID:   order.ID,
			VariantID: item.VariantID,
			Quantity:  item.Quantity,
			UnitPrice: item.UnitPrice,
			Subtotal:  lineSubtotal,
			Total:     lineSubtotal,
		}); err != nil {
			return nil, fmt.Errorf("create line item: %w", err)
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
		Metadata:     map[string]any{"manual": true},
	}); err != nil {
		return nil, fmt.Errorf("audit manual order created: %w", err)
	}

	return order, nil
}

// generateOrderNumber creates a non-guessable order number using random bytes.
// Format: "ORD-XXXXXXXXXX" where X is uppercase alphanumeric (5 random bytes → 10 hex chars).
func generateOrderNumber() string {
	b := make([]byte, 5)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp if crypto/rand fails (extremely unlikely).
		return fmt.Sprintf("ORD-%d", time.Now().UnixMilli())
	}
	return "ORD-" + strings.ToUpper(hex.EncodeToString(b))
}
