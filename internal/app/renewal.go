package app

import (
	"context"
	"errors"
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
	renewalLoc    *time.Location       // populated via WithRenewalAnchor; nil disables renewal-time anchoring
	renewalHour   int                  // hour-of-day (0–23) in renewalLoc that renewals fire at

	// merchantTZ is the zone the local-delivery cutoff is judged in. Kept
	// separate from renewalLoc deliberately: the anchor governs when renewals
	// fire and could be retuned for billing reasons, while this governs what
	// day a customer is told their coffee arrives. They happen to be the same
	// zone today, and neither should silently move the other.
	merchantTZ *time.Location
}

// WithMerchantTZ sets the zone used to resolve local-delivery dates on renewal
// orders against the order-by cutoff.
func (s *RenewalService) WithMerchantTZ(loc *time.Location) *RenewalService {
	s.merchantTZ = loc
	return s
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

// WithRenewalAnchor configures renewals (and dunning retries) to fire at hour:00
// in loc, so the day's charges and the orders they produce batch into one
// pre-dawn window for morning fulfillment. Wiring-time only; when unset,
// next_order_at keeps the raw period-end / retry instant. See anchorRenewalTime.
func (s *RenewalService) WithRenewalAnchor(loc *time.Location, hour int) *RenewalService {
	s.renewalLoc = loc
	s.renewalHour = hour
	return s
}

// anchorRenewal snaps a next_order_at to the configured renewal window. A no-op
// when no anchor is wired.
func (s *RenewalService) anchorRenewal(t time.Time) time.Time {
	return anchorRenewalTime(t, s.renewalLoc, s.renewalHour)
}

// renewalCharges computes the shipping and tax due on a renewal order and
// resolves the local fulfilment method to stamp on it, matching retail
// checkout. The method is resolved first (saved address in a local zip + the
// customer's preference), then shipping is priced off that method: local
// delivery and pickup are free, while an explicit "mail it to me" preference
// pays the standard carrier rate even from a local zip. Tax runs the store's
// configured calculator over per-line taxable amounts honouring product +
// customer exemption. Grandfathered subscriptions keep free renewal shipping
// (migration 054) but still get a resolved method. Best-effort: a missing
// config or catalog lookup yields zero/nil for that component rather than
// blocking the renewal.
func (s *RenewalService) renewalCharges(ctx context.Context, tx pgx.Tx, lines []domain.TaxLineItem, subtotalCents int, customer *domain.Customer, addr *domain.Address, grandfatheredShipping bool) (shipping, taxTotal int, shipMethod *domain.ShippingMethod) {
	if s.shipping != nil {
		if cfg, err := s.shipping.GetConfig(ctx, tx); err == nil {
			shipMethod = pickRenewalLocalMethod(cfg, addr.PostalCode, customer.PreferredLocalFulfillment)
			if !grandfatheredShipping {
				shipping = cfg.CalculateForMethod(subtotalCents, addr.PostalCode, shipMethod)
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

	return shipping, taxTotal, shipMethod
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

// dunningStage is one rung of the past-due ladder: how long to wait before the
// next charge attempt, and which email (if any) goes out when this attempt
// fails.
//
// Keeping the delay and the copy in one table is deliberate — they only make
// sense together, and the previous split between a length constant and a delay
// array made it easy to change one without the other.
type dunningStage struct {
	// wait is the gap before the next charge attempt.
	wait time.Duration
	// emailStage selects the customer email sent when this attempt fails.
	// Zero means stay quiet — see the silent rung below.
	emailStage int
}

// dunningLadder is the schedule a subscription walks after its first declined
// renewal. Index i is the stage entered when attempt i+1 fails; running off the
// end expires the subscription.
//
//	attempt 1 fails  day 0   → "we couldn't charge your card", retry in 3d
//	attempt 2 fails  day 3   → silent, retry in 5d
//	attempt 3 fails  day 8   → "still no luck, your shipment is on hold", retry in 4d
//	attempt 4 fails  day 12  → "last call, we end this in two days", retry in 2d
//	attempt 5 fails  day 14  → expire, "your subscription has ended"
//
// Two weeks, four attempts after the first, three emails. The shape follows
// what the payments industry has converged on: spacing retries across a couple
// of weeks recovers more than clustering them, and recovery depends far more on
// the customer seeing a message than on the number of charge attempts. The
// earlier ladder ran four attempts in ten days on a single email, which meant
// most subscriptions died before their owner had read anything.
//
// Attempt 2 is silent on purpose. Two emails in three days reads as dunning
// spam and costs more in unsubscribes than it recovers.
//
// Five attempts total sits well inside the card networks' retry ceilings
// (Mastercard is the tighter of the two at 10 per 30 days).
var dunningLadder = [...]dunningStage{
	{wait: 72 * time.Hour, emailStage: dunningEmailFirst},
	{wait: 120 * time.Hour, emailStage: dunningEmailSilent},
	{wait: 96 * time.Hour, emailStage: dunningEmailReminder},
	{wait: 48 * time.Hour, emailStage: dunningEmailFinal},
}

// Email stages for the ladder above. These travel in the job args and pick the
// template, so the numbers are persisted — append, never renumber.
const (
	dunningEmailSilent   = 0
	dunningEmailFirst    = 1
	dunningEmailReminder = 2
	dunningEmailFinal    = 3
)

// MaxDunningAttempts is the number of charge attempts (including the first)
// before a past-due subscription is given up on and expired.
const MaxDunningAttempts = len(dunningLadder) + 1

// The ui layer may only import domain, so the attempt cap is mirrored there for
// the admin's "attempt N of M". This fails the build if the two drift — adding
// a rung to the ladder without updating domain would silently make every admin
// page lie about how much runway a customer has left.
var _ = [1]struct{}{}[MaxDunningAttempts-domain.SubscriptionMaxDunningAttempts]

// DunningExpiresAt projects the day a past-due subscription will be given up on,
// so the customer-facing emails can name it. Derived from where the
// subscription currently sits on the ladder: next_order_at is the next attempt,
// and every remaining rung's wait stacks on top of it.
//
// Returns next_order_at unchanged for a subscription that is not mid-ladder —
// on the last rung that is already the expiry date, and for anything else the
// question doesn't apply.
func DunningExpiresAt(sub *domain.Subscription) time.Time {
	attempt := sub.DunningAttempt()
	if attempt <= 0 || attempt >= MaxDunningAttempts {
		return sub.NextOrderAt
	}
	expiry := sub.NextOrderAt
	for i := attempt; i < len(dunningLadder); i++ {
		expiry = expiry.Add(dunningLadder[i].wait)
	}
	return expiry
}

// recordRenewalFailure advances dunning state after a failed charge, inside the
// caller's transaction. It increments the attempt count and either schedules the
// next attempt (past_due, next_order_at pushed forward) or, at the end of the
// ladder, expires the subscription. Emails are enqueued per the ladder's stage.
//
// cause is the error the charge returned, and may be nil when there was nothing
// to charge (no saved payment method). A *payments.DeclineError reporting a hard
// decline latches metaDunningHardDecline: the remaining rungs still run their
// clock and their emails, but no further charge is attempted. Retrying a card
// the issuer has blocked cannot succeed, and the networks fine per attempt — but
// the customer can still rescue the subscription by putting a different card on
// file, which is exactly what the remaining emails ask for.
//
// Idempotent enough for River's at-least-once delivery: re-running bumps the
// attempt by one, which at worst shortens the dunning window slightly.
func (s *RenewalService) recordRenewalFailure(ctx context.Context, tx pgx.Tx, sub *domain.Subscription, customerID uuid.UUID, paymentMethodID string, cause error) error {
	attempt := sub.DunningAttempt() + 1

	// Latch the hard-decline verdict. Once true it stays true for the rest of
	// the ladder — a later rung sends email without charging, so it has no new
	// decline to re-derive this from.
	hardDecline := sub.DunningHardDeclined()
	declineCode := ""
	var declineErr *payments.DeclineError
	if errors.As(cause, &declineErr) {
		declineCode = declineErr.DeclineCode
		if declineErr.Permanent() {
			hardDecline = true
		}
	}

	auditMeta := map[string]any{
		"dunning_attempt": attempt,
		"hard_decline":    hardDecline,
	}
	if declineCode != "" {
		auditMeta["decline_code"] = declineCode
	}

	if attempt >= MaxDunningAttempts {
		if err := s.subscriptions.ExpireForDunning(ctx, tx, sub.ID); err != nil {
			return err
		}
		if s.enqueuer != nil {
			if err := s.enqueuer.EnqueueSubscriptionEnded(ctx, tx, sub.ID, customerID); err != nil {
				return fmt.Errorf("enqueue subscription-ended email: %w", err)
			}
		}
		auditMeta["reason"] = "dunning_exhausted"
		if err := s.audit.Record(ctx, tx, audit.AuditEntry{
			ActorType:    domain.AuditActorTypeSystem,
			ActorName:    "subscription_renewal",
			Action:       audit.AuditSubscriptionExpired,
			ResourceType: "subscription",
			ResourceID:   sub.ID,
			Metadata:     auditMeta,
		}); err != nil {
			return fmt.Errorf("audit subscription expired: %w", err)
		}
		return nil
	}

	stage := dunningLadder[attempt-1]

	// Anchor the retry to the renewal window too: a recovered charge produces a
	// fulfillment order, and we want that in the morning batch like any renewal.
	// Anchoring is forward-only, so the retry is never sooner than the dunning gap.
	nextRetry := s.anchorRenewal(time.Now().Add(stage.wait))
	if err := s.subscriptions.SetDunningRetry(ctx, tx, sub.ID, nextRetry, attempt); err != nil {
		return err
	}
	if hardDecline {
		// Record which card died alongside the latch. A later attempt compares
		// the card on file against this one, so a customer who adds a different
		// card is charged again instead of being stuck behind the latch.
		deadPM := paymentMethodID
		if deadPM == "" {
			deadPM = sub.DunningDeadPaymentMethod()
		}
		// Later rungs do not charge, so they have no decline to report. Carry
		// the original reason forward rather than overwriting it with blank —
		// staff read it off the subscription page, and it is the only record of
		// *why* we stopped trying.
		code := declineCode
		if code == "" {
			code = sub.DunningDeclineCode()
		}
		if err := s.subscriptions.SetDunningHardDecline(ctx, tx, sub.ID, code, deadPM); err != nil {
			return fmt.Errorf("flag hard decline: %w", err)
		}
	}
	if stage.emailStage != dunningEmailSilent && s.enqueuer != nil {
		if err := s.enqueuer.EnqueuePastDueNotice(ctx, tx, sub.ID, customerID, stage.emailStage); err != nil {
			return fmt.Errorf("enqueue past-due email: %w", err)
		}
	}
	auditMeta["next_retry_at"] = nextRetry.Format(time.RFC3339)
	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    domain.AuditActorTypeSystem,
		ActorName:    "subscription_renewal",
		Action:       audit.AuditSubscriptionFailed,
		ResourceType: "subscription",
		ResourceID:   sub.ID,
		Metadata:     auditMeta,
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

		// Shipping + tax + resolved fulfillment method, matching retail
		// checkout. renewalCharges resolves the method (saved address in a
		// local zip + the customer's preference) and prices shipping off it;
		// otherwise the renewal order ships normally.
		taxLine := []domain.TaxLineItem{{
			LineIndex: 0,
			Subtotal:  subtotalCents,
			TaxExempt: s.taxExemptForVariant(ctx, tx, sub.VariantID),
		}}
		shippingCents, taxCents, shipMethod = s.renewalCharges(ctx, tx, taxLine, subtotalCents, customer, addr, sub.ShippingGrandfathered())

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
	paymentMethodID, err := s.pickRenewalPaymentMethod(ctx, *customer.StripeCustomerID, sub.DunningDeadPaymentMethod())
	if err != nil {
		return nil, err
	}
	if paymentMethodID == "" {
		_ = store.Tx(ctx, pool, func(tx pgx.Tx) error {
			return s.recordRenewalFailure(ctx, tx, sub, customer.ID, "", nil)
		})
		s.metrics.SubscriptionRenewals.WithLabelValues("failed").Inc()
		return nil, fmt.Errorf("customer %s has no saved payment methods: %w", customer.ID, ErrRenewalPaymentDeclined)
	}

	// A card the issuer permanently blocked must never be charged again — the
	// networks fine per attempt, and retrying a dead number sours the issuer on
	// this customer's next legitimate charge. So walk the rest of the ladder
	// without touching Stripe: the emails still go out and the deadline still
	// runs, which is what gives the customer a chance to fix it.
	//
	// The gate has to sit *after* the payment method is resolved, because the
	// method is what releases it. Checking the latch alone before this point
	// would make it permanent: no charge attempted means no charge can succeed,
	// and success is the only thing that calls ClearDunning.
	if sub.DunningChargeBlocked(paymentMethodID) {
		_ = store.Tx(ctx, pool, func(tx pgx.Tx) error {
			return s.recordRenewalFailure(ctx, tx, sub, customer.ID, paymentMethodID, nil)
		})
		s.metrics.SubscriptionRenewals.WithLabelValues("failed").Inc()
		return nil, fmt.Errorf("subscription %s card hard-declined: %w", sub.ID, ErrRenewalPaymentDeclined)
	}

	// A different card than the one that died: drop the latch before charging so
	// a decline on the new card starts its own verdict rather than inheriting
	// the old one. If this card is dead too, the charge below re-latches it
	// against the new number.
	if sub.DunningHardDeclined() {
		if clearErr := store.Tx(ctx, pool, func(tx pgx.Tx) error {
			return s.subscriptions.ClearDunningHardDecline(ctx, tx, sub.ID)
		}); clearErr != nil {
			return nil, fmt.Errorf("clear hard-decline latch: %w", clearErr)
		}
		// The in-memory copy has to drop it too. recordRenewalFailure below
		// reads the latch back off this struct, so leaving it set here would
		// re-latch the *replacement* card on an ordinary soft decline —
		// insufficient funds would be recorded as "the bank blocked this card
		// for good" and no further attempt would be made.
		sub.ClearDunningHardDeclineMeta()
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
		// Payment declined — advance dunning state (retry or expire). err
		// carries the decline code, which decides whether we ever charge this
		// card again.
		_ = store.Tx(ctx, pool, func(tx pgx.Tx) error {
			return s.recordRenewalFailure(ctx, tx, sub, customer.ID, paymentMethodID, err)
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
		renewalPlacedAt := time.Now()

		var txErr error
		order, txErr = s.orders.CreateOrder(ctx, tx, store.CreateOrderParams{
			Number:                orderNumber,
			CustomerID:            &customerID,
			Status:                domain.OrderStatusConfirmed,
			PaymentStatus:         domain.PaymentStatusCaptured,
			FulfillmentStatus:     domain.FulfillmentStatusUnfulfilled,
			CurrencyCode:          "USD",
			Subtotal:              subtotalCents,
			ShippingTotal:         shippingCents,
			TaxTotal:              taxCents,
			Total:                 totalCents,
			ShippingAddressID:     sub.ShippingAddressID,
			BillingAddressID:      sub.ShippingAddressID,
			SubscriptionID:        &subID,
			ShippingMethod:        shipMethod,
			ScheduledDeliveryDate: scheduleLocalDelivery(ctx, tx, s.shipping, shipMethod, renewalPlacedAt, s.merchantTZ),
			PlacedAt:              renewalPlacedAt,
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

		// Advance billing period. The period boundary stays exact; only the
		// charge trigger is anchored to the renewal window so the next order
		// lands pre-dawn for morning fulfillment.
		txErr = s.subscriptions.UpdatePeriod(ctx, tx, sub.ID, newStart, newEnd, s.anchorRenewal(newEnd))
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

			// Hard-declined subscriptions do not belong in a batch: whether their
			// latch has been released depends on which card is on file, and that
			// is a Stripe call we cannot make inside this transaction. The
			// scheduler routes them to individual renewals for exactly that
			// reason (see jobs/renewal_scheduler.go), so reaching here means one
			// latched between being scheduled and being run.
			//
			// Drop it from the batch rather than let one dead card fail the whole
			// box, and leave its ladder alone — the individual renewal the
			// scheduler enqueues next time is where it gets a fair hearing.
			// Advancing it here would burn a rung on a card we never tried.
			if sub.DunningHardDeclined() {
				continue
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

		// Nothing survived the hard-decline filter. Stop here rather than pricing,
		// rating shipping, and calculating tax on an empty order; the caller
		// checks len(items) and returns. Their ladders are deliberately left
		// alone — the scheduler re-enqueues them individually, which is where a
		// latched subscription gets a real charge attempt.
		if len(items) == 0 {
			return nil
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
		shippingCents, taxCents, shipMethod = s.renewalCharges(ctx, tx, taxLines, subtotalCents, customer, addr, allGrandfathered)

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("batch renewal read phase: %w", err)
	}

	// Every subscription in the batch was hard-declined and skipped above. There
	// is no order to place and no ladder to advance here; the scheduler routes
	// these to individual renewals, which is the path that can resolve their
	// payment method and decide whether the latch still holds.
	if len(items) == 0 {
		s.metrics.SubscriptionRenewals.WithLabelValues("failed").Inc()
		return nil, fmt.Errorf("all batched subscriptions hard-declined: %w", ErrRenewalPaymentDeclined)
	}

	orderTotal = subtotalCents + shippingCents + taxCents

	if customer.StripeCustomerID == nil {
		return nil, fmt.Errorf("customer %s has no Stripe customer ID", customer.ID)
	}

	// --- Phase 2: create PaymentIntent (external call, outside tx) ---

	// No card to avoid: latched subscriptions are routed out of batching.
	paymentMethodID, err := s.pickRenewalPaymentMethod(ctx, *customer.StripeCustomerID, "")
	if err != nil {
		return nil, err
	}
	if paymentMethodID == "" {
		_ = store.Tx(ctx, pool, func(tx pgx.Tx) error {
			for _, item := range items {
				if err := s.recordRenewalFailure(ctx, tx, item.Sub, customer.ID, "", nil); err != nil {
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
				if rfErr := s.recordRenewalFailure(ctx, tx, item.Sub, customer.ID, paymentMethodID, err); rfErr != nil {
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
		renewalPlacedAt := time.Now()

		var txErr error
		order, txErr = s.orders.CreateOrder(ctx, tx, store.CreateOrderParams{
			Number:                orderNumber,
			CustomerID:            &customerID,
			Status:                domain.OrderStatusConfirmed,
			PaymentStatus:         domain.PaymentStatusCaptured,
			FulfillmentStatus:     domain.FulfillmentStatusUnfulfilled,
			CurrencyCode:          "USD",
			Subtotal:              subtotalCents,
			ShippingTotal:         shippingCents,
			TaxTotal:              taxCents,
			Total:                 orderTotal,
			ShippingAddressID:     addr.ID,
			BillingAddressID:      addr.ID,
			SubscriptionID:        nil, // batched — use subscription_orders for linking
			ShippingMethod:        shipMethod,
			ScheduledDeliveryDate: scheduleLocalDelivery(ctx, tx, s.shipping, shipMethod, renewalPlacedAt, s.merchantTZ),
			PlacedAt:              renewalPlacedAt,
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

			// Advance billing period. Period boundary stays exact; only the
			// charge trigger is anchored to the renewal window so the next order
			// lands pre-dawn for morning fulfillment.
			txErr = s.subscriptions.UpdatePeriod(ctx, tx, item.Sub.ID, newStart, newEnd, s.anchorRenewal(newEnd))
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
// avoid names a card already known to be permanently dead. It is skipped in
// favour of anything else on file, and returned only when it is the only card
// there is — which is how the caller tells "they added a replacement" from
// "the dead card is still all they have".
//
// Skipping matters because a customer can add a card without it becoming the
// default. Stripe's payment_method_update flow does set the default, but the
// full billing portal need not, and preferring a known-dead default over a
// working second card would strand exactly the customer who did what we asked.
//
// Resolution order:
//  1. Customer's default_payment_method, unless that is the dead card.
//  2. First attached method that is not the dead card (legacy customers without
//     a default, or a customer whose default is the dead one).
//  3. The dead card itself, if nothing else is attached.
func (s *RenewalService) pickRenewalPaymentMethod(ctx context.Context, stripeCustomerID, avoid string) (string, error) {
	stripeCust, err := s.payments.GetCustomer(ctx, stripeCustomerID)
	if err != nil {
		return "", fmt.Errorf("get stripe customer: %w", err)
	}
	defaultPM := stripeCust.DefaultPaymentMethodID
	if defaultPM != "" && defaultPM != avoid {
		return defaultPM, nil
	}

	methods, err := s.payments.ListPaymentMethods(ctx, stripeCustomerID)
	if err != nil {
		return "", fmt.Errorf("list payment methods: %w", err)
	}
	for _, m := range methods {
		if m.ID != avoid {
			return m.ID, nil
		}
	}

	// Only the dead card remains (or nothing at all). Hand it back so the caller
	// blocks the charge rather than mistaking this for "no payment method".
	if defaultPM != "" {
		return defaultPM, nil
	}
	if len(methods) > 0 {
		return methods[0].ID, nil
	}
	return "", nil
}

// pickRenewalLocalMethod returns the shipping method to stamp on a renewal
// order. Renewals do not surface UI to the customer, so the only signals are
// the merchant config and the customer's saved preference. Returns nil when
// the address is not local or when the merchant has both channels disabled —
// the order then falls back to the standard "shipped" flow.
func pickRenewalLocalMethod(cfg *domain.ShippingConfig, shipToZip string, preference *domain.ShippingMethod) *domain.ShippingMethod {
	// An explicit "mail it to me" preference always wins — even a local-zone
	// customer who could get free delivery gets it shipped if that's what they
	// chose. nil => the standard shipped flow downstream.
	if preference != nil && *preference == domain.ShippingMethodShipped {
		return nil
	}
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
