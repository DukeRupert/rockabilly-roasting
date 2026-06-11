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
type SubscriptionStore struct {
	metrics QueryRecorder
}

// NewSubscriptionStore creates a new SubscriptionStore. Pass nil for metrics
// to disable query timing instrumentation (e.g. in tests or one-off CLI tools).
func NewSubscriptionStore(metrics QueryRecorder) *SubscriptionStore {
	return &SubscriptionStore{metrics: metrics}
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
func (s *SubscriptionStore) CreatePlan(ctx context.Context, tx pgx.Tx, p CreatePlanParams) (_ *domain.SubscriptionPlan, err error) {
	defer trackQuery(s.metrics, "subscription_plans.create", time.Now(), &err)
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
func (s *SubscriptionStore) GetPlanByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (_ *domain.SubscriptionPlan, err error) {
	defer trackQuery(s.metrics, "subscription_plans.get_by_id", time.Now(), &err)
	row, err := sqlcgen.New(tx).GetSubscriptionPlanByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get subscription plan %s: %w", id, err)
	}
	return planFromRow(row), nil
}

// ListPlans returns all subscription plans.
func (s *SubscriptionStore) ListPlans(ctx context.Context, tx pgx.Tx) (_ []domain.SubscriptionPlan, err error) {
	defer trackQuery(s.metrics, "subscription_plans.list", time.Now(), &err)
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
func (s *SubscriptionStore) UpdatePlanActive(ctx context.Context, tx pgx.Tx, id uuid.UUID, isActive bool) (err error) {
	defer trackQuery(s.metrics, "subscription_plans.update_active", time.Now(), &err)
	err = sqlcgen.New(tx).UpdateSubscriptionPlanActive(ctx, sqlcgen.UpdateSubscriptionPlanActiveParams{
		ID:       id,
		IsActive: isActive,
	})
	if err != nil {
		return fmt.Errorf("update plan active: %w", err)
	}
	return nil
}

// UpdatePlanDiscount sets the discount percentage of a subscription plan.
func (s *SubscriptionStore) UpdatePlanDiscount(ctx context.Context, tx pgx.Tx, id uuid.UUID, discountPct int) (err error) {
	defer trackQuery(s.metrics, "subscription_plans.update_discount", time.Now(), &err)
	err = sqlcgen.New(tx).UpdateSubscriptionPlanDiscount(ctx, sqlcgen.UpdateSubscriptionPlanDiscountParams{
		ID:          id,
		DiscountPct: int32(discountPct),
	})
	if err != nil {
		return fmt.Errorf("update plan discount: %w", err)
	}
	return nil
}

// ListActivePlans returns all active subscription plans.
func (s *SubscriptionStore) ListActivePlans(ctx context.Context, tx pgx.Tx) (_ []domain.SubscriptionPlan, err error) {
	defer trackQuery(s.metrics, "subscription_plans.list_active", time.Now(), &err)
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
func (s *SubscriptionStore) Create(ctx context.Context, tx pgx.Tx, p CreateSubscriptionParams) (_ *domain.Subscription, err error) {
	defer trackQuery(s.metrics, "subscriptions.create", time.Now(), &err)
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
func (s *SubscriptionStore) GetByIDAsStaff(ctx context.Context, tx pgx.Tx, id uuid.UUID) (_ *domain.Subscription, err error) {
	defer trackQuery(s.metrics, "subscriptions.get_by_id", time.Now(), &err)
	row, err := sqlcgen.New(tx).GetSubscriptionByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get subscription %s: %w", id, err)
	}
	return subscriptionFromRow(row), nil
}

// Get returns a subscription by ID scoped to a customer (storefront).
func (s *SubscriptionStore) Get(ctx context.Context, tx pgx.Tx, id, customerID uuid.UUID) (_ *domain.Subscription, err error) {
	defer trackQuery(s.metrics, "subscriptions.get_by_id_and_customer", time.Now(), &err)
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
func (s *SubscriptionStore) ListByCustomer(ctx context.Context, tx pgx.Tx, customerID uuid.UUID) (_ []domain.Subscription, err error) {
	defer trackQuery(s.metrics, "subscriptions.list_by_customer", time.Now(), &err)
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
func (s *SubscriptionStore) UpdateStatus(ctx context.Context, tx pgx.Tx, id uuid.UUID, status domain.SubscriptionStatus) (_ *domain.Subscription, err error) {
	defer trackQuery(s.metrics, "subscriptions.update_status", time.Now(), &err)
	row, err := sqlcgen.New(tx).UpdateSubscriptionStatus(ctx, sqlcgen.UpdateSubscriptionStatusParams{
		ID:     id,
		Status: string(status),
	})
	if err != nil {
		return nil, fmt.Errorf("update subscription status: %w", err)
	}
	// Entering past_due is a (re)failure event. Drop any prior dashboard
	// acknowledgement so the alert re-surfaces on the next failed charge —
	// this is the single chokepoint every dunning path flows through, so no
	// failure branch can leave a stale ack behind. No-op when the key is absent.
	if status == domain.SubscriptionStatusPastDue {
		if _, err = tx.Exec(ctx,
			`UPDATE subscriptions SET metadata = metadata - 'dunning_acknowledged_at' WHERE id = $1`,
			id,
		); err != nil {
			return nil, fmt.Errorf("clear dunning ack: %w", err)
		}
	}
	return subscriptionFromRow(row), nil
}

// SetDunningAcknowledged stamps a past-due subscription as acknowledged so it
// drops off the dashboard's Urgent band. Scoped to status = 'past_due' so a
// race against a resolving renewal can't mark a now-active subscription. The
// stamp is cleared automatically by UpdateStatus on the next failed charge.
func (s *SubscriptionStore) SetDunningAcknowledged(ctx context.Context, tx pgx.Tx, id uuid.UUID) (err error) {
	defer trackQuery(s.metrics, "subscriptions.set_dunning_ack", time.Now(), &err)
	_, err = tx.Exec(ctx,
		`UPDATE subscriptions
		 SET metadata = jsonb_set(COALESCE(metadata, '{}'::jsonb), '{dunning_acknowledged_at}', to_jsonb(now())),
		     updated_at = now()
		 WHERE id = $1 AND status = 'past_due'`,
		id,
	)
	if err != nil {
		return fmt.Errorf("set dunning ack: %w", err)
	}
	return nil
}

// CountPastDueUnacknowledged counts past-due subscriptions that have not been
// acknowledged on the dashboard — the figure that drives the Urgent band's
// past-due alert.
func (s *SubscriptionStore) CountPastDueUnacknowledged(ctx context.Context, tx pgx.Tx) (_ int, err error) {
	defer trackQuery(s.metrics, "subscriptions.count_pastdue_unack", time.Now(), &err)
	var count int
	err = tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM subscriptions
		 WHERE status = 'past_due' AND (metadata->>'dunning_acknowledged_at') IS NULL`,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count past-due unacknowledged: %w", err)
	}
	return count, nil
}

// UpdatePeriod updates a subscription's billing period.
func (s *SubscriptionStore) UpdatePeriod(ctx context.Context, tx pgx.Tx, id uuid.UUID, periodStart, periodEnd, nextOrderAt time.Time) (err error) {
	defer trackQuery(s.metrics, "subscriptions.update_period", time.Now(), &err)
	err = sqlcgen.New(tx).UpdateSubscriptionPeriod(ctx, sqlcgen.UpdateSubscriptionPeriodParams{
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

// UpdateVariant changes a subscription's variant and returns the updated row.
func (s *SubscriptionStore) UpdateVariant(ctx context.Context, tx pgx.Tx, id, variantID uuid.UUID) (_ *domain.Subscription, err error) {
	defer trackQuery(s.metrics, "subscriptions.update_variant", time.Now(), &err)
	row, err := sqlcgen.New(tx).UpdateSubscriptionVariant(ctx, sqlcgen.UpdateSubscriptionVariantParams{
		ID:        id,
		VariantID: variantID,
	})
	if err != nil {
		return nil, fmt.Errorf("update subscription variant: %w", err)
	}
	return subscriptionFromRow(row), nil
}

// UpdatePlan swaps a subscription's plan and reschedules the current period.
// Callers compute the new period_end and next_order_at to keep cadence logic
// in the app layer.
func (s *SubscriptionStore) UpdatePlan(ctx context.Context, tx pgx.Tx, id, planID uuid.UUID, currentPeriodEnd, nextOrderAt time.Time) (_ *domain.Subscription, err error) {
	defer trackQuery(s.metrics, "subscriptions.update_plan", time.Now(), &err)
	row, err := sqlcgen.New(tx).UpdateSubscriptionPlan(ctx, sqlcgen.UpdateSubscriptionPlanParams{
		ID:               id,
		PlanID:           planID,
		CurrentPeriodEnd: currentPeriodEnd,
		NextOrderAt:      nextOrderAt,
	})
	if err != nil {
		return nil, fmt.Errorf("update subscription plan: %w", err)
	}
	return subscriptionFromRow(row), nil
}

// UpdatePauseUntil sets or clears the pause_until date.
func (s *SubscriptionStore) UpdatePauseUntil(ctx context.Context, tx pgx.Tx, id uuid.UUID, pauseUntil *time.Time) (err error) {
	defer trackQuery(s.metrics, "subscriptions.update_pause_until", time.Now(), &err)
	err = sqlcgen.New(tx).UpdateSubscriptionPauseUntil(ctx, sqlcgen.UpdateSubscriptionPauseUntilParams{
		ID:         id,
		PauseUntil: timestampToPG(pauseUntil),
	})
	if err != nil {
		return fmt.Errorf("update pause_until: %w", err)
	}
	return nil
}

// Cancel cancels a subscription.
func (s *SubscriptionStore) Cancel(ctx context.Context, tx pgx.Tx, id uuid.UUID) (err error) {
	defer trackQuery(s.metrics, "subscriptions.cancel", time.Now(), &err)
	if err := sqlcgen.New(tx).CancelSubscription(ctx, id); err != nil {
		return fmt.Errorf("cancel subscription: %w", err)
	}
	return nil
}

// ListDueForRenewal returns active subscriptions that are due for renewal.
func (s *SubscriptionStore) ListDueForRenewal(ctx context.Context, tx pgx.Tx) (_ []domain.Subscription, err error) {
	defer trackQuery(s.metrics, "subscriptions.list_due_for_renewal", time.Now(), &err)
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
func (s *SubscriptionStore) CountByStatus(ctx context.Context, tx pgx.Tx, status domain.SubscriptionStatus) (_ int, err error) {
	defer trackQuery(s.metrics, "subscriptions.count_by_status", time.Now(), &err)
	var count int
	err = tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM subscriptions WHERE status = $1`,
		string(status),
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count subscriptions by status: %w", err)
	}
	return count, nil
}

// subscriptionTermExpr is the SQL expression for the instant a subscription
// left the active base: cancelled_at when cancelled, ends_at when expired, NULL
// while it's still live (active / paused / past_due). Both branches fall back
// to updated_at when the dedicated timestamp is missing on older rows. Shared
// by ActiveSubscriptionsAsOf and ActiveSubscriptionDeltasByDay so the two agree
// on exactly when a subscription stops counting.
const subscriptionTermExpr = `CASE
		WHEN status = 'cancelled' THEN COALESCE(cancelled_at, updated_at)
		WHEN status = 'expired'   THEN COALESCE(ends_at, updated_at)
	END`

// ActiveSubscriptionsAsOf counts subscriptions that were live at the instant
// asOf — created before it and not yet cancelled or expired by then. "Live"
// spans active, paused, and past_due: a subscription leaves the base only when
// cancelled or expired. Pause spans aren't individually timestamped, so a
// paused subscription counts as live throughout; for a subscriber-base trend
// that's the intended reading. Used to seed the running total for the
// active-subscriptions-over-time chart.
func (s *SubscriptionStore) ActiveSubscriptionsAsOf(ctx context.Context, tx pgx.Tx, asOf time.Time) (_ int, err error) {
	defer trackQuery(s.metrics, "subscriptions.active_as_of", time.Now(), &err)
	var count int
	err = tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM subscriptions
		 WHERE created_at < $1
		   AND (`+subscriptionTermExpr+` IS NULL OR `+subscriptionTermExpr+` >= $1)`,
		asOf,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count active subscriptions as of: %w", err)
	}
	return count, nil
}

// ActiveSubscriptionDeltasByDay returns the net change in the live subscription
// base for each day in [from, to) (merchant timezone tz): a created subscription
// adds +1 on its creation day, a cancelled or expired one subtracts 1 on its
// termination day. Days with no change are omitted — callers seed with
// ActiveSubscriptionsAsOf(from) and carry the running total forward.
func (s *SubscriptionStore) ActiveSubscriptionDeltasByDay(ctx context.Context, tx pgx.Tx, from, to time.Time, tz *time.Location) (_ []domain.SubscriptionDelta, err error) {
	defer trackQuery(s.metrics, "subscriptions.active_deltas_by_day", time.Now(), &err)
	tzName := "UTC"
	if tz != nil {
		tzName = tz.String()
	}
	query := `
		WITH life AS (
			SELECT created_at, ` + subscriptionTermExpr + ` AS term_at
			FROM subscriptions
		),
		events AS (
			SELECT (created_at AT TIME ZONE $3)::date AS day, 1 AS delta
			FROM life
			WHERE created_at >= $1 AND created_at < $2
			UNION ALL
			SELECT (term_at AT TIME ZONE $3)::date AS day, -1 AS delta
			FROM life
			WHERE term_at IS NOT NULL AND term_at >= $1 AND term_at < $2
		)
		SELECT day, SUM(delta)::int AS net
		FROM events
		GROUP BY day
		ORDER BY day`
	rows, err := tx.Query(ctx, query, from, to, tzName)
	if err != nil {
		return nil, fmt.Errorf("active subscription deltas by day: %w", err)
	}
	defer rows.Close()
	var out []domain.SubscriptionDelta
	for rows.Next() {
		var d pgtype.Date
		var net int32
		if err := rows.Scan(&d, &net); err != nil {
			return nil, fmt.Errorf("scan active subscription delta: %w", err)
		}
		out = append(out, domain.SubscriptionDelta{Date: d.Time, Net: int(net)})
	}
	return out, rows.Err()
}

// SubscriptionSort identifies how the list query should order results.
type SubscriptionSort string

const (
	SubscriptionSortCreatedDesc   SubscriptionSort = "created_desc"
	SubscriptionSortCreatedAsc    SubscriptionSort = "created_asc"
	SubscriptionSortNextOrderAsc  SubscriptionSort = "next_order_asc"
	SubscriptionSortNextOrderDesc SubscriptionSort = "next_order_desc"
)

// SubscriptionFilter holds optional filters for listing subscriptions.
type SubscriptionFilter struct {
	Status                     *domain.SubscriptionStatus
	CustomerQuery              string // free-text match on customer name / email / company
	ExcludeDunningAcknowledged bool   // drop past-due rows already acknowledged on the dashboard
	Sort                       SubscriptionSort
	Limit                      int
	Offset                     int
}

// List returns subscriptions matching the given filter (hand-written for dynamic WHERE).
func (s *SubscriptionStore) List(ctx context.Context, tx pgx.Tx, f SubscriptionFilter) (_ []domain.Subscription, err error) {
	defer trackQuery(s.metrics, "subscriptions.list", time.Now(), &err)
	query := `SELECT s.id, s.customer_id, s.plan_id, s.variant_id, s.quantity, s.status, s.shipping_address_id,
	                 s.stripe_payment_method_id,
	                 s.current_period_start, s.current_period_end, s.next_order_at,
	                 s.ends_at, s.cancelled_at, s.pause_until, s.metadata, s.created_at, s.updated_at
	          FROM subscriptions s`
	args := []any{}
	argN := 1

	if f.CustomerQuery != "" {
		query += " JOIN customers c ON c.id = s.customer_id"
	}
	query += " WHERE true"

	if f.Status != nil {
		query += fmt.Sprintf(" AND s.status = $%d", argN)
		args = append(args, string(*f.Status))
		argN++
	}

	if f.CustomerQuery != "" {
		query += fmt.Sprintf(" AND (c.email ILIKE $%d OR c.first_name ILIKE $%d OR c.last_name ILIKE $%d OR (c.first_name || ' ' || c.last_name) ILIKE $%d OR c.company_name ILIKE $%d)", argN, argN, argN, argN, argN)
		args = append(args, "%"+f.CustomerQuery+"%")
		argN++
	}

	if f.ExcludeDunningAcknowledged {
		query += " AND (s.metadata->>'dunning_acknowledged_at') IS NULL"
	}

	switch f.Sort {
	case SubscriptionSortCreatedAsc:
		query += " ORDER BY s.created_at ASC"
	case SubscriptionSortNextOrderAsc:
		query += " ORDER BY s.next_order_at ASC"
	case SubscriptionSortNextOrderDesc:
		query += " ORDER BY s.next_order_at DESC"
	default:
		query += " ORDER BY s.created_at DESC"
	}

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
func (s *SubscriptionStore) CreateSubscriptionOrder(ctx context.Context, tx pgx.Tx, subscriptionID, orderID uuid.UUID, periodStart, periodEnd time.Time) (err error) {
	defer trackQuery(s.metrics, "subscription_orders.create", time.Now(), &err)
	err = sqlcgen.New(tx).CreateSubscriptionOrder(ctx, sqlcgen.CreateSubscriptionOrderParams{
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
func (s *SubscriptionStore) ListSubscriptionOrders(ctx context.Context, tx pgx.Tx, subscriptionID uuid.UUID) (_ []domain.SubscriptionOrder, err error) {
	defer trackQuery(s.metrics, "subscription_orders.list_by_subscription", time.Now(), &err)
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
