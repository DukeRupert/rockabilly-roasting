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
	"github.com/dukerupert/hiri/internal/store"
)

// SubscriptionService contains business logic for subscriptions.
type SubscriptionService struct {
	subscriptions *store.SubscriptionStore
	orders        *store.OrderStore
	audit         *audit.AuditWriter
	metrics       *metrics.Registry
	customers     *store.CustomerStore // populated via WithEmail; required for SendConfirmationEmail
	catalog       *store.CatalogStore  // populated via WithEmail/WithCatalog; required for SendConfirmationEmail and ChangeVariant
	pricing       *store.PricingStore  // populated via WithCatalog; required for ChangeVariant same-price guard
	email         EmailEnv             // populated via WithEmail; required for SendConfirmationEmail
}

// NewSubscriptionService creates a new SubscriptionService.
func NewSubscriptionService(
	subscriptions *store.SubscriptionStore,
	orders *store.OrderStore,
	audit *audit.AuditWriter,
	metrics *metrics.Registry,
) *SubscriptionService {
	return &SubscriptionService{
		subscriptions: subscriptions,
		orders:        orders,
		audit:         audit,
		metrics:       metrics,
	}
}

// WithEmail attaches email-send environment and supporting stores. Must be
// called before SendConfirmationEmail.
func (s *SubscriptionService) WithEmail(env EmailEnv, customers *store.CustomerStore, catalog *store.CatalogStore) *SubscriptionService {
	s.email = env
	s.customers = customers
	s.catalog = catalog
	return s
}

// WithCatalog wires the catalog and pricing stores used by ChangeVariant to
// validate the sibling variant and enforce the same-price guard.
func (s *SubscriptionService) WithCatalog(catalog *store.CatalogStore, pricing *store.PricingStore) *SubscriptionService {
	s.catalog = catalog
	s.pricing = pricing
	return s
}

// --- State machine helpers ---

func canPauseSubscription(status domain.SubscriptionStatus) bool {
	return status == domain.SubscriptionStatusActive
}

func canResumeSubscription(status domain.SubscriptionStatus) bool {
	return status == domain.SubscriptionStatusPaused
}

func canCancelSubscription(status domain.SubscriptionStatus) bool {
	return status == domain.SubscriptionStatusActive ||
		status == domain.SubscriptionStatusPaused ||
		status == domain.SubscriptionStatusPastDue
}

func canEditSubscription(status domain.SubscriptionStatus) bool {
	return status == domain.SubscriptionStatusActive ||
		status == domain.SubscriptionStatusPaused ||
		status == domain.SubscriptionStatusPastDue
}

// --- Query methods ---

// GetSubscriptionAsStaff returns a subscription by ID.
func (s *SubscriptionService) GetSubscriptionAsStaff(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Subscription, error) {
	sub, err := s.subscriptions.GetByIDAsStaff(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSubscriptionNotFound
		}
		return nil, fmt.Errorf("get subscription: %w", err)
	}
	return sub, nil
}

// ListSubscriptions returns subscriptions matching the given filter.
func (s *SubscriptionService) ListSubscriptions(ctx context.Context, tx pgx.Tx, f store.SubscriptionFilter) ([]domain.Subscription, error) {
	subs, err := s.subscriptions.List(ctx, tx, f)
	if err != nil {
		return nil, fmt.Errorf("list subscriptions: %w", err)
	}
	return subs, nil
}

// CountSubscriptionsByStatus returns the number of subscriptions with the given status.
func (s *SubscriptionService) CountSubscriptionsByStatus(ctx context.Context, tx pgx.Tx, status domain.SubscriptionStatus) (int, error) {
	count, err := s.subscriptions.CountByStatus(ctx, tx, status)
	if err != nil {
		return 0, fmt.Errorf("count subscriptions by status: %w", err)
	}
	return count, nil
}

// CountUnacknowledgedPastDue returns the number of past-due subscriptions that
// staff have not yet acknowledged on the dashboard.
func (s *SubscriptionService) CountUnacknowledgedPastDue(ctx context.Context, tx pgx.Tx) (int, error) {
	count, err := s.subscriptions.CountPastDueUnacknowledged(ctx, tx)
	if err != nil {
		return 0, fmt.Errorf("count unacknowledged past-due: %w", err)
	}
	return count, nil
}

// ActiveSubscriptionsAsOf returns the number of subscriptions live (created and
// not yet cancelled or expired) at the instant asOf. Seeds the running total
// for the active-subscriptions-over-time chart.
func (s *SubscriptionService) ActiveSubscriptionsAsOf(ctx context.Context, tx pgx.Tx, asOf time.Time) (int, error) {
	count, err := s.subscriptions.ActiveSubscriptionsAsOf(ctx, tx, asOf)
	if err != nil {
		return 0, fmt.Errorf("active subscriptions as of: %w", err)
	}
	return count, nil
}

// ActiveSubscriptionDeltasByDay returns the per-day net change in the active
// subscription base over [from, to). Days with no change are omitted; callers
// carry the running total forward from ActiveSubscriptionsAsOf(from).
func (s *SubscriptionService) ActiveSubscriptionDeltasByDay(ctx context.Context, tx pgx.Tx, from, to time.Time, tz *time.Location) ([]domain.SubscriptionDelta, error) {
	deltas, err := s.subscriptions.ActiveSubscriptionDeltasByDay(ctx, tx, from, to, tz)
	if err != nil {
		return nil, fmt.Errorf("active subscription deltas by day: %w", err)
	}
	return deltas, nil
}

// ListPlans returns all subscription plans.
func (s *SubscriptionService) ListPlans(ctx context.Context, tx pgx.Tx) ([]domain.SubscriptionPlan, error) {
	plans, err := s.subscriptions.ListPlans(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("list subscription plans: %w", err)
	}
	return plans, nil
}

// GetPlan returns a subscription plan by ID.
func (s *SubscriptionService) GetPlan(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.SubscriptionPlan, error) {
	plan, err := s.subscriptions.GetPlanByID(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSubscriptionPlanNotFound
		}
		return nil, fmt.Errorf("get subscription plan: %w", err)
	}
	return plan, nil
}

// ListSubscriptionOrders returns all orders for a subscription.
func (s *SubscriptionService) ListSubscriptionOrders(ctx context.Context, tx pgx.Tx, subscriptionID uuid.UUID) ([]domain.SubscriptionOrder, error) {
	orders, err := s.subscriptions.ListSubscriptionOrders(ctx, tx, subscriptionID)
	if err != nil {
		return nil, fmt.Errorf("list subscription orders: %w", err)
	}
	return orders, nil
}

// ListActivePlans returns all active subscription plans.
func (s *SubscriptionService) ListActivePlans(ctx context.Context, tx pgx.Tx) ([]domain.SubscriptionPlan, error) {
	plans, err := s.subscriptions.ListActivePlans(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("list active plans: %w", err)
	}
	return plans, nil
}

// ListSubscriptionsByCustomer returns all subscriptions for a customer.
func (s *SubscriptionService) ListSubscriptionsByCustomer(ctx context.Context, tx pgx.Tx, customerID uuid.UUID) ([]domain.Subscription, error) {
	subs, err := s.subscriptions.ListByCustomer(ctx, tx, customerID)
	if err != nil {
		return nil, fmt.Errorf("list subscriptions by customer: %w", err)
	}
	return subs, nil
}

// GetSubscriptionByCustomer returns a subscription scoped to a customer.
func (s *SubscriptionService) GetSubscriptionByCustomer(ctx context.Context, tx pgx.Tx, id, customerID uuid.UUID) (*domain.Subscription, error) {
	sub, err := s.subscriptions.Get(ctx, tx, id, customerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSubscriptionNotFound
		}
		return nil, fmt.Errorf("get subscription by customer: %w", err)
	}
	return sub, nil
}

// ListDueForRenewal returns subscriptions due to be charged now — fresh active
// renewals plus past_due subscriptions whose next dunning retry has come due.
func (s *SubscriptionService) ListDueForRenewal(ctx context.Context, tx pgx.Tx) ([]domain.Subscription, error) {
	subs, err := s.subscriptions.ListDueForRenewal(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("list due for renewal: %w", err)
	}
	return subs, nil
}

// --- Plan mutation methods ---

// CreatePlanParams holds the input needed to create a subscription plan.
type CreatePlanParams struct {
	Name          string
	Interval      domain.SubscriptionInterval
	IntervalCount int
	DiscountPct   int
}

// CreatePlan creates a new subscription plan.
func (s *SubscriptionService) CreatePlan(ctx context.Context, tx pgx.Tx, p CreatePlanParams, actor Actor) (*domain.SubscriptionPlan, error) {
	plan, err := s.subscriptions.CreatePlan(ctx, tx, store.CreatePlanParams{
		Name:          p.Name,
		Interval:      p.Interval,
		IntervalCount: p.IntervalCount,
		DiscountPct:   p.DiscountPct,
		IsActive:      true,
	})
	if err != nil {
		return nil, fmt.Errorf("create subscription plan: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditPlanCreated,
		ResourceType: "subscription_plan",
		ResourceID:   plan.ID,
		After:        plan,
	}); err != nil {
		return nil, fmt.Errorf("audit plan created: %w", err)
	}

	return plan, nil
}

// UpdatePlanActive activates or deactivates a subscription plan.
func (s *SubscriptionService) UpdatePlanActive(ctx context.Context, tx pgx.Tx, id uuid.UUID, isActive bool, actor Actor) error {
	if err := s.subscriptions.UpdatePlanActive(ctx, tx, id, isActive); err != nil {
		return fmt.Errorf("update plan active: %w", err)
	}

	action := audit.AuditPlanDeactivated
	if isActive {
		action = audit.AuditPlanActivated
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       action,
		ResourceType: "subscription_plan",
		ResourceID:   id,
	}); err != nil {
		return fmt.Errorf("audit plan active update: %w", err)
	}

	return nil
}

// UpdatePlanDiscount updates the discount percentage of a subscription plan.
func (s *SubscriptionService) UpdatePlanDiscount(ctx context.Context, tx pgx.Tx, id uuid.UUID, discountPct int, actor Actor) error {
	if discountPct < 0 || discountPct > 100 {
		return fmt.Errorf("discount must be 0-100")
	}

	if err := s.subscriptions.UpdatePlanDiscount(ctx, tx, id, discountPct); err != nil {
		return fmt.Errorf("update plan discount: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditPlanUpdated,
		ResourceType: "subscription_plan",
		ResourceID:   id,
		Metadata:     map[string]any{"discount_pct": discountPct},
	}); err != nil {
		return fmt.Errorf("audit plan discount update: %w", err)
	}

	return nil
}

// --- Subscription mutation methods ---

// CreateSubscriptionParams holds all input needed to create a subscription.
type CreateSubscriptionParams struct {
	CustomerID        uuid.UUID
	PlanID            uuid.UUID
	VariantID         uuid.UUID
	Quantity          int
	ShippingAddressID uuid.UUID
	Metadata          map[string]any
}

// CreateSubscription creates a new active subscription for a customer.
func (s *SubscriptionService) CreateSubscription(ctx context.Context, tx pgx.Tx, p CreateSubscriptionParams, actor Actor) (*domain.Subscription, error) {
	if p.Quantity < 1 || p.Quantity > 10 {
		return nil, ErrInvalidQuantity
	}

	plan, err := s.subscriptions.GetPlanByID(ctx, tx, p.PlanID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSubscriptionPlanNotFound
		}
		return nil, fmt.Errorf("get plan for create: %w", err)
	}
	if !plan.IsActive {
		return nil, ErrSubscriptionPlanInactive
	}

	// Block new subscriptions on archived variants. Existing subs on an
	// archived variant keep running — see ArchiveVariant.
	if s.catalog != nil {
		variant, err := s.catalog.GetVariantByID(ctx, tx, p.VariantID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrVariantNotFound
			}
			return nil, fmt.Errorf("check variant for new subscription: %w", err)
		}
		if variant.ArchivedAt != nil {
			return nil, ErrVariantArchived
		}
	}

	return s.createSubscriptionRecord(ctx, tx, p, plan, actor)
}

// createSubscriptionRecord inserts the subscription row and audit entry.
// Callers are responsible for signup-time validation (plan active, variant
// not archived, quantity bounds) — CreateSubscription enforces all of it,
// ActivateFromSignupOrder deliberately skips the plan/variant guards.
func (s *SubscriptionService) createSubscriptionRecord(ctx context.Context, tx pgx.Tx, p CreateSubscriptionParams, plan *domain.SubscriptionPlan, actor Actor) (*domain.Subscription, error) {
	now := time.Now()
	periodEnd := nextPeriodEnd(now, plan.Interval, plan.IntervalCount)

	sub, err := s.subscriptions.Create(ctx, tx, store.CreateSubscriptionParams{
		CustomerID:         p.CustomerID,
		PlanID:             p.PlanID,
		VariantID:          p.VariantID,
		Quantity:           p.Quantity,
		Status:             domain.SubscriptionStatusActive,
		ShippingAddressID:  p.ShippingAddressID,
		CurrentPeriodStart: now,
		CurrentPeriodEnd:   periodEnd,
		NextOrderAt:        periodEnd,
		Metadata:           p.Metadata,
	})
	if err != nil {
		return nil, fmt.Errorf("create subscription: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditSubscriptionCreated,
		ResourceType: "subscription",
		ResourceID:   sub.ID,
		After:        sub,
	}); err != nil {
		return nil, fmt.Errorf("audit subscription created: %w", err)
	}

	return sub, nil
}

// Metadata keys stamped on orders pre-created by the subscribe flow at
// PaymentIntent time. ConfirmCheckoutPayment's callers use them to recognize
// a paid signup order and activate its subscription.
const (
	orderMetaSubscriptionSignup = "subscription_signup"
	orderMetaSubscriptionPlanID = "subscription_plan_id"
)

// SubscriptionSignupOrderMetadata builds the order metadata that marks a
// pre-created order as a subscription signup for the given plan.
func SubscriptionSignupOrderMetadata(planID uuid.UUID, paymentIntentID string) map[string]any {
	return map[string]any{
		orderMetaSubscriptionSignup: true,
		orderMetaSubscriptionPlanID: planID.String(),
		"payment_intent_id":         paymentIntentID,
	}
}

// SubscriptionSignupPlanID extracts the plan ID from a subscription-signup
// order's metadata. ok is false when the order is not a signup order.
func SubscriptionSignupPlanID(metadata map[string]any) (uuid.UUID, bool) {
	if metadata == nil {
		return uuid.Nil, false
	}
	if flag, _ := metadata[orderMetaSubscriptionSignup].(bool); !flag {
		return uuid.Nil, false
	}
	raw, _ := metadata[orderMetaSubscriptionPlanID].(string)
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

// ActivateFromSignupOrder creates and links the subscription promised by a
// paid signup order — an order pre-created at PaymentIntent time by the
// subscribe flow with SubscriptionSignupOrderMetadata. Called from the
// subscribe-confirm endpoint and the payment_intent.succeeded webhook, gated
// on ConfirmCheckoutPayment's transitioned return so exactly one caller runs
// it per order.
//
// Signup-time guards (plan active, variant not archived) are deliberately not
// re-checked: they were enforced when the PaymentIntent was created, the
// customer has already paid, and existing subscriptions are allowed to keep
// running on archived variants and deactivated plans.
func (s *SubscriptionService) ActivateFromSignupOrder(ctx context.Context, tx pgx.Tx, order *domain.Order, actor Actor) (*domain.Subscription, error) {
	planID, ok := SubscriptionSignupPlanID(order.Metadata)
	if !ok {
		return nil, fmt.Errorf("activate signup subscription: order %s has no signup metadata", order.ID)
	}
	if order.CustomerID == nil {
		return nil, fmt.Errorf("activate signup subscription: order %s has no customer", order.ID)
	}

	plan, err := s.subscriptions.GetPlanByID(ctx, tx, planID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSubscriptionPlanNotFound
		}
		return nil, fmt.Errorf("get plan for signup activation: %w", err)
	}

	items, err := s.orders.ListLineItems(ctx, tx, order.ID)
	if err != nil {
		return nil, fmt.Errorf("list signup order line items: %w", err)
	}
	if len(items) != 1 {
		return nil, fmt.Errorf("activate signup subscription: order %s has %d line items, want 1", order.ID, len(items))
	}
	item := items[0]
	if item.Quantity < 1 || item.Quantity > 10 {
		return nil, ErrInvalidQuantity
	}

	sub, err := s.createSubscriptionRecord(ctx, tx, CreateSubscriptionParams{
		CustomerID:        *order.CustomerID,
		PlanID:            planID,
		VariantID:         item.VariantID,
		Quantity:          item.Quantity,
		ShippingAddressID: order.ShippingAddressID,
	}, plan, actor)
	if err != nil {
		return nil, err
	}

	if err := s.LinkOrder(ctx, tx, sub.ID, order.ID, sub.CurrentPeriodStart, sub.CurrentPeriodEnd); err != nil {
		return nil, err
	}
	if err := s.orders.UpdateOrderSubscriptionID(ctx, tx, order.ID, sub.ID); err != nil {
		return nil, fmt.Errorf("stamp subscription on signup order: %w", err)
	}

	return sub, nil
}

// PauseSubscription pauses an active subscription. An optional pauseUntil date
// can be provided for automatic resume scheduling.
func (s *SubscriptionService) PauseSubscription(ctx context.Context, tx pgx.Tx, id uuid.UUID, pauseUntil *time.Time, actor Actor) (*domain.Subscription, error) {
	sub, err := s.subscriptions.GetByIDAsStaff(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSubscriptionNotFound
		}
		return nil, fmt.Errorf("get subscription for pause: %w", err)
	}

	if !canPauseSubscription(sub.Status) {
		return nil, ErrSubscriptionNotPausable
	}

	sub, err = s.subscriptions.UpdateStatus(ctx, tx, id, domain.SubscriptionStatusPaused)
	if err != nil {
		return nil, fmt.Errorf("pause subscription: %w", err)
	}

	if err := s.subscriptions.UpdatePauseUntil(ctx, tx, id, pauseUntil); err != nil {
		return nil, fmt.Errorf("set pause_until: %w", err)
	}
	sub.PauseUntil = pauseUntil

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditSubscriptionPaused,
		ResourceType: "subscription",
		ResourceID:   id,
		After:        sub,
	}); err != nil {
		return nil, fmt.Errorf("audit subscription paused: %w", err)
	}

	return sub, nil
}

// ResumeSubscription resumes a paused subscription and resets the billing period.
func (s *SubscriptionService) ResumeSubscription(ctx context.Context, tx pgx.Tx, id uuid.UUID, actor Actor) (*domain.Subscription, error) {
	sub, err := s.subscriptions.GetByIDAsStaff(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSubscriptionNotFound
		}
		return nil, fmt.Errorf("get subscription for resume: %w", err)
	}

	if !canResumeSubscription(sub.Status) {
		return nil, ErrSubscriptionNotResumable
	}

	plan, err := s.subscriptions.GetPlanByID(ctx, tx, sub.PlanID)
	if err != nil {
		return nil, fmt.Errorf("get plan for resume: %w", err)
	}

	now := time.Now()
	periodEnd := nextPeriodEnd(now, plan.Interval, plan.IntervalCount)

	sub, err = s.subscriptions.UpdateStatus(ctx, tx, id, domain.SubscriptionStatusActive)
	if err != nil {
		return nil, fmt.Errorf("resume subscription status: %w", err)
	}

	if err := s.subscriptions.UpdatePeriod(ctx, tx, id, now, periodEnd, periodEnd); err != nil {
		return nil, fmt.Errorf("reset billing period: %w", err)
	}
	sub.CurrentPeriodStart = now
	sub.CurrentPeriodEnd = periodEnd
	sub.NextOrderAt = periodEnd

	if err := s.subscriptions.UpdatePauseUntil(ctx, tx, id, nil); err != nil {
		return nil, fmt.Errorf("clear pause_until: %w", err)
	}
	sub.PauseUntil = nil

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditSubscriptionResumed,
		ResourceType: "subscription",
		ResourceID:   id,
		After:        sub,
	}); err != nil {
		return nil, fmt.Errorf("audit subscription resumed: %w", err)
	}

	return sub, nil
}

// CancelSubscription cancels a subscription.
func (s *SubscriptionService) CancelSubscription(ctx context.Context, tx pgx.Tx, id uuid.UUID, actor Actor) (*domain.Subscription, error) {
	sub, err := s.subscriptions.GetByIDAsStaff(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSubscriptionNotFound
		}
		return nil, fmt.Errorf("get subscription for cancel: %w", err)
	}

	if !canCancelSubscription(sub.Status) {
		return nil, ErrSubscriptionNotCancellable
	}

	sub, err = s.subscriptions.UpdateStatus(ctx, tx, id, domain.SubscriptionStatusCancelled)
	if err != nil {
		return nil, fmt.Errorf("cancel subscription status: %w", err)
	}

	if err := s.subscriptions.Cancel(ctx, tx, id); err != nil {
		return nil, fmt.Errorf("set cancelled_at: %w", err)
	}
	now := time.Now()
	sub.CancelledAt = &now

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditSubscriptionCancelled,
		ResourceType: "subscription",
		ResourceID:   id,
		After:        sub,
	}); err != nil {
		return nil, fmt.Errorf("audit subscription cancelled: %w", err)
	}

	return sub, nil
}

// ChangeVariant swaps a subscription to a sibling variant on the same product
// (e.g. Whole Bean → Drip Ground). Future renewals will use the new variant;
// orders already generated for the current period are not modified — staff
// must adjust those separately on the order page. The target variant must
// belong to the same product and share the same base price (USD) as the
// current variant, mirroring the order-side guard.
func (s *SubscriptionService) ChangeVariant(ctx context.Context, tx pgx.Tx, id, newVariantID uuid.UUID, actor Actor) (*domain.Subscription, error) {
	if s.catalog == nil || s.pricing == nil {
		return nil, fmt.Errorf("change subscription variant: service not wired with catalog/pricing")
	}

	sub, err := s.subscriptions.GetByIDAsStaff(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSubscriptionNotFound
		}
		return nil, fmt.Errorf("get subscription for variant change: %w", err)
	}
	if !canEditSubscription(sub.Status) {
		return nil, ErrSubscriptionNotEditable
	}
	if sub.VariantID == newVariantID {
		return sub, nil
	}

	oldVariant, err := s.catalog.GetVariantByID(ctx, tx, sub.VariantID)
	if err != nil {
		return nil, fmt.Errorf("get current variant: %w", err)
	}
	newVariant, err := s.catalog.GetVariantByID(ctx, tx, newVariantID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrVariantNotFound
		}
		return nil, fmt.Errorf("get new variant: %w", err)
	}
	if newVariant.ProductID != oldVariant.ProductID {
		return nil, ErrVariantNotOnSameProduct
	}
	if newVariant.ArchivedAt != nil {
		return nil, ErrVariantArchived
	}

	oldPrice, err := s.pricing.GetBasePrice(ctx, tx, oldVariant.ID, "USD")
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPriceNotFound
		}
		return nil, fmt.Errorf("get current variant price: %w", err)
	}
	newPrice, err := s.pricing.GetBasePrice(ctx, tx, newVariantID, "USD")
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPriceNotFound
		}
		return nil, fmt.Errorf("get new variant price: %w", err)
	}
	if newPrice.Amount != oldPrice.Amount {
		return nil, ErrVariantPriceMismatch
	}

	updated, err := s.subscriptions.UpdateVariant(ctx, tx, id, newVariantID)
	if err != nil {
		return nil, fmt.Errorf("update subscription variant: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditSubscriptionVariantChanged,
		ResourceType: "subscription",
		ResourceID:   id,
		After:        updated,
		Metadata: map[string]any{
			"old_variant_id":  oldVariant.ID,
			"new_variant_id":  newVariant.ID,
			"old_variant_sku": oldVariant.SKU,
			"new_variant_sku": newVariant.SKU,
		},
	}); err != nil {
		return nil, fmt.Errorf("audit subscription variant changed: %w", err)
	}

	return updated, nil
}

// ChangePlan swaps a subscription onto a different active plan (e.g. Weekly →
// Bi-Weekly). The current period's start date is preserved; current_period_end
// and next_order_at are recomputed from current_period_start using the new
// plan's interval, so the customer keeps the cadence they already paid into.
// No-op if the subscription is already on the target plan.
func (s *SubscriptionService) ChangePlan(ctx context.Context, tx pgx.Tx, id, newPlanID uuid.UUID, actor Actor) (*domain.Subscription, error) {
	sub, err := s.subscriptions.GetByIDAsStaff(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSubscriptionNotFound
		}
		return nil, fmt.Errorf("get subscription for plan change: %w", err)
	}
	if !canEditSubscription(sub.Status) {
		return nil, ErrSubscriptionNotEditable
	}
	if sub.PlanID == newPlanID {
		return sub, nil
	}

	oldPlan, err := s.subscriptions.GetPlanByID(ctx, tx, sub.PlanID)
	if err != nil {
		return nil, fmt.Errorf("get current plan: %w", err)
	}
	newPlan, err := s.subscriptions.GetPlanByID(ctx, tx, newPlanID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSubscriptionPlanNotFound
		}
		return nil, fmt.Errorf("get new plan: %w", err)
	}
	if !newPlan.IsActive {
		return nil, ErrSubscriptionPlanInactive
	}

	newPeriodEnd := nextPeriodEnd(sub.CurrentPeriodStart, newPlan.Interval, newPlan.IntervalCount)

	updated, err := s.subscriptions.UpdatePlan(ctx, tx, id, newPlanID, newPeriodEnd, newPeriodEnd)
	if err != nil {
		return nil, fmt.Errorf("update subscription plan: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditSubscriptionPlanChanged,
		ResourceType: "subscription",
		ResourceID:   id,
		After:        updated,
		Metadata: map[string]any{
			"old_plan_id":            oldPlan.ID,
			"new_plan_id":            newPlan.ID,
			"old_plan_name":          oldPlan.Name,
			"new_plan_name":          newPlan.Name,
			"old_interval":           string(oldPlan.Interval),
			"new_interval":           string(newPlan.Interval),
			"old_interval_count":     oldPlan.IntervalCount,
			"new_interval_count":     newPlan.IntervalCount,
			"new_current_period_end": newPeriodEnd,
		},
	}); err != nil {
		return nil, fmt.Errorf("audit subscription plan changed: %w", err)
	}

	return updated, nil
}

// MarkPastDue transitions an active subscription to past_due after a payment failure.
func (s *SubscriptionService) MarkPastDue(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Subscription, error) {
	sub, err := s.subscriptions.GetByIDAsStaff(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSubscriptionNotFound
		}
		return nil, fmt.Errorf("get subscription for past_due: %w", err)
	}

	if sub.Status != domain.SubscriptionStatusActive {
		return nil, ErrSubscriptionNotActive
	}

	sub, err = s.subscriptions.UpdateStatus(ctx, tx, id, domain.SubscriptionStatusPastDue)
	if err != nil {
		return nil, fmt.Errorf("mark subscription past_due: %w", err)
	}

	return sub, nil
}

// AcknowledgeDunning marks a past-due subscription's dashboard alert as handled,
// dropping it off the Urgent band until the next failed charge re-surfaces it.
// It does not change the subscription's status — the customer is still past_due;
// staff are only signalling "seen / in progress" so the queue reflects what
// still needs a first look.
func (s *SubscriptionService) AcknowledgeDunning(ctx context.Context, tx pgx.Tx, id uuid.UUID, actor Actor) error {
	sub, err := s.subscriptions.GetByIDAsStaff(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrSubscriptionNotFound
		}
		return fmt.Errorf("get subscription for dunning ack: %w", err)
	}

	if sub.Status != domain.SubscriptionStatusPastDue {
		return ErrSubscriptionNotPastDue
	}

	if err := s.subscriptions.SetDunningAcknowledged(ctx, tx, id); err != nil {
		return fmt.Errorf("acknowledge dunning: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditSubscriptionDunningAck,
		ResourceType: "subscription",
		ResourceID:   id,
		After:        sub,
	}); err != nil {
		return fmt.Errorf("audit dunning acknowledged: %w", err)
	}

	return nil
}

// SetShippingGrandfathered flips a subscription's free-renewal-shipping
// exception on or off and records who changed it. When on, renewals waive the
// shipping charge (the customer keeps the terms they signed up under); when
// off, renewals price shipping like a retail order. No-op-safe: re-setting the
// same value just rewrites the flag and writes an audit row.
func (s *SubscriptionService) SetShippingGrandfathered(ctx context.Context, tx pgx.Tx, id uuid.UUID, enabled bool, actor Actor) (*domain.Subscription, error) {
	sub, err := s.subscriptions.GetByIDAsStaff(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSubscriptionNotFound
		}
		return nil, fmt.Errorf("get subscription for grandfather toggle: %w", err)
	}

	if err := s.subscriptions.SetShippingGrandfathered(ctx, tx, id, enabled); err != nil {
		return nil, err
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditSubscriptionShippingGrandfathered,
		ResourceType: "subscription",
		ResourceID:   id,
		Metadata:     map[string]any{"shipping_grandfathered": enabled},
	}); err != nil {
		return nil, fmt.Errorf("audit shipping grandfather change: %w", err)
	}

	// Reflect the new value on the returned struct without a re-read.
	if sub.Metadata == nil {
		sub.Metadata = map[string]any{}
	}
	if enabled {
		sub.Metadata[domain.SubscriptionMetaShippingGrandfathered] = true
	} else {
		delete(sub.Metadata, domain.SubscriptionMetaShippingGrandfathered)
	}
	return sub, nil
}

// AdvancePeriod advances the subscription's billing period after a successful renewal.
func (s *SubscriptionService) AdvancePeriod(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Subscription, error) {
	sub, err := s.subscriptions.GetByIDAsStaff(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSubscriptionNotFound
		}
		return nil, fmt.Errorf("get subscription for advance: %w", err)
	}

	plan, err := s.subscriptions.GetPlanByID(ctx, tx, sub.PlanID)
	if err != nil {
		return nil, fmt.Errorf("get plan for advance: %w", err)
	}

	newStart := sub.CurrentPeriodEnd
	newEnd := nextPeriodEnd(newStart, plan.Interval, plan.IntervalCount)

	if err := s.subscriptions.UpdatePeriod(ctx, tx, id, newStart, newEnd, newEnd); err != nil {
		return nil, fmt.Errorf("advance period: %w", err)
	}

	// If subscription was past_due, restore to active on successful renewal
	if sub.Status == domain.SubscriptionStatusPastDue {
		sub, err = s.subscriptions.UpdateStatus(ctx, tx, id, domain.SubscriptionStatusActive)
		if err != nil {
			return nil, fmt.Errorf("restore active status: %w", err)
		}
	}

	sub.CurrentPeriodStart = newStart
	sub.CurrentPeriodEnd = newEnd
	sub.NextOrderAt = newEnd

	return sub, nil
}

// LinkOrder associates an order with a subscription for a billing period.
func (s *SubscriptionService) LinkOrder(ctx context.Context, tx pgx.Tx, subscriptionID, orderID uuid.UUID, periodStart, periodEnd time.Time) error {
	if err := s.subscriptions.CreateSubscriptionOrder(ctx, tx, subscriptionID, orderID, periodStart, periodEnd); err != nil {
		return fmt.Errorf("link order to subscription: %w", err)
	}
	return nil
}

// --- Period calculation ---

// intervalDays returns the billing cadence in days for the given interval and
// count. Used for customer-facing copy (emails, account UI) where the raw enum
// value (e.g. "every_30_days") is too awkward. Returns 0 for the dev-only
// every_2_minutes interval — it should never appear in customer comms.
func intervalDays(interval domain.SubscriptionInterval, count int) int {
	if count < 1 {
		count = 1
	}
	switch interval {
	case domain.SubscriptionIntervalEvery7Days:
		return 7 * count
	case domain.SubscriptionIntervalEvery14Days:
		return 14 * count
	case domain.SubscriptionIntervalEvery21Days:
		return 21 * count
	case domain.SubscriptionIntervalEvery30Days:
		return 30 * count
	case domain.SubscriptionIntervalEvery60Days:
		return 60 * count
	case domain.SubscriptionIntervalEvery90Days:
		return 90 * count
	default:
		return 0
	}
}

// NextRenewalDate previews when the first renewal charge would land for a
// subscription started now on the given plan. Used by the subscribe page to
// state the billing rhythm before the subscription exists; the real date is
// stamped at payment-confirm time (CreateSubscription), which is approximately
// now, so this is accurate to the cadence's granularity.
func (s *SubscriptionService) NextRenewalDate(from time.Time, plan *domain.SubscriptionPlan) time.Time {
	return nextPeriodEnd(from, plan.Interval, plan.IntervalCount)
}

func nextPeriodEnd(start time.Time, interval domain.SubscriptionInterval, count int) time.Time {
	switch interval {
	case domain.SubscriptionIntervalEvery2Minutes:
		return start.Add(time.Duration(2*count) * time.Minute)
	case domain.SubscriptionIntervalEvery7Days:
		return start.AddDate(0, 0, 7*count)
	case domain.SubscriptionIntervalEvery14Days:
		return start.AddDate(0, 0, 14*count)
	case domain.SubscriptionIntervalEvery21Days:
		return start.AddDate(0, 0, 21*count)
	case domain.SubscriptionIntervalEvery30Days:
		return start.AddDate(0, 0, 30*count)
	case domain.SubscriptionIntervalEvery60Days:
		return start.AddDate(0, 0, 60*count)
	case domain.SubscriptionIntervalEvery90Days:
		return start.AddDate(0, 0, 90*count)
	default:
		return start.AddDate(0, 0, 30*count)
	}
}
