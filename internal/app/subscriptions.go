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

// --- Query methods ---

// GetSubscription returns a subscription by ID.
func (s *SubscriptionService) GetSubscription(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Subscription, error) {
	sub, err := s.subscriptions.GetByID(ctx, tx, id)
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

// ListDueForRenewal returns active subscriptions due for renewal.
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
	ShippingAddressID uuid.UUID
	Metadata          map[string]any
}

// CreateSubscription creates a new active subscription for a customer.
func (s *SubscriptionService) CreateSubscription(ctx context.Context, tx pgx.Tx, p CreateSubscriptionParams, actor Actor) (*domain.Subscription, error) {
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

	now := time.Now()
	periodEnd := nextPeriodEnd(now, plan.Interval, plan.IntervalCount)

	sub, err := s.subscriptions.Create(ctx, tx, store.CreateSubscriptionParams{
		CustomerID:         p.CustomerID,
		PlanID:             p.PlanID,
		VariantID:          p.VariantID,
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

// PauseSubscription pauses an active subscription. An optional pauseUntil date
// can be provided for automatic resume scheduling.
func (s *SubscriptionService) PauseSubscription(ctx context.Context, tx pgx.Tx, id uuid.UUID, pauseUntil *time.Time, actor Actor) (*domain.Subscription, error) {
	sub, err := s.subscriptions.GetByID(ctx, tx, id)
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
	sub, err := s.subscriptions.GetByID(ctx, tx, id)
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
	sub, err := s.subscriptions.GetByID(ctx, tx, id)
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

// MarkPastDue transitions an active subscription to past_due after a payment failure.
func (s *SubscriptionService) MarkPastDue(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Subscription, error) {
	sub, err := s.subscriptions.GetByID(ctx, tx, id)
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

// AdvancePeriod advances the subscription's billing period after a successful renewal.
func (s *SubscriptionService) AdvancePeriod(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Subscription, error) {
	sub, err := s.subscriptions.GetByID(ctx, tx, id)
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

func nextPeriodEnd(start time.Time, interval domain.SubscriptionInterval, count int) time.Time {
	switch interval {
	case domain.SubscriptionIntervalEvery2Minutes:
		return start.Add(time.Duration(2*count) * time.Minute)
	case domain.SubscriptionIntervalEvery14Days:
		return start.AddDate(0, 0, 14*count)
	case domain.SubscriptionIntervalEvery21Days:
		return start.AddDate(0, 0, 21*count)
	case domain.SubscriptionIntervalEvery30Days:
		return start.AddDate(0, 0, 30*count)
	case domain.SubscriptionIntervalEvery60Days:
		return start.AddDate(0, 0, 60*count)
	default:
		return start.AddDate(0, 0, 30*count)
	}
}
