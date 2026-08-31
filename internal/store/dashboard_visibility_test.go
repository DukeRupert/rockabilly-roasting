package store_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// markedReadyAt puts an order into ready_for_pickup and writes the audit row
// the queries read the timestamp from — the same pair MarkReadyForPickup writes
// in one transaction.
func markedReadyAt(t *testing.T, tx pgx.Tx, orderID uuid.UUID, at time.Time) {
	t.Helper()
	ctx := t.Context()
	_, err := tx.Exec(ctx,
		`UPDATE orders SET fulfillment_status = 'ready_for_pickup', status = 'processing' WHERE id = $1`,
		orderID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx,
		`INSERT INTO audit_log (actor_type, actor_name, action, resource_type, resource_id, metadata, created_at)
		 VALUES ('staff', 'fixture', 'order.ready_for_pickup', 'order', $1, '{}'::jsonb, $2)`,
		orderID, at)
	require.NoError(t, err)
}

func newPickupOrder(t *testing.T, tx pgx.Tx) uuid.UUID {
	t.Helper()
	customer := testutil.CreateCustomer(t, tx)
	addr := testutil.CreateAddress(t, tx, customer.ID)
	order := testutil.CreateOrder(t, tx, customer.ID, addr.ID, addr.ID)
	return order.ID
}

// createSubscriptionPlanFixture writes a cadence. Plans stopped carrying a
// variant at migration 017, so this is just the schedule.
func createSubscriptionPlanFixture(t *testing.T, tx pgx.Tx) struct{ ID uuid.UUID } {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, tx.QueryRow(t.Context(),
		`INSERT INTO subscription_plans (name, interval, interval_count, is_active)
		 VALUES ($1, 'month', 1, true) RETURNING id`,
		"Plan "+uuid.NewString()[:8]).Scan(&id))
	return struct{ ID uuid.UUID }{id}
}

func setVariantWeight(t *testing.T, tx pgx.Tx, variantID uuid.UUID, grams int) {
	t.Helper()
	_, err := tx.Exec(t.Context(),
		`UPDATE variants SET weight_grams = $2 WHERE id = $1`, variantID, grams)
	require.NoError(t, err)
}

// newSubscription writes one straight to the table. The service path would drag
// in payment setup these queries do not care about; what they read is status,
// variant, quantity and next_order_at.
func newSubscription(t *testing.T, tx pgx.Tx, customerID, addressID, planID, variantID uuid.UUID,
	quantity int, status string, nextOrderAt time.Time) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, tx.QueryRow(t.Context(),
		`INSERT INTO subscriptions
		     (customer_id, plan_id, status, shipping_address_id, variant_id, quantity,
		      current_period_start, current_period_end, next_order_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $7)
		 RETURNING id`,
		customerID, planID, status, addressID, variantID, quantity,
		nextOrderAt, nextOrderAt.AddDate(0, 1, 0)).Scan(&id))
	return id
}

// The pickup queries have never had a test, and the "ready at" they depend on
// lives in the audit log rather than on the order — a join that would silently
// return nothing if the action string or resource type ever drifted.
func TestWaitingPickups(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := t.Context()
	orders := store.NewOrderStore(nil)

	now := time.Date(2026, time.September, 10, 12, 0, 0, 0, time.UTC)
	cutoff := now.Add(-72 * time.Hour)

	stale := newPickupOrder(t, tx)
	markedReadyAt(t, tx, stale, now.Add(-11*24*time.Hour))

	older := newPickupOrder(t, tx)
	markedReadyAt(t, tx, older, now.Add(-20*24*time.Hour))

	// Ready this morning: real, but not yet worth chasing.
	fresh := newPickupOrder(t, tx)
	markedReadyAt(t, tx, fresh, now.Add(-2*time.Hour))

	// Cancelled after being marked ready. The bag is not waiting for anyone.
	cancelled := newPickupOrder(t, tx)
	markedReadyAt(t, tx, cancelled, now.Add(-30*24*time.Hour))
	_, err := tx.Exec(ctx, `UPDATE orders SET status = 'cancelled' WHERE id = $1`, cancelled)
	require.NoError(t, err)

	// A wholesale account set to pickup. Reachable, but /admin/orders — where
	// this row's link goes — is the retail channel, so counting it would send
	// staff to a list that does not contain it.
	wholesale := newPickupOrder(t, tx)
	markedReadyAt(t, tx, wholesale, now.Add(-15*24*time.Hour))
	_, err = tx.Exec(ctx, `UPDATE orders SET channel = 'wholesale' WHERE id = $1`, wholesale)
	require.NoError(t, err)

	// The customer came in and took it. This is the case the fulfillment_status
	// predicate exists for, and the only one it catches on its own: MarkPickedUp
	// leaves status = 'complete', which is not in the query's
	// NOT IN ('cancelled','refunded') list, so nothing else here would drop it.
	// Its audit row survives collection, and it is the oldest of the lot — miss
	// this and the dashboard chases customers who already have their coffee.
	collected := newPickupOrder(t, tx)
	markedReadyAt(t, tx, collected, now.Add(-40*24*time.Hour))
	_, err = tx.Exec(ctx,
		`UPDATE orders SET fulfillment_status = 'delivered', status = 'complete' WHERE id = $1`,
		collected)
	require.NoError(t, err)

	count, err := orders.CountWaitingPickups(ctx, tx, cutoff)
	require.NoError(t, err)
	assert.Equal(t, 2, count,
		"only the two retail bags still waiting past the threshold — not the fresh one, the cancelled one, the wholesale one, or the one already collected")

	list, err := orders.ListWaitingPickups(ctx, tx, cutoff, 5)
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, older, list[0].OrderID, "longest wait first — that is the phone call to make")
	assert.Equal(t, now.Add(-20*24*time.Hour).UTC(), list[0].ReadyAt.UTC(),
		"the timestamp is the audit row's, so it survives later edits to the order")

	// The dashboard asks for exactly one.
	oldest, err := orders.ListWaitingPickups(ctx, tx, cutoff, 1)
	require.NoError(t, err)
	require.Len(t, oldest, 1)
	assert.Equal(t, older, oldest[0].OrderID)
}

// A pickup order with no audit row cannot be dated, so it cannot be aged, so it
// does not appear.
//
// This pins the behaviour, not the join: a LEFT JOIN would leave ra.at NULL,
// the ra.at < $1 comparison would be NULL, and the row would drop out anyway.
// What it catches is a future writer that sets ready_for_pickup without
// auditing — those orders would silently never reach the dashboard, and the
// count here says so out loud.
func TestWaitingPickupsNeedsTheAuditRow(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := t.Context()
	orders := store.NewOrderStore(nil)

	id := newPickupOrder(t, tx)
	_, err := tx.Exec(ctx,
		`UPDATE orders SET fulfillment_status = 'ready_for_pickup', status = 'processing' WHERE id = $1`, id)
	require.NoError(t, err)

	count, err := orders.CountWaitingPickups(ctx, tx, time.Now())
	require.NoError(t, err)
	assert.Equal(t, 0, count, "undateable, so it cannot be aged")
}

// The roast forecast, which decides how much green gets bought. It rolls up by
// product rather than variant, weighs by variant, and must not count anything
// the shop has not been paid for.
func TestForecastRenewals(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := t.Context()
	subs := store.NewSubscriptionStore(nil)

	from := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 14)

	customer := testutil.CreateCustomer(t, tx)
	addr := testutil.CreateAddress(t, tx, customer.ID)
	plan := createSubscriptionPlanFixture(t, tx)

	product := testutil.CreateProduct(t, tx, testutil.WithProductTitle("Rebel Blend"))
	twelveOz := testutil.CreateVariant(t, tx, product.ID)
	setVariantWeight(t, tx, twelveOz.ID, 340)
	// A second size of the same coffee. The roast is of the coffee, not the
	// bag it ends up in, so both sizes have to land on one line — with only one
	// variant in play, grouping by variant would look identical and this test
	// would not notice.
	fivePound := testutil.CreateVariant(t, tx, product.ID)
	setVariantWeight(t, tx, fivePound.ID, 2270)

	// A second coffee, lighter, to give the heaviest-first ordering something
	// to order.
	other := testutil.CreateProduct(t, tx, testutil.WithProductTitle("Iron Horse"))
	otherBag := testutil.CreateVariant(t, tx, other.ID)
	setVariantWeight(t, tx, otherBag.ID, 250)
	newSubscription(t, tx, customer.ID, addr.ID, plan.ID, otherBag.ID, 1, "active", from.AddDate(0, 0, 6))

	// Two subscriptions on the same coffee, one of them for two bags: the
	// forecast is per coffee, not per subscription.
	newSubscription(t, tx, customer.ID, addr.ID, plan.ID, twelveOz.ID, 1, "active", from.AddDate(0, 0, 3))
	newSubscription(t, tx, customer.ID, addr.ID, plan.ID, twelveOz.ID, 2, "active", from.AddDate(0, 0, 5))
	newSubscription(t, tx, customer.ID, addr.ID, plan.ID, fivePound.ID, 1, "active", from.AddDate(0, 0, 7))

	// past_due is a charge that already failed. Roasting against it buys green
	// for coffee nobody has paid for.
	newSubscription(t, tx, customer.ID, addr.ID, plan.ID, twelveOz.ID, 5, "past_due", from.AddDate(0, 0, 4))

	// Outside the window.
	newSubscription(t, tx, customer.ID, addr.ID, plan.ID, twelveOz.ID, 9, "active", to.AddDate(0, 0, 1))

	lines, err := subs.ForecastRenewals(ctx, tx, from, to)
	require.NoError(t, err)
	require.Len(t, lines, 2, "one line per coffee, however many sizes or subscriptions")

	assert.Equal(t, "Rebel Blend", lines[0].Title, "heaviest first — the roast schedule is built around the big batches")
	assert.Equal(t, "Iron Horse", lines[1].Title)

	line := lines[0]
	assert.Equal(t, 3, line.Subscriptions, "two on the 12oz, one on the five-pound")
	assert.Equal(t, 4, line.Units, "one bag plus two, plus the five-pound")
	assert.Equal(t, 3*340+2270, line.WeightGrams, "both sizes weighed at their own weight")
	assert.Zero(t, line.UnitsMissingWeight)
}

// A variant with no weight recorded must be called out, not swallowed: a roast
// plan that is quietly short is worse than one that flags itself.
func TestForecastRenewalsFlagsMissingWeight(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := t.Context()
	subs := store.NewSubscriptionStore(nil)

	from := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 14)

	customer := testutil.CreateCustomer(t, tx)
	addr := testutil.CreateAddress(t, tx, customer.ID)
	plan := createSubscriptionPlanFixture(t, tx)
	product := testutil.CreateProduct(t, tx, testutil.WithProductTitle("Unweighed"))
	variant := testutil.CreateVariant(t, tx, product.ID)

	newSubscription(t, tx, customer.ID, addr.ID, plan.ID, variant.ID, 4, "active", from.AddDate(0, 0, 2))

	lines, err := subs.ForecastRenewals(ctx, tx, from, to)
	require.NoError(t, err)
	require.Len(t, lines, 1)
	assert.Equal(t, 4, lines[0].Units)
	assert.Zero(t, lines[0].WeightGrams, "no weight to add")
	assert.Equal(t, 4, lines[0].UnitsMissingWeight, "and the shortfall says so")
}
