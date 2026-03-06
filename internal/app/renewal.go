package app

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/platform/payments"
	"github.com/dukerupert/hiri/internal/store"
)

// RenewalService orchestrates subscription renewal: create payment, place order, advance period.
type RenewalService struct {
	subscriptions *store.SubscriptionStore
	orders        *store.OrderStore
	customers     *store.CustomerStore
	pricing       *store.PricingStore
	payments      payments.Provider
	audit         *audit.AuditWriter
	metrics       *metrics.Registry
}

// NewRenewalService creates a new RenewalService.
func NewRenewalService(
	subscriptions *store.SubscriptionStore,
	orders *store.OrderStore,
	customers *store.CustomerStore,
	pricing *store.PricingStore,
	payments payments.Provider,
	audit *audit.AuditWriter,
	metrics *metrics.Registry,
) *RenewalService {
	return &RenewalService{
		subscriptions: subscriptions,
		orders:        orders,
		customers:     customers,
		pricing:       pricing,
		payments:      payments,
		audit:         audit,
		metrics:       metrics,
	}
}

// RenewSubscription processes a single subscription renewal:
// 1. Load subscription + plan + customer + address + price
// 2. Create off-session PaymentIntent via Stripe (external, outside tx)
// 3. Place order, link to subscription, advance billing period (all in one tx)
//
// Returns the created order on success. On payment failure, marks the subscription
// past_due and returns an error.
func (s *RenewalService) RenewSubscription(ctx context.Context, pool *pgxpool.Pool, subscriptionID uuid.UUID) (*domain.Order, error) {
	// --- Phase 1: read data (in a read-only tx) ---
	var sub *domain.Subscription
	var plan *domain.SubscriptionPlan
	var customer *domain.Customer
	var addr *domain.Address
	var priceCents int

	err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		var txErr error
		sub, txErr = s.subscriptions.GetByID(ctx, tx, subscriptionID)
		if txErr != nil {
			return fmt.Errorf("get subscription: %w", txErr)
		}

		if sub.Status != domain.SubscriptionStatusActive && sub.Status != domain.SubscriptionStatusPastDue {
			return ErrSubscriptionNotActive
		}

		plan, txErr = s.subscriptions.GetPlanByID(ctx, tx, sub.PlanID)
		if txErr != nil {
			return fmt.Errorf("get plan: %w", txErr)
		}

		customer, txErr = s.customers.GetByID(ctx, tx, sub.CustomerID)
		if txErr != nil {
			return fmt.Errorf("get customer: %w", txErr)
		}

		addr, txErr = s.customers.GetAddressByID(ctx, tx, sub.ShippingAddressID)
		if txErr != nil {
			return fmt.Errorf("get address: %w", txErr)
		}

		price, txErr := s.pricing.GetBasePrice(ctx, tx, sub.VariantID, "USD")
		if txErr != nil {
			return fmt.Errorf("get variant price: %w", txErr)
		}
		priceCents = price.Amount
		if plan.DiscountPct > 0 {
			priceCents = priceCents - (priceCents * plan.DiscountPct / 100)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("renewal read phase: %w", err)
	}

	totalCents := priceCents * sub.Quantity

	if customer.StripeCustomerID == nil {
		return nil, fmt.Errorf("customer %s has no Stripe customer ID", customer.ID)
	}

	// --- Phase 2: create PaymentIntent (external call, outside tx) ---

	// Look up saved payment methods for off-session charging
	methods, err := s.payments.ListPaymentMethods(ctx, *customer.StripeCustomerID)
	if err != nil {
		return nil, fmt.Errorf("list payment methods: %w", err)
	}
	if len(methods) == 0 {
		_ = store.Tx(ctx, pool, func(tx pgx.Tx) error {
			s.subscriptions.UpdateStatus(ctx, tx, sub.ID, domain.SubscriptionStatusPastDue) //nolint:errcheck
			return nil
		})
		s.metrics.SubscriptionRenewalFailuresTotal.WithLabelValues("no_payment_method").Inc()
		return nil, fmt.Errorf("customer %s has no saved payment methods", customer.ID)
	}

	pi, err := s.payments.CreatePaymentIntent(ctx, payments.CreatePaymentIntentRequest{
		AmountCents:     int64(totalCents),
		Currency:        "usd",
		CustomerID:      *customer.StripeCustomerID,
		PaymentMethodID: methods[0].ID,
		OffSession:      true,
		Metadata: map[string]string{
			"subscription_id": sub.ID.String(),
			"customer_id":     customer.ID.String(),
		},
		ShippingAddress: &payments.ShippingAddress{
			Name:       addr.FirstName + " " + addr.LastName,
			Line1:      addr.Line1,
			Line2:      ptrVal(addr.Line2),
			City:       addr.City,
			State:      addr.State,
			PostalCode: addr.PostalCode,
			Country:    addr.CountryCode,
		},
	})
	if err != nil {
		// Payment failed — mark subscription past_due
		_ = store.Tx(ctx, pool, func(tx pgx.Tx) error {
			s.subscriptions.UpdateStatus(ctx, tx, sub.ID, domain.SubscriptionStatusPastDue) //nolint:errcheck
			return nil
		})
		s.metrics.SubscriptionRenewalFailuresTotal.WithLabelValues("payment_failed").Inc()
		return nil, fmt.Errorf("create renewal payment intent: %w", err)
	}

	// --- Phase 3: create order + link + advance period (single tx) ---
	var order *domain.Order

	err = store.Tx(ctx, pool, func(tx pgx.Tx) error {
		orderNumber := fmt.Sprintf("SUB-%d", time.Now().UnixMilli())
		customerID := customer.ID
		subID := sub.ID

		var txErr error
		order, txErr = s.orders.CreateOrder(ctx, tx, store.CreateOrderParams{
			Number:            orderNumber,
			CustomerID:        &customerID,
			Status:            domain.OrderStatusConfirmed,
			PaymentStatus:     domain.PaymentStatusCaptured,
			FulfillmentStatus: domain.FulfillmentStatusUnfulfilled,
			CurrencyCode:      "USD",
			Subtotal:          totalCents,
			Total:             totalCents,
			ShippingAddressID: sub.ShippingAddressID,
			BillingAddressID:  sub.ShippingAddressID,
			SubscriptionID:    &subID,
			PlacedAt:          time.Now(),
			Metadata: map[string]any{
				"subscription_renewal": true,
				"period_start":         sub.CurrentPeriodEnd.Format(time.RFC3339),
			},
		})
		if txErr != nil {
			return fmt.Errorf("create renewal order: %w", txErr)
		}

		// Create line item
		_, txErr = s.orders.CreateLineItem(ctx, tx, store.CreateLineItemParams{
			OrderID:   order.ID,
			VariantID: sub.VariantID,
			Quantity:  sub.Quantity,
			UnitPrice: priceCents,
			Subtotal:  totalCents,
			Total:     totalCents,
		})
		if txErr != nil {
			return fmt.Errorf("create renewal line item: %w", txErr)
		}

		// Store Stripe PI ID
		_, txErr = s.orders.UpdateOrderStripePaymentIntentID(ctx, tx, order.ID, pi.ID)
		if txErr != nil {
			return fmt.Errorf("set stripe payment intent: %w", txErr)
		}

		// Link order to subscription
		newStart := sub.CurrentPeriodEnd
		newEnd := nextPeriodEnd(newStart, plan.Interval, plan.IntervalCount)

		txErr = s.subscriptions.CreateSubscriptionOrder(ctx, tx, sub.ID, order.ID, newStart, newEnd)
		if txErr != nil {
			return fmt.Errorf("link subscription order: %w", txErr)
		}

		// Advance billing period
		txErr = s.subscriptions.UpdatePeriod(ctx, tx, sub.ID, newStart, newEnd, newEnd)
		if txErr != nil {
			return fmt.Errorf("advance period: %w", txErr)
		}

		// If subscription was past_due, restore to active
		if sub.Status == domain.SubscriptionStatusPastDue {
			_, txErr = s.subscriptions.UpdateStatus(ctx, tx, sub.ID, domain.SubscriptionStatusActive)
			if txErr != nil {
				return fmt.Errorf("restore active: %w", txErr)
			}
		}

		// Audit
		if txErr = s.audit.Record(ctx, tx, audit.AuditEntry{
			ActorType:    domain.AuditActorTypeSystem,
			ActorName:    "subscription_renewal",
			Action:       audit.AuditSubscriptionRenewed,
			ResourceType: "subscription",
			ResourceID:   sub.ID,
			After:        order,
		}); txErr != nil {
			return fmt.Errorf("audit renewal: %w", txErr)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("renewal write phase: %w", err)
	}

	s.metrics.SubscriptionRenewalsTotal.WithLabelValues("success").Inc()
	return order, nil
}

// subscriptionLineItem holds computed pricing for one subscription in a batch.
type subscriptionLineItem struct {
	Sub        *domain.Subscription
	Plan       *domain.SubscriptionPlan
	UnitPrice  int // per-unit price after discount
	TotalPrice int // unit_price * quantity
}

// RenewBatch processes multiple subscriptions for the same customer as a single order.
// All subscriptions must belong to the same customer and shipping address.
// Returns the created order on success.
func (s *RenewalService) RenewBatch(ctx context.Context, pool *pgxpool.Pool, subscriptionIDs []uuid.UUID) (*domain.Order, error) {
	if len(subscriptionIDs) == 0 {
		return nil, fmt.Errorf("no subscription IDs provided")
	}

	// For single-subscription batches, use the existing path
	if len(subscriptionIDs) == 1 {
		return s.RenewSubscription(ctx, pool, subscriptionIDs[0])
	}

	// --- Phase 1: read data (in a read-only tx) ---
	var items []subscriptionLineItem
	var customer *domain.Customer
	var addr *domain.Address
	var orderTotal int

	err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		var customerID uuid.UUID
		var addressID uuid.UUID

		for i, subID := range subscriptionIDs {
			sub, txErr := s.subscriptions.GetByID(ctx, tx, subID)
			if txErr != nil {
				return fmt.Errorf("get subscription %s: %w", subID, txErr)
			}

			if sub.Status != domain.SubscriptionStatusActive && sub.Status != domain.SubscriptionStatusPastDue {
				return fmt.Errorf("subscription %s: %w", subID, ErrSubscriptionNotActive)
			}

			// Verify all subscriptions belong to the same customer and address
			if i == 0 {
				customerID = sub.CustomerID
				addressID = sub.ShippingAddressID
			} else {
				if sub.CustomerID != customerID {
					return fmt.Errorf("subscription %s belongs to different customer", subID)
				}
				if sub.ShippingAddressID != addressID {
					return fmt.Errorf("subscription %s has different shipping address", subID)
				}
			}

			plan, txErr := s.subscriptions.GetPlanByID(ctx, tx, sub.PlanID)
			if txErr != nil {
				return fmt.Errorf("get plan for %s: %w", subID, txErr)
			}

			price, txErr := s.pricing.GetBasePrice(ctx, tx, sub.VariantID, "USD")
			if txErr != nil {
				return fmt.Errorf("get price for %s: %w", subID, txErr)
			}

			unitPrice := price.Amount
			if plan.DiscountPct > 0 {
				unitPrice = unitPrice - (unitPrice * plan.DiscountPct / 100)
			}
			totalPrice := unitPrice * sub.Quantity

			items = append(items, subscriptionLineItem{
				Sub:        sub,
				Plan:       plan,
				UnitPrice:  unitPrice,
				TotalPrice: totalPrice,
			})
			orderTotal += totalPrice
		}

		var txErr error
		customer, txErr = s.customers.GetByID(ctx, tx, customerID)
		if txErr != nil {
			return fmt.Errorf("get customer: %w", txErr)
		}

		addr, txErr = s.customers.GetAddressByID(ctx, tx, addressID)
		if txErr != nil {
			return fmt.Errorf("get address: %w", txErr)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("batch renewal read phase: %w", err)
	}

	if customer.StripeCustomerID == nil {
		return nil, fmt.Errorf("customer %s has no Stripe customer ID", customer.ID)
	}

	// --- Phase 2: create PaymentIntent (external call, outside tx) ---

	methods, err := s.payments.ListPaymentMethods(ctx, *customer.StripeCustomerID)
	if err != nil {
		return nil, fmt.Errorf("list payment methods: %w", err)
	}
	if len(methods) == 0 {
		_ = store.Tx(ctx, pool, func(tx pgx.Tx) error {
			for _, item := range items {
				s.subscriptions.UpdateStatus(ctx, tx, item.Sub.ID, domain.SubscriptionStatusPastDue) //nolint:errcheck
			}
			return nil
		})
		s.metrics.SubscriptionRenewalFailuresTotal.WithLabelValues("no_payment_method").Inc()
		return nil, fmt.Errorf("customer %s has no saved payment methods", customer.ID)
	}

	// Build subscription ID list for metadata
	subIDStrs := make([]string, len(items))
	for i, item := range items {
		subIDStrs[i] = item.Sub.ID.String()
	}

	pi, err := s.payments.CreatePaymentIntent(ctx, payments.CreatePaymentIntentRequest{
		AmountCents:     int64(orderTotal),
		Currency:        "usd",
		CustomerID:      *customer.StripeCustomerID,
		PaymentMethodID: methods[0].ID,
		OffSession:      true,
		Metadata: map[string]string{
			"batch_renewal": "true",
			"customer_id":   customer.ID.String(),
		},
		ShippingAddress: &payments.ShippingAddress{
			Name:       addr.FirstName + " " + addr.LastName,
			Line1:      addr.Line1,
			Line2:      ptrVal(addr.Line2),
			City:       addr.City,
			State:      addr.State,
			PostalCode: addr.PostalCode,
			Country:    addr.CountryCode,
		},
	})
	if err != nil {
		_ = store.Tx(ctx, pool, func(tx pgx.Tx) error {
			for _, item := range items {
				s.subscriptions.UpdateStatus(ctx, tx, item.Sub.ID, domain.SubscriptionStatusPastDue) //nolint:errcheck
			}
			return nil
		})
		s.metrics.SubscriptionRenewalFailuresTotal.WithLabelValues("payment_failed").Inc()
		return nil, fmt.Errorf("create batch renewal payment intent: %w", err)
	}

	// --- Phase 3: create order + link + advance periods (single tx) ---
	var order *domain.Order

	err = store.Tx(ctx, pool, func(tx pgx.Tx) error {
		orderNumber := fmt.Sprintf("SUB-%d", time.Now().UnixMilli())
		customerID := customer.ID

		var txErr error
		order, txErr = s.orders.CreateOrder(ctx, tx, store.CreateOrderParams{
			Number:            orderNumber,
			CustomerID:        &customerID,
			Status:            domain.OrderStatusConfirmed,
			PaymentStatus:     domain.PaymentStatusCaptured,
			FulfillmentStatus: domain.FulfillmentStatusUnfulfilled,
			CurrencyCode:      "USD",
			Subtotal:          orderTotal,
			Total:             orderTotal,
			ShippingAddressID: addr.ID,
			BillingAddressID:  addr.ID,
			SubscriptionID:    nil, // batched — use subscription_orders for linking
			PlacedAt:          time.Now(),
			Metadata: map[string]any{
				"subscription_renewal": true,
				"batch_size":           len(items),
			},
		})
		if txErr != nil {
			return fmt.Errorf("create batch renewal order: %w", txErr)
		}

		// Create line items and advance each subscription
		for _, item := range items {
			_, txErr = s.orders.CreateLineItem(ctx, tx, store.CreateLineItemParams{
				OrderID:   order.ID,
				VariantID: item.Sub.VariantID,
				Quantity:  item.Sub.Quantity,
				UnitPrice: item.UnitPrice,
				Subtotal:  item.TotalPrice,
				Total:     item.TotalPrice,
			})
			if txErr != nil {
				return fmt.Errorf("create line item for %s: %w", item.Sub.ID, txErr)
			}

			// Link order to subscription
			newStart := item.Sub.CurrentPeriodEnd
			newEnd := nextPeriodEnd(newStart, item.Plan.Interval, item.Plan.IntervalCount)

			txErr = s.subscriptions.CreateSubscriptionOrder(ctx, tx, item.Sub.ID, order.ID, newStart, newEnd)
			if txErr != nil {
				return fmt.Errorf("link subscription order %s: %w", item.Sub.ID, txErr)
			}

			// Advance billing period
			txErr = s.subscriptions.UpdatePeriod(ctx, tx, item.Sub.ID, newStart, newEnd, newEnd)
			if txErr != nil {
				return fmt.Errorf("advance period for %s: %w", item.Sub.ID, txErr)
			}

			// Restore past_due subscriptions to active
			if item.Sub.Status == domain.SubscriptionStatusPastDue {
				_, txErr = s.subscriptions.UpdateStatus(ctx, tx, item.Sub.ID, domain.SubscriptionStatusActive)
				if txErr != nil {
					return fmt.Errorf("restore active for %s: %w", item.Sub.ID, txErr)
				}
			}
		}

		// Store Stripe PI ID
		_, txErr = s.orders.UpdateOrderStripePaymentIntentID(ctx, tx, order.ID, pi.ID)
		if txErr != nil {
			return fmt.Errorf("set stripe payment intent: %w", txErr)
		}

		// Audit
		if txErr = s.audit.Record(ctx, tx, audit.AuditEntry{
			ActorType:    domain.AuditActorTypeSystem,
			ActorName:    "subscription_renewal",
			Action:       audit.AuditSubscriptionRenewed,
			ResourceType: "order",
			ResourceID:   order.ID,
			After:        order,
			Metadata: map[string]any{
				"batch_size":       len(items),
				"subscription_ids": subIDStrs,
			},
		}); txErr != nil {
			return fmt.Errorf("audit batch renewal: %w", txErr)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("batch renewal write phase: %w", err)
	}

	s.metrics.SubscriptionRenewalsTotal.WithLabelValues("success").Inc()
	return order, nil
}

func ptrVal(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
