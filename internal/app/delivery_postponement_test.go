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

// testNow is the instant every postponement test judges "has this run gone?"
// against. Fixed rather than time.Now(): the service takes a clock precisely so
// these tests do not rot, and pinning it here is what makes the dates below
// mean the same thing forever.
//
// A Friday, so the Monday it points at is genuinely ahead.
func testNow(loc *time.Location) time.Time {
	return time.Date(2026, time.September, 4, 10, 0, 0, 0, loc)
}

// runDays returns the Mon/Thu run days around testNow, derived from it rather
// than written out. An earlier version hard-coded September dates, which passed
// only until the wall clock caught up with them — the past-run guard would have
// turned the whole suite red four days after it was written.
func runDays(loc *time.Location) (monday, thursday, nextMonday time.Time) {
	base := dateOnlyIn(testNow(loc))
	for base.Weekday() != time.Monday {
		base = base.AddDate(0, 0, 1)
	}
	return base, base.AddDate(0, 0, 3), base.AddDate(0, 0, 7)
}

// laborDay2026 is the upcoming Monday run and the Tuesday it moves to — the
// shape of the Labor Day case this feature was built for.
func laborDay2026(loc *time.Location) (holiday, tuesday time.Time) {
	monday, _, _ := runDays(loc)
	return monday, monday.AddDate(0, 0, 1)
}

func dateOnlyIn(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
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
	_, thursday, _ := runDays(loc)
	onThursday := testutil.CreateOrder(t, tx, custID, shipID, billID,
		testutil.WithDeliveryRun(thursday))

	// Before: a customer ordering the Friday before is quoted the holiday.
	cfg, err := shipping.GetConfig(ctx, tx)
	require.NoError(t, err)
	before, ok := cfg.NextDeliveryDate(testNow(loc), loc)
	require.True(t, ok)
	require.Equal(t, holiday.Day(), before.Day(), "fixture assumes the Mon/Thu schedule")

	result, err := svc.PostponeDeliveryRun(ctx, tx, testNow(loc), holiday, tuesday, "Labor Day", staffActorFixture())
	require.NoError(t, err)
	assert.EqualValues(t, 1, result.OrdersMoved, "only the holiday order moves")

	// After: the same customer is quoted Tuesday.
	cfg, err = shipping.GetConfig(ctx, tx)
	require.NoError(t, err)
	after, ok := cfg.NextDeliveryDate(testNow(loc), loc)
	require.True(t, ok)
	assert.Equal(t, tuesday.Day(), after.Day(), "checkout now quotes the moved run")

	// The order the shop already promised moved with it.
	orders := store.NewOrderStore(nil)
	moved, err := orders.GetOrderByIDAsStaff(ctx, tx, onHoliday.ID)
	require.NoError(t, err)
	require.NotNil(t, moved.ScheduledDeliveryDate)
	assert.Equal(t, tuesday.Day(), moved.ScheduledDeliveryDate.Day(), "the order followed the van")

	// The unrelated run is untouched.
	untouched, err := orders.GetOrderByIDAsStaff(ctx, tx, onThursday.ID)
	require.NoError(t, err)
	require.NotNil(t, untouched.ScheduledDeliveryDate)
	assert.Equal(t, thursday.Day(), untouched.ScheduledDeliveryDate.Day())
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

	_, err := svc.PostponeDeliveryRun(ctx, tx, testNow(loc), holiday, tuesday, "wrong day", staffActorFixture())
	require.NoError(t, err)

	result, err := svc.RestoreDeliveryRun(ctx, tx, testNow(loc), holiday, staffActorFixture())
	require.NoError(t, err)
	assert.EqualValues(t, 1, result.OrdersMoved)

	cfg, err := shipping.GetConfig(ctx, tx)
	require.NoError(t, err)
	assert.Empty(t, cfg.DeliveryPostponements, "the postponement is gone")

	back, ok := cfg.NextDeliveryDate(testNow(loc), loc)
	require.True(t, ok)
	assert.Equal(t, holiday.Day(), back.Day(), "quotes are back on the scheduled day")

	orders := store.NewOrderStore(nil)
	restored, err := orders.GetOrderByIDAsStaff(ctx, tx, order.ID)
	require.NoError(t, err)
	require.NotNil(t, restored.ScheduledDeliveryDate)
	assert.Equal(t, holiday.Day(), restored.ScheduledDeliveryDate.Day())
}

// Restoring a day that was never moved is a no-op, not an error — a second
// click on Restore should do nothing rather than fail.
func TestRestoreDeliveryRunUnknownDate(t *testing.T) {
	ctx := context.Background()
	tx := testutil.NewTestTx(t, testPool)
	loc := merchantTZ(t)
	svc := postponementService()

	holiday, _ := laborDay2026(loc)
	result, err := svc.RestoreDeliveryRun(ctx, tx, testNow(loc), holiday, staffActorFixture())
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
	wednesday := holiday.AddDate(0, 0, 2)

	custID, shipID, billID := orderFixtures(t, tx)
	order := testutil.CreateOrder(t, tx, custID, shipID, billID,
		testutil.WithDeliveryRun(holiday))

	_, err := svc.PostponeDeliveryRun(ctx, tx, testNow(loc), holiday, tuesday, "", staffActorFixture())
	require.NoError(t, err)
	// Corrected to Wednesday. The order is now sitting on Tuesday, so the
	// second move has to find it there rather than on the original date.
	_, err = svc.PostponeDeliveryRun(ctx, tx, testNow(loc), holiday, wednesday, "actually Wednesday", staffActorFixture())
	require.NoError(t, err)

	cfg, err := shipping.GetConfig(ctx, tx)
	require.NoError(t, err)
	require.Len(t, cfg.DeliveryPostponements, 1, "one run, one answer")

	got, ok := cfg.NextDeliveryDate(testNow(loc), loc)
	require.True(t, ok)
	assert.Equal(t, wednesday.Day(), got.Day())

	orders := store.NewOrderStore(nil)
	moved, err := orders.GetOrderByIDAsStaff(ctx, tx, order.ID)
	require.NoError(t, err)
	require.NotNil(t, moved.ScheduledDeliveryDate)
	assert.Equal(t, wednesday.Day(), moved.ScheduledDeliveryDate.Day(),
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
			original: holiday.AddDate(0, 0, 2), // Wednesday
			move:     holiday.AddDate(0, 0, 3),
			want:     app.ErrPostponeNotDeliveryDay,
		},
		{
			name:     "moving a run earlier",
			original: holiday,
			move:     holiday.AddDate(0, 0, -2),
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
			_, err := svc.PostponeDeliveryRun(ctx, tx, testNow(loc), tc.original, tc.move, "", staffActorFixture())
			assert.ErrorIs(t, err, tc.want)

			// Nothing was recorded, so quotes are unchanged.
			cfg, cfgErr := store.NewShippingStore().GetConfig(ctx, tx)
			require.NoError(t, cfgErr)
			assert.Empty(t, cfg.DeliveryPostponements)
		})
	}

	// Sanity: the fixture the rejections are measured against does work.
	tx := testutil.NewTestTx(t, testPool)
	_, err := postponementService().PostponeDeliveryRun(ctx, tx, testNow(loc), holiday, tuesday, "", staffActorFixture())
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

	holiday, thursday, _ := runDays(loc) // Monday's run, and Thursday's own

	custID, shipID, billID := orderFixtures(t, tx)
	onMonday := testutil.CreateOrder(t, tx, custID, shipID, billID,
		testutil.WithDeliveryRun(holiday))
	nativeThursday := testutil.CreateOrder(t, tx, custID, shipID, billID,
		testutil.WithDeliveryRun(thursday))

	// Monday's run collapses onto Thursday.
	result, err := svc.PostponeDeliveryRun(ctx, tx, testNow(loc), holiday, thursday, "Labor Day", staffActorFixture())
	require.NoError(t, err)
	assert.EqualValues(t, 1, result.OrdersMoved, "only Monday's order moves")

	// Both now show Thursday, which is correct — and precisely why the date can
	// no longer tell them apart.
	moved, err := orders.GetOrderByIDAsStaff(ctx, tx, onMonday.ID)
	require.NoError(t, err)
	assert.Equal(t, thursday.Day(), moved.ScheduledDeliveryDate.Day())

	// Restore. Only the order that actually rode Monday goes back.
	result, err = svc.RestoreDeliveryRun(ctx, tx, testNow(loc), holiday, staffActorFixture())
	require.NoError(t, err)
	assert.EqualValues(t, 1, result.OrdersMoved, "Thursday's own order is not swept up")

	back, err := orders.GetOrderByIDAsStaff(ctx, tx, onMonday.ID)
	require.NoError(t, err)
	assert.Equal(t, holiday.Day(), back.ScheduledDeliveryDate.Day(), "Monday's order returns to Monday")

	untouched, err := orders.GetOrderByIDAsStaff(ctx, tx, nativeThursday.ID)
	require.NoError(t, err)
	assert.Equal(t, thursday.Day(), untouched.ScheduledDeliveryDate.Day(),
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

	holiday, thursday, _ := runDays(loc)
	friday := thursday.AddDate(0, 0, 1)

	custID, shipID, billID := orderFixtures(t, tx)
	onMonday := testutil.CreateOrder(t, tx, custID, shipID, billID,
		testutil.WithDeliveryRun(holiday))
	nativeThursday := testutil.CreateOrder(t, tx, custID, shipID, billID,
		testutil.WithDeliveryRun(thursday))

	_, err := svc.PostponeDeliveryRun(ctx, tx, testNow(loc), holiday, thursday, "", staffActorFixture())
	require.NoError(t, err)
	// Actually, make it Friday.
	result, err := svc.PostponeDeliveryRun(ctx, tx, testNow(loc), holiday, friday, "actually Friday", staffActorFixture())
	require.NoError(t, err)
	assert.EqualValues(t, 1, result.OrdersMoved)

	corrected, err := orders.GetOrderByIDAsStaff(ctx, tx, onMonday.ID)
	require.NoError(t, err)
	assert.Equal(t, friday.Day(), corrected.ScheduledDeliveryDate.Day())

	stayed, err := orders.GetOrderByIDAsStaff(ctx, tx, nativeThursday.ID)
	require.NoError(t, err)
	assert.Equal(t, thursday.Day(), stayed.ScheduledDeliveryDate.Day(),
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

	monday, _, _ := runDays(loc)
	past := monday.AddDate(0, 0, -7) // the Monday before the clock

	_, err := svc.PostponeDeliveryRun(ctx, tx, testNow(loc), past, past.AddDate(0, 0, 1), "", staffActorFixture())
	assert.ErrorIs(t, err, app.ErrPostponeAlreadyRun)
}

// The guard asks whether the run has *gone*, not whether its scheduled day has
// passed — and those differ exactly when it has already been moved once.
//
// A Monday holiday postponed to Tuesday, and on Tuesday morning the van still
// cannot go: the run has not happened, so correcting it must be allowed.
// Judging on the scheduled Monday would refuse with a message that is untrue
// and leave staff no correct action at all, since Restore would only put the
// orders back on the closed Monday.
func TestPostponeAlreadyMovedRunOnTheDayItWasMovedTo(t *testing.T) {
	ctx := context.Background()
	tx := testutil.NewTestTx(t, testPool)
	loc := merchantTZ(t)
	svc := postponementService()

	holiday, tuesday := laborDay2026(loc)
	wednesday := holiday.AddDate(0, 0, 2)

	_, err := svc.PostponeDeliveryRun(ctx, tx, testNow(loc), holiday, tuesday, "Labor Day", staffActorFixture())
	require.NoError(t, err)

	// It is now Tuesday morning — the scheduled Monday is behind us, the run is not.
	tuesdayMorning := tuesday.Add(7 * time.Hour)
	_, err = svc.PostponeDeliveryRun(ctx, tx, tuesdayMorning, holiday, wednesday, "still no van", staffActorFixture())
	assert.NoError(t, err, "a run that has not gone must still be movable")

	// Once the moved day is behind us too, it really has gone.
	thursdayMorning := tuesday.AddDate(0, 0, 2).Add(7 * time.Hour)
	_, err = svc.PostponeDeliveryRun(ctx, tx, thursdayMorning, holiday, wednesday.AddDate(0, 0, 2), "", staffActorFixture())
	assert.ErrorIs(t, err, app.ErrPostponeAlreadyRun)
}

// Restore asks the opposite question: is there still a day to put the run back
// on? Restoring into the past would rewrite the promised date on orders already
// delivered, moving them onto the closed day the shop postponed away from —
// which is what the Restore button used to offer on every past row.
func TestRestoreDeliveryRunRejectsPassedRun(t *testing.T) {
	ctx := context.Background()
	tx := testutil.NewTestTx(t, testPool)
	loc := merchantTZ(t)
	svc := postponementService()

	holiday, tuesday := laborDay2026(loc)
	_, err := svc.PostponeDeliveryRun(ctx, tx, testNow(loc), holiday, tuesday, "Labor Day", staffActorFixture())
	require.NoError(t, err)

	// Tuesday morning: the scheduled Monday has passed, so there is nothing to
	// put the run back onto.
	_, err = svc.RestoreDeliveryRun(ctx, tx, tuesday.Add(7*time.Hour), holiday, staffActorFixture())
	assert.ErrorIs(t, err, app.ErrRestoreRunPassed)

	// Before the scheduled day it is fine.
	_, err = svc.RestoreDeliveryRun(ctx, tx, testNow(loc), holiday, staffActorFixture())
	assert.NoError(t, err)
}

// The fortnight cap has to agree with the database CHECK. Measuring it as a
// duration rather than in calendar days makes an exact fourteen-day move across
// a daylight-saving transition come to 337 hours, which the app would reject
// and the constraint would accept.
func TestPostponeDeliveryRunFortnightBoundary(t *testing.T) {
	ctx := context.Background()
	loc := merchantTZ(t)

	// A Monday whose fortnight spans a daylight-saving transition, found rather
	// than written down so this cannot rot the way the first version did.
	monday, _, _ := runDays(loc)
	var original time.Time
	for d := monday; d.Before(monday.AddDate(1, 0, 0)); d = d.AddDate(0, 0, 7) {
		if d.AddDate(0, 0, 14).Sub(d) != 14*24*time.Hour {
			original = d
			break
		}
	}
	require.False(t, original.IsZero(), "no DST-spanning fortnight found within a year")
	require.Equal(t, time.Monday, original.Weekday())
	require.Greater(t, original.AddDate(0, 0, 14).Sub(original), 14*24*time.Hour,
		"fixture must actually span the transition, or it proves nothing")

	t.Run("exactly a fortnight is allowed", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		_, err := postponementService().PostponeDeliveryRun(
			ctx, tx, testNow(loc), original, original.AddDate(0, 0, 14), "", staffActorFixture())
		assert.NoError(t, err)
	})

	t.Run("a day past it is not", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		_, err := postponementService().PostponeDeliveryRun(
			ctx, tx, testNow(loc), original, original.AddDate(0, 0, 15), "", staffActorFixture())
		assert.ErrorIs(t, err, app.ErrPostponeTooFar)
	})
}

// A run cannot be corrected onto a day that has already gone.
//
// The checks on a move are all relative to the run's own scheduled day —
// "later than it was", "within a fortnight" — and none of them mentions today.
// Loosening the past-run guard to ask about the effective date (so a run
// already moved once stays correctable) opened the gap: a run postponed a week
// out could be corrected back onto yesterday, stamping a dead date on every
// order riding it. Neither half of the pair would touch it again — postpone
// would see a run that had gone, restore a scheduled day that had passed — so
// the orders were stranded while the panel called the run settled.
func TestPostponeDeliveryRunRejectsMoveIntoThePast(t *testing.T) {
	ctx := context.Background()
	tx := testutil.NewTestTx(t, testPool)
	loc := merchantTZ(t)
	svc := postponementService()

	holiday, _ := laborDay2026(loc)
	weekOut := holiday.AddDate(0, 0, 8)

	_, err := svc.PostponeDeliveryRun(ctx, tx, testNow(loc), holiday, weekOut, "", staffActorFixture())
	require.NoError(t, err)

	// Two days after the scheduled Monday, correct it onto the day before.
	//nolint:gocritic // the point is that this date is behind `now`
	wednesday := holiday.AddDate(0, 0, 2).Add(9 * time.Hour)
	_, err = svc.PostponeDeliveryRun(ctx, tx, wednesday, holiday, holiday.AddDate(0, 0, 1), "", staffActorFixture())
	assert.ErrorIs(t, err, app.ErrPostponeIntoPast)

	// The run is still where it was, and still correctable to a day ahead.
	cfg, err := store.NewShippingStore().GetConfig(ctx, tx)
	require.NoError(t, err)
	require.Len(t, cfg.DeliveryPostponements, 1)
	assert.Equal(t, weekOut.Day(), dateOnlyIn(cfg.DeliveryPostponements[0].MovedTo).Day())

	_, err = svc.PostponeDeliveryRun(ctx, tx, wednesday, holiday, holiday.AddDate(0, 0, 3), "", staffActorFixture())
	assert.NoError(t, err, "a day still ahead is fine")
}

// Restoring a day nothing was recorded for stays a no-op even once that day has
// passed — a second click should read as done, not as a refusal.
func TestRestoreDeliveryRunUnknownPastDateIsStillNoOp(t *testing.T) {
	ctx := context.Background()
	tx := testutil.NewTestTx(t, testPool)
	loc := merchantTZ(t)

	monday, _, _ := runDays(loc)
	past := monday.AddDate(0, 0, -7)

	result, err := postponementService().RestoreDeliveryRun(ctx, tx, testNow(loc), past, staffActorFixture())
	require.NoError(t, err)
	assert.EqualValues(t, 0, result.OrdersMoved)
}

// A postponement resolves one hop and does not chase chains, so a chain must
// never be allowed to form: with Monday moved onto Thursday and Thursday then
// moved onto Saturday, Monday's run still reports Thursday — a day the shop is
// shut, which is the exact failure this feature exists to prevent. The panel
// would show two innocuous rows and nothing would warn.
//
// Both orders of doing it are refused, because the hazard is symmetric and this
// feature has been bitten three times by a rule written for one half of a pair.
func TestPostponeRefusesChainedRuns(t *testing.T) {
	loc := merchantTZ(t)
	monday, thursday, _ := runDays(loc)
	saturday := thursday.AddDate(0, 0, 2)

	t.Run("onto a day whose own run has been moved away", func(t *testing.T) {
		ctx := context.Background()
		tx := testutil.NewTestTx(t, testPool)
		svc := postponementService()

		_, err := svc.PostponeDeliveryRun(ctx, tx, testNow(loc), thursday, saturday, "closed Thursday", staffActorFixture())
		require.NoError(t, err)

		_, err = svc.PostponeDeliveryRun(ctx, tx, testNow(loc), monday, thursday, "Labor Day", staffActorFixture())
		assert.ErrorIs(t, err, app.ErrPostponeTargetRunMoved)

		// Refused before anything was written: the Thursday move is untouched
		// and Monday was not recorded.
		cfg, err := store.NewShippingStore().GetConfig(ctx, tx)
		require.NoError(t, err)
		require.Len(t, cfg.DeliveryPostponements, 1)
		assert.Equal(t, thursday.Day(), dateOnlyIn(cfg.DeliveryPostponements[0].OriginalDate).Day())
	})

	t.Run("off a day another run has been moved onto", func(t *testing.T) {
		ctx := context.Background()
		tx := testutil.NewTestTx(t, testPool)
		svc := postponementService()

		_, err := svc.PostponeDeliveryRun(ctx, tx, testNow(loc), monday, thursday, "Labor Day", staffActorFixture())
		require.NoError(t, err)

		_, err = svc.PostponeDeliveryRun(ctx, tx, testNow(loc), thursday, saturday, "closed Thursday", staffActorFixture())
		assert.ErrorIs(t, err, app.ErrPostponeStrandsMovedRun)

		cfg, err := store.NewShippingStore().GetConfig(ctx, tx)
		require.NoError(t, err)
		require.Len(t, cfg.DeliveryPostponements, 1)
		assert.Equal(t, monday.Day(), dateOnlyIn(cfg.DeliveryPostponements[0].OriginalDate).Day())
	})

	// The guard must not mistake a run for its own chain partner. Correcting an
	// existing postponement replaces the row rather than adding to it, so the
	// row being replaced cannot chain with itself — and correction is the path
	// staff use most.
	t.Run("correcting an existing postponement is not a chain", func(t *testing.T) {
		ctx := context.Background()
		tx := testutil.NewTestTx(t, testPool)
		svc := postponementService()

		_, err := svc.PostponeDeliveryRun(ctx, tx, testNow(loc), monday, thursday, "Labor Day", staffActorFixture())
		require.NoError(t, err)

		_, err = svc.PostponeDeliveryRun(ctx, tx, testNow(loc), monday, monday.AddDate(0, 0, 1), "Tuesday after all", staffActorFixture())
		assert.NoError(t, err)

		cfg, err := store.NewShippingStore().GetConfig(ctx, tx)
		require.NoError(t, err)
		require.Len(t, cfg.DeliveryPostponements, 1)
		assert.Equal(t, monday.AddDate(0, 0, 1).Day(), dateOnlyIn(cfg.DeliveryPostponements[0].MovedTo).Day())
	})

	// Restoring the other postponement is the way out, and it has to actually
	// clear the way — otherwise the refusal above is a dead end.
	t.Run("restoring the other run clears the way", func(t *testing.T) {
		ctx := context.Background()
		tx := testutil.NewTestTx(t, testPool)
		svc := postponementService()

		_, err := svc.PostponeDeliveryRun(ctx, tx, testNow(loc), thursday, saturday, "closed Thursday", staffActorFixture())
		require.NoError(t, err)
		_, err = svc.RestoreDeliveryRun(ctx, tx, testNow(loc), thursday, staffActorFixture())
		require.NoError(t, err)

		_, err = svc.PostponeDeliveryRun(ctx, tx, testNow(loc), monday, thursday, "Labor Day", staffActorFixture())
		assert.NoError(t, err)
	})
}
