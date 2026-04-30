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
	shipping      *store.ShippingStore
	payments      payments.Provider
	audit         *audit.AuditWriter
	metrics       *metrics.Registry
	enqueuer      JobEnqueuer // populated via WithJobEnqueuer; nil-safe (renewal still proceeds without it)
}

// NewRenewalService creates a new RenewalService.
func NewRenewalService(
	subscriptions *store.SubscriptionStore,
	orders *store.OrderStore,
	customers *store.CustomerStore,
	pricing *store.PricingStore,
	shipping *store.ShippingStore,
	payments payments.Provider,
	audit *audit.AuditWriter,
	metrics *metrics.Registry,
) *RenewalService {
	return &RenewalService{
		subscriptions: subscriptions,
		orders:        orders,
		customers:     customers,
		pricing:       pricing,
		shipping:      shipping,
		payments:      payments,
		audit:         audit,
		metrics:       metrics,
	}
}

// WithJobEnqueuer attaches the enqueuer used to fan out renewal-receipt and
// past-due notification emails atomically with renewal state changes.
// Wiring-time only.
func (s *RenewalService) WithJobEnqueuer(e JobEnqueuer) *RenewalService {
	s.enqueuer = e
	return s
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
	var shipMethod *domain.ShippingMethod

	err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		var txErr error
		sub, txErr = s.subscriptions.GetByIDAsStaff(ctx, tx, subscriptionID)
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

		addr, txErr = s.customers.GetAddressByIDAsStaff(ctx, tx, sub.ShippingAddressID)
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

		// Stamp the chosen local fulfillment method when the saved address
		// sits inside a local zip and the merchant's config plus the
		// customer's preference yield a single sensible choice. Otherwise
		// the renewal order ships normally.
		if s.shipping != nil {
			cfg, txErr := s.shipping.GetConfig(ctx, tx)
			if txErr == nil {
				shipMethod = pickRenewalLocalMethod(cfg, addr.PostalCode, customer.PreferredLocalFulfillment)
			}
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

	// Capture pre-renewal status so we only notify the customer on the
	// active → past_due transition (not on subsequent failed retries while
	// already past_due).
	wasActive := sub.Status == domain.SubscriptionStatusActive

	// --- Phase 2: create PaymentIntent (external call, outside tx) ---

	// Prefer the customer's default payment method (set when they update their
	// card via Stripe Billing Portal). Fall back to the first attached method
	// for legacy customers without a default. ListPaymentMethods returns every
	// attached PM regardless of type, which is required for Stripe Link users.
	paymentMethodID, err := s.pickRenewalPaymentMethod(ctx, *customer.StripeCustomerID)
	if err != nil {
		return nil, err
	}
	if paymentMethodID == "" {
		_ = store.Tx(ctx, pool, func(tx pgx.Tx) error {
			s.subscriptions.UpdateStatus(ctx, tx, sub.ID, domain.SubscriptionStatusPastDue) //nolint:errcheck
			if wasActive && s.enqueuer != nil {
				_ = s.enqueuer.EnqueuePastDueNotice(ctx, tx, sub.ID, customer.ID)
			}
			return nil
		})
		s.metrics.SubscriptionRenewals.WithLabelValues("failed").Inc()
		return nil, fmt.Errorf("customer %s has no saved payment methods", customer.ID)
	}

	pi, err := s.payments.CreatePaymentIntent(ctx, payments.CreatePaymentIntentRequest{
		AmountCents:     int64(totalCents),
		Currency:        "usd",
		CustomerID:      *customer.StripeCustomerID,
		PaymentMethodID: paymentMethodID,
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
			if wasActive && s.enqueuer != nil {
				_ = s.enqueuer.EnqueuePastDueNotice(ctx, tx, sub.ID, customer.ID)
			}
			return nil
		})
		s.metrics.SubscriptionRenewals.WithLabelValues("failed").Inc()
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
			ShippingMethod:    shipMethod,
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

		// Enqueue renewal-receipt email atomically with the order.
		if s.enqueuer != nil {
			if txErr = s.enqueuer.EnqueueRenewalReceipt(ctx, tx, order.ID, customer.ID); txErr != nil {
				return fmt.Errorf("enqueue renewal receipt: %w", txErr)
			}
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("renewal write phase: %w", err)
	}

	s.metrics.SubscriptionRenewals.WithLabelValues("success").Inc()
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
	var shipMethod *domain.ShippingMethod

	err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		var customerID uuid.UUID
		var addressID uuid.UUID

		for i, subID := range subscriptionIDs {
			sub, txErr := s.subscriptions.GetByIDAsStaff(ctx, tx, subID)
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

		addr, txErr = s.customers.GetAddressByIDAsStaff(ctx, tx, addressID)
		if txErr != nil {
			return fmt.Errorf("get address: %w", txErr)
		}

		if s.shipping != nil {
			cfg, txErr := s.shipping.GetConfig(ctx, tx)
			if txErr == nil {
				shipMethod = pickRenewalLocalMethod(cfg, addr.PostalCode, customer.PreferredLocalFulfillment)
			}
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

	paymentMethodID, err := s.pickRenewalPaymentMethod(ctx, *customer.StripeCustomerID)
	if err != nil {
		return nil, err
	}
	if paymentMethodID == "" {
		_ = store.Tx(ctx, pool, func(tx pgx.Tx) error {
			for _, item := range items {
				wasActive := item.Sub.Status == domain.SubscriptionStatusActive
				s.subscriptions.UpdateStatus(ctx, tx, item.Sub.ID, domain.SubscriptionStatusPastDue) //nolint:errcheck
				if wasActive && s.enqueuer != nil {
					_ = s.enqueuer.EnqueuePastDueNotice(ctx, tx, item.Sub.ID, customer.ID)
				}
			}
			return nil
		})
		s.metrics.SubscriptionRenewals.WithLabelValues("failed").Inc()
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
		PaymentMethodID: paymentMethodID,
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
				wasActive := item.Sub.Status == domain.SubscriptionStatusActive
				s.subscriptions.UpdateStatus(ctx, tx, item.Sub.ID, domain.SubscriptionStatusPastDue) //nolint:errcheck
				if wasActive && s.enqueuer != nil {
					_ = s.enqueuer.EnqueuePastDueNotice(ctx, tx, item.Sub.ID, customer.ID)
				}
			}
			return nil
		})
		s.metrics.SubscriptionRenewals.WithLabelValues("failed").Inc()
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
			ShippingMethod:    shipMethod,
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

		// Enqueue renewal-receipt email atomically with the order.
		if s.enqueuer != nil {
			if txErr = s.enqueuer.EnqueueRenewalReceipt(ctx, tx, order.ID, customer.ID); txErr != nil {
				return fmt.Errorf("enqueue renewal receipt: %w", txErr)
			}
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("batch renewal write phase: %w", err)
	}

	s.metrics.SubscriptionRenewals.WithLabelValues("success").Inc()
	return order, nil
}

func ptrVal(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// pickRenewalPaymentMethod resolves the Stripe payment method to charge for an
// off-session renewal. Returns the empty string when the customer has no usable
// method (caller marks the subscription past_due).
//
// Resolution order:
//  1. Customer's default_payment_method (set when they update their card via
//     Stripe Billing Portal — works for any PM type, including Stripe Link).
//  2. First attached payment method (legacy customers without a default set).
//
// Filtering by type=card was tried previously and broke Link customers — do
// not reintroduce it.
func (s *RenewalService) pickRenewalPaymentMethod(ctx context.Context, stripeCustomerID string) (string, error) {
	stripeCust, err := s.payments.GetCustomer(ctx, stripeCustomerID)
	if err != nil {
		return "", fmt.Errorf("get stripe customer: %w", err)
	}
	if stripeCust.DefaultPaymentMethodID != "" {
		return stripeCust.DefaultPaymentMethodID, nil
	}

	methods, err := s.payments.ListPaymentMethods(ctx, stripeCustomerID)
	if err != nil {
		return "", fmt.Errorf("list payment methods: %w", err)
	}
	if len(methods) == 0 {
		return "", nil
	}
	return methods[0].ID, nil
}

// pickRenewalLocalMethod returns the shipping method to stamp on a renewal
// order. Renewals do not surface UI to the customer, so the only signals are
// the merchant config and the customer's saved preference. Returns nil when
// the address is not local or when the merchant has both channels disabled —
// the order then falls back to the standard "shipped" flow.
func pickRenewalLocalMethod(cfg *domain.ShippingConfig, shipToZip string, preference *domain.ShippingMethod) *domain.ShippingMethod {
	eligible := cfg.EligibleLocalMethods(shipToZip)
	if len(eligible) == 0 {
		return nil
	}
	if preference != nil {
		for _, m := range eligible {
			if m == *preference {
				v := m
				return &v
			}
		}
	}
	if len(eligible) == 1 {
		v := eligible[0]
		return &v
	}
	// Both channels eligible but no preference saved — default to delivery
	// (the legacy behaviour before pickup existed) so subscriptions keep
	// shipping the way they always have until the customer opts in.
	for _, m := range eligible {
		if m == domain.ShippingMethodLocalDelivery {
			v := m
			return &v
		}
	}
	v := eligible[0]
	return &v
}
