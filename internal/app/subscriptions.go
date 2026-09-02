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
	"github.com/dukerupert/hiri/internal/platform/auth"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
)

// SubscriptionService contains business logic for subscriptions.
type SubscriptionService struct {
	subscriptions *store.SubscriptionStore
	orders        *store.OrderStore
	audit         *audit.AuditWriter
	metrics       *metrics.Registry
	customers     *store.CustomerStore    // populated via WithEmail; required for SendConfirmationEmail
	catalog       *store.CatalogStore     // populated via WithEmail/WithCatalog; required for SendConfirmationEmail and ChangeVariant
	pricing       *store.PricingStore     // populated via WithCatalog; required for ChangeVariant same-price guard
	email         EmailEnv                // populated via WithEmail; required for SendConfirmationEmail
	renewalLoc    *time.Location          // populated via WithRenewalAnchor; nil disables renewal-time anchoring
	renewalHour   int                     // hour-of-day (0–23) in renewalLoc that renewals fire at
	orderActions  *auth.OrderActionSigner // populated via WithOrderActionSigner; nil omits the one-click undo link
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

// WithOrderActionSigner wires the signer behind the one-click undo link in the
// skip notification. Without it the email still sends — it just points at the
// account page rather than printing a link nothing could verify.
func (s *SubscriptionService) WithOrderActionSigner(signer *auth.OrderActionSigner) *SubscriptionService {
	s.orderActions = signer
	return s
}

// WithRenewalAnchor configures renewals to fire at hour:00 in loc rather than at
// each subscription's signup time-of-day, so the day's renewals batch into one
// pre-dawn window and orders are ready for staff to fulfill in the morning
// instead of trickling in. Wiring-time only; when unset, next_order_at keeps the
// raw period-end instant (the pre-anchor behaviour, used in tests).
func (s *SubscriptionService) WithRenewalAnchor(loc *time.Location, hour int) *SubscriptionService {
	s.renewalLoc = loc
	s.renewalHour = hour
	return s
}

// anchorRenewal snaps a next_order_at to the configured renewal window. A no-op
// when no anchor is wired.
func (s *SubscriptionService) anchorRenewal(t time.Time) time.Time {
	return anchorRenewalTime(t, s.renewalLoc, s.renewalHour)
}

// ResumeOrderDate reports when a subscription resumed at t would have its next
// order placed. Exported so the account page and the resume email can promise
// the same date ResumeSubscription will actually set, rather than each
// re-deriving the anchor arithmetic and drifting from it.
func (s *SubscriptionService) ResumeOrderDate(t time.Time) time.Time {
	return s.anchorRenewal(t)
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

// CountSubscriptions returns how many subscriptions match the filter, ignoring
// its Limit and Offset — the "of N" behind the paged list.
func (s *SubscriptionService) CountSubscriptions(ctx context.Context, tx pgx.Tx, f store.SubscriptionFilter) (int, error) {
	count, err := s.subscriptions.Count(ctx, tx, f)
	if err != nil {
		return 0, fmt.Errorf("count subscriptions: %w", err)
	}
	return count, nil
}

// SubscriptionStatusCounts returns the per-status totals under every filter
// dimension except status — what each status pill on the admin list shows.
func (s *SubscriptionService) SubscriptionStatusCounts(ctx context.Context, tx pgx.Tx, f store.SubscriptionFilter) (map[domain.SubscriptionStatus]int, error) {
	counts, err := s.subscriptions.CountsByStatus(ctx, tx, f)
	if err != nil {
		return nil, fmt.Errorf("subscription status counts: %w", err)
	}
	return counts, nil
}

// ListSubscribedProducts returns the coffees behind existing subscriptions,
// most-subscribed first — the option list for the admin coffee filter.
func (s *SubscriptionService) ListSubscribedProducts(ctx context.Context, tx pgx.Tx) ([]store.SubscribedProduct, error) {
	products, err := s.subscriptions.ListSubscribedProducts(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("list subscribed products: %w", err)
	}
	return products, nil
}

// CountSubscriptionsByStatus returns the number of subscriptions with the given status.
func (s *SubscriptionService) CountSubscriptionsByStatus(ctx context.Context, tx pgx.Tx, status domain.SubscriptionStatus) (int, error) {
	count, err := s.subscriptions.CountByStatus(ctx, tx, status)
	if err != nil {
		return 0, fmt.Errorf("count subscriptions by status: %w", err)
	}
	return count, nil
}

// RenewalForecastMaxDays bounds the forecast window. Ninety days is roughly a
// green-coffee purchasing cycle; past that the forecast is fiction anyway,
// since subscribers pause, skip, and cancel.
const RenewalForecastMaxDays = 90

// ForecastRenewals rolls up the coffee that upcoming subscription renewals will
// need, by product, over the next `days` days starting at now. The window is
// half-open [now, now+days): a renewal that already fired is fulfillment's
// problem, not the roaster's.
func (s *SubscriptionService) ForecastRenewals(ctx context.Context, tx pgx.Tx, now time.Time, days int) ([]domain.RenewalForecastLine, error) {
	if days <= 0 {
		return nil, nil
	}
	if days > RenewalForecastMaxDays {
		days = RenewalForecastMaxDays
	}
	lines, err := s.subscriptions.ForecastRenewals(ctx, tx, now, now.AddDate(0, 0, days))
	if err != nil {
		return nil, fmt.Errorf("forecast renewals: %w", err)
	}
	return lines, nil
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
		NextOrderAt:        s.anchorRenewal(periodEnd),
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

// ResumeSubscription resumes a paused subscription and puts its next order at
// the next renewal window — the following pre-dawn run — rather than a fresh
// interval out.
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

	// Resuming means "send my coffee", not "start me a fresh free interval".
	// The next order is placed at the next renewal window — the following
	// pre-dawn run, never a second one — and the cadence runs on from there.
	// (Not "within 24 hours": the anchor is a wall-clock hour, and the night the
	// clocks go back stretches that gap to 24h45m. See
	// TestAnchorRenewalTimeAcrossDST.) Until this, resume handed out
	// a whole new interval and *then* rounded up to the anchor, so a customer
	// who resumed at 9am on a Sunday waited eight days and change for the
	// shipment they had just asked to restart.
	now := time.Now()
	nextOrder := s.anchorRenewal(now)
	previousNextOrder := sub.NextOrderAt

	// Clear the end date BEFORE the status flip, not after. A resumed
	// subscription must not keep one — the renewal scheduler reads ends_at, not
	// the badge, so a survivor here makes a subscription that looks perfectly
	// healthy and can never bill. The ordering is load-bearing: migration 075
	// forbids a live status alongside ends_at, and a CHECK constraint is
	// evaluated per statement, so flipping to active first would violate it
	// mid-transaction.
	if err := s.subscriptions.ClearEndsAt(ctx, tx, id); err != nil {
		return nil, fmt.Errorf("clear ends_at on resume: %w", err)
	}
	sub.EndsAt = nil

	sub, err = s.subscriptions.UpdateStatus(ctx, tx, id, domain.SubscriptionStatusActive)
	if err != nil {
		return nil, fmt.Errorf("resume subscription status: %w", err)
	}

	// The period the resume opens ends when that order is placed; the renewal
	// then walks the cadence forward from it, so the subscription settles onto
	// the anchor hour rather than the minute the customer happened to click.
	if err := s.subscriptions.UpdatePeriod(ctx, tx, id, now, nextOrder, nextOrder); err != nil {
		return nil, fmt.Errorf("reset billing period: %w", err)
	}
	sub.CurrentPeriodStart = now
	sub.CurrentPeriodEnd = nextOrder
	sub.NextOrderAt = nextOrder

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
		Metadata: map[string]any{
			"previous_next_order_at": previousNextOrder,
			"next_order_at":          nextOrder,
		},
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
// (e.g. Whole Bean → Drip Ground, or 3lb → 12oz). Future renewals will use the
// new variant and its current price; orders already generated for the current
// period are not modified — staff must adjust those separately on the order
// page. The target variant must belong to the same product and not be archived.
//
// By default the target must also share the same base price (USD) as the
// current variant — this protects same-price "grind" swaps from silently
// changing what the customer pays. Pass allowPriceChange to permit a swap that
// moves the subscription to a different price tier (e.g. a size change); future
// renewals then charge the new variant's current price. No out-of-cycle order
// is ever created by this method.
func (s *SubscriptionService) ChangeVariant(ctx context.Context, tx pgx.Tx, id, newVariantID uuid.UUID, allowPriceChange bool, actor Actor) (*domain.Subscription, error) {
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
	if newPrice.Amount != oldPrice.Amount && !allowPriceChange {
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
			"old_price":       oldPrice.Amount,
			"new_price":       newPrice.Amount,
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
	nextOrder := s.anchorRenewal(newEnd)

	if err := s.subscriptions.UpdatePeriod(ctx, tx, id, newStart, newEnd, nextOrder); err != nil {
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
	sub.NextOrderAt = nextOrder

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

// anchorRenewalTime snaps a renewal timestamp forward to the next occurrence of
// hour:00 in loc, so every subscription renews in the same pre-dawn window and
// the whole day's orders land before staff arrive — rather than each sub firing
// at its own signup time-of-day throughout the day. Forward-only: the result is
// always at or after t, so a subscription is never charged before its period
// truly elapses (at most ~1 day later, once). A nil loc disables anchoring and
// returns t unchanged — callers that haven't opted in via WithRenewalAnchor
// (and all tests) keep the raw timestamp.
func anchorRenewalTime(t time.Time, loc *time.Location, hour int) time.Time {
	if loc == nil {
		return t
	}
	lt := t.In(loc)
	anchor := time.Date(lt.Year(), lt.Month(), lt.Day(), hour, 0, 0, 0, loc)
	if anchor.Before(t) {
		anchor = anchor.AddDate(0, 0, 1)
	}
	return anchor
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

// --- Skipping shipments ---

// SkipSubscriptionParams describes a skip request. Exactly one of the two forms
// must be set: Intervals skips that many upcoming shipments at the subscription's
// own cadence; ResumeOn names the day the customer wants shipments to start
// again. Both empty (or both set) is a bad request.
type SkipSubscriptionParams struct {
	Intervals int
	ResumeOn  *time.Time
}

func canSkipSubscription(status domain.SubscriptionStatus) bool {
	return status == domain.SubscriptionStatusActive
}

// SkipSubscription pushes a subscription's next shipment into the future
// without generating an order for the skipped window. It is the light-touch
// alternative to pausing: the subscription stays active, keeps its plan,
// variant and cadence, and simply resumes on its own.
//
// Mechanically the current period is stretched — current_period_start is left
// alone and current_period_end (with next_order_at anchored off it) moves out
// to the resume instant. The renewal path starts the next period at
// current_period_end, so once the skip elapses the subscription picks the
// cadence back up from the resume date rather than snapping back to the old
// calendar. No charge is made and no order is created for the skipped span:
// subscriptions bill per shipment, so a skipped shipment is simply not billed.
//
// Only active subscriptions can be skipped. A paused subscription already has
// no upcoming shipments, and a past-due one has an unpaid charge that must be
// resolved before its schedule means anything.
func (s *SubscriptionService) SkipSubscription(ctx context.Context, tx pgx.Tx, id uuid.UUID, p SkipSubscriptionParams, actor Actor) (*domain.Subscription, error) {
	sub, err := s.subscriptions.GetByIDAsStaff(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSubscriptionNotFound
		}
		return nil, fmt.Errorf("get subscription for skip: %w", err)
	}
	if !canSkipSubscription(sub.Status) {
		return nil, ErrSubscriptionNotSkippable
	}

	plan, err := s.subscriptions.GetPlanByID(ctx, tx, sub.PlanID)
	if err != nil {
		return nil, fmt.Errorf("get plan for skip: %w", err)
	}

	now := time.Now()
	resumeAt, err := resolveSkipResume(sub, plan, p, now)
	if err != nil {
		return nil, err
	}

	nextOrder := s.anchorRenewal(resumeAt)
	if err := s.subscriptions.UpdatePeriod(ctx, tx, id, sub.CurrentPeriodStart, resumeAt, nextOrder); err != nil {
		return nil, fmt.Errorf("skip subscription period: %w", err)
	}

	// Snapshot what the skip replaced, so a mistaken skip (the email offers a
	// one-click way out) is restored exactly rather than re-derived.
	undo := domain.SkipUndo{
		PeriodEnd:          sub.CurrentPeriodEnd,
		NextOrderAt:        sub.NextOrderAt,
		AppliedNextOrderAt: nextOrder,
	}
	if err := s.subscriptions.SetSkipUndo(ctx, tx, id, &undo); err != nil {
		return nil, fmt.Errorf("record skip undo: %w", err)
	}

	previousNextOrder := sub.NextOrderAt
	sub.CurrentPeriodEnd = resumeAt
	sub.NextOrderAt = nextOrder
	if sub.Metadata == nil {
		sub.Metadata = map[string]any{}
	}
	sub.Metadata[domain.SubscriptionMetaSkipUndo] = undo.Metadata()

	meta := map[string]any{
		"previous_next_order_at": previousNextOrder,
		"next_order_at":          nextOrder,
	}
	if p.Intervals > 0 {
		meta["skipped_shipments"] = p.Intervals
	} else {
		meta["resume_on"] = resumeAt
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditSubscriptionSkipped,
		ResourceType: "subscription",
		ResourceID:   id,
		After:        sub,
		Metadata:     meta,
	}); err != nil {
		return nil, fmt.Errorf("audit subscription skipped: %w", err)
	}

	return sub, nil
}

// resolveSkipResume validates a skip request and returns the instant the
// subscription should ship again. Kept separate from SkipSubscription so the
// date arithmetic is unit-testable without a database.
func resolveSkipResume(sub *domain.Subscription, plan *domain.SubscriptionPlan, p SkipSubscriptionParams, now time.Time) (time.Time, error) {
	switch {
	case p.Intervals > 0 && p.ResumeOn != nil:
		return time.Time{}, ErrInvalidSkipRequest
	case p.Intervals > 0:
		if p.Intervals > domain.SubscriptionMaxSkipIntervals {
			return time.Time{}, ErrSkipIntervalsOutOfRange
		}
		// Walk the cadence forward from the shipment being skipped, so each
		// skipped period is a real period rather than a flat multiple — plans
		// with a multi-unit interval count stay honest.
		resume := sub.CurrentPeriodEnd
		for i := 0; i < p.Intervals; i++ {
			resume = nextPeriodEnd(resume, plan.Interval, plan.IntervalCount)
		}
		// A subscription whose period end is already in the past (its renewals
		// are backlogged, or a worker has been failing) would otherwise land a
		// "skip" on a date that has already passed — and the next renewal sweep
		// would immediately bill the shipment the customer just asked to skip.
		// Re-walk from now in that case: N shipments skipped means N cadences
		// from today.
		if !resume.After(now) {
			resume = now
			for i := 0; i < p.Intervals; i++ {
				resume = nextPeriodEnd(resume, plan.Interval, plan.IntervalCount)
			}
		}
		return resume, nil
	case p.ResumeOn != nil:
		resume := *p.ResumeOn
		if !resume.After(now) || resume.After(now.AddDate(0, 0, domain.SubscriptionMaxSkipDays)) {
			return time.Time{}, ErrSkipDateOutOfRange
		}
		// A "skip" that pulls the next shipment forward isn't a skip — it would
		// bill the customer earlier than they agreed to. Rescheduling earlier is
		// a plan change, not a skip. Reported separately from the window check:
		// the date is perfectly reasonable, it just isn't later than the
		// shipment already booked, and the customer needs to be told which.
		if !resume.After(sub.NextOrderAt) {
			return time.Time{}, ErrSkipDateBeforeNextOrder
		}
		return resume, nil
	default:
		return time.Time{}, ErrInvalidSkipRequest
	}
}

// UndoSkip puts a skipped subscription back on the schedule it had before the
// skip — the "that wasn't meant to happen" path, reachable from the skip
// confirmation email, the customer's account page, and the admin detail page.
//
// It restores the exact snapshot the skip took rather than recomputing a date,
// and only while the subscription is still sitting on the date that skip set:
// if anything has moved the schedule since (a renewal, a resume, a plan change,
// a second skip), there is no longer a single skip to reverse and the undo is
// refused rather than guessing. A restored date that has already passed is
// refused too — undoing must never queue an immediate surprise charge.
func (s *SubscriptionService) UndoSkip(ctx context.Context, tx pgx.Tx, id uuid.UUID, actor Actor) (*domain.Subscription, error) {
	sub, err := s.subscriptions.GetByIDAsStaff(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSubscriptionNotFound
		}
		return nil, fmt.Errorf("get subscription for skip undo: %w", err)
	}
	if !canSkipSubscription(sub.Status) {
		return nil, ErrSubscriptionNotSkippable
	}

	undo, ok := sub.SkipUndo()
	if !ok || !undo.AppliedNextOrderAt.Equal(sub.NextOrderAt) {
		return nil, ErrNoSkipToUndo
	}
	if !undo.NextOrderAt.After(time.Now()) {
		return nil, ErrSkipUndoTooLate
	}

	if err := s.subscriptions.UpdatePeriod(ctx, tx, id, sub.CurrentPeriodStart, undo.PeriodEnd, undo.NextOrderAt); err != nil {
		return nil, fmt.Errorf("restore skipped period: %w", err)
	}
	if err := s.subscriptions.SetSkipUndo(ctx, tx, id, nil); err != nil {
		return nil, fmt.Errorf("clear skip undo: %w", err)
	}

	skippedTo := sub.NextOrderAt
	sub.CurrentPeriodEnd = undo.PeriodEnd
	sub.NextOrderAt = undo.NextOrderAt
	delete(sub.Metadata, domain.SubscriptionMetaSkipUndo)

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditSubscriptionSkipUndone,
		ResourceType: "subscription",
		ResourceID:   id,
		After:        sub,
		Metadata: map[string]any{
			"skipped_to":    skippedTo,
			"next_order_at": undo.NextOrderAt,
		},
	}); err != nil {
		return nil, fmt.Errorf("audit subscription skip undone: %w", err)
	}

	return sub, nil
}
