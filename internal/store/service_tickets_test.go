package store_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

func ticketParams(customerID uuid.UUID, number string) store.CreateServiceTicketParams {
	return store.CreateServiceTicketParams{
		Number:      number,
		CustomerID:  customerID,
		Title:       "Group head leaking",
		Description: "Drips steadily between shots.",
		Severity:    domain.ServiceSeverityDegraded,
	}
}

func TestServiceTicketCreateStartsNewAndContacted(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	tickets := store.NewServiceTicketStore()
	customer := testutil.CreateCustomer(t, tx)

	before := time.Now()
	ticket, err := tickets.Create(ctx, tx, ticketParams(customer.ID, "SVC-TEST0001"))
	require.NoError(t, err)

	assert.Equal(t, domain.ServiceTicketStatusNew, ticket.Status)
	assert.True(t, ticket.Status.Open())
	assert.Nil(t, ticket.ResolvedAt)
	// The staleness clock starts at creation, so no query ever has to fall back
	// to created_at.
	assert.False(t, ticket.LastContactAt.Before(before.Add(-time.Second)),
		"a new ticket counts as contacted now")
}

func TestServiceTicketGetIsCustomerScoped(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	tickets := store.NewServiceTicketStore()

	owner := testutil.CreateCustomer(t, tx, testutil.WithEmail("owner@example.test"))
	other := testutil.CreateCustomer(t, tx, testutil.WithEmail("other@example.test"))
	ticket, err := tickets.Create(ctx, tx, ticketParams(owner.ID, "SVC-TEST0002"))
	require.NoError(t, err)

	_, err = tickets.Get(ctx, tx, ticket.ID, owner.ID)
	require.NoError(t, err)

	_, err = tickets.Get(ctx, tx, ticket.ID, other.ID)
	require.Error(t, err, "one cafe must not read another's ticket by id")
}

func TestServiceTicketResolveStampsAndReopenClears(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	tickets := store.NewServiceTicketStore()
	customer := testutil.CreateCustomer(t, tx)

	ticket, err := tickets.Create(ctx, tx, ticketParams(customer.ID, "SVC-TEST0003"))
	require.NoError(t, err)

	resolved, err := tickets.UpdateStatus(ctx, tx, ticket.ID, domain.ServiceTicketStatusResolved, "New gasket fitted")
	require.NoError(t, err)
	require.NotNil(t, resolved.ResolvedAt, "resolving stamps the time from the status itself")
	assert.Equal(t, "New gasket fitted", resolved.Resolution)
	assert.False(t, resolved.Status.Open())

	// Reopening has to clear the stamp, or a live ticket goes on claiming it
	// was fixed last Tuesday.
	reopened, err := tickets.UpdateStatus(ctx, tx, ticket.ID, domain.ServiceTicketStatusInProgress, "")
	require.NoError(t, err)
	assert.Nil(t, reopened.ResolvedAt)
	assert.True(t, reopened.Status.Open())
}

func TestServiceTicketTouchContactOnlyMovesForward(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	tickets := store.NewServiceTicketStore()
	customer := testutil.CreateCustomer(t, tx)

	ticket, err := tickets.Create(ctx, tx, ticketParams(customer.ID, "SVC-TEST0004"))
	require.NoError(t, err)

	// Tuesday's call, written up on Thursday, must not drag the clock back and
	// make the ticket look staler than it is.
	require.NoError(t, tickets.TouchContact(ctx, tx, ticket.ID, ticket.LastContactAt.Add(-48*time.Hour)))
	unchanged, err := tickets.GetByID(ctx, tx, ticket.ID)
	require.NoError(t, err)
	assert.True(t, unchanged.LastContactAt.Equal(ticket.LastContactAt))

	later := ticket.LastContactAt.Add(time.Hour)
	require.NoError(t, tickets.TouchContact(ctx, tx, ticket.ID, later))
	moved, err := tickets.GetByID(ctx, tx, ticket.ID)
	require.NoError(t, err)
	assert.True(t, moved.LastContactAt.After(ticket.LastContactAt))
}

func TestServiceTicketListStaleExcludesClosed(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	tickets := store.NewServiceTicketStore()
	customer := testutil.CreateCustomer(t, tx)

	quiet, err := tickets.Create(ctx, tx, ticketParams(customer.ID, "SVC-TEST0005"))
	require.NoError(t, err)
	closed, err := tickets.Create(ctx, tx, ticketParams(customer.ID, "SVC-TEST0006"))
	require.NoError(t, err)
	fresh, err := tickets.Create(ctx, tx, ticketParams(customer.ID, "SVC-TEST0007"))
	require.NoError(t, err)

	// Push two of them back beyond the window; leave the third at "just now".
	old := time.Now().Add(-30 * 24 * time.Hour)
	_, err = tx.Exec(ctx, `UPDATE service_tickets SET last_contact_at = $2 WHERE id = ANY($1)`,
		[]uuid.UUID{quiet.ID, closed.ID}, old)
	require.NoError(t, err)
	_, err = tickets.UpdateStatus(ctx, tx, closed.ID, domain.ServiceTicketStatusResolved, "done")
	require.NoError(t, err)

	cutoff := time.Now().Add(-7 * 24 * time.Hour)
	stale, err := tickets.List(ctx, tx, store.ServiceTicketFilter{StaleBefore: &cutoff})
	require.NoError(t, err)

	require.Len(t, stale, 1, "silence on finished work is the correct outcome, not a failure")
	assert.Equal(t, quiet.ID, stale[0].ID)
	assert.NotEqual(t, fresh.ID, stale[0].ID)
}

func TestServiceTicketListFilters(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	tickets := store.NewServiceTicketStore()
	equip := store.NewEquipmentStore()
	customer := testutil.CreateCustomer(t, tx)
	staffID := testutil.CreateStaff(t, tx)

	machine, err := equip.Create(ctx, tx, equipmentParams(customer.ID))
	require.NoError(t, err)

	onMachine := ticketParams(customer.ID, "SVC-TEST0008")
	onMachine.EquipmentID = &machine.ID
	onMachine.Severity = domain.ServiceSeverityDown
	onMachine.AssignedStaffID = &staffID
	assigned, err := tickets.Create(ctx, tx, onMachine)
	require.NoError(t, err)

	_, err = tickets.Create(ctx, tx, ticketParams(customer.ID, "SVC-TEST0009"))
	require.NoError(t, err)

	byEquipment, err := tickets.List(ctx, tx, store.ServiceTicketFilter{EquipmentID: &machine.ID})
	require.NoError(t, err)
	require.Len(t, byEquipment, 1)
	assert.Equal(t, assigned.ID, byEquipment[0].ID)

	byAssignee, err := tickets.List(ctx, tx, store.ServiceTicketFilter{AssignedTo: &staffID})
	require.NoError(t, err)
	require.Len(t, byAssignee, 1)

	bySeverity, err := tickets.List(ctx, tx, store.ServiceTicketFilter{
		CustomerID: &customer.ID,
		Severity:   domain.ServiceSeverityDown,
		OpenOnly:   true,
	})
	require.NoError(t, err)
	require.Len(t, bySeverity, 1)
	assert.Equal(t, assigned.ID, bySeverity[0].ID)
}

func TestServiceTicketNotesCustomerVisibility(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	tickets := store.NewServiceTicketStore()
	customer := testutil.CreateCustomer(t, tx)
	staffID := testutil.CreateStaff(t, tx)

	ticket, err := tickets.Create(ctx, tx, ticketParams(customer.ID, "SVC-TEST0010"))
	require.NoError(t, err)

	_, err = tickets.CreateNote(ctx, tx, store.CreateNoteParams{
		TicketID: ticket.ID,
		Kind:     domain.ServiceNoteKindNote,
		Body:     "Owner is being difficult about the bill",
		StaffID:  &staffID,
	})
	require.NoError(t, err)
	_, err = tickets.CreateNote(ctx, tx, store.CreateNoteParams{
		TicketID:        ticket.ID,
		Kind:            domain.ServiceNoteKindCall,
		Body:            "Rang to say the part ships Thursday",
		StaffID:         &staffID,
		CustomerVisible: true,
	})
	require.NoError(t, err)

	all, err := tickets.ListNotes(ctx, tx, ticket.ID, false)
	require.NoError(t, err)
	assert.Len(t, all, 2)

	// The portal read must never surface the internal half of the thread.
	visible, err := tickets.ListNotes(ctx, tx, ticket.ID, true)
	require.NoError(t, err)
	require.Len(t, visible, 1)
	assert.Equal(t, domain.ServiceNoteKindCall, visible[0].Kind)
}

func TestServicePartStatusKeepsEarlierDates(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	tickets := store.NewServiceTicketStore()
	customer := testutil.CreateCustomer(t, tx)

	ticket, err := tickets.Create(ctx, tx, ticketParams(customer.ID, "SVC-TEST0011"))
	require.NoError(t, err)

	part, err := tickets.CreatePart(ctx, tx, store.CreatePartParams{
		TicketID:      ticket.ID,
		Name:          "Group head gasket",
		PartNumber:    "LM-GK-8",
		Supplier:      "Espresso Parts",
		Quantity:      2,
		UnitCostCents: 425,
	})
	require.NoError(t, err)
	assert.Equal(t, domain.ServicePartStatusNeeded, part.Status)
	assert.Equal(t, 850, part.TotalCostCents())

	orderedOn := time.Now().AddDate(0, 0, -9)
	ordered, err := tickets.UpdatePartStatus(ctx, tx, part.ID, domain.ServicePartStatusOrdered, orderedOn)
	require.NoError(t, err)
	require.NotNil(t, ordered.OrderedOn)

	installed, err := tickets.UpdatePartStatus(ctx, tx, part.ID, domain.ServicePartStatusInstalled, time.Now())
	require.NoError(t, err)
	// A fitted part still has to remember when it was ordered — that gap is the
	// answer to "why did this repair take three weeks".
	require.NotNil(t, installed.OrderedOn)
	require.NotNil(t, installed.InstalledOn)
	assert.Equal(t, domain.ServicePartStatusInstalled, installed.Status)
}

func TestServiceTotalsRollUpPartsAndTime(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	tickets := store.NewServiceTicketStore()
	customer := testutil.CreateCustomer(t, tx)
	staffID := testutil.CreateStaff(t, tx)

	ticket, err := tickets.Create(ctx, tx, ticketParams(customer.ID, "SVC-TEST0012"))
	require.NoError(t, err)

	_, err = tickets.CreatePart(ctx, tx, store.CreatePartParams{
		TicketID: ticket.ID, Name: "Gasket", Quantity: 2, UnitCostCents: 425,
	})
	require.NoError(t, err)
	_, err = tickets.CreatePart(ctx, tx, store.CreatePartParams{
		TicketID: ticket.ID, Name: "Pump", Quantity: 1, UnitCostCents: 12000,
	})
	require.NoError(t, err)

	_, err = tickets.CreateTimeEntry(ctx, tx, store.CreateTimeEntryParams{
		TicketID: ticket.ID, StaffID: staffID, Kind: domain.ServiceTimeKindLabor,
		Minutes: 90, Billable: true,
	})
	require.NoError(t, err)
	_, err = tickets.CreateTimeEntry(ctx, tx, store.CreateTimeEntryParams{
		TicketID: ticket.ID, StaffID: staffID, Kind: domain.ServiceTimeKindTravel,
		Minutes: 45,
	})
	require.NoError(t, err)

	totals, err := tickets.Totals(ctx, tx, ticket.ID)
	require.NoError(t, err)

	assert.Equal(t, 12850, totals.PartsCostCents)
	assert.Equal(t, 90, totals.LaborMinutes)
	assert.Equal(t, 45, totals.TravelMinutes)
	assert.Equal(t, 90, totals.BillableMinutes, "unbilled travel is still travel")
	assert.Equal(t, 135, totals.TotalMinutes())
}

func TestServiceTotalsOnEmptyTicket(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	tickets := store.NewServiceTicketStore()
	customer := testutil.CreateCustomer(t, tx)

	ticket, err := tickets.Create(ctx, tx, ticketParams(customer.ID, "SVC-TEST0013"))
	require.NoError(t, err)

	// SUM over no rows is NULL; the COALESCEs are what stop that reaching Go.
	totals, err := tickets.Totals(ctx, tx, ticket.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.ServiceTotals{}, totals)
}

func TestServiceTimeEntryDelete(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	tickets := store.NewServiceTicketStore()
	customer := testutil.CreateCustomer(t, tx)
	staffID := testutil.CreateStaff(t, tx)

	ticket, err := tickets.Create(ctx, tx, ticketParams(customer.ID, "SVC-TEST0014"))
	require.NoError(t, err)
	entry, err := tickets.CreateTimeEntry(ctx, tx, store.CreateTimeEntryParams{
		TicketID: ticket.ID, StaffID: staffID, Kind: domain.ServiceTimeKindLabor, Minutes: 600,
	})
	require.NoError(t, err)

	require.NoError(t, tickets.DeleteTimeEntry(ctx, tx, entry.ID))

	left, err := tickets.ListTimeEntries(ctx, tx, ticket.ID)
	require.NoError(t, err)
	assert.Empty(t, left)
}

func TestServiceTicketNumberIsUnique(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	tickets := store.NewServiceTicketStore()
	customer := testutil.CreateCustomer(t, tx)

	_, err := tickets.Create(ctx, tx, ticketParams(customer.ID, "SVC-DUPLICATE"))
	require.NoError(t, err)

	// UNIQUE is what makes any future import re-run duplicate-safe, the same
	// way orders.number does.
	_, err = tickets.Create(ctx, tx, ticketParams(customer.ID, "SVC-DUPLICATE"))
	require.Error(t, err)
}

// The portal answers "when did you last look at this" per machine, and it has to
// mean somebody finished something. An open ticket is the complaint the customer
// is already looking at, not a service visit.
func TestLastServiceByEquipmentCountsOnlyResolvedWork(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	tickets := store.NewServiceTicketStore()
	equip := store.NewEquipmentStore()
	customer := testutil.CreateCustomer(t, tx)

	machine, err := equip.Create(ctx, tx, equipmentParams(customer.ID))
	require.NoError(t, err)

	// Two resolved visits and one still open, all on the same machine.
	for _, number := range []string{"SVC-LSVC0001", "SVC-LSVC0002"} {
		p := ticketParams(customer.ID, number)
		p.EquipmentID = &machine.ID
		created, cErr := tickets.Create(ctx, tx, p)
		require.NoError(t, cErr)
		_, cErr = tickets.UpdateStatus(ctx, tx, created.ID, domain.ServiceTicketStatusResolved, "Seals replaced.")
		require.NoError(t, cErr)
	}
	open := ticketParams(customer.ID, "SVC-LSVC0003")
	open.EquipmentID = &machine.ID
	_, err = tickets.Create(ctx, tx, open)
	require.NoError(t, err)

	dates, err := tickets.LastServiceByEquipment(ctx, tx, customer.ID)
	require.NoError(t, err)

	at, ok := dates[machine.ID]
	require.True(t, ok, "a machine with resolved work has a last-serviced date")
	assert.False(t, at.IsZero())
}

// A machine nobody has been called out to is simply absent from the map — the
// caller decides how to say "not yet", and a zero time would read as 1 AD.
func TestLastServiceByEquipmentOmitsUnservicedMachines(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	tickets := store.NewServiceTicketStore()
	equip := store.NewEquipmentStore()
	customer := testutil.CreateCustomer(t, tx)

	machine, err := equip.Create(ctx, tx, equipmentParams(customer.ID))
	require.NoError(t, err)

	dates, err := tickets.LastServiceByEquipment(ctx, tx, customer.ID)
	require.NoError(t, err)

	_, ok := dates[machine.ID]
	assert.False(t, ok)
}

// Customer scoping is the security boundary on this page, and this query is one
// of the reads behind it.
func TestLastServiceByEquipmentIsCustomerScoped(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	tickets := store.NewServiceTicketStore()
	equip := store.NewEquipmentStore()

	owner := testutil.CreateCustomer(t, tx, testutil.WithEmail("owner-lsvc@example.test"))
	stranger := testutil.CreateCustomer(t, tx, testutil.WithEmail("stranger-lsvc@example.test"))

	machine, err := equip.Create(ctx, tx, equipmentParams(owner.ID))
	require.NoError(t, err)
	p := ticketParams(owner.ID, "SVC-LSVC0004")
	p.EquipmentID = &machine.ID
	created, err := tickets.Create(ctx, tx, p)
	require.NoError(t, err)
	_, err = tickets.UpdateStatus(ctx, tx, created.ID, domain.ServiceTicketStatusResolved, "Done.")
	require.NoError(t, err)

	dates, err := tickets.LastServiceByEquipment(ctx, tx, stranger.ID)
	require.NoError(t, err)
	assert.Empty(t, dates, "another customer's repair history must not leak")
}
