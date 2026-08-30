package app_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// The three windows are a comparison, so the boundaries have to be right: work
// from eight months ago belongs in the year and not in the quarter.
func TestCostWindowsSplitByAge(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newTicketService()
	tickets := store.NewServiceTicketStore()

	customer := testutil.CreateCustomer(t, tx)
	machine, err := newEquipmentService().Register(ctx, tx, registerParams(customer.ID), testutil.TestActor())
	require.NoError(t, err)

	machineID := machine.ID
	ticket, err := tickets.Create(ctx, tx, store.CreateServiceTicketParams{
		Number:      "SVC-WINDOW-1",
		CustomerID:  customer.ID,
		EquipmentID: &machineID,
		Title:       "Recurring leak",
		Severity:    domain.ServiceSeverityRoutine,
	})
	require.NoError(t, err)

	now := time.Date(2026, time.September, 1, 15, 0, 0, 0, time.UTC)
	staffID := testutil.CreateStaff(t, tx)

	logAt := func(daysAgo, minutes int) {
		_, logErr := tickets.CreateTimeEntry(ctx, tx, store.CreateTimeEntryParams{
			TicketID:    ticket.ID,
			StaffID:     staffID,
			Kind:        domain.ServiceTimeKindLabor,
			Minutes:     minutes,
			PerformedOn: now.AddDate(0, 0, -daysAgo),
		})
		require.NoError(t, logErr)
	}

	logAt(10, 60)   // inside the quarter
	logAt(200, 120) // inside the year, outside the quarter
	logAt(900, 240) // all time only

	windows, err := svc.CostForEquipment(ctx, tx, machine.ID, now)
	require.NoError(t, err)
	require.Len(t, windows, 3)

	assert.Equal(t, "Last 90 days", windows[0].Label)
	assert.Equal(t, 60, windows[0].Summary.TotalMinutes())

	assert.Equal(t, "Last 12 months", windows[1].Label)
	assert.Equal(t, 180, windows[1].Summary.TotalMinutes(), "the year contains the quarter")

	assert.Equal(t, "All time", windows[2].Label)
	assert.True(t, windows[2].Since.IsZero(), "all time is unbounded, not a very long window")
	assert.Equal(t, 420, windows[2].Summary.TotalMinutes())

	assert.True(t, domain.AnyServiceCost(windows))
}

// A machine nobody has touched reports three empty windows rather than an
// error, so the card can say so in words.
func TestCostWindowsEmpty(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newTicketService()

	customer := testutil.CreateCustomer(t, tx)
	machine, err := newEquipmentService().Register(ctx, tx, registerParams(customer.ID), testutil.TestActor())
	require.NoError(t, err)

	windows, err := svc.CostForEquipment(ctx, tx, machine.ID, time.Now())
	require.NoError(t, err)
	require.Len(t, windows, 3)
	assert.False(t, domain.AnyServiceCost(windows))
}
