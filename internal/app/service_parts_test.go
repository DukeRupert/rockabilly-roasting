package app_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/testutil"
)

func openTestTicket(t *testing.T, svc *app.ServiceTicketService, tx pgx.Tx) *domain.ServiceTicket {
	t.Helper()
	customer := testutil.CreateCustomer(t, tx)
	ticket, err := svc.Open(t.Context(), tx, openParams(customer.ID), testutil.TestActor())
	require.NoError(t, err)
	return ticket
}

func TestPartAddRejectsNamelessLine(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newTicketService()
	ticket := openTestTicket(t, svc, tx)

	// "1 × $4.25" on a repair record six months later is worse than no line.
	_, err := svc.AddPart(ctx, tx, app.AddPartParams{
		TicketID: ticket.ID, Name: "  ", Quantity: 1,
	}, testutil.TestActor())
	require.ErrorIs(t, err, app.ErrPartNameRequired)
}

func TestPartAddRejectsNonsenseQuantityAndCost(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newTicketService()
	ticket := openTestTicket(t, svc, tx)

	_, err := svc.AddPart(ctx, tx, app.AddPartParams{
		TicketID: ticket.ID, Name: "Gasket", Quantity: 0,
	}, testutil.TestActor())
	require.ErrorIs(t, err, app.ErrInvalidPartQuantity)

	_, err = svc.AddPart(ctx, tx, app.AddPartParams{
		TicketID: ticket.ID, Name: "Gasket", Quantity: 1, UnitCostCents: -100,
	}, testutil.TestActor())
	require.ErrorIs(t, err, app.ErrInvalidPartCost)
}

// A part off a shelf the shop already paid for still belongs on the record.
func TestPartAddAllowsAZeroCost(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newTicketService()
	ticket := openTestTicket(t, svc, tx)

	part, err := svc.AddPart(ctx, tx, app.AddPartParams{
		TicketID: ticket.ID, Name: "Gasket", Quantity: 2,
	}, testutil.TestActor())
	require.NoError(t, err)

	assert.Equal(t, domain.ServicePartStatusNeeded, part.Status)
	assert.Equal(t, 0, part.TotalCostCents())
	assert.Contains(t, auditActionsFor(t, tx, "service_ticket", ticket.ID), audit.AuditServicePartAdded)
}

// The ordered → arrived → fitted trail is the "what was ordered and replaced"
// question the whole feature was asked for.
func TestPartStatusKeepsTheWholeTrail(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newTicketService()
	ticket := openTestTicket(t, svc, tx)

	part, err := svc.AddPart(ctx, tx, app.AddPartParams{
		TicketID: ticket.ID, Name: "Group head gasket", Quantity: 2, UnitCostCents: 425,
	}, testutil.TestActor())
	require.NoError(t, err)

	orderedOn := time.Now().AddDate(0, 0, -9)
	_, err = svc.SetPartStatus(ctx, tx, ticket.ID, part.ID, domain.ServicePartStatusOrdered, orderedOn, testutil.TestActor())
	require.NoError(t, err)
	_, err = svc.SetPartStatus(ctx, tx, ticket.ID, part.ID, domain.ServicePartStatusReceived, time.Time{}, testutil.TestActor())
	require.NoError(t, err)
	fitted, err := svc.SetPartStatus(ctx, tx, ticket.ID, part.ID, domain.ServicePartStatusInstalled, time.Time{}, testutil.TestActor())
	require.NoError(t, err)

	// A fitted part still remembers when it was ordered — the gap is the answer
	// to "why did this repair take three weeks".
	require.NotNil(t, fitted.OrderedOn)
	require.NotNil(t, fitted.ReceivedOn)
	require.NotNil(t, fitted.InstalledOn)
	assert.Equal(t, 850, fitted.TotalCostCents())
}

// A part id from one ticket posted to another's route must not update.
func TestPartStatusRefusesAPartFromAnotherTicket(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newTicketService()

	mine := openTestTicket(t, svc, tx)
	theirs := openTestTicket(t, svc, tx)
	part, err := svc.AddPart(ctx, tx, app.AddPartParams{
		TicketID: mine.ID, Name: "Gasket", Quantity: 1,
	}, testutil.TestActor())
	require.NoError(t, err)

	_, err = svc.SetPartStatus(ctx, tx, theirs.ID, part.ID, domain.ServicePartStatusOrdered, time.Time{}, testutil.TestActor())
	require.ErrorIs(t, err, app.ErrServicePartNotFound)

	err = svc.RemovePart(ctx, tx, theirs.ID, part.ID, testutil.TestActor())
	require.ErrorIs(t, err, app.ErrServicePartNotFound)
}

func TestPartRemoveIsAudited(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newTicketService()
	ticket := openTestTicket(t, svc, tx)

	part, err := svc.AddPart(ctx, tx, app.AddPartParams{
		TicketID: ticket.ID, Name: "Pump", Quantity: 1, UnitCostCents: 12000,
	}, testutil.TestActor())
	require.NoError(t, err)

	require.NoError(t, svc.RemovePart(ctx, tx, ticket.ID, part.ID, testutil.TestActor()))

	left, err := svc.ListParts(ctx, tx, ticket.ID)
	require.NoError(t, err)
	assert.Empty(t, left)
	// A removal changes what the repair appears to have cost, so it is audited
	// as loudly as the addition.
	assert.Contains(t, auditActionsFor(t, tx, "service_ticket", ticket.ID), audit.AuditServicePartRemoved)
}

func TestLogTimeRejectsZeroMinutes(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newTicketService()
	ticket := openTestTicket(t, svc, tx)
	staffID := testutil.CreateStaff(t, tx)

	// Zero is somebody tabbing past the field, and it would quietly dilute
	// every hours-per-account number computed later.
	_, err := svc.LogTime(ctx, tx, app.LogTimeParams{
		TicketID: ticket.ID, StaffID: staffID, Minutes: 0,
	}, testutil.TestActor())
	require.ErrorIs(t, err, app.ErrInvalidTimeMinutes)
}

func TestLogTimeDefaultsToLabour(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newTicketService()
	ticket := openTestTicket(t, svc, tx)
	staffID := testutil.CreateStaff(t, tx)

	entry, err := svc.LogTime(ctx, tx, app.LogTimeParams{
		TicketID: ticket.ID, StaffID: staffID, Minutes: 90,
	}, testutil.TestActor())
	require.NoError(t, err)

	assert.Equal(t, domain.ServiceTimeKindLabor, entry.Kind)
	assert.Contains(t, auditActionsFor(t, tx, "service_ticket", ticket.ID), audit.AuditServiceTimeLogged)
}

func TestTotalsSeparateTravelFromLabour(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newTicketService()
	ticket := openTestTicket(t, svc, tx)
	staffID := testutil.CreateStaff(t, tx)

	_, err := svc.AddPart(ctx, tx, app.AddPartParams{
		TicketID: ticket.ID, Name: "Gasket", Quantity: 2, UnitCostCents: 425,
	}, testutil.TestActor())
	require.NoError(t, err)
	_, err = svc.LogTime(ctx, tx, app.LogTimeParams{
		TicketID: ticket.ID, StaffID: staffID, Kind: domain.ServiceTimeKindLabor,
		Minutes: 90, Billable: true,
	}, testutil.TestActor())
	require.NoError(t, err)
	_, err = svc.LogTime(ctx, tx, app.LogTimeParams{
		TicketID: ticket.ID, StaffID: staffID, Kind: domain.ServiceTimeKindTravel, Minutes: 45,
	}, testutil.TestActor())
	require.NoError(t, err)

	totals, err := svc.Totals(ctx, tx, ticket.ID)
	require.NoError(t, err)

	assert.Equal(t, 850, totals.PartsCostCents)
	assert.Equal(t, 90, totals.LaborMinutes)
	// "We drove 90 minutes for a $4 gasket" is the fact worth totting up.
	assert.Equal(t, 45, totals.TravelMinutes)
	assert.Equal(t, 90, totals.BillableMinutes)
	assert.Equal(t, 135, totals.TotalMinutes())
}

func TestTimeEntryRemoveRefusesAnotherTicketsEntry(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newTicketService()
	staffID := testutil.CreateStaff(t, tx)

	mine := openTestTicket(t, svc, tx)
	theirs := openTestTicket(t, svc, tx)
	entry, err := svc.LogTime(ctx, tx, app.LogTimeParams{
		TicketID: mine.ID, StaffID: staffID, Minutes: 30,
	}, testutil.TestActor())
	require.NoError(t, err)

	err = svc.RemoveTimeEntry(ctx, tx, theirs.ID, entry.ID, testutil.TestActor())
	require.ErrorIs(t, err, app.ErrServiceTimeEntryNotFound)

	require.NoError(t, svc.RemoveTimeEntry(ctx, tx, mine.ID, entry.ID, testutil.TestActor()))
	assert.Contains(t, auditActionsFor(t, tx, "service_ticket", mine.ID), audit.AuditServiceTimeRemoved)
}

func TestPartsAndTimeOnAMissingTicketAreNotFound(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newTicketService()

	_, err := svc.AddPart(ctx, tx, app.AddPartParams{
		TicketID: uuid.New(), Name: "Gasket", Quantity: 1,
	}, testutil.TestActor())
	require.ErrorIs(t, err, app.ErrServiceTicketNotFound)
}
