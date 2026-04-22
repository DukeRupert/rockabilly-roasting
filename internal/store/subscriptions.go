package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store/sqlcgen"
)

// SubscriptionStore provides database access for subscription plans and subscriptions.
type SubscriptionStore struct{}

// NewSubscriptionStore creates a new SubscriptionStore.
func NewSubscriptionStore() *SubscriptionStore {
	return &SubscriptionStore{}
}

// --- Subscription Plans ---

// CreatePlanParams holds the fields needed to create a subscription plan.
type CreatePlanParams struct {
	Name          string
	Interval      domain.SubscriptionInterval
	IntervalCount int
	DiscountPct   int
	IsActive      bool
	Metadata      map[string]any
}

// CreatePlan inserts a new subscription plan and returns it.
func (s *SubscriptionStore) CreatePlan(ctx context.Context, tx pgx.Tx, p CreatePlanParams) (*domain.SubscriptionPlan, error) {
	row, err := sqlcgen.New(tx).CreateSubscriptionPlan(ctx, sqlcgen.CreateSubscriptionPlanParams{
		ID:            uuid.New(),
		Name:          p.Name,
		Interval:      string(p.Interval),
		IntervalCount: int32(p.IntervalCount),
		DiscountPct:   int32(p.DiscountPct),
		IsActive:      p.IsActive,
		Metadata:      metadataToJSON(p.Metadata),
	})
	if err != nil {
		return nil, fmt.Errorf("insert subscription plan: %w", err)
	}
	return planFromRow(row), nil
}

// GetPlanByID returns a subscription plan by ID.
func (s *SubscriptionStore) GetPlanByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.SubscriptionPlan, error) {
	row, err := sqlcgen.New(tx).GetSubscriptionPlanByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get subscription plan %s: %w", id, err)
	}
	return planFromRow(row), nil
}

// ListPlans returns all subscription plans.
func (s *SubscriptionStore) ListPlans(ctx context.Context, tx pgx.Tx) ([]domain.SubscriptionPlan, error) {
	rows, err := sqlcgen.New(tx).ListSubscriptionPlans(ctx)
	if err != nil {
		return nil, fmt.Errorf("list subscription plans: %w", err)
	}
	plans := make([]domain.SubscriptionPlan, len(rows))
	for i, r := range rows {
		plans[i] = *planFromRow(r)
	}
	return plans, nil
}

// UpdatePlanActive sets the active status of a subscription plan.
func (s *SubscriptionStore) UpdatePlanActive(ctx context.Context, tx pgx.Tx, id uuid.UUID, isActive bool) error {
	err := sqlcgen.New(tx).UpdateSubscriptionPlanActive(ctx, sqlcgen.UpdateSubscriptionPlanActiveParams{
		ID:       id,
		IsActive: isActive,
	})
	if err != nil {
		return fmt.Errorf("update plan active: %w", err)
	}
	return nil
}

// UpdatePlanDiscount sets the discount percentage of a subscription plan.
func (s *SubscriptionStore) UpdatePlanDiscount(ctx context.Context, tx pgx.Tx, id uuid.UUID, discountPct int) error {
	err := sqlcgen.New(tx).UpdateSubscriptionPlanDiscount(ctx, sqlcgen.UpdateSubscriptionPlanDiscountParams{
		ID:          id,
		DiscountPct: int32(discountPct),
	})
	if err != nil {
		return fmt.Errorf("update plan discount: %w", err)
	}
	return nil
}

// ListActivePlans returns all active subscription plans.
func (s *SubscriptionStore) ListActivePlans(ctx context.Context, tx pgx.Tx) ([]domain.SubscriptionPlan, error) {
	rows, err := tx.Query(ctx,
		`SELECT id, name, interval, interval_count, discount_pct, is_active, metadata
		 FROM subscription_plans
		 WHERE is_active = true
		 ORDER BY interval_count, name`,
	)
	if err != nil {
		return nil, fmt.Errorf("list active plans: %w", err)
	}
	defer rows.Close()

	var plans []domain.SubscriptionPlan
	for rows.Next() {
		var p domain.SubscriptionPlan
		var interval string
		var metadata []byte
		if err := rows.Scan(&p.ID, &p.Name, &interval, &p.IntervalCount, &p.DiscountPct, &p.IsActive, &metadata); err != nil {
			return nil, fmt.Errorf("scan plan: %w", err)
		}
		p.Interval = domain.SubscriptionInterval(interval)
		p.Metadata = metadataFromJSON(metadata)
		plans = append(plans, p)
	}
	return plans, rows.Err()
}

// --- Subscriptions ---

// CreateSubscriptionParams holds the fields needed to create a subscription.
type CreateSubscriptionParams struct {
	CustomerID            uuid.UUID
	PlanID                uuid.UUID
	VariantID             uuid.UUID
	Quantity              int
	Status                domain.SubscriptionStatus
	ShippingAddressID     uuid.UUID
	StripePaymentMethodID *string
	CurrentPeriodStart    time.Time
	CurrentPeriodEnd      time.Time
	NextOrderAt           time.Time
	EndsAt                *time.Time
	Metadata              map[string]any
}

// Create inserts a new subscription and returns it.
func (s *SubscriptionStore) Create(ctx context.Context, tx pgx.Tx, p CreateSubscriptionParams) (*domain.Subscription, error) {
	row, err := sqlcgen.New(tx).CreateSubscription(ctx, sqlcgen.CreateSubscriptionParams{
		ID:                    uuid.New(),
		CustomerID:            p.CustomerID,
		PlanID:                p.PlanID,
		VariantID:             p.VariantID,
		Quantity:              int32(p.Quantity),
		Status:                string(p.Status),
		ShippingAddressID:     p.ShippingAddressID,
		CurrentPeriodStart:    p.CurrentPeriodStart,
		CurrentPeriodEnd:      p.CurrentPeriodEnd,
		NextOrderAt:           p.NextOrderAt,
		EndsAt:                timestampToPG(p.EndsAt),
		StripePaymentMethodID: p.StripePaymentMethodID,
		Metadata:              metadataToJSON(p.Metadata),
	})
	if err != nil {
		return nil, fmt.Errorf("insert subscription: %w", err)
	}
	return subscriptionFromRow(row), nil
}

// GetByIDAsStaff returns a subscription by ID (staff-only — no customer scoping).
func (s *SubscriptionStore) GetByIDAsStaff(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Subscription, error) {
	row, err := sqlcgen.New(tx).GetSubscriptionByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get subscription %s: %w", id, err)
	}
	return subscriptionFromRow(row), nil
}

// Get returns a subscription by ID scoped to a customer (storefront).
func (s *SubscriptionStore) Get(ctx context.Context, tx pgx.Tx, id, customerID uuid.UUID) (*domain.Subscription, error) {
	row, err := sqlcgen.New(tx).GetSubscriptionByIDAndCustomer(ctx, sqlcgen.GetSubscriptionByIDAndCustomerParams{
		ID:         id,
		CustomerID: customerID,
	})
	if err != nil {
		return nil, fmt.Errorf("get subscription %s for customer %s: %w", id, customerID, err)
	}
	return subscriptionFromRow(row), nil
}

// ListByCustomer returns all subscriptions for a customer.
func (s *SubscriptionStore) ListByCustomer(ctx context.Context, tx pgx.Tx, customerID uuid.UUID) ([]domain.Subscription, error) {
	rows, err := sqlcgen.New(tx).ListSubscriptionsByCustomer(ctx, customerID)
	if err != nil {
		return nil, fmt.Errorf("list subscriptions: %w", err)
	}
	subs := make([]domain.Subscription, len(rows))
	for i, r := range rows {
		subs[i] = *subscriptionFromRow(r)
	}
	return subs, nil
}

// UpdateStatus updates a subscription's status and returns it.
func (s *SubscriptionStore) UpdateStatus(ctx context.Context, tx pgx.Tx, id uuid.UUID, status domain.SubscriptionStatus) (*domain.Subscription, error) {
	row, err := sqlcgen.New(tx).UpdateSubscriptionStatus(ctx, sqlcgen.UpdateSubscriptionStatusParams{
		ID:     id,
		Status: string(status),
	})
	if err != nil {
		return nil, fmt.Errorf("update subscription status: %w", err)
	}
	return subscriptionFromRow(row), nil
}

// UpdatePeriod updates a subscription's billing period.
func (s *SubscriptionStore) UpdatePeriod(ctx context.Context, tx pgx.Tx, id uuid.UUID, periodStart, periodEnd, nextOrderAt time.Time) error {
	err := sqlcgen.New(tx).UpdateSubscriptionPeriod(ctx, sqlcgen.UpdateSubscriptionPeriodParams{
		ID:                 id,
		CurrentPeriodStart: periodStart,
		CurrentPeriodEnd:   periodEnd,
		NextOrderAt:        nextOrderAt,
	})
	if err != nil {
		return fmt.Errorf("update subscription period: %w", err)
	}
	return nil
}

// UpdatePauseUntil sets or clears the pause_until date.
func (s *SubscriptionStore) UpdatePauseUntil(ctx context.Context, tx pgx.Tx, id uuid.UUID, pauseUntil *time.Time) error {
	err := sqlcgen.New(tx).UpdateSubscriptionPauseUntil(ctx, sqlcgen.UpdateSubscriptionPauseUntilParams{
		ID:         id,
		PauseUntil: timestampToPG(pauseUntil),
	})
	if err != nil {
		return fmt.Errorf("update pause_until: %w", err)
	}
	return nil
}

// Cancel cancels a subscription.
func (s *SubscriptionStore) Cancel(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	if err := sqlcgen.New(tx).CancelSubscription(ctx, id); err != nil {
		return fmt.Errorf("cancel subscription: %w", err)
	}
	return nil
}

// ListDueForRenewal returns active subscriptions that are due for renewal.
func (s *SubscriptionStore) ListDueForRenewal(ctx context.Context, tx pgx.Tx) ([]domain.Subscription, error) {
	rows, err := sqlcgen.New(tx).ListSubscriptionsDueForRenewal(ctx)
	if err != nil {
		return nil, fmt.Errorf("list subscriptions due: %w", err)
	}
	subs := make([]domain.Subscription, len(rows))
	for i, r := range rows {
		subs[i] = *subscriptionFromRow(r)
	}
	return subs, nil
}

// CountByStatus returns the number of subscriptions with the given status.
func (s *SubscriptionStore) CountByStatus(ctx context.Context, tx pgx.Tx, status domain.SubscriptionStatus) (int, error) {
	var count int
	err := tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM subscriptions WHERE status = $1`,
		string(status),
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count subscriptions by status: %w", err)
	}
	return count, nil
}

// SubscriptionFilter holds optional filters for listing subscriptions.
type SubscriptionFilter struct {
	Status *domain.SubscriptionStatus
	Limit  int
	Offset int
}

// List returns subscriptions matching the given filter (hand-written for dynamic WHERE).
func (s *SubscriptionStore) List(ctx context.Context, tx pgx.Tx, f SubscriptionFilter) ([]domain.Subscription, error) {
	query := `SELECT id, customer_id, plan_id, variant_id, quantity, status, shipping_address_id,
	                 stripe_payment_method_id,
	                 current_period_start, current_period_end, next_order_at,
	                 ends_at, cancelled_at, pause_until, metadata, created_at, updated_at
	          FROM subscriptions WHERE true`
	args := []any{}
	argN := 1

	if f.Status != nil {
		query += fmt.Sprintf(" AND status = $%d", argN)
		args = append(args, string(*f.Status))
		argN++
	}

	query += " ORDER BY created_at DESC"

	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	query += fmt.Sprintf(" LIMIT $%d", argN)
	args = append(args, limit)
	argN++

	if f.Offset > 0 {
		query += fmt.Sprintf(" OFFSET $%d", argN)
		args = append(args, f.Offset)
	}

	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list subscriptions: %w", err)
	}
	defer rows.Close()

	var subs []domain.Subscription
	for rows.Next() {
		var sub domain.Subscription
		var status string
		var endsAt, cancelledAt, pauseUntil pgtype.Timestamptz
		var metadata []byte
		if err := rows.Scan(
			&sub.ID, &sub.CustomerID, &sub.PlanID, &sub.VariantID, &sub.Quantity, &status, &sub.ShippingAddressID,
			&sub.StripePaymentMethodID,
			&sub.CurrentPeriodStart, &sub.CurrentPeriodEnd, &sub.NextOrderAt,
			&endsAt, &cancelledAt, &pauseUntil, &metadata, &sub.CreatedAt, &sub.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan subscription: %w", err)
		}
		sub.Status = domain.SubscriptionStatus(status)
		sub.EndsAt = timestampFromPG(endsAt)
		sub.CancelledAt = timestampFromPG(cancelledAt)
		sub.PauseUntil = timestampFromPG(pauseUntil)
		sub.Metadata = metadataFromJSON(metadata)
		subs = append(subs, sub)
	}
	return subs, rows.Err()
}

// --- Subscription Orders ---

// CreateSubscriptionOrder links a subscription to an order for a billing period.
func (s *SubscriptionStore) CreateSubscriptionOrder(ctx context.Context, tx pgx.Tx, subscriptionID, orderID uuid.UUID, periodStart, periodEnd time.Time) error {
	err := sqlcgen.New(tx).CreateSubscriptionOrder(ctx, sqlcgen.CreateSubscriptionOrderParams{
		SubscriptionID: subscriptionID,
		OrderID:        orderID,
		PeriodStart:    periodStart,
		PeriodEnd:      periodEnd,
	})
	if err != nil {
		return fmt.Errorf("insert subscription order: %w", err)
	}
	return nil
}

// ListSubscriptionOrders returns all orders for a subscription.
func (s *SubscriptionStore) ListSubscriptionOrders(ctx context.Context, tx pgx.Tx, subscriptionID uuid.UUID) ([]domain.SubscriptionOrder, error) {
	rows, err := sqlcgen.New(tx).ListSubscriptionOrdersBySubscription(ctx, subscriptionID)
	if err != nil {
		return nil, fmt.Errorf("list subscription orders: %w", err)
	}
	orders := make([]domain.SubscriptionOrder, len(rows))
	for i, r := range rows {
		orders[i] = domain.SubscriptionOrder{
			SubscriptionID: r.SubscriptionID,
			OrderID:        r.OrderID,
			PeriodStart:    r.PeriodStart,
			PeriodEnd:      r.PeriodEnd,
		}
	}
	return orders, nil
}

// --- Row converters ---

func planFromRow(r sqlcgen.SubscriptionPlan) *domain.SubscriptionPlan {
	return &domain.SubscriptionPlan{
		ID:            r.ID,
		Name:          r.Name,
		Interval:      domain.SubscriptionInterval(r.Interval),
		IntervalCount: int(r.IntervalCount),
		DiscountPct:   int(r.DiscountPct),
		IsActive:      r.IsActive,
		Metadata:      metadataFromJSON(r.Metadata),
	}
}

func subscriptionFromRow(r sqlcgen.Subscription) *domain.Subscription {
	return &domain.Subscription{
		ID:                    r.ID,
		CustomerID:            r.CustomerID,
		PlanID:                r.PlanID,
		VariantID:             r.VariantID,
		Quantity:              int(r.Quantity),
		Status:                domain.SubscriptionStatus(r.Status),
		ShippingAddressID:     r.ShippingAddressID,
		StripePaymentMethodID: r.StripePaymentMethodID,
		CurrentPeriodStart:    r.CurrentPeriodStart,
		CurrentPeriodEnd:      r.CurrentPeriodEnd,
		NextOrderAt:           r.NextOrderAt,
		EndsAt:                timestampFromPG(r.EndsAt),
		CancelledAt:           timestampFromPG(r.CancelledAt),
		PauseUntil:            timestampFromPG(r.PauseUntil),
		Metadata:              metadataFromJSON(r.Metadata),
		CreatedAt:             r.CreatedAt,
		UpdatedAt:             r.UpdatedAt,
	}
}
