package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// postponementService wires just enough of CheckoutService to exercise the
// postponement path — it touches shipping config, orders, and audit, nothing
// else.
func postponementService() *app.CheckoutService {
	return app.NewCheckoutService(
		store.NewOrderStore(nil),
		store.NewCustomerStore(),
		nil, nil,
		store.NewShippingStore(),
		nil,
		audit.NewAuditWriter(),
		metrics.NewRegistry(),
	)
}

// staffActorFixture is the staffer doing the postponing.
func staffActorFixture() app.Actor {
	return app.Actor{Type: domain.AuditActorTypeStaff, Name: "Test Staff"}
}

// merchantTZ matches the zone the cutoff and the schedule are evaluated in.
func merchantTZ(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)
	return loc
}

// laborDay2026 is Monday September 7th, a real run day on the Mon/Thu route.
func laborDay2026(loc *time.Location) (holiday, tuesday time.Time) {
	return time.Date(2026, time.September, 7, 0, 0, 0, 0, loc),
		time.Date(2026, time.September, 8, 0, 0, 0, 0, loc)
}

// TestPostponeDeliveryRunMovesQuotesAndOrders is the whole feature end to end:
// after staff move a run, the date customers are quoted changes *and* the
// orders already promised the old day follow it.
//
// The second half is the part that is easy to skip and expensive to miss. The
// fulfillment queue and the load list read the stored date on the order, not
// the schedule rule — leave those behind and the van's own paperwork still says
// Monday for a day the shop is shut.
func TestPostponeDeliveryRunMovesQuotesAndOrders(t *testing.T) {
	ctx := context.Background()
	tx := testutil.NewTestTx(t, testPool)
	loc := merchantTZ(t)
	svc := postponementService()
	shipping := store.NewShippingStore()

	holiday, tuesday := laborDay2026(loc)

	// An order already promised the holiday run.
	custID, shipID, billID := orderFixtures(t, tx)
	onHoliday := testutil.CreateOrder(t, tx, custID, shipID, billID,
		testutil.WithDeliveryRun(holiday))
	// And one on an untouched run, to prove the update is not a blanket rewrite.
	thursday := time.Date(2026, time.September, 10, 0, 0, 0, 0, loc)
	onThursday := testutil.CreateOrder(t, tx, custID, shipID, billID,
		testutil.WithDeliveryRun(thursday))

	// Before: a customer ordering the Friday before is quoted the holiday.
	cfg, err := shipping.GetConfig(ctx, tx)
	require.NoError(t, err)
	before, ok := cfg.NextDeliveryDate(time.Date(2026, time.September, 4, 10, 0, 0, 0, loc), loc)
	require.True(t, ok)
	require.Equal(t, 7, before.Day(), "fixture assumes the Mon/Thu schedule")

	result, err := svc.PostponeDeliveryRun(ctx, tx, holiday, tuesday, "Labor Day", staffActorFixture())
	require.NoError(t, err)
	assert.EqualValues(t, 1, result.OrdersMoved, "only the holiday order moves")

	// After: the same customer is quoted Tuesday.
	cfg, err = shipping.GetConfig(ctx, tx)
	require.NoError(t, err)
	after, ok := cfg.NextDeliveryDate(time.Date(2026, time.September, 4, 10, 0, 0, 0, loc), loc)
	require.True(t, ok)
	assert.Equal(t, 8, after.Day(), "checkout now quotes the moved run")

	// The order the shop already promised moved with it.
	orders := store.NewOrderStore(nil)
	moved, err := orders.GetOrderByIDAsStaff(ctx, tx, onHoliday.ID)
	require.NoError(t, err)
	require.NotNil(t, moved.ScheduledDeliveryDate)
	assert.Equal(t, 8, moved.ScheduledDeliveryDate.Day(), "the order followed the van")

	// The unrelated run is untouched.
	untouched, err := orders.GetOrderByIDAsStaff(ctx, tx, onThursday.ID)
	require.NoError(t, err)
	require.NotNil(t, untouched.ScheduledDeliveryDate)
	assert.Equal(t, 10, untouched.ScheduledDeliveryDate.Day())
}

// Restoring a run puts both the schedule and the orders back. A staffer who
// marked the wrong day has to be able to undo it cleanly, or the orders are
// stranded on a day with no van.
func TestRestoreDeliveryRunPutsOrdersBack(t *testing.T) {
	ctx := context.Background()
	tx := testutil.NewTestTx(t, testPool)
	loc := merchantTZ(t)
	svc := postponementService()
	shipping := store.NewShippingStore()

	holiday, tuesday := laborDay2026(loc)

	custID, shipID, billID := orderFixtures(t, tx)
	order := testutil.CreateOrder(t, tx, custID, shipID, billID,
		testutil.WithDeliveryRun(holiday))

	_, err := svc.PostponeDeliveryRun(ctx, tx, holiday, tuesday, "wrong day", staffActorFixture())
	require.NoError(t, err)

	result, err := svc.RestoreDeliveryRun(ctx, tx, holiday, staffActorFixture())
	require.NoError(t, err)
	assert.EqualValues(t, 1, result.OrdersMoved)

	cfg, err := shipping.GetConfig(ctx, tx)
	require.NoError(t, err)
	assert.Empty(t, cfg.DeliveryPostponements, "the postponement is gone")

	back, ok := cfg.NextDeliveryDate(time.Date(2026, time.September, 4, 10, 0, 0, 0, loc), loc)
	require.True(t, ok)
	assert.Equal(t, 7, back.Day(), "quotes are back on the scheduled day")

	orders := store.NewOrderStore(nil)
	restored, err := orders.GetOrderByIDAsStaff(ctx, tx, order.ID)
	require.NoError(t, err)
	require.NotNil(t, restored.ScheduledDeliveryDate)
	assert.Equal(t, 7, restored.ScheduledDeliveryDate.Day())
}

// Restoring a day that was never moved is a no-op, not an error — a second
// click on Restore should do nothing rather than fail.
func TestRestoreDeliveryRunUnknownDate(t *testing.T) {
	ctx := context.Background()
	tx := testutil.NewTestTx(t, testPool)
	loc := merchantTZ(t)
	svc := postponementService()

	holiday, _ := laborDay2026(loc)
	result, err := svc.RestoreDeliveryRun(ctx, tx, holiday, staffActorFixture())
	require.NoError(t, err)
	assert.EqualValues(t, 0, result.OrdersMoved)
}

// Marking the same run twice is a correction, not a conflict. A staffer who
// typed the wrong Tuesday must be able to say it again, and the orders have to
// end up on the second answer rather than the first.
func TestPostponeDeliveryRunTwiceCorrects(t *testing.T) {
	ctx := context.Background()
	tx := testutil.NewTestTx(t, testPool)
	loc := merchantTZ(t)
	svc := postponementService()
	shipping := store.NewShippingStore()

	holiday, tuesday := laborDay2026(loc)
	wednesday := time.Date(2026, time.September, 9, 0, 0, 0, 0, loc)

	custID, shipID, billID := orderFixtures(t, tx)
	order := testutil.CreateOrder(t, tx, custID, shipID, billID,
		testutil.WithDeliveryRun(holiday))

	_, err := svc.PostponeDeliveryRun(ctx, tx, holiday, tuesday, "", staffActorFixture())
	require.NoError(t, err)
	// Corrected to Wednesday. The order is now sitting on Tuesday, so the
	// second move has to find it there rather than on the original date.
	_, err = svc.PostponeDeliveryRun(ctx, tx, holiday, wednesday, "actually Wednesday", staffActorFixture())
	require.NoError(t, err)

	cfg, err := shipping.GetConfig(ctx, tx)
	require.NoError(t, err)
	require.Len(t, cfg.DeliveryPostponements, 1, "one run, one answer")

	got, ok := cfg.NextDeliveryDate(time.Date(2026, time.September, 4, 10, 0, 0, 0, loc), loc)
	require.True(t, ok)
	assert.Equal(t, 9, got.Day())

	orders := store.NewOrderStore(nil)
	moved, err := orders.GetOrderByIDAsStaff(ctx, tx, order.ID)
	require.NoError(t, err)
	require.NotNil(t, moved.ScheduledDeliveryDate)
	assert.Equal(t, 9, moved.ScheduledDeliveryDate.Day(),
		"the order follows the correction, not the first answer")
}

// The validation exists so a postponement cannot record a rule that never
// fires, or one the schedule arithmetic cannot honour.
func TestPostponeDeliveryRunRejects(t *testing.T) {
	ctx := context.Background()
	loc := merchantTZ(t)
	holiday, tuesday := laborDay2026(loc)

	tests := []struct {
		name           string
		original, move time.Time
		want           error
	}{
		{
			// Wednesday has no run. Marking it would look handled and do nothing.
			name:     "a day the van does not run",
			original: time.Date(2026, time.September, 9, 0, 0, 0, 0, loc),
			move:     time.Date(2026, time.September, 10, 0, 0, 0, 0, loc),
			want:     app.ErrPostponeNotDeliveryDay,
		},
		{
			name:     "moving a run earlier",
			original: holiday,
			move:     time.Date(2026, time.September, 5, 0, 0, 0, 0, loc),
			want:     app.ErrPostponeNotForward,
		},
		{
			name:     "moving a run onto itself",
			original: holiday,
			move:     holiday,
			want:     app.ErrPostponeNotForward,
		},
		{
			// Past a fortnight this is a schedule change, not a postponement —
			// and the backwards scan in NextDeliveryDate would stop finding it.
			name:     "further than two weeks",
			original: holiday,
			move:     holiday.AddDate(0, 0, domain2Weeks+1),
			want:     app.ErrPostponeTooFar,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tx := testutil.NewTestTx(t, testPool)
			svc := postponementService()
			_, err := svc.PostponeDeliveryRun(ctx, tx, tc.original, tc.move, "", staffActorFixture())
			assert.ErrorIs(t, err, tc.want)

			// Nothing was recorded, so quotes are unchanged.
			cfg, cfgErr := store.NewShippingStore().GetConfig(ctx, tx)
			require.NoError(t, cfgErr)
			assert.Empty(t, cfg.DeliveryPostponements)
		})
	}

	// Sanity: the fixture the rejections are measured against does work.
	tx := testutil.NewTestTx(t, testPool)
	_, err := postponementService().PostponeDeliveryRun(ctx, tx, holiday, tuesday, "", staffActorFixture())
	require.NoError(t, err)
}

// domain2Weeks mirrors domain.MaxDeliveryPostponementDays without importing it
// into the table above, where the literal reads more clearly.
const domain2Weeks = 14

// TestPostponeOntoAnotherRunDayKeepsRunsApart is the case a date-keyed update
// cannot survive: a run moved onto a day that already has one.
//
// Postponing Monday onto Thursday leaves Monday's orders sharing a date with
// Thursday's own, after which "everything promised Thursday" is the wrong set.
// Restoring the Monday then drags Thursday's orders back to a Monday they never
// rode — and if that Monday is the holiday the shop was shut for, the van's
// paperwork points at a closed day, which is the whole thing this feature
// exists to prevent.
func TestPostponeOntoAnotherRunDayKeepsRunsApart(t *testing.T) {
	ctx := context.Background()
	tx := testutil.NewTestTx(t, testPool)
	loc := merchantTZ(t)
	svc := postponementService()
	orders := store.NewOrderStore(nil)

	holiday, _ := laborDay2026(loc)                                  // Mon Sep 7
	thursday := time.Date(2026, time.September, 10, 0, 0, 0, 0, loc) // its own run

	custID, shipID, billID := orderFixtures(t, tx)
	onMonday := testutil.CreateOrder(t, tx, custID, shipID, billID,
		testutil.WithDeliveryRun(holiday))
	nativeThursday := testutil.CreateOrder(t, tx, custID, shipID, billID,
		testutil.WithDeliveryRun(thursday))

	// Monday's run collapses onto Thursday.
	result, err := svc.PostponeDeliveryRun(ctx, tx, holiday, thursday, "Labor Day", staffActorFixture())
	require.NoError(t, err)
	assert.EqualValues(t, 1, result.OrdersMoved, "only Monday's order moves")

	// Both now show Thursday, which is correct — and precisely why the date can
	// no longer tell them apart.
	moved, err := orders.GetOrderByIDAsStaff(ctx, tx, onMonday.ID)
	require.NoError(t, err)
	assert.Equal(t, 10, moved.ScheduledDeliveryDate.Day())

	// Restore. Only the order that actually rode Monday goes back.
	result, err = svc.RestoreDeliveryRun(ctx, tx, holiday, staffActorFixture())
	require.NoError(t, err)
	assert.EqualValues(t, 1, result.OrdersMoved, "Thursday's own order is not swept up")

	back, err := orders.GetOrderByIDAsStaff(ctx, tx, onMonday.ID)
	require.NoError(t, err)
	assert.Equal(t, 7, back.ScheduledDeliveryDate.Day(), "Monday's order returns to Monday")

	untouched, err := orders.GetOrderByIDAsStaff(ctx, tx, nativeThursday.ID)
	require.NoError(t, err)
	assert.Equal(t, 10, untouched.ScheduledDeliveryDate.Day(),
		"an order that never rode the moved run must not be dragged onto it")
}

// The same hazard on the correction path: correcting a collapse must not take
// the other run's orders along for the ride.
func TestPostponeCorrectionOffAnotherRunDay(t *testing.T) {
	ctx := context.Background()
	tx := testutil.NewTestTx(t, testPool)
	loc := merchantTZ(t)
	svc := postponementService()
	orders := store.NewOrderStore(nil)

	holiday, _ := laborDay2026(loc)
	thursday := time.Date(2026, time.September, 10, 0, 0, 0, 0, loc)
	friday := time.Date(2026, time.September, 11, 0, 0, 0, 0, loc)

	custID, shipID, billID := orderFixtures(t, tx)
	onMonday := testutil.CreateOrder(t, tx, custID, shipID, billID,
		testutil.WithDeliveryRun(holiday))
	nativeThursday := testutil.CreateOrder(t, tx, custID, shipID, billID,
		testutil.WithDeliveryRun(thursday))

	_, err := svc.PostponeDeliveryRun(ctx, tx, holiday, thursday, "", staffActorFixture())
	require.NoError(t, err)
	// Actually, make it Friday.
	result, err := svc.PostponeDeliveryRun(ctx, tx, holiday, friday, "actually Friday", staffActorFixture())
	require.NoError(t, err)
	assert.EqualValues(t, 1, result.OrdersMoved)

	corrected, err := orders.GetOrderByIDAsStaff(ctx, tx, onMonday.ID)
	require.NoError(t, err)
	assert.Equal(t, 11, corrected.ScheduledDeliveryDate.Day())

	stayed, err := orders.GetOrderByIDAsStaff(ctx, tx, nativeThursday.ID)
	require.NoError(t, err)
	assert.Equal(t, 10, stayed.ScheduledDeliveryDate.Day(),
		"Thursday's own order stays on Thursday")
}

// A run that has already gone cannot be moved. Without the guard, marking an
// old Monday rewrites the promised date on orders delivered that day —
// corrupting the record of what happened while changing nothing about any
// future run.
func TestPostponeDeliveryRunRejectsPastRun(t *testing.T) {
	ctx := context.Background()
	tx := testutil.NewTestTx(t, testPool)
	loc := merchantTZ(t)
	svc := postponementService()

	// The most recent Monday strictly before today.
	past := todayInTest(loc).AddDate(0, 0, -7)
	for past.Weekday() != time.Monday {
		past = past.AddDate(0, 0, -1)
	}

	_, err := svc.PostponeDeliveryRun(ctx, tx, past, past.AddDate(0, 0, 1), "", staffActorFixture())
	assert.ErrorIs(t, err, app.ErrPostponeAlreadyRun)
}

// The fortnight cap has to agree with the database CHECK. Measuring it as a
// duration rather than in calendar days makes an exact fourteen-day move across
// a daylight-saving transition come to 337 hours, which the app would reject
// and the constraint would accept.
func TestPostponeDeliveryRunFortnightBoundary(t *testing.T) {
	ctx := context.Background()
	loc := merchantTZ(t)

	// Monday 2026-10-26 + 14 days spans the 2026-11-01 fall-back.
	original := time.Date(2026, time.October, 26, 0, 0, 0, 0, loc)
	require.Equal(t, time.Monday, original.Weekday())
	require.Greater(t, original.AddDate(0, 0, 14).Sub(original), 14*24*time.Hour,
		"fixture must actually span the transition, or it proves nothing")

	t.Run("exactly a fortnight is allowed", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		_, err := postponementService().PostponeDeliveryRun(
			ctx, tx, original, original.AddDate(0, 0, 14), "", staffActorFixture())
		assert.NoError(t, err)
	})

	t.Run("a day past it is not", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		_, err := postponementService().PostponeDeliveryRun(
			ctx, tx, original, original.AddDate(0, 0, 15), "", staffActorFixture())
		assert.ErrorIs(t, err, app.ErrPostponeTooFar)
	})
}

// todayInTest is local midnight today in loc.
func todayInTest(loc *time.Location) time.Time {
	n := time.Now().In(loc)
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, loc)
}
