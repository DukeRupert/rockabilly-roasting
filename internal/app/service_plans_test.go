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
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

func newServicePlanService() *app.ServicePlanService {
	return app.NewServicePlanService(store.NewServicePlanStore(), store.NewEquipmentStore(), audit.NewAuditWriter())
}

func planDay(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// planWithTask sets up the common fixture: a plan holding one quarterly task.
func planWithTask(t *testing.T, tx pgx.Tx, svc *app.ServicePlanService, name string, intervalDays int) (*domain.ServicePlan, *domain.ServicePlanTask) {
	t.Helper()
	ctx := t.Context()

	plan, err := svc.CreatePlan(ctx, tx, app.CreateServicePlanParams{
		Name:     name,
		Category: domain.EquipmentCategoryEspressoMachine,
	}, testutil.TestActor())
	require.NoError(t, err)

	task, err := svc.AddTask(ctx, tx, app.AddPlanTaskParams{
		PlanID:       plan.ID,
		Name:         "Full service",
		IntervalDays: intervalDays,
		LeadDays:     14,
	}, testutil.TestActor())
	require.NoError(t, err)

	return plan, task
}

// staffActorFor makes an actor backed by a real staff row.
//
// Closing an occurrence records who did it, and completed_by_staff_id is a
// genuine foreign key — the generic TestActor's invented id is fine for audit
// rows and not for this.
func staffActorFor(t *testing.T, tx pgx.Tx) app.Actor {
	t.Helper()
	return testutil.TestActorFromStaff(testutil.CreateStaff(t, tx))
}

// registerMachine puts a machine on the register for these tests.
func registerMachine(t *testing.T, tx pgx.Tx, customerID uuid.UUID) *domain.Equipment {
	t.Helper()
	e, err := newEquipmentService().Register(t.Context(), tx, registerParams(customerID), testutil.TestActor())
	require.NoError(t, err)
	return e
}

func TestAssignPlanGeneratesTheSchedule(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newServicePlanService()
	customer := testutil.CreateCustomer(t, tx)
	machine := registerMachine(t, tx, customer.ID)

	plan, _ := planWithTask(t, tx, svc, "Linea PB warranty", 90)

	anchor := planDay(2026, time.June, 1)
	_, err := svc.AssignPlan(ctx, tx, app.AssignServicePlanParams{
		EquipmentID: machine.ID,
		PlanID:      plan.ID,
		StartsOn:    anchor,
	}, testDay(), testutil.TestActor())
	require.NoError(t, err)

	machineID := machine.ID
	rows, err := svc.ListDue(ctx, tx, store.MaintenanceFilter{
		EquipmentID: &machineID,
		Now:         planDay(2026, time.June, 1),
	})
	require.NoError(t, err)
	require.Len(t, rows, 1, "assigning a plan writes the first occurrence straight away")

	assert.Equal(t, planDay(2026, time.August, 30).UTC(), rows[0].DueOn.UTC(),
		"the first occurrence is one interval after the anchor")
	assert.Equal(t, domain.MaintenanceStatusPending, rows[0].Status)
}

// An empty plan generates nothing, so assigning it would leave staff believing
// a machine was covered when it was not.
func TestAssignEmptyPlanIsRefused(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newServicePlanService()
	customer := testutil.CreateCustomer(t, tx)
	machine := registerMachine(t, tx, customer.ID)

	plan, err := svc.CreatePlan(ctx, tx, app.CreateServicePlanParams{Name: "Empty"}, testutil.TestActor())
	require.NoError(t, err)

	_, err = svc.AssignPlan(ctx, tx, app.AssignServicePlanParams{
		EquipmentID: machine.ID,
		PlanID:      plan.ID,
		StartsOn:    planDay(2026, time.June, 1),
	}, testDay(), testutil.TestActor())

	assert.ErrorIs(t, err, app.ErrPlanHasNoTasks)
}

func TestAssignPlanTwiceIsRefused(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newServicePlanService()
	customer := testutil.CreateCustomer(t, tx)
	machine := registerMachine(t, tx, customer.ID)
	plan, _ := planWithTask(t, tx, svc, "Linea PB warranty", 90)

	params := app.AssignServicePlanParams{
		EquipmentID: machine.ID,
		PlanID:      plan.ID,
		StartsOn:    planDay(2026, time.June, 1),
	}
	_, err := svc.AssignPlan(ctx, tx, params, testDay(), testutil.TestActor())
	require.NoError(t, err)

	// The unique violation aborts the transaction, so this assertion has to be
	// the last thing the test does with it.
	_, err = svc.AssignPlan(ctx, tx, params, testDay(), testutil.TestActor())
	assert.ErrorIs(t, err, app.ErrPlanAlreadyAssigned,
		"the same plan twice would double every due item on the machine")
}

// The invariant the whole schedule rests on: closing an occurrence writes the
// next one, in the same transaction.
func TestCompleteDueWritesTheNextOne(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newServicePlanService()
	customer := testutil.CreateCustomer(t, tx)
	machine := registerMachine(t, tx, customer.ID)
	plan, _ := planWithTask(t, tx, svc, "Linea PB warranty", 90)

	_, err := svc.AssignPlan(ctx, tx, app.AssignServicePlanParams{
		EquipmentID: machine.ID,
		PlanID:      plan.ID,
		StartsOn:    planDay(2026, time.March, 1),
	}, testDay(), testutil.TestActor())
	require.NoError(t, err)

	machineID := machine.ID
	pending := func() []domain.MaintenanceDueRow {
		rows, listErr := svc.ListDue(ctx, tx, store.MaintenanceFilter{
			EquipmentID: &machineID,
			Now:         planDay(2026, time.June, 20),
		})
		require.NoError(t, listErr)
		return rows
	}

	first := pending()
	require.Len(t, first, 1)

	// Done a fortnight late, and written up later still.
	doneOn := planDay(2026, time.June, 13)
	_, err = svc.CompleteDue(ctx, tx, first[0].ID, app.CompleteDueParams{CompletedOn: doneOn}, testDay(), staffActorFor(t, tx))
	require.NoError(t, err)

	next := pending()
	require.Len(t, next, 1, "one pending occurrence at a time, always")
	assert.Equal(t, planDay(2026, time.September, 11).UTC(), next[0].DueOn.UTC(),
		"the next one counts from when the work happened, not from when it was due")

	history, err := svc.ListDue(ctx, tx, store.MaintenanceFilter{
		EquipmentID: &machineID,
		Scope:       store.MaintenanceScopeHistory,
		Now:         planDay(2026, time.June, 20),
	})
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, domain.MaintenanceStatusCompleted, history[0].Status)
}

// Skipping keeps the original cadence rather than re-anchoring to today.
func TestSkipDueKeepsTheCadence(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newServicePlanService()
	customer := testutil.CreateCustomer(t, tx)
	machine := registerMachine(t, tx, customer.ID)
	plan, _ := planWithTask(t, tx, svc, "Backflush plan", 30)

	_, err := svc.AssignPlan(ctx, tx, app.AssignServicePlanParams{
		EquipmentID: machine.ID,
		PlanID:      plan.ID,
		StartsOn:    planDay(2026, time.August, 1),
	}, testDay(), testutil.TestActor())
	require.NoError(t, err)

	machineID := machine.ID
	rows, err := svc.ListDue(ctx, tx, store.MaintenanceFilter{EquipmentID: &machineID, Now: planDay(2026, time.September, 5)})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, planDay(2026, time.August, 31).UTC(), rows[0].DueOn.UTC())

	_, err = svc.SkipDue(ctx, tx, rows[0].ID, planDay(2026, time.September, 5), planDay(2026, time.September, 5), "cafe was shut", staffActorFor(t, tx))
	require.NoError(t, err)

	rows, err = svc.ListDue(ctx, tx, store.MaintenanceFilter{EquipmentID: &machineID, Now: planDay(2026, time.September, 5)})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, planDay(2026, time.September, 30).UTC(), rows[0].DueOn.UTC(),
		"an interval after the date it was due — a skip must not shift the whole schedule")
}

// The double submit. Closing an occurrence twice must not write two successors.
func TestCompleteDueTwiceIsRefused(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newServicePlanService()
	customer := testutil.CreateCustomer(t, tx)
	machine := registerMachine(t, tx, customer.ID)
	plan, _ := planWithTask(t, tx, svc, "Linea PB warranty", 90)

	_, err := svc.AssignPlan(ctx, tx, app.AssignServicePlanParams{
		EquipmentID: machine.ID,
		PlanID:      plan.ID,
		StartsOn:    planDay(2026, time.March, 1),
	}, testDay(), testutil.TestActor())
	require.NoError(t, err)

	machineID := machine.ID
	rows, err := svc.ListDue(ctx, tx, store.MaintenanceFilter{EquipmentID: &machineID, Now: planDay(2026, time.June, 1)})
	require.NoError(t, err)
	require.Len(t, rows, 1)

	done := app.CompleteDueParams{CompletedOn: planDay(2026, time.June, 1)}
	actor := staffActorFor(t, tx)
	_, err = svc.CompleteDue(ctx, tx, rows[0].ID, done, testDay(), actor)
	require.NoError(t, err)

	_, err = svc.CompleteDue(ctx, tx, rows[0].ID, done, testDay(), actor)
	assert.ErrorIs(t, err, app.ErrMaintenanceAlreadyClosed)

	still, err := svc.ListDue(ctx, tx, store.MaintenanceFilter{EquipmentID: &machineID, Now: planDay(2026, time.June, 1)})
	require.NoError(t, err)
	assert.Len(t, still, 1, "a second click must not produce a second successor")
}

// Editing a task's interval has to reach the machines already on the plan —
// otherwise the plan says one thing and forty schedules say another.
func TestEditTaskIntervalReschedulesLiveMachines(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newServicePlanService()
	customer := testutil.CreateCustomer(t, tx)
	machine := registerMachine(t, tx, customer.ID)
	plan, task := planWithTask(t, tx, svc, "Linea PB warranty", 90)

	_, err := svc.AssignPlan(ctx, tx, app.AssignServicePlanParams{
		EquipmentID: machine.ID,
		PlanID:      plan.ID,
		StartsOn:    planDay(2026, time.June, 1),
	}, testDay(), testutil.TestActor())
	require.NoError(t, err)

	_, err = svc.EditTask(ctx, tx, task.ID, app.EditPlanTaskParams{
		Name:         task.Name,
		IntervalDays: 60,
		LeadDays:     14,
	}, planDay(2026, time.June, 1), testutil.TestActor())
	require.NoError(t, err)

	machineID := machine.ID
	rows, err := svc.ListDue(ctx, tx, store.MaintenanceFilter{EquipmentID: &machineID, Now: planDay(2026, time.June, 1)})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, planDay(2026, time.July, 31).UTC(), rows[0].DueOn.UTC(),
		"the pending occurrence moves onto the new interval, measured from the same anchor")
}

// Ending an assignment clears what it had waiting but keeps what was done.
func TestEndAssignmentClearsPendingKeepsHistory(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newServicePlanService()
	customer := testutil.CreateCustomer(t, tx)
	machine := registerMachine(t, tx, customer.ID)
	plan, _ := planWithTask(t, tx, svc, "Linea PB warranty", 90)

	assignment, err := svc.AssignPlan(ctx, tx, app.AssignServicePlanParams{
		EquipmentID: machine.ID,
		PlanID:      plan.ID,
		StartsOn:    planDay(2026, time.March, 1),
	}, testDay(), testutil.TestActor())
	require.NoError(t, err)

	machineID := machine.ID
	rows, err := svc.ListDue(ctx, tx, store.MaintenanceFilter{EquipmentID: &machineID, Now: planDay(2026, time.June, 1)})
	require.NoError(t, err)
	require.Len(t, rows, 1)

	_, err = svc.CompleteDue(ctx, tx, rows[0].ID, app.CompleteDueParams{CompletedOn: planDay(2026, time.June, 1)}, testDay(), staffActorFor(t, tx))
	require.NoError(t, err)

	require.NoError(t, svc.EndAssignment(ctx, tx, machine.ID, assignment.ID, time.Now(), testutil.TestActor()))

	pending, err := svc.ListDue(ctx, tx, store.MaintenanceFilter{EquipmentID: &machineID, Now: planDay(2026, time.June, 1)})
	require.NoError(t, err)
	assert.Empty(t, pending, "an ended arrangement stops generating work")

	history, err := svc.ListDue(ctx, tx, store.MaintenanceFilter{
		EquipmentID: &machineID,
		Scope:       store.MaintenanceScopeHistory,
		Now:         planDay(2026, time.June, 1),
	})
	require.NoError(t, err)
	assert.Len(t, history, 1, "what was done to the machine outlives the arrangement that produced it")
}

// Closing an occurrence on an assignment that has since ended must not
// resurrect the schedule.
func TestCompleteAfterEndDoesNotResurrect(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newServicePlanService()
	customer := testutil.CreateCustomer(t, tx)
	machine := registerMachine(t, tx, customer.ID)
	plan, _ := planWithTask(t, tx, svc, "Linea PB warranty", 90)

	assignment, err := svc.AssignPlan(ctx, tx, app.AssignServicePlanParams{
		EquipmentID: machine.ID,
		PlanID:      plan.ID,
		StartsOn:    planDay(2026, time.March, 1),
	}, testDay(), testutil.TestActor())
	require.NoError(t, err)

	machineID := machine.ID
	rows, err := svc.ListDue(ctx, tx, store.MaintenanceFilter{EquipmentID: &machineID, Now: planDay(2026, time.June, 1)})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	dueID := rows[0].ID

	// End the assignment, which deletes the pending row — then try to close the
	// id somebody still had on a stale page.
	require.NoError(t, svc.EndAssignment(ctx, tx, machine.ID, assignment.ID, time.Now(), testutil.TestActor()))

	_, err = svc.CompleteDue(ctx, tx, dueID, app.CompleteDueParams{CompletedOn: planDay(2026, time.June, 1)}, testDay(), staffActorFor(t, tx))
	assert.ErrorIs(t, err, app.ErrMaintenanceNotFound)
}

func TestPlanValidation(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newServicePlanService()

	t.Run("a plan needs a name", func(t *testing.T) {
		_, err := svc.CreatePlan(ctx, tx, app.CreateServicePlanParams{Name: "  "}, testutil.TestActor())
		assert.ErrorIs(t, err, app.ErrPlanNameRequired)
	})

	plan, err := svc.CreatePlan(ctx, tx, app.CreateServicePlanParams{Name: "House standard"}, testutil.TestActor())
	require.NoError(t, err)

	t.Run("a task needs an interval", func(t *testing.T) {
		_, err := svc.AddTask(ctx, tx, app.AddPlanTaskParams{
			PlanID: plan.ID, Name: "Backflush", IntervalDays: 0,
		}, testutil.TestActor())
		assert.ErrorIs(t, err, app.ErrPlanIntervalInvalid)
	})

	t.Run("the notice period cannot swallow the interval", func(t *testing.T) {
		_, err := svc.AddTask(ctx, tx, app.AddPlanTaskParams{
			PlanID: plan.ID, Name: "Backflush", IntervalDays: 30, LeadDays: 30,
		}, testutil.TestActor())
		assert.ErrorIs(t, err, app.ErrPlanLeadInvalid,
			"a task permanently inside its own warning window tells you nothing")
	})
}

// A retired machine must not acquire a schedule: it would generate maintenance
// nobody will ever do, which is the noise the due list has to stay free of.
func TestAssignPlanToRetiredMachineIsRefused(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newServicePlanService()
	equip := newEquipmentService()
	customer := testutil.CreateCustomer(t, tx)
	machine := registerMachine(t, tx, customer.ID)
	plan, _ := planWithTask(t, tx, svc, "Linea PB warranty", 90)

	_, err := equip.SetStatus(ctx, tx, machine.ID, domain.EquipmentStatusRetired, testutil.TestActor())
	require.NoError(t, err)

	_, err = svc.AssignPlan(ctx, tx, app.AssignServicePlanParams{
		EquipmentID: machine.ID,
		PlanID:      plan.ID,
		StartsOn:    planDay(2026, time.June, 1),
	}, testDay(), testutil.TestActor())

	assert.ErrorIs(t, err, app.ErrEquipmentRetired)
}

// Editing an interval must not drag a future occurrence into the past.
//
// The harm is specific: past-due covered work is inside MaintenanceScopeBookable,
// so the overnight sweep opens a real customer ticket for it. If the occurrence
// moved was one somebody had deliberately skipped, the shop books the visit the
// customer declined.
//
// The original defect was worse than an off-by-a-few: the recompute anchored on
// max(completed_on), which a skip leaves untouched, so a skipped occurrence
// jumped back to one interval after the last *completion* — months into the past.
func TestEditTaskIntervalNeverBooksASkippedVisit(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newServicePlanService()
	customer := testutil.CreateCustomer(t, tx)
	machine := registerMachine(t, tx, customer.ID)
	plan, task := planWithTask(t, tx, svc, "Skip regression", 90)

	_, err := svc.AssignPlan(ctx, tx, app.AssignServicePlanParams{
		EquipmentID:   machine.ID,
		PlanID:        plan.ID,
		StartsOn:      planDay(2025, time.October, 3),
		UnderContract: true,
	}, testDay(), testutil.TestActor())
	require.NoError(t, err)

	machineID := machine.ID
	pending := func(now time.Time) domain.MaintenanceDueRow {
		rows, listErr := svc.ListDue(ctx, tx, store.MaintenanceFilter{EquipmentID: &machineID, Now: now})
		require.NoError(t, listErr)
		require.Len(t, rows, 1)
		return rows[0]
	}

	// Done once, then the next one deliberately skipped. The skip keeps the
	// cadence, landing on 30 June.
	today := planDay(2026, time.May, 1)
	first := pending(today)
	_, err = svc.CompleteDue(ctx, tx, first.ID, app.CompleteDueParams{
		CompletedOn: planDay(2026, time.January, 1),
	}, testDay(), staffActorFor(t, tx))
	require.NoError(t, err)

	second := pending(today)
	require.Equal(t, planDay(2026, time.April, 1).UTC(), second.DueOn.UTC())
	_, err = svc.SkipDue(ctx, tx, second.ID, planDay(2026, time.April, 2), today, "customer declined", staffActorFor(t, tx))
	require.NoError(t, err)

	afterSkip := pending(today)
	require.Equal(t, planDay(2026, time.June, 30).UTC(), afterSkip.DueOn.UTC(),
		"the skip kept the cadence and landed in the future")
	require.False(t, afterSkip.BookableOn(today))

	// Somebody shortens the interval sharply. Shifting alone would land on
	// 16 April — behind today, and immediately bookable.
	_, err = svc.EditTask(ctx, tx, task.ID, app.EditPlanTaskParams{
		Name:         "Skip regression",
		IntervalDays: 15,
		LeadDays:     5,
	}, today, testutil.TestActor())
	require.NoError(t, err)

	moved := pending(today)
	assert.False(t, moved.DueOn.Before(today),
		"a future occurrence must not be moved into the past (got %s)",
		moved.DueOn.UTC().Format("2006-01-02"))
	assert.NotEqual(t, domain.MaintenanceOverdue, moved.Urgency(today),
		"editing an interval must not retrospectively make work late")

	// Clear of today, not merely level with it.
	//
	// This assertion used to read 1 May — today — on the reasoning that a
	// fifteen-day cadence makes work due today legitimately due. That reasoning
	// is fine for an occurrence that was already owed, and wrong here: this one
	// was in the future until an interval edit moved it, the customer declined
	// the visit two weeks ago, and BookableOn returns true for a due-today row.
	// Landing on today is the sweep booking it tonight — precisely what this
	// test is named after.
	assert.True(t, moved.DueOn.UTC().After(today.UTC()),
		"got %s, wanted strictly after today", moved.DueOn.UTC().Format("2006-01-02"))
	assert.Equal(t, planDay(2026, time.May, 16).UTC(), moved.DueOn.UTC(),
		"clamped forward a whole interval at a time, so it stays on the new cadence")
	assert.False(t, moved.BookableOn(today),
		"and the sweep leaves it alone tonight, which is the whole point")
}

// An occurrence that was already late stays late — it genuinely is, and hiding
// it behind the clamp would be the opposite mistake.
func TestEditTaskIntervalKeepsOverdueWorkOverdue(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newServicePlanService()
	customer := testutil.CreateCustomer(t, tx)
	machine := registerMachine(t, tx, customer.ID)
	plan, task := planWithTask(t, tx, svc, "Overdue regression", 90)

	_, err := svc.AssignPlan(ctx, tx, app.AssignServicePlanParams{
		EquipmentID: machine.ID,
		PlanID:      plan.ID,
		StartsOn:    planDay(2025, time.January, 1),
	}, testDay(), testutil.TestActor())
	require.NoError(t, err)

	today := planDay(2026, time.September, 1)
	machineID := machine.ID
	rows, err := svc.ListDue(ctx, tx, store.MaintenanceFilter{EquipmentID: &machineID, Now: today})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.True(t, rows[0].DueOn.Before(today), "already badly overdue")

	_, err = svc.EditTask(ctx, tx, task.ID, app.EditPlanTaskParams{
		Name:         "Overdue regression",
		IntervalDays: 100,
		LeadDays:     14,
	}, testDay(), testutil.TestActor())
	require.NoError(t, err)

	rows, err = svc.ListDue(ctx, tx, store.MaintenanceFilter{EquipmentID: &machineID, Now: today})
	require.NoError(t, err)
	assert.Equal(t, planDay(2025, time.April, 1).UTC(), rows[0].DueOn.UTC(),
		"left exactly where it was — the work is owed today, and a longer interval must not clear it")
	assert.True(t, rows[0].DueOn.Before(today), "still on the overdue list")
}

// testDay is a fixed "today" for the service calls that now take one. Dates in
// these tests are chosen relative to it.
func testDay() time.Time { return planDay(2026, time.September, 1) }

// The bounds live in the service, so both routes get them.
//
// They used to live in the handler, where the future-date check was guarded by
// `!skip` — so a hand-crafted POST to the skip route walked straight past it and
// NextDueAfterSkip took the machine off the due list for a decade. That is what
// validation outside the service buys: one route remembers and the other does
// not.
func TestBothCloseRoutesBoundTheirDates(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newServicePlanService()
	customer := testutil.CreateCustomer(t, tx)
	machine := registerMachine(t, tx, customer.ID)
	plan, _ := planWithTask(t, tx, svc, "Bounds", 90)

	_, err := svc.AssignPlan(ctx, tx, app.AssignServicePlanParams{
		EquipmentID: machine.ID, PlanID: plan.ID, StartsOn: planDay(2026, time.March, 1),
	}, testDay(), testutil.TestActor())
	require.NoError(t, err)

	machineID := machine.ID
	rows, err := svc.ListDue(ctx, tx, store.MaintenanceFilter{EquipmentID: &machineID, Now: testDay()})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	id := rows[0].ID
	now := testDay()

	t.Run("completing in the future", func(t *testing.T) {
		_, err := svc.CompleteDue(ctx, tx, id, app.CompleteDueParams{
			CompletedOn: now.AddDate(0, 0, 1),
		}, now, staffActorFor(t, tx))
		assert.ErrorIs(t, err, app.ErrMaintenanceDateInFuture)
	})

	t.Run("skipping in the future", func(t *testing.T) {
		_, err := svc.SkipDue(ctx, tx, id, now.AddDate(0, 0, 1), now, "", staffActorFor(t, tx))
		assert.ErrorIs(t, err, app.ErrMaintenanceDateInFuture,
			"the route with no date field on its form is the one somebody would post by hand")
	})

	t.Run("a slipped year, either route", func(t *testing.T) {
		_, err := svc.CompleteDue(ctx, tx, id, app.CompleteDueParams{
			CompletedOn: now.AddDate(-11, 0, 0),
		}, now, staffActorFor(t, tx))
		assert.ErrorIs(t, err, app.ErrMaintenanceDateOutOfRange)

		_, err = svc.SkipDue(ctx, tx, id, now.AddDate(-11, 0, 0), now, "", staffActorFor(t, tx))
		assert.ErrorIs(t, err, app.ErrMaintenanceDateOutOfRange)
	})
}

// The append rule belongs to AddTask, so it holds however the caller reaches it
// — including a second caller that would not have known to count first.
func TestAddTaskAppendsToTheSeries(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := t.Context()
	svc := newServicePlanService()

	plan, err := svc.CreatePlan(ctx, tx, app.CreateServicePlanParams{Name: "Order " + uuid.NewString()[:8]}, testutil.TestActor())
	require.NoError(t, err)

	var orders []int
	for _, name := range []string{"Backflush", "Gaskets", "Full service"} {
		task, err := svc.AddTask(ctx, tx, app.AddPlanTaskParams{
			PlanID: plan.ID, Name: name, IntervalDays: 30, LeadDays: 7,
		}, testutil.TestActor())
		require.NoError(t, err)
		orders = append(orders, task.SortOrder)
	}
	assert.Equal(t, []int{0, 1, 2}, orders, "each one lands after the last")

	// And a gap does not make the next one collide. Removing the middle task
	// leaves 0 and 2 behind; counting would put the next at 2.
	full, err := svc.GetPlanWithTasks(ctx, tx, plan.ID)
	require.NoError(t, err)
	require.NoError(t, svc.RemoveTask(ctx, tx, plan.ID, full.Tasks[1].ID, time.Now(), testutil.TestActor()))

	task, err := svc.AddTask(ctx, tx, app.AddPlanTaskParams{
		PlanID: plan.ID, Name: "Water filter", IntervalDays: 180, LeadDays: 14,
	}, testutil.TestActor())
	require.NoError(t, err)
	assert.Equal(t, 3, task.SortOrder, "still after everything, not on top of the last one")
}

// Removing a task that has never been used deletes it outright. Nothing is at
// stake, and leaving a typo on the plan forever would be silly.
func TestRemoveUnusedTaskDeletesIt(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newServicePlanService()
	customer := testutil.CreateCustomer(t, tx)
	machine := registerMachine(t, tx, customer.ID)
	plan, task := planWithTask(t, tx, svc, "Unused "+uuid.NewString()[:8], 90)

	_, err := svc.AssignPlan(ctx, tx, app.AssignServicePlanParams{
		EquipmentID: machine.ID,
		PlanID:      plan.ID,
		StartsOn:    planDay(2026, time.June, 1),
	}, testDay(), testutil.TestActor())
	require.NoError(t, err)

	require.NoError(t, svc.RemoveTask(ctx, tx, plan.ID, task.ID, testDay(), testutil.TestActor()))

	after, err := svc.GetPlanWithTasks(ctx, tx, plan.ID)
	require.NoError(t, err)
	assert.Empty(t, after.Tasks, "gone, not retired — there was nothing to keep")

	machineID := machine.ID
	rows, err := svc.ListDue(ctx, tx, store.MaintenanceFilter{
		EquipmentID: &machineID, Now: planDay(2026, time.June, 1),
	})
	require.NoError(t, err)
	assert.Empty(t, rows, "the visit nobody is going to goes with it")
}

// A task with history is retired instead of deleted. This is the whole point:
// deleting it cascades service_maintenance_due and takes completed visits with
// it, and refusing outright — which is what this used to do — left the task
// generating work forever with no way off the plan.
func TestRemoveUsedTaskRetiresItAndKeepsTheRecord(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newServicePlanService()
	customer := testutil.CreateCustomer(t, tx)
	machine := registerMachine(t, tx, customer.ID)
	plan, task := planWithTask(t, tx, svc, "Used "+uuid.NewString()[:8], 90)

	_, err := svc.AssignPlan(ctx, tx, app.AssignServicePlanParams{
		EquipmentID: machine.ID,
		PlanID:      plan.ID,
		StartsOn:    planDay(2026, time.March, 1),
	}, testDay(), testutil.TestActor())
	require.NoError(t, err)

	machineID := machine.ID
	pending, err := svc.ListDue(ctx, tx, store.MaintenanceFilter{
		EquipmentID: &machineID, Now: planDay(2026, time.June, 20),
	})
	require.NoError(t, err)
	require.Len(t, pending, 1)

	// Do it once. That closes the occurrence and writes the successor, so the
	// task now has both a record and a live pending row — the two things the
	// removal has to treat differently.
	_, err = svc.CompleteDue(ctx, tx, pending[0].ID,
		app.CompleteDueParams{CompletedOn: planDay(2026, time.June, 1)}, testDay(), staffActorFor(t, tx))
	require.NoError(t, err)

	require.NoError(t, svc.RemoveTask(ctx, tx, plan.ID, task.ID, testDay(), testutil.TestActor()))

	after, err := svc.GetPlanWithTasks(ctx, tx, plan.ID)
	require.NoError(t, err)
	require.Len(t, after.Tasks, 1, "still on the plan, so staff can see where the job went")
	assert.True(t, after.Tasks[0].Retired(), "but retired")
	require.NotNil(t, after.Tasks[0].RetiredAt)

	live, err := svc.GetPlanWithLiveTasks(ctx, tx, plan.ID)
	require.NoError(t, err)
	assert.Empty(t, live.Tasks, "and invisible to anything that generates work")

	history, err := svc.ListDue(ctx, tx, store.MaintenanceFilter{
		EquipmentID: &machineID, Scope: store.MaintenanceScopeHistory,
		Now: planDay(2026, time.June, 20),
	})
	require.NoError(t, err)
	require.Len(t, history, 1, "the completed visit is exactly what deleting would have destroyed")
	assert.Equal(t, domain.MaintenanceStatusCompleted, history[0].Status)

	stillPending, err := svc.ListDue(ctx, tx, store.MaintenanceFilter{
		EquipmentID: &machineID, Now: planDay(2026, time.June, 20),
	})
	require.NoError(t, err)
	assert.Empty(t, stillPending, "the successor nobody will now attend is dropped")
}

// A retired task must not come back the next time the sweep runs, which is the
// failure that would make the whole feature pointless.
func TestRetiredTaskGeneratesNoNewWork(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newServicePlanService()
	customer := testutil.CreateCustomer(t, tx)
	machine := registerMachine(t, tx, customer.ID)
	plan, task := planWithTask(t, tx, svc, "Retired "+uuid.NewString()[:8], 90)

	_, err := svc.AssignPlan(ctx, tx, app.AssignServicePlanParams{
		EquipmentID: machine.ID,
		PlanID:      plan.ID,
		StartsOn:    planDay(2026, time.March, 1),
	}, testDay(), testutil.TestActor())
	require.NoError(t, err)

	machineID := machine.ID
	pending, err := svc.ListDue(ctx, tx, store.MaintenanceFilter{
		EquipmentID: &machineID, Now: planDay(2026, time.June, 20),
	})
	require.NoError(t, err)
	require.Len(t, pending, 1)
	_, err = svc.CompleteDue(ctx, tx, pending[0].ID,
		app.CompleteDueParams{CompletedOn: planDay(2026, time.June, 1)}, testDay(), staffActorFor(t, tx))
	require.NoError(t, err)

	require.NoError(t, svc.RemoveTask(ctx, tx, plan.ID, task.ID, testDay(), testutil.TestActor()))

	missing, err := store.NewServicePlanStore().ListMissingDue(ctx, tx)
	require.NoError(t, err)
	for _, m := range missing {
		assert.NotEqual(t, task.ID, m.TaskID,
			"the backfill would put the retired job straight back on the machine")
	}
}

// A plan whose whole series has been retired generates nothing, so it cannot be
// put on a machine — the same rule an empty plan follows, for the same reason.
func TestAssignPlanWithOnlyRetiredTasksIsRefused(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newServicePlanService()
	customer := testutil.CreateCustomer(t, tx)
	first := registerMachine(t, tx, customer.ID)
	plan, task := planWithTask(t, tx, svc, "AllRetired "+uuid.NewString()[:8], 90)

	_, err := svc.AssignPlan(ctx, tx, app.AssignServicePlanParams{
		EquipmentID: first.ID, PlanID: plan.ID, StartsOn: planDay(2026, time.March, 1),
	}, testDay(), testutil.TestActor())
	require.NoError(t, err)

	firstID := first.ID
	pending, err := svc.ListDue(ctx, tx, store.MaintenanceFilter{
		EquipmentID: &firstID, Now: planDay(2026, time.June, 20),
	})
	require.NoError(t, err)
	require.Len(t, pending, 1)
	_, err = svc.CompleteDue(ctx, tx, pending[0].ID,
		app.CompleteDueParams{CompletedOn: planDay(2026, time.June, 1)}, testDay(), staffActorFor(t, tx))
	require.NoError(t, err)
	require.NoError(t, svc.RemoveTask(ctx, tx, plan.ID, task.ID, testDay(), testutil.TestActor()))

	second := registerMachine(t, tx, customer.ID)
	_, err = svc.AssignPlan(ctx, tx, app.AssignServicePlanParams{
		EquipmentID: second.ID, PlanID: plan.ID, StartsOn: planDay(2026, time.March, 1),
	}, testDay(), testutil.TestActor())

	assert.ErrorIs(t, err, app.ErrPlanHasNoTasks,
		"a plan that looks covered from the machine page and generates nothing is the worst of both")
}

// The gap between "retire keeps booked visits" and "a retired task generates
// nothing": closing one of those kept visits must not write a successor.
//
// RemoveTask deliberately leaves a pending occurrence that carries a ticket —
// somebody is going to that visit. It is also, by itself, history, so the task
// retires rather than deletes. Closing it later ran the same
// successor-writing path as any other completion, which put the retired job
// straight back on the machine. On a contract account the nightly sweep then
// books that row a ticket, making it booked-pending again, which regenerates on
// close: the task would never come off.
func TestClosingARetiredTasksBookedVisitWritesNoSuccessor(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newServicePlanService()
	tickets := newTicketService()
	customer := testutil.CreateCustomer(t, tx)
	machine := registerMachine(t, tx, customer.ID)
	plan, task := planWithTask(t, tx, svc, "Booked "+uuid.NewString()[:8], 90)

	_, err := svc.AssignPlan(ctx, tx, app.AssignServicePlanParams{
		EquipmentID: machine.ID,
		PlanID:      plan.ID,
		StartsOn:    planDay(2026, time.March, 1),
	}, testDay(), testutil.TestActor())
	require.NoError(t, err)

	machineID := machine.ID
	pending, err := svc.ListDue(ctx, tx, store.MaintenanceFilter{
		EquipmentID: &machineID, Now: planDay(2026, time.June, 20),
	})
	require.NoError(t, err)
	require.Len(t, pending, 1)

	// Book the visit. This is what the nightly sweep does for contract work and
	// what the call list does by hand for uncovered work.
	ticket, err := tickets.Open(ctx, tx, openParams(customer.ID), testutil.TestActor())
	require.NoError(t, err)
	require.NoError(t, svc.AttachTicket(ctx, tx, pending[0].ID, ticket.ID))

	// The booked row is the task's only occurrence, so it counts as history and
	// survives the removal — the task retires rather than deletes.
	require.NoError(t, svc.RemoveTask(ctx, tx, plan.ID, task.ID, testDay(), testutil.TestActor()))

	after, err := svc.GetPlanWithTasks(ctx, tx, plan.ID)
	require.NoError(t, err)
	require.Len(t, after.Tasks, 1)
	require.True(t, after.Tasks[0].Retired())

	stillBooked, err := svc.ListDue(ctx, tx, store.MaintenanceFilter{
		EquipmentID: &machineID, Now: planDay(2026, time.June, 20),
	})
	require.NoError(t, err)
	require.Len(t, stillBooked, 1, "the promised visit stands")

	// The tech goes, and writes it up.
	_, err = svc.CompleteDue(ctx, tx, stillBooked[0].ID,
		app.CompleteDueParams{CompletedOn: planDay(2026, time.June, 13)}, testDay(), staffActorFor(t, tx))
	require.NoError(t, err)

	next, err := svc.ListDue(ctx, tx, store.MaintenanceFilter{
		EquipmentID: &machineID, Now: planDay(2026, time.September, 30),
	})
	require.NoError(t, err)
	assert.Empty(t, next, "a retired task does not come back the moment its last visit is closed")

	history, err := svc.ListDue(ctx, tx, store.MaintenanceFilter{
		EquipmentID: &machineID, Scope: store.MaintenanceScopeHistory,
		Now: planDay(2026, time.September, 30),
	})
	require.NoError(t, err)
	require.Len(t, history, 1, "and the visit that did happen is still on the record")
}

// The two write paths a retired task must not take. The plan page hides both
// controls; the routes are POSTable regardless.
func TestRetiredTaskRefusesEditsAndASecondRetirement(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	svc := newServicePlanService()
	customer := testutil.CreateCustomer(t, tx)
	machine := registerMachine(t, tx, customer.ID)
	plan, task := planWithTask(t, tx, svc, "Guarded "+uuid.NewString()[:8], 90)

	_, err := svc.AssignPlan(ctx, tx, app.AssignServicePlanParams{
		EquipmentID: machine.ID, PlanID: plan.ID, StartsOn: planDay(2026, time.March, 1),
	}, testDay(), testutil.TestActor())
	require.NoError(t, err)

	machineID := machine.ID
	pending, err := svc.ListDue(ctx, tx, store.MaintenanceFilter{
		EquipmentID: &machineID, Now: planDay(2026, time.June, 20),
	})
	require.NoError(t, err)
	require.Len(t, pending, 1)
	_, err = svc.CompleteDue(ctx, tx, pending[0].ID,
		app.CompleteDueParams{CompletedOn: planDay(2026, time.June, 1)}, testDay(), staffActorFor(t, tx))
	require.NoError(t, err)
	require.NoError(t, svc.RemoveTask(ctx, tx, plan.ID, task.ID, testDay(), testutil.TestActor()))

	_, err = svc.EditTask(ctx, tx, task.ID, app.EditPlanTaskParams{
		Name: "Full service", IntervalDays: 30, LeadDays: 7,
	}, testDay(), testutil.TestActor())
	assert.ErrorIs(t, err, app.ErrPlanTaskRetired,
		"an interval change would move visits on a job that has stopped")

	err = svc.RemoveTask(ctx, tx, plan.ID, task.ID, testDay(), testutil.TestActor())
	assert.ErrorIs(t, err, app.ErrPlanTaskRetired,
		"a double submit must not write a second audit row for one retirement")
}
