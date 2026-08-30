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
	}, testutil.TestActor())
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
	}, testutil.TestActor())

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
	_, err := svc.AssignPlan(ctx, tx, params, testutil.TestActor())
	require.NoError(t, err)

	// The unique violation aborts the transaction, so this assertion has to be
	// the last thing the test does with it.
	_, err = svc.AssignPlan(ctx, tx, params, testutil.TestActor())
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
	}, testutil.TestActor())
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
	_, err = svc.CompleteDue(ctx, tx, first[0].ID, app.CompleteDueParams{CompletedOn: doneOn}, staffActorFor(t, tx))
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
	}, testutil.TestActor())
	require.NoError(t, err)

	machineID := machine.ID
	rows, err := svc.ListDue(ctx, tx, store.MaintenanceFilter{EquipmentID: &machineID, Now: planDay(2026, time.September, 5)})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, planDay(2026, time.August, 31).UTC(), rows[0].DueOn.UTC())

	_, err = svc.SkipDue(ctx, tx, rows[0].ID, planDay(2026, time.September, 5), "cafe was shut", staffActorFor(t, tx))
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
	}, testutil.TestActor())
	require.NoError(t, err)

	machineID := machine.ID
	rows, err := svc.ListDue(ctx, tx, store.MaintenanceFilter{EquipmentID: &machineID, Now: planDay(2026, time.June, 1)})
	require.NoError(t, err)
	require.Len(t, rows, 1)

	done := app.CompleteDueParams{CompletedOn: planDay(2026, time.June, 1)}
	actor := staffActorFor(t, tx)
	_, err = svc.CompleteDue(ctx, tx, rows[0].ID, done, actor)
	require.NoError(t, err)

	_, err = svc.CompleteDue(ctx, tx, rows[0].ID, done, actor)
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
	}, testutil.TestActor())
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
	}, testutil.TestActor())
	require.NoError(t, err)

	machineID := machine.ID
	rows, err := svc.ListDue(ctx, tx, store.MaintenanceFilter{EquipmentID: &machineID, Now: planDay(2026, time.June, 1)})
	require.NoError(t, err)
	require.Len(t, rows, 1)

	_, err = svc.CompleteDue(ctx, tx, rows[0].ID, app.CompleteDueParams{CompletedOn: planDay(2026, time.June, 1)}, staffActorFor(t, tx))
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
	}, testutil.TestActor())
	require.NoError(t, err)

	machineID := machine.ID
	rows, err := svc.ListDue(ctx, tx, store.MaintenanceFilter{EquipmentID: &machineID, Now: planDay(2026, time.June, 1)})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	dueID := rows[0].ID

	// End the assignment, which deletes the pending row — then try to close the
	// id somebody still had on a stale page.
	require.NoError(t, svc.EndAssignment(ctx, tx, machine.ID, assignment.ID, time.Now(), testutil.TestActor()))

	_, err = svc.CompleteDue(ctx, tx, dueID, app.CompleteDueParams{CompletedOn: planDay(2026, time.June, 1)}, staffActorFor(t, tx))
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
	}, testutil.TestActor())

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
	}, testutil.TestActor())
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
	}, staffActorFor(t, tx))
	require.NoError(t, err)

	second := pending(today)
	require.Equal(t, planDay(2026, time.April, 1).UTC(), second.DueOn.UTC())
	_, err = svc.SkipDue(ctx, tx, second.ID, planDay(2026, time.April, 2), "customer declined", staffActorFor(t, tx))
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
	assert.Equal(t, planDay(2026, time.May, 1).UTC(), moved.DueOn.UTC(),
		"clamped forward a whole interval at a time, so it stays on the new cadence")

	// Due today on a fifteen-day cadence is legitimately due — the plan now
	// says so. What must not happen is it arriving already late.
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
	}, testutil.TestActor())
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
	}, today, testutil.TestActor())
	require.NoError(t, err)

	rows, err = svc.ListDue(ctx, tx, store.MaintenanceFilter{EquipmentID: &machineID, Now: today})
	require.NoError(t, err)
	assert.Equal(t, planDay(2025, time.April, 11).UTC(), rows[0].DueOn.UTC(),
		"shifted by the ten days the interval grew, and still overdue")
}
