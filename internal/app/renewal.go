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
	"github.com/dukerupert/hiri/internal/platform/tax"
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
	enqueuer      JobEnqueuer          // populated via WithJobEnqueuer; nil-safe (renewal still proceeds without it)
	settings      *store.SettingsStore // populated via WithTaxCalc; nil-safe (tax skipped without it)
	catalog       *store.CatalogStore  // populated via WithTaxCalc; needed to read per-product tax exemption
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

// WithTaxCalc attaches the stores needed to charge shipping and tax on renewal
// orders, matching retail checkout. Wiring-time only. When not set, renewals
// fall back to charging the bare line subtotal (the pre-P2 behaviour).
func (s *RenewalService) WithTaxCalc(settings *store.SettingsStore, catalog *store.CatalogStore) *RenewalService {
	s.settings = settings
	s.catalog = catalog
	return s
}

// renewalCharges computes the shipping and tax due on a renewal order, matching
// retail checkout: shipping is the merchant flat rate (free over threshold, free
// for local zips) on the order subtotal, tax runs the store's configured
// calculator over per-line taxable amounts honouring product + customer
// exemption. cfg is returned so the caller can resolve the local fulfilment
// method without re-reading config. Best-effort: a missing config or catalog
// lookup yields zero for that component rather than blocking the renewal.
func (s *RenewalService) renewalCharges(ctx context.Context, tx pgx.Tx, lines []domain.TaxLineItem, subtotalCents int, customer *domain.Customer, addr *domain.Address, grandfatheredShipping bool) (shipping, taxTotal int, cfg *domain.ShippingConfig) {
	if s.shipping != nil {
		if c, err := s.shipping.GetConfig(ctx, tx); err == nil {
			cfg = c
			// Grandfathered subscriptions keep free renewal shipping (migration
			// 054). cfg is still returned so the caller can resolve the local
			// fulfillment method — only the shipping cost is waived.
			if !grandfatheredShipping {
				shipping = cfg.Calculate(subtotalCents, addr.PostalCode)
			}
		}
	}

	if s.settings != nil {
		taxCfg, err := s.settings.GetTaxConfig(ctx, tx)
		if err == nil {
			isWholesale := customer.AccountType == domain.AccountTypeWholesale
			calculator := taxCalculatorForConfig(taxCfg, isWholesale)
			if res, cErr := calculator.Calculate(ctx, tax.TaxOrder{
				CustomerExempt: customer.TaxExempt,
				ShippingState:  addr.State,
				LineItems:      lines,
			}); cErr == nil {
				taxTotal = res.TaxTotal
			}
		}
	}

	return shipping, taxTotal, cfg
}

// taxExemptForVariant reports whether the variant's product is tax-exempt.
// Defaults to false (taxable) when catalog is unwired or the lookup fails —
// the safe direction for tax collection.
func (s *RenewalService) taxExemptForVariant(ctx context.Context, tx pgx.Tx, variantID uuid.UUID) bool {
	if s.catalog == nil {
		return false
	}
	variant, err := s.catalog.GetVariantByID(ctx, tx, variantID)
	if err != nil {
		return false
	}
	product, err := s.catalog.GetProductByID(ctx, tx, variant.ProductID)
	if err != nil {
		return false
	}
	return product.TaxExempt
}

// --- Dunning ---

// maxDunningAttempts is the number of charge attempts (including the first)
// before a past-due subscription is given up on and expired. With the retry
// delays below, the dunning window is roughly ten days.
const maxDunningAttempts = 4

// dunningRetryDelays are the gaps before each retry, indexed by the attempt
// number that just failed (1-based). After attempt 1 fails we wait 3 days,
// after attempt 2 another 3, after attempt 3 another 4 — then attempt 4's
// failure exhausts the schedule and the subscription expires. Length must be
// maxDunningAttempts-1.
var dunningRetryDelays = [maxDunningAttempts - 1]time.Duration{
	72 * time.Hour,
	72 * time.Hour,
	96 * time.Hour,
}

// dunningAttempt reads the running failed-charge count off a subscription's
// metadata. Absent (a never-failed subscription) reads as 0. JSON decoding
// yields float64 for numbers, so both float64 and int are tolerated.
func dunningAttempt(metadata map[string]any) int {
	if metadata == nil {
		return 0
	}
	switch v := metadata["dunning_attempt"].(type) {
	case float64:
		return int(v)
	case int:
		return v
	default:
		return 0
	}
}

// recordRenewalFailure advances dunning state after a declined charge, inside
// the caller's transaction. It increments the attempt count and either
// schedules the next retry (past_due, next_order_at pushed forward) or, at the
// cap, expires the subscription. The past-due notice email goes out on the
// first failure; the "subscription ended" email on expiry. Idempotent enough
// for River's at-least-once delivery: re-running bumps the attempt by one,
// which at worst shortens the dunning window slightly.
func (s *RenewalService) recordRenewalFailure(ctx context.Context, tx pgx.Tx, sub *domain.Subscription, customerID uuid.UUID) error {
	attempt := dunningAttempt(sub.Metadata) + 1

	if attempt >= maxDunningAttempts {
		if err := s.subscriptions.ExpireForDunning(ctx, tx, sub.ID); err != nil {
			return err
		}
		if s.enqueuer != nil {
			if err := s.enqueuer.EnqueueSubscriptionEnded(ctx, tx, sub.ID, customerID); err != nil {
				return fmt.Errorf("enqueue subscription-ended email: %w", err)
			}
		}
		if err := s.audit.Record(ctx, tx, audit.AuditEntry{
			ActorType:    domain.AuditActorTypeSystem,
			ActorName:    "subscription_renewal",
			Action:       audit.AuditSubscriptionExpired,
			ResourceType: "subscription",
			ResourceID:   sub.ID,
			Metadata:     map[string]any{"dunning_attempt": attempt, "reason": "dunning_exhausted"},
		}); err != nil {
			return fmt.Errorf("audit subscription expired: %w", err)
		}
		return nil
	}

	nextRetry := time.Now().Add(dunningRetryDelays[attempt-1])
	if err := s.subscriptions.SetDunningRetry(ctx, tx, sub.ID, nextRetry, attempt); err != nil {
		return err
	}
	// First failure only — re-notifying on every retry would be spam. The
	// final outcome (recovery or expiry) is what the customer hears next.
	if attempt == 1 && s.enqueuer != nil {
		if err := s.enqueuer.EnqueuePastDueNotice(ctx, tx, sub.ID, customerID); err != nil {
			return fmt.Errorf("enqueue past-due email: %w", err)
		}
	}
	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    domain.AuditActorTypeSystem,
		ActorName:    "subscription_renewal",
		Action:       audit.AuditSubscriptionFailed,
		ResourceType: "subscription",
		ResourceID:   sub.ID,
		Metadata:     map[string]any{"dunning_attempt": attempt, "next_retry_at": nextRetry.Format(time.RFC3339)},
	}); err != nil {
		return fmt.Errorf("audit renewal failed: %w", err)
	}
	return nil
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
	var subtotalCents int
	var shippingCents int
	var taxCents int
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
		subtotalCents = priceCents * sub.Quantity

		// Shipping + tax, matching retail checkout. The cfg returned by
		// renewalCharges also resolves the local fulfillment method (saved
		// address in a local zip + the customer's preference); otherwise the
		// renewal order ships normally.
		taxLine := []domain.TaxLineItem{{
			LineIndex: 0,
			Subtotal:  subtotalCents,
			TaxExempt: s.taxExemptForVariant(ctx, tx, sub.VariantID),
		}}
		var cfg *domain.ShippingConfig
		shippingCents, taxCents, cfg = s.renewalCharges(ctx, tx, taxLine, subtotalCents, customer, addr, sub.ShippingGrandfathered())
		if cfg != nil {
			shipMethod = pickRenewalLocalMethod(cfg, addr.PostalCode, customer.PreferredLocalFulfillment)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("renewal read phase: %w", err)
	}

	totalCents := subtotalCents + shippingCents + taxCents

	if customer.StripeCustomerID == nil {
		return nil, fmt.Errorf("customer %s has no Stripe customer ID", customer.ID)
	}

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
			return s.recordRenewalFailure(ctx, tx, sub, customer.ID)
		})
		s.metrics.SubscriptionRenewals.WithLabelValues("failed").Inc()
		return nil, fmt.Errorf("customer %s has no saved payment methods: %w", customer.ID, ErrRenewalPaymentDeclined)
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
		// Payment declined — advance dunning state (retry or expire).
		_ = store.Tx(ctx, pool, func(tx pgx.Tx) error {
			return s.recordRenewalFailure(ctx, tx, sub, customer.ID)
		})
		s.metrics.SubscriptionRenewals.WithLabelValues("failed").Inc()
		return nil, fmt.Errorf("create renewal payment intent: %w: %w", err, ErrRenewalPaymentDeclined)
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
			Subtotal:          subtotalCents,
			ShippingTotal:     shippingCents,
			TaxTotal:          taxCents,
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
			Subtotal:  subtotalCents,
			Total:     subtotalCents,
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

		// If subscription was past_due, restore to active and clear the
		// dunning counter so a future failure starts a fresh schedule.
		if sub.Status == domain.SubscriptionStatusPastDue {
			_, txErr = s.subscriptions.UpdateStatus(ctx, tx, sub.ID, domain.SubscriptionStatusActive)
			if txErr != nil {
				return fmt.Errorf("restore active: %w", txErr)
			}
			if txErr = s.subscriptions.ClearDunning(ctx, tx, sub.ID); txErr != nil {
				return fmt.Errorf("clear dunning: %w", txErr)
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
	var subtotalCents int
	var shippingCents int
	var taxCents int
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
			subtotalCents += totalPrice
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

		// Shipping (one charge on the combined subtotal — single box) + tax
		// (per line, honouring each product's exemption), matching retail.
		taxLines := make([]domain.TaxLineItem, len(items))
		// Grandfather the order's shipping only if every subscription in the
		// batch is grandfathered — a single post-P2 sub in the same box means
		// the order pays shipping (it's one charge per order, not per line).
		allGrandfathered := true
		for i, item := range items {
			taxLines[i] = domain.TaxLineItem{
				LineIndex: i,
				Subtotal:  item.TotalPrice,
				TaxExempt: s.taxExemptForVariant(ctx, tx, item.Sub.VariantID),
			}
			if !item.Sub.ShippingGrandfathered() {
				allGrandfathered = false
			}
		}
		var cfg *domain.ShippingConfig
		shippingCents, taxCents, cfg = s.renewalCharges(ctx, tx, taxLines, subtotalCents, customer, addr, allGrandfathered)
		if cfg != nil {
			shipMethod = pickRenewalLocalMethod(cfg, addr.PostalCode, customer.PreferredLocalFulfillment)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("batch renewal read phase: %w", err)
	}

	orderTotal = subtotalCents + shippingCents + taxCents

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
				if err := s.recordRenewalFailure(ctx, tx, item.Sub, customer.ID); err != nil {
					return err
				}
			}
			return nil
		})
		s.metrics.SubscriptionRenewals.WithLabelValues("failed").Inc()
		return nil, fmt.Errorf("customer %s has no saved payment methods: %w", customer.ID, ErrRenewalPaymentDeclined)
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
				if rfErr := s.recordRenewalFailure(ctx, tx, item.Sub, customer.ID); rfErr != nil {
					return rfErr
				}
			}
			return nil
		})
		s.metrics.SubscriptionRenewals.WithLabelValues("failed").Inc()
		return nil, fmt.Errorf("create batch renewal payment intent: %w: %w", err, ErrRenewalPaymentDeclined)
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
			Subtotal:          subtotalCents,
			ShippingTotal:     shippingCents,
			TaxTotal:          taxCents,
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

			// Restore past_due subscriptions to active and clear dunning state.
			if item.Sub.Status == domain.SubscriptionStatusPastDue {
				_, txErr = s.subscriptions.UpdateStatus(ctx, tx, item.Sub.ID, domain.SubscriptionStatusActive)
				if txErr != nil {
					return fmt.Errorf("restore active for %s: %w", item.Sub.ID, txErr)
				}
				if txErr = s.subscriptions.ClearDunning(ctx, tx, item.Sub.ID); txErr != nil {
					return fmt.Errorf("clear dunning for %s: %w", item.Sub.ID, txErr)
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
