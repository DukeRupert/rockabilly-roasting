package app_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

func newTicketService() *app.ServiceTicketService {
	return app.NewServiceTicketService(store.NewServiceTicketStore(), store.NewEquipmentStore(), audit.NewAuditWriter())
}

func openParams(customerID uuid.UUID) app.OpenTicketParams {
	return app.OpenTicketParams{
		CustomerID:  customerID,
		Title:       "  Group head leaking  ",
		Description: "Drips between shots.",
		Severity:    domain.ServiceSeverityDegraded,
	}
}

func TestTicketOpenMintsANumberAndAudits(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newTicketService()
	customer := testutil.CreateCustomer(t, tx)

	ticket, err := svc.Open(ctx, tx, openParams(customer.ID), testutil.TestActor())
	require.NoError(t, err)

	assert.Equal(t, "Group head leaking", ticket.Title)
	assert.Equal(t, domain.ServiceTicketStatusNew, ticket.Status)
	// Same shape as orders.number, so the two schemes cannot drift.
	assert.Regexp(t, `^SVC-[0-9A-F]{10}$`, ticket.Number)

	assert.Contains(t, auditActionsFor(t, tx, "service_ticket", ticket.ID), audit.AuditServiceTicketOpened)
}

func TestTicketOpenRejectsBlankTitle(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newTicketService()
	customer := testutil.CreateCustomer(t, tx)

	p := openParams(customer.ID)
	p.Title = "   "
	_, err := svc.Open(ctx, tx, p, testutil.TestActor())

	// The queue is a column of titles; a blank one cannot be triaged.
	require.ErrorIs(t, err, app.ErrServiceTicketTitleRequired)
}

func TestTicketOpenDefaultsToRoutine(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newTicketService()
	customer := testutil.CreateCustomer(t, tx)

	p := openParams(customer.ID)
	p.Severity = ""
	ticket, err := svc.Open(ctx, tx, p, testutil.TestActor())
	require.NoError(t, err)

	// Guessing that a hand-opened ticket is an emergency would train staff to
	// ignore the severity badge.
	assert.Equal(t, domain.ServiceSeverityRoutine, ticket.Severity)
}

// A ticket filed against somebody else's machine would put one cafe's repair
// history on another's page.
func TestTicketOpenRefusesAnotherCustomersMachine(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newTicketService()
	equip := newEquipmentService()

	owner := testutil.CreateCustomer(t, tx, testutil.WithEmail("owner@example.test"))
	other := testutil.CreateCustomer(t, tx, testutil.WithEmail("other@example.test"))
	machine, err := equip.Register(ctx, tx, registerParams(owner.ID), testutil.TestActor())
	require.NoError(t, err)

	p := openParams(other.ID)
	p.EquipmentID = &machine.ID
	_, err = svc.Open(ctx, tx, p, testutil.TestActor())

	require.ErrorIs(t, err, app.ErrTicketEquipmentMismatch)
}

// A tech reading the ticket needs to know which shop to drive to, and re-typing
// the address is how that goes wrong.
func TestTicketOpenInheritsTheMachinesSite(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newTicketService()
	equip := newEquipmentService()

	customer := testutil.CreateCustomer(t, tx)
	address := testutil.CreateAddress(t, tx, customer.ID)
	p := registerParams(customer.ID)
	p.AddressID = &address.ID
	machine, err := equip.Register(ctx, tx, p, testutil.TestActor())
	require.NoError(t, err)

	tp := openParams(customer.ID)
	tp.EquipmentID = &machine.ID
	ticket, err := svc.Open(ctx, tx, tp, testutil.TestActor())
	require.NoError(t, err)

	require.NotNil(t, ticket.AddressID)
	assert.Equal(t, address.ID, *ticket.AddressID)
}

// The rule the whole staleness flag rests on, exercised through the service
// that applies it rather than only through the enum that states it.
func TestTicketNoteMovesTheContactClockOnlyForContact(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newTicketService()
	customer := testutil.CreateCustomer(t, tx)
	staffID := testutil.CreateStaff(t, tx)

	ticket, err := svc.Open(ctx, tx, openParams(customer.ID), testutil.TestActor())
	require.NoError(t, err)

	// Push the clock back so a fresh touch is visibly later.
	_, err = tx.Exec(ctx, `UPDATE service_tickets SET last_contact_at = now() - interval '10 days' WHERE id = $1`, ticket.ID)
	require.NoError(t, err)
	quiet, err := svc.GetByID(ctx, tx, ticket.ID)
	require.NoError(t, err)

	// An internal note is not telling the cafe anything.
	_, err = svc.AddNote(ctx, tx, app.AddNoteParams{
		TicketID: ticket.ID, Kind: domain.ServiceNoteKindNote,
		Body: "Chased the supplier again", StaffID: &staffID,
	}, testutil.TestActor())
	require.NoError(t, err)

	afterNote, err := svc.GetByID(ctx, tx, ticket.ID)
	require.NoError(t, err)
	assert.True(t, afterNote.LastContactAt.Equal(quiet.LastContactAt),
		"an internal note must not silence the quiet flag")

	// A call is.
	_, err = svc.AddNote(ctx, tx, app.AddNoteParams{
		TicketID: ticket.ID, Kind: domain.ServiceNoteKindCall,
		Body: "Rang Dana, part ships Thursday", StaffID: &staffID,
	}, testutil.TestActor())
	require.NoError(t, err)

	afterCall, err := svc.GetByID(ctx, tx, ticket.ID)
	require.NoError(t, err)
	assert.True(t, afterCall.LastContactAt.After(quiet.LastContactAt))
}

func TestTicketNoteRejectsBlankBody(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newTicketService()
	customer := testutil.CreateCustomer(t, tx)

	ticket, err := svc.Open(ctx, tx, openParams(customer.ID), testutil.TestActor())
	require.NoError(t, err)

	// A blank note counting as contact would reset the clock while saying
	// nothing — exactly what the flag exists to catch.
	_, err = svc.AddNote(ctx, tx, app.AddNoteParams{
		TicketID: ticket.ID, Kind: domain.ServiceNoteKindCall, Body: "   ",
	}, testutil.TestActor())
	require.ErrorIs(t, err, app.ErrEmptyServiceNote)
}

// A call logged late must not drag the clock backwards and make a ticket look
// staler than it is.
func TestTicketBackdatedNoteDoesNotRewindTheClock(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newTicketService()
	customer := testutil.CreateCustomer(t, tx)
	staffID := testutil.CreateStaff(t, tx)

	ticket, err := svc.Open(ctx, tx, openParams(customer.ID), testutil.TestActor())
	require.NoError(t, err)

	_, err = svc.AddNote(ctx, tx, app.AddNoteParams{
		TicketID:   ticket.ID,
		Kind:       domain.ServiceNoteKindCall,
		Body:       "Tuesday's call, written up on Thursday",
		OccurredAt: time.Now().Add(-72 * time.Hour),
		StaffID:    &staffID,
	}, testutil.TestActor())
	require.NoError(t, err)

	after, err := svc.GetByID(ctx, tx, ticket.ID)
	require.NoError(t, err)
	assert.True(t, after.LastContactAt.Equal(ticket.LastContactAt))
}

func TestTicketStatusNamesTheMovesWorthColouring(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newTicketService()
	customer := testutil.CreateCustomer(t, tx)

	ticket, err := svc.Open(ctx, tx, openParams(customer.ID), testutil.TestActor())
	require.NoError(t, err)

	_, err = svc.SetStatus(ctx, tx, ticket.ID, domain.ServiceTicketStatusWaitingParts, "", testutil.TestActor())
	require.NoError(t, err)
	resolved, err := svc.SetStatus(ctx, tx, ticket.ID, domain.ServiceTicketStatusResolved, "New gasket", testutil.TestActor())
	require.NoError(t, err)
	require.NotNil(t, resolved.ResolvedAt)
	_, err = svc.SetStatus(ctx, tx, ticket.ID, domain.ServiceTicketStatusInProgress, "", testutil.TestActor())
	require.NoError(t, err)

	actions := auditActionsFor(t, tx, "service_ticket", ticket.ID)
	assert.Contains(t, actions, audit.AuditServiceTicketStatus)
	assert.Contains(t, actions, audit.AuditServiceTicketResolved)
	assert.Contains(t, actions, audit.AuditServiceTicketReopened)
}

func TestTicketStatusIgnoresNoOp(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newTicketService()
	customer := testutil.CreateCustomer(t, tx)

	ticket, err := svc.Open(ctx, tx, openParams(customer.ID), testutil.TestActor())
	require.NoError(t, err)
	before := auditActionsFor(t, tx, "service_ticket", ticket.ID)

	_, err = svc.SetStatus(ctx, tx, ticket.ID, domain.ServiceTicketStatusNew, "", testutil.TestActor())
	require.NoError(t, err)

	assert.Len(t, auditActionsFor(t, tx, "service_ticket", ticket.ID), len(before))
}

func TestTicketListStaleIgnoresClosedWork(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newTicketService()
	customer := testutil.CreateCustomer(t, tx)

	quiet, err := svc.Open(ctx, tx, openParams(customer.ID), testutil.TestActor())
	require.NoError(t, err)
	closed, err := svc.Open(ctx, tx, openParams(customer.ID), testutil.TestActor())
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `UPDATE service_tickets SET last_contact_at = now() - interval '30 days'`)
	require.NoError(t, err)
	_, err = svc.SetStatus(ctx, tx, closed.ID, domain.ServiceTicketStatusResolved, "done", testutil.TestActor())
	require.NoError(t, err)

	stale, err := svc.ListStale(ctx, tx, time.Now().Add(-domain.DefaultStaleContactWindow), 0)
	require.NoError(t, err)

	require.Len(t, stale, 1)
	assert.Equal(t, quiet.ID, stale[0].ID)
}

func TestTicketGetHidesAnotherCustomersTicket(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newTicketService()

	owner := testutil.CreateCustomer(t, tx, testutil.WithEmail("owner@example.test"))
	other := testutil.CreateCustomer(t, tx, testutil.WithEmail("other@example.test"))
	ticket, err := svc.Open(ctx, tx, openParams(owner.ID), testutil.TestActor())
	require.NoError(t, err)

	_, err = svc.Get(ctx, tx, ticket.ID, other.ID)
	require.ErrorIs(t, err, app.ErrServiceTicketNotFound)
}

func TestTicketNotesRespectCustomerVisibility(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newTicketService()
	customer := testutil.CreateCustomer(t, tx)
	staffID := testutil.CreateStaff(t, tx)

	ticket, err := svc.Open(ctx, tx, openParams(customer.ID), testutil.TestActor())
	require.NoError(t, err)

	_, err = svc.AddNote(ctx, tx, app.AddNoteParams{
		TicketID: ticket.ID, Kind: domain.ServiceNoteKindNote,
		Body: "Owner is being difficult about the bill", StaffID: &staffID,
	}, testutil.TestActor())
	require.NoError(t, err)
	_, err = svc.AddNote(ctx, tx, app.AddNoteParams{
		TicketID: ticket.ID, Kind: domain.ServiceNoteKindEmail,
		Body: "Sent them the quote", StaffID: &staffID, CustomerVisible: true,
	}, testutil.TestActor())
	require.NoError(t, err)

	all, err := svc.ListNotes(ctx, tx, ticket.ID, false)
	require.NoError(t, err)
	assert.Len(t, all, 2)

	visible, err := svc.ListNotes(ctx, tx, ticket.ID, true)
	require.NoError(t, err)
	require.Len(t, visible, 1)
	assert.Equal(t, domain.ServiceNoteKindEmail, visible[0].Kind)
}
