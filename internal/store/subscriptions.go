package store

import (
	"context"
	"encoding/json"
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
	// Entering past_due is a (re)failure event. The dashboard alert this
	// acknowledgement fed is gone, but rows written before it was removed can
	// still carry the key, so keep stripping it here and let the data drain
	// itself. No-op when the key is absent.
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

// SetDunningHardDecline marks a past-due subscription whose card the issuer has
// permanently blocked, so no later renewal attempt charges that card again.
// declineCode is the issuer's reason, kept for staff and for the customer-facing
// copy; it may be empty when the provider gave a decline without one.
// deadPaymentMethods is every card known to be dead on this run — recording them
// is what lets the latch release when the customer puts a different one on file,
// and keeping the whole set is what stops a second dead card from making the
// first one look chargeable again.
//
// Scoped to status = 'past_due' so a race against a charge that succeeded in the
// meantime cannot brand a now-active subscription as dead.
func (s *SubscriptionStore) SetDunningHardDecline(ctx context.Context, tx pgx.Tx, id uuid.UUID, declineCode string, deadPaymentMethods []string) (err error) {
	defer trackQuery(s.metrics, "subscriptions.set_dunning_hard_decline", time.Now(), &err)
	// deadPaymentMethods is written whole rather than appended to, because the
	// caller has already merged the new card into the set it read. Doing the
	// merge here would need a read-modify-write inside the same statement for no
	// benefit — the caller is holding the subscription either way.
	if deadPaymentMethods == nil {
		deadPaymentMethods = []string{}
	}
	_, err = tx.Exec(ctx,
		`UPDATE subscriptions
		 SET metadata = jsonb_set(
		         jsonb_set(
		             jsonb_set(COALESCE(metadata, '{}'::jsonb), '{dunning_hard_decline}', 'true'::jsonb),
		             '{dunning_decline_code}', to_jsonb($2::text)),
		         '{dunning_dead_payment_methods}', to_jsonb($3::text[])),
		     updated_at = now()
		 WHERE id = $1 AND status = 'past_due'`,
		id, declineCode, deadPaymentMethods,
	)
	if err != nil {
		return fmt.Errorf("set dunning hard decline: %w", err)
	}
	return nil
}

// ReleaseDunningHardDecline lifts the hard-decline latch so the next renewal
// charge goes ahead. Called when the card on file is no longer the one that
// died, so that charge is judged on its own merits — the attempt count and the
// deadline keep running, because a new card is not yet a card that works.
//
// It removes the latch and nothing else. The dead card's ID and the issuer's
// reason stay put: they are the record that stops us charging that card again,
// and a release that erased them would let the very next rung fall back to it
// once the replacement card went away. Only ClearDunning, on a charge that
// actually succeeded, wipes the whole set.
func (s *SubscriptionStore) ReleaseDunningHardDecline(ctx context.Context, tx pgx.Tx, id uuid.UUID) (err error) {
	defer trackQuery(s.metrics, "subscriptions.release_dunning_hard_decline", time.Now(), &err)
	_, err = tx.Exec(ctx,
		`UPDATE subscriptions
		 SET metadata = metadata - 'dunning_hard_decline',
		     updated_at = now()
		 WHERE id = $1`,
		id,
	)
	if err != nil {
		return fmt.Errorf("release dunning hard decline: %w", err)
	}
	return nil
}

// SetDunningRetry records a failed renewal charge: it moves the subscription
// to past_due, schedules the next dunning retry by setting next_order_at (the
// renewal scheduler picks up past_due rows whose next_order_at is due), and
// stamps the running attempt count in metadata. The attempt count drives both
// the cap in the app layer (ExpireForDunning) and the escalating retry cadence.
// The legacy acknowledgement key is stripped for the reason given in UpdateStatus.
func (s *SubscriptionStore) SetDunningRetry(ctx context.Context, tx pgx.Tx, id uuid.UUID, nextRetryAt time.Time, attempt int) (err error) {
	defer trackQuery(s.metrics, "subscriptions.set_dunning_retry", time.Now(), &err)
	_, err = tx.Exec(ctx,
		`UPDATE subscriptions
		 SET status = 'past_due',
		     next_order_at = $2,
		     metadata = jsonb_set(COALESCE(metadata, '{}'::jsonb), '{dunning_attempt}', to_jsonb($3::int))
		                 - 'dunning_acknowledged_at',
		     updated_at = now()
		 WHERE id = $1`,
		id, nextRetryAt, attempt,
	)
	if err != nil {
		return fmt.Errorf("set dunning retry: %w", err)
	}
	return nil
}

// ExpireForDunning ends a subscription whose dunning retries are exhausted:
// status → expired, ends_at = now. This is involuntary churn (the card never
// recovered), distinct from a customer-initiated cancel. The attempt count is
// left in metadata as a record of why it ended.
func (s *SubscriptionStore) ExpireForDunning(ctx context.Context, tx pgx.Tx, id uuid.UUID) (err error) {
	defer trackQuery(s.metrics, "subscriptions.expire_for_dunning", time.Now(), &err)
	_, err = tx.Exec(ctx,
		`UPDATE subscriptions
		 SET status = 'expired', ends_at = now(), updated_at = now()
		 WHERE id = $1`,
		id,
	)
	if err != nil {
		return fmt.Errorf("expire for dunning: %w", err)
	}
	return nil
}

// ClearDunning removes every trace of dunning bookkeeping after a successful
// charge: the attempt counter, the hard-decline latch and its reason, and the
// legacy dashboard acknowledgement. Called on the renewal success path so a
// subscription that recovers starts clean if it ever fails again.
//
// Clearing the hard-decline latch is the important one — it is what puts a
// customer who added a working card back into the normal charge path. Leaving
// it set would silently stop charging a healthy subscription.
func (s *SubscriptionStore) ClearDunning(ctx context.Context, tx pgx.Tx, id uuid.UUID) (err error) {
	defer trackQuery(s.metrics, "subscriptions.clear_dunning", time.Now(), &err)
	_, err = tx.Exec(ctx,
		`UPDATE subscriptions
		 SET metadata = metadata - 'dunning_attempt' - 'dunning_acknowledged_at'
		                         - 'dunning_hard_decline' - 'dunning_decline_code'
		                         - 'dunning_dead_payment_method'
		                         - 'dunning_dead_payment_methods',
		     updated_at = now()
		 WHERE id = $1`,
		id,
	)
	if err != nil {
		return fmt.Errorf("clear dunning: %w", err)
	}
	return nil
}

// SetShippingGrandfathered sets or clears the shipping_grandfathered metadata
// flag, which the renewal engine reads to waive (or charge) renewal shipping.
// Staff toggle this per subscription from the admin detail page.
func (s *SubscriptionStore) SetShippingGrandfathered(ctx context.Context, tx pgx.Tx, id uuid.UUID, enabled bool) (err error) {
	defer trackQuery(s.metrics, "subscriptions.set_shipping_grandfathered", time.Now(), &err)
	if enabled {
		_, err = tx.Exec(ctx,
			`UPDATE subscriptions
			 SET metadata = jsonb_set(COALESCE(metadata, '{}'::jsonb), $2, 'true'::jsonb),
			     updated_at = now()
			 WHERE id = $1`,
			id, []string{domain.SubscriptionMetaShippingGrandfathered},
		)
	} else {
		_, err = tx.Exec(ctx,
			`UPDATE subscriptions
			 SET metadata = metadata - $2, updated_at = now()
			 WHERE id = $1`,
			id, domain.SubscriptionMetaShippingGrandfathered,
		)
	}
	if err != nil {
		return fmt.Errorf("set shipping grandfathered: %w", err)
	}
	return nil
}

// SetSkipUndo stores (or clears, when undo is nil) the schedule snapshot a skip
// replaced. Written in the same transaction as the skip itself so a subscription
// is never left showing a skipped date with no way back.
func (s *SubscriptionStore) SetSkipUndo(ctx context.Context, tx pgx.Tx, id uuid.UUID, undo *domain.SkipUndo) (err error) {
	defer trackQuery(s.metrics, "subscriptions.set_skip_undo", time.Now(), &err)
	if undo == nil {
		_, err = tx.Exec(ctx,
			`UPDATE subscriptions
			 SET metadata = metadata - $2, updated_at = now()
			 WHERE id = $1`,
			id, domain.SubscriptionMetaSkipUndo,
		)
	} else {
		// Marshalled here rather than handed over as a map: the parameter has
		// no declared type on the wire, and jsonb_set needs actual JSON.
		payload, mErr := json.Marshal(undo.Metadata())
		if mErr != nil {
			return fmt.Errorf("marshal skip undo: %w", mErr)
		}
		_, err = tx.Exec(ctx,
			`UPDATE subscriptions
			 SET metadata = jsonb_set(COALESCE(metadata, '{}'::jsonb), $2, $3::jsonb),
			     updated_at = now()
			 WHERE id = $1`,
			id, []string{domain.SubscriptionMetaSkipUndo}, payload,
		)
	}
	if err != nil {
		return fmt.Errorf("set skip undo: %w", err)
	}
	return nil
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

// ClearEndsAt removes a subscription's end date.
//
// The counterpart ExpireForDunning has existed since June; this did not, which
// meant a subscription could be put beyond the renewal scheduler's reach by the
// application but never brought back by it. The only way out was manual SQL,
// and manual SQL is how three subscriptions ended up live-but-unbillable with
// no audit trail. Anything that returns a subscription to a billing status must
// call this.
func (s *SubscriptionStore) ClearEndsAt(ctx context.Context, tx pgx.Tx, id uuid.UUID) (err error) {
	defer trackQuery(s.metrics, "subscriptions.clear_ends_at", time.Now(), &err)
	_, err = tx.Exec(ctx,
		`UPDATE subscriptions SET ends_at = NULL, updated_at = now() WHERE id = $1`,
		id,
	)
	if err != nil {
		return fmt.Errorf("clear ends_at: %w", err)
	}
	return nil
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
// ListDueForRenewal returns subscriptions due to be charged now — both fresh
// active renewals and past_due subscriptions whose next dunning retry has come
// due (see ListSubscriptionsDueForRenewal).
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
	SubscriptionSortCustomerAsc   SubscriptionSort = "customer_asc"
	SubscriptionSortCustomerDesc  SubscriptionSort = "customer_desc"
)

// SubscriptionFilter holds optional filters for listing subscriptions.
type SubscriptionFilter struct {
	Status        *domain.SubscriptionStatus
	PlanID        *uuid.UUID
	ProductID     *uuid.UUID // the coffee behind the subscribed variant
	NextOrderFrom *time.Time // inclusive lower bound on next_order_at
	NextOrderTo   *time.Time // inclusive upper bound on next_order_at
	CustomerQuery string     // free-text match on customer name / email / company
	Sort          SubscriptionSort
	Limit         int
	Offset        int
}

// subscriptionCustomerNameExpr orders by the same string the list renders:
// the person's name, falling back to company then email when the name is
// blank. Sorting on first_name alone would strand every company-only account
// at the top under an empty string.
const subscriptionCustomerNameExpr = `lower(coalesce(nullif(trim(c.first_name || ' ' || c.last_name), ''), nullif(c.company_name, ''), c.email))`

// needsCustomerJoin reports whether the query has to reach into customers —
// either to search them or to order by the displayed name.
func (f SubscriptionFilter) needsCustomerJoin() bool {
	return f.CustomerQuery != "" ||
		f.Sort == SubscriptionSortCustomerAsc ||
		f.Sort == SubscriptionSortCustomerDesc
}

// subscriptionFrom builds the FROM clause and only the joins the filter earns.
// Every selected column is s.-qualified, so adding a join can't collide with a
// same-named column on customers or variants.
func (f SubscriptionFilter) subscriptionFrom() string {
	q := " FROM subscriptions s"
	if f.needsCustomerJoin() {
		q += " JOIN customers c ON c.id = s.customer_id"
	}
	if f.ProductID != nil {
		q += " JOIN variants v ON v.id = s.variant_id"
	}
	return q
}

// subscriptionWhere builds the WHERE clause shared by List, Count, and
// CountsByStatus, appending placeholders from argN onward. One builder, not
// three copies: a filter added to the list and missed in the count is how the
// "of N" total starts contradicting the rows on screen, and how the status
// pill counts drift from the list beneath them.
//
// skipStatus omits the status predicate — CountsByStatus varies that dimension
// itself, so each pill's number is exactly what clicking it would show.
func subscriptionWhere(f SubscriptionFilter, argN int, skipStatus bool) (string, []any, int) {
	clause := " WHERE true"
	args := []any{}

	if f.Status != nil && !skipStatus {
		clause += fmt.Sprintf(" AND s.status = $%d", argN)
		args = append(args, string(*f.Status))
		argN++
	}

	if f.PlanID != nil {
		clause += fmt.Sprintf(" AND s.plan_id = $%d", argN)
		args = append(args, *f.PlanID)
		argN++
	}

	if f.ProductID != nil {
		clause += fmt.Sprintf(" AND v.product_id = $%d", argN)
		args = append(args, *f.ProductID)
		argN++
	}

	if f.NextOrderFrom != nil {
		clause += fmt.Sprintf(" AND s.next_order_at >= $%d", argN)
		args = append(args, *f.NextOrderFrom)
		argN++
	}

	if f.NextOrderTo != nil {
		clause += fmt.Sprintf(" AND s.next_order_at <= $%d", argN)
		args = append(args, *f.NextOrderTo)
		argN++
	}

	if f.CustomerQuery != "" {
		clause += fmt.Sprintf(" AND (c.email ILIKE $%d OR c.first_name ILIKE $%d OR c.last_name ILIKE $%d OR (c.first_name || ' ' || c.last_name) ILIKE $%d OR c.company_name ILIKE $%d)", argN, argN, argN, argN, argN)
		args = append(args, "%"+f.CustomerQuery+"%")
		argN++
	}

	return clause, args, argN
}

// subscriptionOrderBy maps the closed sort enum onto an ORDER BY. Callers pass
// the enum, never a raw identifier, so no query param can reach the clause.
func subscriptionOrderBy(sort SubscriptionSort) string {
	switch sort {
	case SubscriptionSortCreatedAsc:
		return " ORDER BY s.created_at ASC"
	case SubscriptionSortNextOrderAsc:
		return " ORDER BY s.next_order_at ASC"
	case SubscriptionSortNextOrderDesc:
		return " ORDER BY s.next_order_at DESC"
	case SubscriptionSortCustomerAsc:
		return " ORDER BY " + subscriptionCustomerNameExpr + " ASC, s.created_at DESC"
	case SubscriptionSortCustomerDesc:
		return " ORDER BY " + subscriptionCustomerNameExpr + " DESC, s.created_at DESC"
	default:
		return " ORDER BY s.created_at DESC"
	}
}

// List returns subscriptions matching the given filter (hand-written for dynamic WHERE).
func (s *SubscriptionStore) List(ctx context.Context, tx pgx.Tx, f SubscriptionFilter) (_ []domain.Subscription, err error) {
	defer trackQuery(s.metrics, "subscriptions.list", time.Now(), &err)
	query := `SELECT s.id, s.customer_id, s.plan_id, s.variant_id, s.quantity, s.status, s.shipping_address_id,
	                 s.stripe_payment_method_id,
	                 s.current_period_start, s.current_period_end, s.next_order_at,
	                 s.ends_at, s.cancelled_at, s.pause_until, s.metadata, s.created_at, s.updated_at`
	query += f.subscriptionFrom()

	where, args, argN := subscriptionWhere(f, 1, false)
	query += where
	query += subscriptionOrderBy(f.Sort)

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

// Count returns how many subscriptions match the filter, ignoring Limit and
// Offset. Shares List's WHERE builder so the "of N" total can't disagree with
// the rows.
func (s *SubscriptionStore) Count(ctx context.Context, tx pgx.Tx, f SubscriptionFilter) (_ int, err error) {
	defer trackQuery(s.metrics, "subscriptions.count", time.Now(), &err)
	query := "SELECT COUNT(*)" + f.subscriptionFrom()
	where, args, _ := subscriptionWhere(f, 1, false)
	query += where

	var count int
	if err = tx.QueryRow(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count subscriptions: %w", err)
	}
	return count, nil
}

// CountsByStatus returns the number of subscriptions per status under every
// filter dimension except status itself — the numbers behind the status pills.
// One grouped query rather than one count per pill: six round trips on every
// page load, all reading the same rows, would be the same answer for more work.
func (s *SubscriptionStore) CountsByStatus(ctx context.Context, tx pgx.Tx, f SubscriptionFilter) (_ map[domain.SubscriptionStatus]int, err error) {
	defer trackQuery(s.metrics, "subscriptions.counts_by_status", time.Now(), &err)
	query := "SELECT s.status, COUNT(*)" + f.subscriptionFrom()
	where, args, _ := subscriptionWhere(f, 1, true)
	query += where + " GROUP BY s.status"

	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("count subscriptions by status: %w", err)
	}
	defer rows.Close()

	counts := map[domain.SubscriptionStatus]int{}
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return nil, fmt.Errorf("scan subscription status count: %w", err)
		}
		counts[domain.SubscriptionStatus(status)] = n
	}
	return counts, rows.Err()
}

// SubscribedProduct is one coffee that at least one subscription points at,
// with how many subscriptions that is.
type SubscribedProduct struct {
	ID    uuid.UUID
	Title string
	Count int
}

// ListSubscribedProducts returns the products behind existing subscriptions,
// most-subscribed first. This is the option list for the admin coffee filter:
// derived from what customers actually subscribe to rather than from the
// subscribable catalog, so a coffee that has since been marked unsubscribable
// stays filterable, and a subscribable coffee nobody has taken up doesn't
// offer staff an option that can only return nothing.
func (s *SubscriptionStore) ListSubscribedProducts(ctx context.Context, tx pgx.Tx) (_ []SubscribedProduct, err error) {
	defer trackQuery(s.metrics, "subscriptions.list_subscribed_products", time.Now(), &err)
	rows, err := tx.Query(ctx, `SELECT p.id, p.title, COUNT(*)
	          FROM subscriptions s
	          JOIN variants v ON v.id = s.variant_id
	          JOIN products p ON p.id = v.product_id
	          GROUP BY p.id, p.title
	          ORDER BY COUNT(*) DESC, p.title ASC`)
	if err != nil {
		return nil, fmt.Errorf("list subscribed products: %w", err)
	}
	defer rows.Close()

	var out []SubscribedProduct
	for rows.Next() {
		var p SubscribedProduct
		if err := rows.Scan(&p.ID, &p.Title, &p.Count); err != nil {
			return nil, fmt.Errorf("scan subscribed product: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
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

// ForecastRenewals rolls up the subscription renewals due in [from, to) by
// product: how many subscriptions bill, how many bags that is, and what it
// weighs. Ordered heaviest first — the roast schedule is built around the big
// batches.
//
// Only 'active' subscriptions count. ListSubscriptionsDueForRenewal also picks
// up 'past_due' (where next_order_at is the next dunning retry), but those are
// charges that have already failed at least once and may fail again. Roasting
// against them over-roasts, and green bought for coffee nobody paid for is the
// expensive mistake here. The card says "active" so the omission is visible.
//
// The ends_at clause is belt and braces, and says so rather than pretending to
// work: migration 075 added CHECK (ends_at IS NULL OR status NOT IN ('active',
// 'past_due')) after a past ends_at silently stopped three live subscriptions
// billing, so on an active row ends_at is now provably NULL and this predicate
// can never fire. It stays because it costs nothing and because the guard it
// duplicates lives in the schema, not here — if that CHECK is ever relaxed, the
// right comparison is against each subscription's own next_order_at rather than
// the window edge, so a subscription ending mid-window still ships the renewal
// that falls before it ends.
func (s *SubscriptionStore) ForecastRenewals(ctx context.Context, tx pgx.Tx, from, to time.Time) (_ []domain.RenewalForecastLine, err error) {
	defer trackQuery(s.metrics, "subscriptions.forecast_renewals", time.Now(), &err)

	const query = `
		SELECT p.id, p.title,
		       COUNT(*)::int                                                          AS subscriptions,
		       COALESCE(SUM(s.quantity), 0)::int                                      AS units,
		       COALESCE(SUM(s.quantity * COALESCE(v.weight_grams, 0)), 0)::int        AS weight_grams,
		       COALESCE(SUM(s.quantity) FILTER (WHERE v.weight_grams IS NULL), 0)::int AS units_missing_weight
		FROM subscriptions s
		JOIN variants v ON v.id = s.variant_id
		JOIN products p ON p.id = v.product_id
		WHERE s.status = 'active'
		  AND (s.ends_at IS NULL OR s.ends_at > s.next_order_at)
		  AND s.next_order_at >= $1
		  AND s.next_order_at <  $2
		GROUP BY p.id, p.title
		ORDER BY weight_grams DESC, units DESC, p.title ASC`

	rows, err := tx.Query(ctx, query, from, to)
	if err != nil {
		return nil, fmt.Errorf("forecast renewals: %w", err)
	}
	defer rows.Close()

	var out []domain.RenewalForecastLine
	for rows.Next() {
		var l domain.RenewalForecastLine
		if err := rows.Scan(&l.ProductID, &l.Title, &l.Subscriptions, &l.Units, &l.WeightGrams, &l.UnitsMissingWeight); err != nil {
			return nil, fmt.Errorf("scan renewal forecast line: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
