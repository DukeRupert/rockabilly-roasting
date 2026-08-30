package app_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

func newRatedTicketService() *app.ServiceTicketService {
	return app.NewServiceTicketService(store.NewServiceTicketStore(), store.NewEquipmentStore(), audit.NewAuditWriter()).
		WithSettings(store.NewSettingsStore())
}

func cents(n int) *int { return &n }

func TestLaborRatesRoundTrip(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newRatedTicketService()

	got, err := svc.LaborRates(ctx, tx)
	require.NoError(t, err)
	assert.False(t, got.Set(), "a fresh shop has no rate, and that is not an error")

	require.NoError(t, svc.SetLaborRates(ctx, tx, domain.ServiceLaborRates{
		LaborCentsPerHour:  cents(6500),
		TravelCentsPerHour: cents(4000),
	}, testutil.TestActor()))

	got, err = svc.LaborRates(ctx, tx)
	require.NoError(t, err)
	require.NotNil(t, got.LaborCentsPerHour)
	assert.Equal(t, 6500, *got.LaborCentsPerHour)
	require.NotNil(t, got.TravelCentsPerHour)
	assert.Equal(t, 4000, *got.TravelCentsPerHour)
}

// Clearing the rate takes the money column off the reports. It has to be
// possible: a shop that set a rate it no longer trusts should be able to go
// back to hours and parts rather than living with a wrong number.
func TestLaborRatesCanBeCleared(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newRatedTicketService()

	require.NoError(t, svc.SetLaborRates(ctx, tx, domain.ServiceLaborRates{
		LaborCentsPerHour: cents(6500),
	}, testutil.TestActor()))
	require.NoError(t, svc.SetLaborRates(ctx, tx, domain.ServiceLaborRates{}, testutil.TestActor()))

	got, err := svc.LaborRates(ctx, tx)
	require.NoError(t, err)
	assert.False(t, got.Set())
	assert.Nil(t, got.LaborCentsPerHour)
}

func TestLaborRatesValidation(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newRatedTicketService()

	t.Run("a travel rate alone does nothing", func(t *testing.T) {
		err := svc.SetLaborRates(ctx, tx, domain.ServiceLaborRates{
			TravelCentsPerHour: cents(4000),
		}, testutil.TestActor())
		assert.ErrorIs(t, err, app.ErrTravelRateWithoutLabor)
	})

	t.Run("cents typed into a dollars field", func(t *testing.T) {
		err := svc.SetLaborRates(ctx, tx, domain.ServiceLaborRates{
			LaborCentsPerHour: cents(650000000),
		}, testutil.TestActor())
		assert.ErrorIs(t, err, app.ErrLaborRateInvalid)
	})

	t.Run("zero travel is a decision, not a mistake", func(t *testing.T) {
		err := svc.SetLaborRates(ctx, tx, domain.ServiceLaborRates{
			LaborCentsPerHour:  cents(6500),
			TravelCentsPerHour: cents(0),
		}, testutil.TestActor())
		assert.NoError(t, err, "a shop that absorbs the drive must be able to say so")
	})
}

// A rate change is auditable: every cost figure the reports show is computed
// from whatever is set now, so the change history is the only way to explain a
// jump.
func TestSetLaborRatesIsAudited(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newRatedTicketService()

	require.NoError(t, svc.SetLaborRates(ctx, tx, domain.ServiceLaborRates{
		LaborCentsPerHour: cents(6500),
	}, testutil.TestActor()))

	var action string
	require.NoError(t, tx.QueryRow(ctx,
		`SELECT action FROM audit_log WHERE resource_type = 'store_settings' ORDER BY created_at DESC LIMIT 1`,
	).Scan(&action))
	assert.Equal(t, "service.labor_rates_updated", action)
}

// The behaviour the snapshot exists for: a rate change prices the next hour and
// leaves every past one alone.
func TestRateChangeDoesNotRepriceThePast(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newRatedTicketService()
	tickets := store.NewServiceTicketStore()
	customer := testutil.CreateCustomer(t, tx)
	staffID := testutil.CreateStaff(t, tx)

	ticket, err := tickets.Create(ctx, tx, store.CreateServiceTicketParams{
		Number: "SVC-SNAP-1", CustomerID: customer.ID, Title: "long job",
		Severity: domain.ServiceSeverityRoutine,
	})
	require.NoError(t, err)

	require.NoError(t, svc.SetLaborRates(ctx, tx, domain.ServiceLaborRates{
		LaborCentsPerHour: cents(6000),
	}, testutil.TestActor()))

	// An hour logged at $60.
	_, err = svc.LogTime(ctx, tx, app.LogTimeParams{
		TicketID: ticket.ID, StaffID: staffID, Kind: domain.ServiceTimeKindLabor, Minutes: 60,
	}, testutil.TestActor())
	require.NoError(t, err)

	totals, err := svc.Totals(ctx, tx, ticket.ID)
	require.NoError(t, err)
	require.Equal(t, 6000, totals.LaborCostCents)

	// The shop puts its rate up.
	require.NoError(t, svc.SetLaborRates(ctx, tx, domain.ServiceLaborRates{
		LaborCentsPerHour: cents(9000),
	}, testutil.TestActor()))

	totals, err = svc.Totals(ctx, tx, ticket.ID)
	require.NoError(t, err)
	assert.Equal(t, 6000, totals.LaborCostCents,
		"the hour was bought at $60 and stays bought at $60")

	// The next hour goes on at the new rate, and the two live side by side.
	_, err = svc.LogTime(ctx, tx, app.LogTimeParams{
		TicketID: ticket.ID, StaffID: staffID, Kind: domain.ServiceTimeKindLabor, Minutes: 60,
	}, testutil.TestActor())
	require.NoError(t, err)

	totals, err = svc.Totals(ctx, tx, ticket.ID)
	require.NoError(t, err)
	assert.Equal(t, 15000, totals.LaborCostCents, "$60 then $90 — not two hours at either")
}

// Travel resolves its fallback at stamp time, so a shop that later sets a
// travel rate does not retrospectively re-price drives already made.
func TestTravelRateStampedAtWriteTime(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newRatedTicketService()
	tickets := store.NewServiceTicketStore()
	customer := testutil.CreateCustomer(t, tx)
	staffID := testutil.CreateStaff(t, tx)

	ticket, err := tickets.Create(ctx, tx, store.CreateServiceTicketParams{
		Number: "SVC-SNAP-2", CustomerID: customer.ID, Title: "drive",
		Severity: domain.ServiceSeverityRoutine,
	})
	require.NoError(t, err)

	// No travel rate yet, so the drive is costed as labour.
	require.NoError(t, svc.SetLaborRates(ctx, tx, domain.ServiceLaborRates{
		LaborCentsPerHour: cents(6000),
	}, testutil.TestActor()))
	drive, err := svc.LogTime(ctx, tx, app.LogTimeParams{
		TicketID: ticket.ID, StaffID: staffID, Kind: domain.ServiceTimeKindTravel, Minutes: 60,
	}, testutil.TestActor())
	require.NoError(t, err)
	require.NotNil(t, drive.RateCents)
	assert.Equal(t, 6000, *drive.RateCents, "with no travel rate the drive is an hour of labour")

	// A travel rate arrives. The drive already made keeps what it was booked at.
	require.NoError(t, svc.SetLaborRates(ctx, tx, domain.ServiceLaborRates{
		LaborCentsPerHour: cents(6000), TravelCentsPerHour: cents(3000),
	}, testutil.TestActor()))

	totals, err := svc.Totals(ctx, tx, ticket.ID)
	require.NoError(t, err)
	assert.Equal(t, 6000, totals.LaborCostCents)
}

// Hours logged before any rate existed stay unpriced, and say so.
func TestHoursLoggedBeforeARateAreUnpriced(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newRatedTicketService()
	tickets := store.NewServiceTicketStore()
	customer := testutil.CreateCustomer(t, tx)
	staffID := testutil.CreateStaff(t, tx)

	ticket, err := tickets.Create(ctx, tx, store.CreateServiceTicketParams{
		Number: "SVC-SNAP-3", CustomerID: customer.ID, Title: "early work",
		Severity: domain.ServiceSeverityRoutine,
	})
	require.NoError(t, err)

	entry, err := svc.LogTime(ctx, tx, app.LogTimeParams{
		TicketID: ticket.ID, StaffID: staffID, Kind: domain.ServiceTimeKindLabor, Minutes: 120,
	}, testutil.TestActor())
	require.NoError(t, err)
	assert.False(t, entry.Costed())

	// Setting a rate now does NOT reach backwards — that is the whole point.
	require.NoError(t, svc.SetLaborRates(ctx, tx, domain.ServiceLaborRates{
		LaborCentsPerHour: cents(6500),
	}, testutil.TestActor()))

	totals, err := svc.Totals(ctx, tx, ticket.ID)
	require.NoError(t, err)
	assert.Equal(t, 0, totals.LaborCostCents)
	assert.Equal(t, 120, totals.UncostedMinutes)
	assert.False(t, totals.FullyCosted(), "the page has to be able to say the total is a floor")
}

// The escape hatch: an unpriced hour can be priced by hand, one row at a time.
func TestRepriceTimeEntry(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newRatedTicketService()
	tickets := store.NewServiceTicketStore()
	customer := testutil.CreateCustomer(t, tx)
	staffID := testutil.CreateStaff(t, tx)

	ticket, err := tickets.Create(ctx, tx, store.CreateServiceTicketParams{
		Number: "SVC-SNAP-4", CustomerID: customer.ID, Title: "fixable",
		Severity: domain.ServiceSeverityRoutine,
	})
	require.NoError(t, err)

	entry, err := svc.LogTime(ctx, tx, app.LogTimeParams{
		TicketID: ticket.ID, StaffID: staffID, Kind: domain.ServiceTimeKindLabor, Minutes: 120,
	}, testutil.TestActor())
	require.NoError(t, err)
	require.False(t, entry.Costed())

	priced, err := svc.RepriceTimeEntry(ctx, tx, ticket.ID, entry.ID, cents(6500), testutil.TestActor())
	require.NoError(t, err)
	require.NotNil(t, priced.RateCents)
	assert.Equal(t, 6500, *priced.RateCents)
	assert.Equal(t, 13000, priced.CostCents(), "2h at $65")

	totals, err := svc.Totals(ctx, tx, ticket.ID)
	require.NoError(t, err)
	assert.Equal(t, 13000, totals.LaborCostCents)
	assert.True(t, totals.FullyCosted())

	// And back out again, without touching the hours.
	cleared, err := svc.RepriceTimeEntry(ctx, tx, ticket.ID, entry.ID, nil, testutil.TestActor())
	require.NoError(t, err)
	assert.False(t, cleared.Costed())

	totals, err = svc.Totals(ctx, tx, ticket.ID)
	require.NoError(t, err)
	assert.Equal(t, 0, totals.LaborCostCents)
	assert.Equal(t, 120, totals.LaborMinutes, "the hours are still there")

	var action string
	require.NoError(t, tx.QueryRow(ctx,
		`SELECT action FROM audit_log WHERE resource_type = 'service_ticket'
		   AND action = 'service_ticket.time_repriced' ORDER BY created_at DESC LIMIT 1`,
	).Scan(&action))
	assert.Equal(t, "service_ticket.time_repriced", action,
		"a hand-priced hour is a decision, and decisions are recorded")
}

// The point of the whole feature: with a rate set, accounts can be ranked by
// The point of the whole feature: with a rate set, accounts can be ranked by
// what they actually cost — which is a different order from hours alone.
func TestCostByAccountRanksByCostOnceRated(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newRatedTicketService()
	tickets := store.NewServiceTicketStore()
	now := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
	staffID := testutil.CreateStaff(t, tx)

	// One account with a lot of hours and no parts, one with a little time and
	// an expensive part.
	hoursHeavy := testutil.CreateCustomer(t, tx)
	partsHeavy := testutil.CreateCustomer(t, tx)

	t1, err := tickets.Create(ctx, tx, store.CreateServiceTicketParams{
		Number: "SVC-RATE-1", CustomerID: hoursHeavy.ID, Title: "long job", Severity: domain.ServiceSeverityRoutine,
	})
	require.NoError(t, err)
	t2, err := tickets.Create(ctx, tx, store.CreateServiceTicketParams{
		Number: "SVC-RATE-2", CustomerID: partsHeavy.ID, Title: "expensive part", Severity: domain.ServiceSeverityRoutine,
	})
	require.NoError(t, err)

	// The rate has to exist before the hours are logged: it is stamped on each
	// entry as it is written, and setting it afterwards would not reach back.
	require.NoError(t, svc.SetLaborRates(ctx, tx, domain.ServiceLaborRates{
		LaborCentsPerHour: cents(6500),
	}, testutil.TestActor()))

	// 10 hours of labour: at $65/h that is $650.
	_, err = svc.LogTime(ctx, tx, app.LogTimeParams{
		TicketID: t1.ID, StaffID: staffID, Kind: domain.ServiceTimeKindLabor,
		Minutes: 600, PerformedOn: now.AddDate(0, 0, -5),
	}, testutil.TestActor())
	require.NoError(t, err)
	// 30 minutes and a $500 part: $32.50 + $500 = $532.50.
	_, err = svc.LogTime(ctx, tx, app.LogTimeParams{
		TicketID: t2.ID, StaffID: staffID, Kind: domain.ServiceTimeKindLabor,
		Minutes: 30, PerformedOn: now.AddDate(0, 0, -5),
	}, testutil.TestActor())
	require.NoError(t, err)
	_, err = tickets.CreatePart(ctx, tx, store.CreatePartParams{
		TicketID: t2.ID, Name: "Burr set", Quantity: 1, UnitCostCents: 50000,
	})
	require.NoError(t, err)

	report, err := svc.CostByAccount(ctx, tx, 90, domain.ServiceAccountCostByCost, now)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(report.Rows), 2)
	assert.Equal(t, hoursHeavy.ID, report.Rows[0].CustomerID,
		"$650 of hours outranks a $532.50 part")
	assert.Equal(t, 65000, report.Rows[0].Summary.TotalCostCents())

	// By parts spend the order inverts, which is the comparison the rate makes
	// meaningful rather than redundant.
	byParts, err := svc.CostByAccount(ctx, tx, 90, domain.ServiceAccountCostByParts, now)
	require.NoError(t, err)
	assert.Equal(t, partsHeavy.ID, byParts.Rows[0].CustomerID)
}

// The money column turns on from either signal: an hour that already carries a
// rate, or a rate set for the next one. Both matter — the first keeps the
// column after a rate is cleared, the second makes a shop's first rate visibly
// do something before anybody has logged an hour at it.
func TestCostByAccountShowsCostFromEitherSignal(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newRatedTicketService()
	tickets := store.NewServiceTicketStore()
	customer := testutil.CreateCustomer(t, tx)
	staffID := testutil.CreateStaff(t, tx)
	now := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)

	ticket, err := tickets.Create(ctx, tx, store.CreateServiceTicketParams{
		Number: "SVC-SHOW-1", CustomerID: customer.ID, Title: "work",
		Severity: domain.ServiceSeverityRoutine,
	})
	require.NoError(t, err)

	// Nothing priced and no rate: no column, and a cost ranking falls back
	// rather than ordering by parts spend under a Cost heading.
	report, err := svc.CostByAccount(ctx, tx, 90, domain.ServiceAccountCostByCost, now)
	require.NoError(t, err)
	assert.False(t, report.ShowCost())
	assert.Equal(t, domain.ServiceAccountCostByHours, report.Sort,
		"the strip has to highlight the ranking that actually ran")

	// A rate set, still no hours logged at it.
	require.NoError(t, svc.SetLaborRates(ctx, tx, domain.ServiceLaborRates{
		LaborCentsPerHour: cents(6500),
	}, testutil.TestActor()))

	report, err = svc.CostByAccount(ctx, tx, 90, domain.ServiceAccountCostByCost, now)
	require.NoError(t, err)
	assert.True(t, report.ShowCost(), "a first rate should visibly do something")
	assert.Equal(t, domain.ServiceAccountCostByCost, report.Sort)

	// An hour logged at it, then the rate cleared: the stamped cost keeps the
	// column alive.
	_, err = svc.LogTime(ctx, tx, app.LogTimeParams{
		TicketID: ticket.ID, StaffID: staffID, Kind: domain.ServiceTimeKindLabor,
		Minutes: 60, PerformedOn: now.AddDate(0, 0, -5),
	}, testutil.TestActor())
	require.NoError(t, err)
	require.NoError(t, svc.SetLaborRates(ctx, tx, domain.ServiceLaborRates{}, testutil.TestActor()))

	report, err = svc.CostByAccount(ctx, tx, 90, domain.ServiceAccountCostByCost, now)
	require.NoError(t, err)
	assert.True(t, report.ShowCost(), "hours already priced still cost what they cost")
	assert.Equal(t, domain.ServiceAccountCostByCost, report.Sort)
}

// One definition of "unset", and it is blank.
//
// A saved 0.00 labour rate reads as unset to ServiceLaborRates.Set(), so it
// would stamp every hour uncosted while the settings page showed a figure
// somebody had deliberately typed — and a travel rate paired with it would be
// exactly as inert as one paired with nothing, which is what
// ErrTravelRateWithoutLabor exists to prevent.
func TestLaborRateZeroIsRefused(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newRatedTicketService()

	err := svc.SetLaborRates(ctx, tx, domain.ServiceLaborRates{
		LaborCentsPerHour: cents(0),
	}, testutil.TestActor())
	assert.ErrorIs(t, err, app.ErrLaborRateZero)

	// The case the previous spelling let through: zero labour, real travel.
	err = svc.SetLaborRates(ctx, tx, domain.ServiceLaborRates{
		LaborCentsPerHour:  cents(0),
		TravelCentsPerHour: cents(4000),
	}, testutil.TestActor())
	assert.Error(t, err, "a travel rate behind a zero labour rate prices nothing")

	// Travel keeps its zero: there it means the shop absorbs the drive.
	assert.NoError(t, svc.SetLaborRates(ctx, tx, domain.ServiceLaborRates{
		LaborCentsPerHour:  cents(6500),
		TravelCentsPerHour: cents(0),
	}, testutil.TestActor()))
}

// Zero and unset are different things on an entry, and both are reachable.
//
// At settings level a zero labour rate is refused — Set() reads it as unset.
// Per entry, zero means the shop absorbed the hour: the settings page offers
// exactly that for travel, and rateFor stamps it. So an hour can be priced at
// nothing, which is not the same as never having been priced, and only the
// second belongs in the unpriced-hours warning.
func TestZeroRateOnAnEntryMeansAbsorbedNotUnpriced(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newRatedTicketService()
	tickets := store.NewServiceTicketStore()
	customer := testutil.CreateCustomer(t, tx)
	staffID := testutil.CreateStaff(t, tx)

	ticket, err := tickets.Create(ctx, tx, store.CreateServiceTicketParams{
		Number: "SVC-ABSORB-1", CustomerID: customer.ID, Title: "drive",
		Severity: domain.ServiceSeverityRoutine,
	})
	require.NoError(t, err)

	// Labour priced, travel absorbed — the configuration the settings page
	// describes in so many words.
	require.NoError(t, svc.SetLaborRates(ctx, tx, domain.ServiceLaborRates{
		LaborCentsPerHour:  cents(6000),
		TravelCentsPerHour: cents(0),
	}, testutil.TestActor()))

	drive, err := svc.LogTime(ctx, tx, app.LogTimeParams{
		TicketID: ticket.ID, StaffID: staffID, Kind: domain.ServiceTimeKindTravel, Minutes: 90,
	}, testutil.TestActor())
	require.NoError(t, err)
	require.True(t, drive.Costed(), "priced at nothing is still priced")
	assert.Equal(t, 0, drive.CostCents())

	totals, err := svc.Totals(ctx, tx, ticket.ID)
	require.NoError(t, err)
	assert.Equal(t, 0, totals.UncostedMinutes,
		"an absorbed drive must not show up as hours nobody has priced")
	assert.True(t, totals.FullyCosted())

	// And the manual path can express the same state the stamp just created.
	repriced, err := svc.RepriceTimeEntry(ctx, tx, ticket.ID, drive.ID, cents(0), testutil.TestActor())
	require.NoError(t, err)
	assert.True(t, repriced.Costed())

	// Nil is the other thing: back to genuinely unpriced.
	cleared, err := svc.RepriceTimeEntry(ctx, tx, ticket.ID, drive.ID, nil, testutil.TestActor())
	require.NoError(t, err)
	assert.False(t, cleared.Costed())

	totals, err = svc.Totals(ctx, tx, ticket.ID)
	require.NoError(t, err)
	assert.Equal(t, 90, totals.UncostedMinutes)
	assert.False(t, totals.FullyCosted())
}
