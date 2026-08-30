package store_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

func costDay(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// costFixture is a customer with a machine and a ticket against it, plus a
// second ticket on the same account that names no machine.
type costFixture struct {
	tickets    *store.ServiceTicketStore
	customerID uuid.UUID
	equipment  *domain.Equipment
	onMachine  *domain.ServiceTicket
	callOut    *domain.ServiceTicket
	staffID    uuid.UUID
}

func newCostFixture(t *testing.T, tx pgx.Tx) costFixture {
	t.Helper()
	ctx := t.Context()

	tickets := store.NewServiceTicketStore()
	customer := testutil.CreateCustomer(t, tx)

	equip, err := store.NewEquipmentStore().Create(ctx, tx, equipmentParams(customer.ID))
	require.NoError(t, err)

	machineID := equip.ID
	onMachine, err := tickets.Create(ctx, tx, store.CreateServiceTicketParams{
		Number:      "SVC-COST-" + uuid.NewString()[:8],
		CustomerID:  customer.ID,
		EquipmentID: &machineID,
		Title:       "Group head leaking",
		Severity:    domain.ServiceSeverityDegraded,
	})
	require.NoError(t, err)

	callOut, err := tickets.Create(ctx, tx, store.CreateServiceTicketParams{
		Number:     "SVC-COST-" + uuid.NewString()[:8],
		CustomerID: customer.ID,
		Title:      "Water taste complaint, no machine named",
		Severity:   domain.ServiceSeverityRoutine,
	})
	require.NoError(t, err)

	return costFixture{
		tickets:    tickets,
		customerID: customer.ID,
		equipment:  equip,
		onMachine:  onMachine,
		callOut:    callOut,
		staffID:    testutil.CreateStaff(t, tx),
	}
}

func (f costFixture) logTime(t *testing.T, tx pgx.Tx, ticketID uuid.UUID, minutes int, kind domain.ServiceTimeKind, on time.Time, billable bool) {
	t.Helper()
	_, err := f.tickets.CreateTimeEntry(t.Context(), tx, store.CreateTimeEntryParams{
		TicketID:    ticketID,
		StaffID:     f.staffID,
		Kind:        kind,
		Minutes:     minutes,
		PerformedOn: on,
		Billable:    billable,
	})
	require.NoError(t, err)
}

func (f costFixture) addPart(t *testing.T, tx pgx.Tx, ticketID uuid.UUID, qty, unitCents int) *domain.ServicePart {
	t.Helper()
	p, err := f.tickets.CreatePart(t.Context(), tx, store.CreatePartParams{
		TicketID:      ticketID,
		Name:          "Group head gasket",
		Quantity:      qty,
		UnitCostCents: unitCents,
	})
	require.NoError(t, err)
	return p
}

// The core promise: everything recorded counts, whether or not it was billed.
func TestCostSummaryCountsNonBillableWork(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := t.Context()
	f := newCostFixture(t, tx)
	on := costDay(2026, time.August, 10)

	f.logTime(t, tx, f.onMachine.ID, 90, domain.ServiceTimeKindLabor, on, false)
	f.logTime(t, tx, f.onMachine.ID, 30, domain.ServiceTimeKindTravel, on, false)
	f.logTime(t, tx, f.onMachine.ID, 45, domain.ServiceTimeKindLabor, on, true)
	f.addPart(t, tx, f.onMachine.ID, 2, 425)

	got, err := f.tickets.CostSummary(ctx, tx, store.ServiceCostFilter{EquipmentID: &f.equipment.ID})
	require.NoError(t, err)

	assert.Equal(t, 850, got.PartsCostCents, "two gaskets at $4.25")
	assert.Equal(t, 1, got.PartCount)
	assert.Equal(t, 135, got.LaborMinutes)
	assert.Equal(t, 30, got.TravelMinutes)
	assert.Equal(t, 165, got.TotalMinutes(), "billable or not, the hours were spent")
	assert.Equal(t, 45, got.BillableMinutes, "reported alongside the total, never instead of it")
	assert.Equal(t, 1, got.Visits)
}

// A part still sitting at 'needed' has cost committed. Dropping it would make a
// repair in progress look free.
func TestCostSummaryCountsPartsBeforeTheyAreInstalled(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := t.Context()
	f := newCostFixture(t, tx)

	f.addPart(t, tx, f.onMachine.ID, 1, 12000)

	got, err := f.tickets.CostSummary(ctx, tx, store.ServiceCostFilter{EquipmentID: &f.equipment.ID})
	require.NoError(t, err)
	assert.Equal(t, 12000, got.PartsCostCents)
}

// The account roll-up has to include work that never named a machine — those
// hours went into the customer just the same.
func TestCostSummaryByCustomerIncludesUnattributedWork(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := t.Context()
	f := newCostFixture(t, tx)
	on := costDay(2026, time.August, 10)

	f.logTime(t, tx, f.onMachine.ID, 60, domain.ServiceTimeKindLabor, on, false)
	f.logTime(t, tx, f.callOut.ID, 40, domain.ServiceTimeKindLabor, on, false)

	byMachine, err := f.tickets.CostSummary(ctx, tx, store.ServiceCostFilter{EquipmentID: &f.equipment.ID})
	require.NoError(t, err)
	assert.Equal(t, 60, byMachine.TotalMinutes())
	assert.Equal(t, 1, byMachine.Visits)

	byAccount, err := f.tickets.CostSummary(ctx, tx, store.ServiceCostFilter{CustomerID: &f.customerID})
	require.NoError(t, err)
	assert.Equal(t, 100, byAccount.TotalMinutes(), "the machineless call-out counts against the account")
	assert.Equal(t, 2, byAccount.Visits)
}

// The window is measured against the day work happened, not the day the ticket
// was raised: a ticket opened in January and worked in April is April's cost.
func TestCostSummaryWindowUsesTheDayWorkHappened(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := t.Context()
	f := newCostFixture(t, tx)

	f.logTime(t, tx, f.onMachine.ID, 120, domain.ServiceTimeKindLabor, costDay(2025, time.February, 1), false)
	f.logTime(t, tx, f.onMachine.ID, 60, domain.ServiceTimeKindLabor, costDay(2026, time.August, 1), false)

	allTime, err := f.tickets.CostSummary(ctx, tx, store.ServiceCostFilter{EquipmentID: &f.equipment.ID})
	require.NoError(t, err)
	assert.Equal(t, 180, allTime.TotalMinutes())

	recent, err := f.tickets.CostSummary(ctx, tx, store.ServiceCostFilter{
		EquipmentID: &f.equipment.ID,
		Since:       costDay(2026, time.June, 1),
	})
	require.NoError(t, err)
	assert.Equal(t, 60, recent.TotalMinutes(), "last year's visit is outside the window")
	assert.Equal(t, 1, recent.Visits)
}

// Visits are tickets that carried work, not tickets that exist. A ticket that
// was only ever a phone call is not a visit.
func TestCostSummaryVisitsCountOnlyTicketsWithWork(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := t.Context()
	f := newCostFixture(t, tx)

	f.logTime(t, tx, f.onMachine.ID, 60, domain.ServiceTimeKindLabor, costDay(2026, time.August, 1), false)

	got, err := f.tickets.CostSummary(ctx, tx, store.ServiceCostFilter{CustomerID: &f.customerID})
	require.NoError(t, err)
	assert.Equal(t, 1, got.Visits, "the second ticket has no parts and no hours on it")
}

// A ticket with both parts and hours is one visit, not two — the count comes
// off a UNION that has to be de-duplicated.
func TestCostSummaryVisitsDoNotDoubleCount(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := t.Context()
	f := newCostFixture(t, tx)

	f.logTime(t, tx, f.onMachine.ID, 60, domain.ServiceTimeKindLabor, costDay(2026, time.August, 1), false)
	f.addPart(t, tx, f.onMachine.ID, 1, 500)
	f.addPart(t, tx, f.onMachine.ID, 1, 300)

	got, err := f.tickets.CostSummary(ctx, tx, store.ServiceCostFilter{EquipmentID: &f.equipment.ID})
	require.NoError(t, err)
	assert.Equal(t, 1, got.Visits)
	assert.Equal(t, 2, got.PartCount)
	assert.Equal(t, 800, got.PartsCostCents)
}

// The widened query and the per-ticket one must agree. A machine page saying
// $40 beside a ticket saying $58 reads as a bug in the money.
func TestCostSummaryAgreesWithTicketTotals(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := t.Context()
	f := newCostFixture(t, tx)
	on := costDay(2026, time.August, 10)

	f.logTime(t, tx, f.onMachine.ID, 90, domain.ServiceTimeKindLabor, on, true)
	f.logTime(t, tx, f.onMachine.ID, 25, domain.ServiceTimeKindTravel, on, false)
	f.addPart(t, tx, f.onMachine.ID, 3, 1999)

	perTicket, err := f.tickets.Totals(ctx, tx, f.onMachine.ID)
	require.NoError(t, err)

	widened, err := f.tickets.CostSummary(ctx, tx, store.ServiceCostFilter{EquipmentID: &f.equipment.ID})
	require.NoError(t, err)

	assert.Equal(t, perTicket, widened.ServiceTotals,
		"the roll-up is a widening of Totals, not a second opinion about it")
}

// A caller that forgot to scope must not silently get the whole shop's numbers.
func TestCostSummaryRefusesAnUnscopedQuery(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)

	_, err := store.NewServiceTicketStore().CostSummary(t.Context(), tx, store.ServiceCostFilter{})
	assert.Error(t, err)
}

// Empty is a legitimate answer, and has to be distinguishable from work.
func TestCostSummaryEmpty(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	f := newCostFixture(t, tx)

	got, err := f.tickets.CostSummary(t.Context(), tx, store.ServiceCostFilter{EquipmentID: &f.equipment.ID})
	require.NoError(t, err)
	assert.False(t, got.Any())
	assert.Equal(t, 0, got.MinutesPerVisit(), "no visits must not divide by zero")
}

// --- The cross-account table ---

// twoAccounts sets up a cheap account and an expensive one, so the ranking has
// something to get wrong.
func twoAccounts(t *testing.T, tx pgx.Tx) (cheap, dear costFixture) {
	t.Helper()
	on := costDay(2026, time.August, 10)

	cheap = newCostFixture(t, tx)
	// Few hours, expensive part.
	cheap.logTime(t, tx, cheap.onMachine.ID, 30, domain.ServiceTimeKindLabor, on, false)
	cheap.addPart(t, tx, cheap.onMachine.ID, 1, 50000)

	dear = newCostFixture(t, tx)
	// Many hours over several visits, cheap parts.
	dear.logTime(t, tx, dear.onMachine.ID, 300, domain.ServiceTimeKindLabor, on, false)
	dear.logTime(t, tx, dear.callOut.ID, 120, domain.ServiceTimeKindLabor, on, false)
	dear.addPart(t, tx, dear.onMachine.ID, 1, 900)

	return cheap, dear
}

func TestCostByCustomerRanksByHours(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := t.Context()
	_, dear := twoAccounts(t, tx)

	rows, err := store.NewServiceTicketStore().CostByCustomer(ctx, tx, store.ServiceAccountCostFilter{})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(rows), 2)

	assert.Equal(t, dear.customerID, rows[0].CustomerID,
		"hours are the default ranking — the account eating the crew's week leads")
	assert.Equal(t, 420, rows[0].Summary.TotalMinutes())
	assert.Equal(t, 2, rows[0].Summary.Visits, "two tickets carried work on that account")
}

func TestCostByCustomerRanksByParts(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := t.Context()
	cheap, _ := twoAccounts(t, tx)

	rows, err := store.NewServiceTicketStore().CostByCustomer(ctx, tx, store.ServiceAccountCostFilter{
		Sort: domain.ServiceAccountCostByParts,
	})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(rows), 2)

	assert.Equal(t, cheap.customerID, rows[0].CustomerID,
		"by spend the ranking inverts — the same two accounts, a different answer")
	assert.Equal(t, 50000, rows[0].Summary.PartsCostCents)
}

// An account with parts but no hours logged, and one with hours but no parts,
// must both appear. Joining two per-customer aggregates is the obvious
// implementation and it drops exactly these two rows.
func TestCostByCustomerKeepsOneSidedAccounts(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := t.Context()

	partsOnly := newCostFixture(t, tx)
	partsOnly.addPart(t, tx, partsOnly.onMachine.ID, 1, 2500)

	hoursOnly := newCostFixture(t, tx)
	hoursOnly.logTime(t, tx, hoursOnly.onMachine.ID, 45, domain.ServiceTimeKindLabor, costDay(2026, time.August, 1), false)

	rows, err := store.NewServiceTicketStore().CostByCustomer(ctx, tx, store.ServiceAccountCostFilter{})
	require.NoError(t, err)

	byCustomer := make(map[uuid.UUID]domain.ServiceAccountCost, len(rows))
	for _, r := range rows {
		byCustomer[r.CustomerID] = r
	}

	parts, ok := byCustomer[partsOnly.customerID]
	require.True(t, ok, "an account with parts and no hours still costs money")
	assert.Equal(t, 2500, parts.Summary.PartsCostCents)
	assert.Equal(t, 0, parts.Summary.TotalMinutes())

	hours, ok := byCustomer[hoursOnly.customerID]
	require.True(t, ok, "an account with hours and no parts still costs time")
	assert.Equal(t, 45, hours.Summary.TotalMinutes())
	assert.Equal(t, 0, hours.Summary.PartsCostCents)
}

// The table is driven by work recorded, not by the customer list — a table
// padded with zeros buries the rows worth reading.
func TestCostByCustomerOmitsUntouchedAccounts(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := t.Context()

	worked := newCostFixture(t, tx)
	worked.logTime(t, tx, worked.onMachine.ID, 60, domain.ServiceTimeKindLabor, costDay(2026, time.August, 1), false)
	untouched := newCostFixture(t, tx)

	rows, err := store.NewServiceTicketStore().CostByCustomer(ctx, tx, store.ServiceAccountCostFilter{})
	require.NoError(t, err)

	for _, r := range rows {
		assert.NotEqual(t, untouched.customerID, r.CustomerID,
			"an account nobody touched has no row")
	}
}

func TestCostByCustomerReportsMachinesAndLastWorked(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := t.Context()
	f := newCostFixture(t, tx)

	f.logTime(t, tx, f.onMachine.ID, 60, domain.ServiceTimeKindLabor, costDay(2025, time.May, 4), false)
	f.logTime(t, tx, f.onMachine.ID, 30, domain.ServiceTimeKindLabor, costDay(2026, time.July, 19), false)

	rows, err := store.NewServiceTicketStore().CostByCustomer(ctx, tx, store.ServiceAccountCostFilter{})
	require.NoError(t, err)

	var got *domain.ServiceAccountCost
	for i := range rows {
		if rows[i].CustomerID == f.customerID {
			got = &rows[i]
		}
	}
	require.NotNil(t, got)

	assert.Equal(t, 1, got.MachineCount, "one machine on the register for this account")
	require.NotNil(t, got.LastWorkOn)
	assert.Equal(t, costDay(2026, time.July, 19), got.LastWorkOn.UTC(),
		"the most recent day anything was recorded, not the first")
}

// The window applies to the ranking too, or "this quarter" would silently mean
// "ever".
func TestCostByCustomerHonoursTheWindow(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := t.Context()
	f := newCostFixture(t, tx)

	f.logTime(t, tx, f.onMachine.ID, 500, domain.ServiceTimeKindLabor, costDay(2024, time.March, 1), false)
	f.logTime(t, tx, f.onMachine.ID, 20, domain.ServiceTimeKindLabor, costDay(2026, time.August, 1), false)

	rows, err := store.NewServiceTicketStore().CostByCustomer(ctx, tx, store.ServiceAccountCostFilter{
		Since: costDay(2026, time.June, 1),
	})
	require.NoError(t, err)

	for _, r := range rows {
		if r.CustomerID == f.customerID {
			assert.Equal(t, 20, r.Summary.TotalMinutes(), "the 2024 visit is outside the window")
			return
		}
	}
	t.Fatal("the account with recent work should be in the table")
}

// --- Rate snapshots ---

// logTimeAt logs a stint already stamped with a rate, the way the service does.
func (f costFixture) logTimeAt(t *testing.T, tx pgx.Tx, ticketID uuid.UUID, minutes int, kind domain.ServiceTimeKind, on time.Time, rateCents *int) {
	t.Helper()
	_, err := f.tickets.CreateTimeEntry(t.Context(), tx, store.CreateTimeEntryParams{
		TicketID:    ticketID,
		StaffID:     f.staffID,
		Kind:        kind,
		Minutes:     minutes,
		PerformedOn: on,
		RateCents:   rateCents,
	})
	require.NoError(t, err)
}

func ratePtr(cents int) *int { return &cents }

// Two hours booked at two different rates cost the sum of what each was booked
// at — not either rate applied to both.
func TestCostSummarySumsEachHourAtItsOwnRate(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := t.Context()
	f := newCostFixture(t, tx)
	on := costDay(2026, time.August, 10)

	f.logTimeAt(t, tx, f.onMachine.ID, 60, domain.ServiceTimeKindLabor, on, ratePtr(6000))
	f.logTimeAt(t, tx, f.onMachine.ID, 60, domain.ServiceTimeKindLabor, on, ratePtr(9000))

	got, err := f.tickets.CostSummary(ctx, tx, store.ServiceCostFilter{EquipmentID: &f.equipment.ID})
	require.NoError(t, err)

	assert.Equal(t, 15000, got.LaborCostCents, "$60 then $90 — not two hours at either")
	assert.Equal(t, 120, got.LaborMinutes)
	assert.True(t, got.FullyCosted())
}

// Unpriced hours contribute minutes and no money, and are counted separately so
// a report can say the total is a floor.
func TestCostSummarySeparatesUncostedHours(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := t.Context()
	f := newCostFixture(t, tx)
	on := costDay(2026, time.August, 10)

	f.logTimeAt(t, tx, f.onMachine.ID, 60, domain.ServiceTimeKindLabor, on, ratePtr(6000))
	f.logTimeAt(t, tx, f.onMachine.ID, 90, domain.ServiceTimeKindLabor, on, nil)

	got, err := f.tickets.CostSummary(ctx, tx, store.ServiceCostFilter{EquipmentID: &f.equipment.ID})
	require.NoError(t, err)

	assert.Equal(t, 6000, got.LaborCostCents)
	assert.Equal(t, 150, got.LaborMinutes, "every minute counts as time")
	assert.Equal(t, 90, got.UncostedMinutes)
	assert.False(t, got.FullyCosted())
}

// An explicit zero rate is a decision — the shop absorbs the hour — and is not
// the same as never having priced it.
func TestCostSummaryZeroRateIsNotUncosted(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := t.Context()
	f := newCostFixture(t, tx)

	f.logTimeAt(t, tx, f.onMachine.ID, 60, domain.ServiceTimeKindTravel, costDay(2026, time.August, 10), ratePtr(0))

	got, err := f.tickets.CostSummary(ctx, tx, store.ServiceCostFilter{EquipmentID: &f.equipment.ID})
	require.NoError(t, err)

	assert.Equal(t, 0, got.LaborCostCents)
	assert.Equal(t, 0, got.UncostedMinutes, "priced at nothing is still priced")
	assert.True(t, got.FullyCosted())
}

// The per-ticket roll-up and the widened one have to agree about cost too.
func TestTotalsAndCostSummaryAgreeOnCost(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := t.Context()
	f := newCostFixture(t, tx)
	on := costDay(2026, time.August, 10)

	f.logTimeAt(t, tx, f.onMachine.ID, 150, domain.ServiceTimeKindLabor, on, ratePtr(6500))
	f.logTimeAt(t, tx, f.onMachine.ID, 45, domain.ServiceTimeKindTravel, on, nil)
	f.addPart(t, tx, f.onMachine.ID, 2, 425)

	perTicket, err := f.tickets.Totals(ctx, tx, f.onMachine.ID)
	require.NoError(t, err)
	widened, err := f.tickets.CostSummary(ctx, tx, store.ServiceCostFilter{EquipmentID: &f.equipment.ID})
	require.NoError(t, err)

	assert.Equal(t, perTicket, widened.ServiceTotals)
	assert.Equal(t, 16250, perTicket.LaborCostCents)
	assert.Equal(t, 45, perTicket.UncostedMinutes)
}

// The cross-account table costs each account's hours the same way, and ranks on
// the result rather than on a rate passed in.
func TestCostByCustomerCostsFromStampedRates(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := t.Context()
	on := costDay(2026, time.August, 10)

	cheapHours := newCostFixture(t, tx)
	cheapHours.logTimeAt(t, tx, cheapHours.onMachine.ID, 600, domain.ServiceTimeKindLabor, on, ratePtr(2000))

	dearHours := newCostFixture(t, tx)
	dearHours.logTimeAt(t, tx, dearHours.onMachine.ID, 300, domain.ServiceTimeKindLabor, on, ratePtr(9000))

	rows, err := store.NewServiceTicketStore().CostByCustomer(ctx, tx, store.ServiceAccountCostFilter{
		Sort: domain.ServiceAccountCostByCost,
	})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(rows), 2)

	assert.Equal(t, dearHours.customerID, rows[0].CustomerID,
		"5h at $90 outranks 10h at $20 — which ranking by hours alone would get backwards")
	assert.Equal(t, 45000, rows[0].Summary.LaborCostCents)
}
