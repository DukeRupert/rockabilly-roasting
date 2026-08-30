package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
)

// The maintenance sweep is the only path in the module that opens real customer
// tickets with no human behind it, so it is worth pinning down properly.
//
// Like the stale sweep, it opens its own transactions from the pool — tx-scoped
// fixtures are invisible to it, so everything here is committed and torn down.

// sweepService builds a ServicePlanService wired the way main wires it, with the
// equipment module forced on and put back afterwards.
func sweepService(t *testing.T, ctx context.Context) *app.ServicePlanService {
	t.Helper()

	tickets, _ := notifyingService(t, ctx, true)

	moduleSvc := app.NewModuleService(store.NewModuleStore(), audit.NewAuditWriter())
	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	require.NoError(t, moduleSvc.Refresh(ctx, tx))
	require.NoError(t, tx.Commit(ctx))

	return app.NewServicePlanService(store.NewServicePlanStore(), store.NewEquipmentStore(), audit.NewAuditWriter()).
		WithScheduling(tickets, moduleSvc, metrics.NewRegistry())
}

// commitMachine puts a machine on the register outside a transaction.
func commitMachine(t *testing.T, ctx context.Context, customerID uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := testPool.Exec(ctx,
		`INSERT INTO equipment (id, customer_id, category, make, model, ownership, status)
		 VALUES ($1, $2, 'espresso_machine', 'La Marzocco', 'Linea PB', 'customer', 'active')`,
		id, customerID)
	require.NoError(t, err)
	t.Cleanup(func() {
		//nolint:errcheck // teardown
		testPool.Exec(context.Background(), `DELETE FROM equipment WHERE id = $1`, id)
	})
	return id
}

// commitPlan writes a plan with one task, committed.
func commitPlan(t *testing.T, ctx context.Context, intervalDays, leadDays int) (planID, taskID uuid.UUID) {
	t.Helper()
	planID, taskID = uuid.New(), uuid.New()
	name := "Sweep plan " + planID.String()[:8]

	_, err := testPool.Exec(ctx,
		`INSERT INTO service_plans (id, name) VALUES ($1, $2)`, planID, name)
	require.NoError(t, err)
	t.Cleanup(func() {
		//nolint:errcheck // teardown
		testPool.Exec(context.Background(), `DELETE FROM service_plans WHERE id = $1`, planID)
	})

	_, err = testPool.Exec(ctx,
		`INSERT INTO service_plan_tasks (id, plan_id, name, instructions, interval_days, lead_days, warranty_required)
		 VALUES ($1, $2, 'Full service', 'Descale and pressure test.', $3, $4, true)`,
		taskID, planID, intervalDays, leadDays)
	require.NoError(t, err)
	return planID, taskID
}

// commitAssignment puts a machine on a plan from an anchor date.
func commitAssignment(t *testing.T, ctx context.Context, equipmentID, planID uuid.UUID, startsOn time.Time, underContract bool) uuid.UUID {
	return commitAssignmentUntil(t, ctx, equipmentID, planID, startsOn, underContract, nil)
}

// commitAssignmentUntil is the same with a contract end date.
func commitAssignmentUntil(t *testing.T, ctx context.Context, equipmentID, planID uuid.UUID, startsOn time.Time, underContract bool, endsOn *time.Time) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := testPool.Exec(ctx,
		`INSERT INTO equipment_service_plans (id, equipment_id, plan_id, starts_on, under_contract, contract_ends_on)
		 VALUES ($1, $2, $3, $4, $5, $6)`, id, equipmentID, planID, startsOn, underContract, endsOn)
	require.NoError(t, err)
	t.Cleanup(func() {
		//nolint:errcheck // teardown
		testPool.Exec(context.Background(), `DELETE FROM equipment_service_plans WHERE id = $1`, id)
	})
	return id
}

// sweptTickets counts the routine tickets the sweep opened for a customer, and
// cleans them up — the sweep creates them outside any fixture.
func sweptTickets(t *testing.T, ctx context.Context, customerID uuid.UUID) int {
	t.Helper()
	var n int
	require.NoError(t, testPool.QueryRow(ctx,
		`SELECT count(*) FROM service_tickets WHERE customer_id = $1`, customerID).Scan(&n))
	t.Cleanup(func() {
		//nolint:errcheck // teardown
		testPool.Exec(context.Background(), `DELETE FROM service_tickets WHERE customer_id = $1`, customerID)
	})
	return n
}

func pendingDue(t *testing.T, ctx context.Context, equipmentID uuid.UUID) (dueOn time.Time, ticketed bool, found bool) {
	t.Helper()
	var ticketID *uuid.UUID
	err := testPool.QueryRow(ctx,
		`SELECT due_on, ticket_id FROM service_maintenance_due
		  WHERE equipment_id = $1 AND status = 'pending'`, equipmentID).Scan(&dueOn, &ticketID)
	if err != nil {
		return time.Time{}, false, false
	}
	return dueOn, ticketID != nil, true
}

// Covered work inside its lead window books itself a ticket, and the occurrence
// is attached to it so tomorrow's run leaves it alone.
func TestSweepBooksCoveredMaintenance(t *testing.T) {
	ctx := t.Context()
	svc := sweepService(t, ctx)
	customer := commitCustomer(t, ctx)
	machine := commitMachine(t, ctx, customer)

	// Anchored a year and a bit back on a yearly task: overdue, and covered.
	plan, _ := commitPlan(t, ctx, 365, 30)
	commitAssignment(t, ctx, machine, plan, time.Now().AddDate(-1, 0, -30), true)

	require.NoError(t, svc.SweepMaintenance(ctx, testPool, time.Now(), 0))

	assert.Equal(t, 1, sweptTickets(t, ctx, customer),
		"the backfill wrote the occurrence and the booking opened a ticket for it")

	_, ticketed, found := pendingDue(t, ctx, machine)
	require.True(t, found)
	assert.True(t, ticketed, "an occurrence booked but not attached would be booked again tomorrow")

	var title, description, severity string
	var billable bool
	require.NoError(t, testPool.QueryRow(ctx,
		`SELECT title, description, severity, billable FROM service_tickets WHERE customer_id = $1`,
		customer).Scan(&title, &description, &severity, &billable))

	assert.Contains(t, title, "Full service")
	assert.Contains(t, description, "Descale and pressure test.",
		"the tech on site reads the procedure off the ticket, not the plan")
	assert.Contains(t, description, "keep the manufacturer's warranty")
	assert.Equal(t, string(domain.ServiceSeverityRoutine), severity,
		"planned work must never outrank a cafe that is down")
	assert.False(t, billable, "the contract has already been paid for")
}

// Uncovered work is never booked. Opening a ticket would commit the shop to a
// visit nobody agreed to pay for.
func TestSweepNeverBooksUncoveredMaintenance(t *testing.T) {
	ctx := t.Context()
	svc := sweepService(t, ctx)
	customer := commitCustomer(t, ctx)
	machine := commitMachine(t, ctx, customer)

	plan, _ := commitPlan(t, ctx, 365, 30)
	commitAssignment(t, ctx, machine, plan, time.Now().AddDate(-1, 0, -30), false)

	require.NoError(t, svc.SweepMaintenance(ctx, testPool, time.Now(), 0))

	assert.Equal(t, 0, sweptTickets(t, ctx, customer))

	_, ticketed, found := pendingDue(t, ctx, machine)
	require.True(t, found, "it is still due — it just goes on the call list instead")
	assert.False(t, ticketed)
}

// Covered work that is not yet inside its lead window waits.
func TestSweepLeavesWorkOutsideItsWindow(t *testing.T) {
	ctx := t.Context()
	svc := sweepService(t, ctx)
	customer := commitCustomer(t, ctx)
	machine := commitMachine(t, ctx, customer)

	// Yearly task anchored today: due in a year, with a month's notice.
	plan, _ := commitPlan(t, ctx, 365, 30)
	commitAssignment(t, ctx, machine, plan, time.Now(), true)

	require.NoError(t, svc.SweepMaintenance(ctx, testPool, time.Now(), 0))

	assert.Equal(t, 0, sweptTickets(t, ctx, customer))
}

// The claim the commit message makes: a second run books nothing new.
func TestSweepIsIdempotent(t *testing.T) {
	ctx := t.Context()
	svc := sweepService(t, ctx)
	customer := commitCustomer(t, ctx)
	machine := commitMachine(t, ctx, customer)

	plan, _ := commitPlan(t, ctx, 365, 30)
	commitAssignment(t, ctx, machine, plan, time.Now().AddDate(-1, 0, -30), true)

	now := time.Now()
	require.NoError(t, svc.SweepMaintenance(ctx, testPool, now, 0))
	require.NoError(t, svc.SweepMaintenance(ctx, testPool, now, 0))
	require.NoError(t, svc.SweepMaintenance(ctx, testPool, now, 0))

	assert.Equal(t, 1, sweptTickets(t, ctx, customer),
		"three runs, one visit — the attachment is what stops the second one")

	var due int
	require.NoError(t, testPool.QueryRow(ctx,
		`SELECT count(*) FROM service_maintenance_due WHERE equipment_id = $1`, machine).Scan(&due))
	assert.Equal(t, 1, due, "the backfill must not write the same occurrence twice")
}

// The path by which a task added to a plan reaches machines already on it.
func TestSweepBackfillsTasksAddedLater(t *testing.T) {
	ctx := t.Context()
	svc := sweepService(t, ctx)
	customer := commitCustomer(t, ctx)
	machine := commitMachine(t, ctx, customer)

	plan, _ := commitPlan(t, ctx, 365, 30)
	commitAssignment(t, ctx, machine, plan, time.Now().AddDate(0, -1, 0), false)
	require.NoError(t, svc.SweepMaintenance(ctx, testPool, time.Now(), 0))

	// A second task joins the plan after the machine is already on it.
	_, err := testPool.Exec(ctx,
		`INSERT INTO service_plan_tasks (plan_id, name, interval_days, lead_days)
		 VALUES ($1, 'Change water filter', 180, 14)`, plan)
	require.NoError(t, err)

	require.NoError(t, svc.SweepMaintenance(ctx, testPool, time.Now(), 0))

	var due int
	require.NoError(t, testPool.QueryRow(ctx,
		`SELECT count(*) FROM service_maintenance_due WHERE equipment_id = $1 AND status = 'pending'`,
		machine).Scan(&due))
	assert.Equal(t, 2, due, "the new task reaches the machine that was already on the plan")

	sweptTickets(t, ctx, customer) // registers teardown
}

// A retired machine generates nothing: maintenance nobody will ever do is the
// noise the due list has to stay free of.
func TestSweepIgnoresRetiredMachines(t *testing.T) {
	ctx := t.Context()
	svc := sweepService(t, ctx)
	customer := commitCustomer(t, ctx)
	machine := commitMachine(t, ctx, customer)

	plan, _ := commitPlan(t, ctx, 365, 30)
	commitAssignment(t, ctx, machine, plan, time.Now().AddDate(-1, 0, -30), true)
	_, err := testPool.Exec(ctx, `UPDATE equipment SET status = 'retired' WHERE id = $1`, machine)
	require.NoError(t, err)

	require.NoError(t, svc.SweepMaintenance(ctx, testPool, time.Now(), 0))

	assert.Equal(t, 0, sweptTickets(t, ctx, customer))
	_, _, found := pendingDue(t, ctx, machine)
	assert.False(t, found, "a machine that is gone is not getting a visit")
}

// Off means off: a shop that does not service machines must not have a job
// quietly opening tickets on it.
func TestSweepDoesNothingWithTheModuleOff(t *testing.T) {
	ctx := t.Context()

	tickets, _ := notifyingService(t, ctx, false)
	moduleSvc := app.NewModuleService(store.NewModuleStore(), audit.NewAuditWriter())
	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	require.NoError(t, moduleSvc.Refresh(ctx, tx))
	require.NoError(t, tx.Commit(ctx))

	svc := app.NewServicePlanService(store.NewServicePlanStore(), store.NewEquipmentStore(), audit.NewAuditWriter()).
		WithScheduling(tickets, moduleSvc, metrics.NewRegistry())

	customer := commitCustomer(t, ctx)
	machine := commitMachine(t, ctx, customer)
	plan, _ := commitPlan(t, ctx, 365, 30)
	commitAssignment(t, ctx, machine, plan, time.Now().AddDate(-1, 0, -30), true)

	require.NoError(t, svc.SweepMaintenance(ctx, testPool, time.Now(), 0),
		"the module being off is not a fault — an error would make River retry forever")

	assert.Equal(t, 0, sweptTickets(t, ctx, customer))
	_, _, found := pendingDue(t, ctx, machine)
	assert.False(t, found, "not even the backfill runs")
}

// A booked visit leaves a record saying why it exists: the ticket has no human
// behind it and is otherwise unexplainable from its own page.
func TestSweepAuditsWhatItBooked(t *testing.T) {
	ctx := t.Context()
	svc := sweepService(t, ctx)
	customer := commitCustomer(t, ctx)
	machine := commitMachine(t, ctx, customer)

	plan, _ := commitPlan(t, ctx, 365, 30)
	commitAssignment(t, ctx, machine, plan, time.Now().AddDate(-1, 0, -30), true)

	require.NoError(t, svc.SweepMaintenance(ctx, testPool, time.Now(), 0))
	sweptTickets(t, ctx, customer) // registers teardown

	var booked, swept int
	require.NoError(t, testPool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE action = 'equipment.maintenance_booked'
		   AND metadata->>'equipment_id' = $1`, machine.String()).Scan(&booked))
	assert.Equal(t, 1, booked, "a ticket nobody opened has to be explainable")

	require.NoError(t, testPool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE action = 'equipment.maintenance_swept'`).Scan(&swept))
	assert.Positive(t, swept, "one row a day, so a quiet list and a dead job look different")

	t.Cleanup(func() {
		//nolint:errcheck // teardown
		testPool.Exec(context.Background(),
			`DELETE FROM audit_log WHERE action IN ('equipment.maintenance_booked', 'equipment.maintenance_swept')`)
	})
}

// A contract that has run out is not a contract.
//
// This is the branch most likely to silently book an unpaid visit: the flag
// still says under_contract, and only the end date says otherwise. Every other
// "never books uncovered work" test leaves under_contract false, so the lapsed
// case was the one path through Covered() that nothing exercised.
func TestSweepNeverBooksAgainstALapsedContract(t *testing.T) {
	ctx := t.Context()
	svc := sweepService(t, ctx)
	customer := commitCustomer(t, ctx)
	machine := commitMachine(t, ctx, customer)

	lapsed := time.Now().AddDate(0, -2, 0)
	plan, _ := commitPlan(t, ctx, 365, 30)
	commitAssignmentUntil(t, ctx, machine, plan, time.Now().AddDate(-1, 0, -30), true, &lapsed)

	require.NoError(t, svc.SweepMaintenance(ctx, testPool, time.Now(), 0))

	assert.Equal(t, 0, sweptTickets(t, ctx, customer),
		"the term ran out two months ago — the shop must not book itself a visit on it")

	_, ticketed, found := pendingDue(t, ctx, machine)
	require.True(t, found, "still due, and now on the call list")
	assert.False(t, ticketed)
}

// The seam between those two: a contract that is live today but gone by the day
// the visit lands.
//
// Interval 365, lead 30, anchored so the occurrence falls seven days out; cover
// runs three more days. On the sweep day the contract is unambiguously live, so
// asking "covered?" as of today says yes and books a ticket for work happening
// four days after the term ends. Nothing above catches it — one fixture lapsed
// two months ago, the other runs a year out, and on both of those the sweep day
// and the due date agree.
func TestSweepNeverBooksAContractThatLapsesBeforeTheVisit(t *testing.T) {
	ctx := t.Context()
	svc := sweepService(t, ctx)
	customer := commitCustomer(t, ctx)
	machine := commitMachine(t, ctx, customer)

	lapsesBeforeTheVisit := time.Now().AddDate(0, 0, 3)
	plan, _ := commitPlan(t, ctx, 365, 30)
	commitAssignmentUntil(t, ctx, machine, plan,
		time.Now().AddDate(0, 0, 7-365), true, &lapsesBeforeTheVisit)

	require.NoError(t, svc.SweepMaintenance(ctx, testPool, time.Now(), 0))

	assert.Equal(t, 0, sweptTickets(t, ctx, customer),
		"cover has four days left and the visit is seven days out — the shop must not book itself that visit")

	_, ticketed, found := pendingDue(t, ctx, machine)
	require.True(t, found, "still due, and it belongs on the call list so somebody can sell it")
	assert.False(t, ticketed)
}

// The mirror: a contract still running does book, so the test above is proving
// the end date matters rather than that booking is broken.
func TestSweepBooksAgainstALiveTermContract(t *testing.T) {
	ctx := t.Context()
	svc := sweepService(t, ctx)
	customer := commitCustomer(t, ctx)
	machine := commitMachine(t, ctx, customer)

	running := time.Now().AddDate(1, 0, 0)
	plan, _ := commitPlan(t, ctx, 365, 30)
	commitAssignmentUntil(t, ctx, machine, plan, time.Now().AddDate(-1, 0, -30), true, &running)

	require.NoError(t, svc.SweepMaintenance(ctx, testPool, time.Now(), 0))

	assert.Equal(t, 1, sweptTickets(t, ctx, customer))
}
