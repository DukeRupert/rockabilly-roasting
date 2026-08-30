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

func planTestDay(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// planFixture is the shape every test here needs: a customer with a machine, a
// plan holding one task, and the machine on that plan.
type planFixture struct {
	plans      *store.ServicePlanStore
	equipment  *domain.Equipment
	plan       *domain.ServicePlan
	task       *domain.ServicePlanTask
	assignment *domain.EquipmentServicePlan
}

func newPlanFixture(t *testing.T, tx pgx.Tx, intervalDays, leadDays int, underContract bool) planFixture {
	t.Helper()
	ctx := t.Context()

	plans := store.NewServicePlanStore()
	customer := testutil.CreateCustomer(t, tx)

	equip, err := store.NewEquipmentStore().Create(ctx, tx, equipmentParams(customer.ID))
	require.NoError(t, err)

	plan, err := plans.Create(ctx, tx, store.CreateServicePlanParams{Name: "Plan " + uuid.NewString()})
	require.NoError(t, err)

	task, err := plans.CreateTask(ctx, tx, store.CreateServicePlanTaskParams{
		PlanID:       plan.ID,
		Name:         "Full service",
		IntervalDays: intervalDays,
		LeadDays:     leadDays,
	})
	require.NoError(t, err)

	assignment, err := plans.Assign(ctx, tx, store.AssignPlanParams{
		EquipmentID:   equip.ID,
		PlanID:        plan.ID,
		StartsOn:      planTestDay(2026, time.January, 1),
		UnderContract: underContract,
	})
	require.NoError(t, err)

	return planFixture{plans: plans, equipment: equip, plan: plan, task: task, assignment: assignment}
}

// due writes a pending occurrence on a day.
func (f planFixture) due(t *testing.T, tx pgx.Tx, on time.Time) *domain.MaintenanceDue {
	t.Helper()
	d, err := f.plans.CreateDue(t.Context(), tx, store.CreateMaintenanceDueParams{
		AssignmentID: f.assignment.ID,
		TaskID:       f.task.ID,
		EquipmentID:  f.equipment.ID,
		DueOn:        on,
	})
	require.NoError(t, err)
	require.NotNil(t, d)
	return d
}

// The invariant behind every idempotent path: one pending occurrence per task
// per machine, enforced by the database rather than by hoping.
func TestCreateDueIsIdempotent(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	f := newPlanFixture(t, tx, 90, 14, false)

	f.due(t, tx, planTestDay(2026, time.April, 1))

	second, err := f.plans.CreateDue(t.Context(), tx, store.CreateMaintenanceDueParams{
		AssignmentID: f.assignment.ID,
		TaskID:       f.task.ID,
		EquipmentID:  f.equipment.ID,
		DueOn:        planTestDay(2026, time.May, 1),
	})
	require.NoError(t, err, "a second insert is a no-op, not an error — the sweep runs over the same data daily")
	assert.Nil(t, second, "nil says the row was already there")

	rows, err := f.plans.ListDue(t.Context(), tx, store.MaintenanceFilter{
		EquipmentID: &f.equipment.ID,
		Now:         planTestDay(2026, time.April, 1),
	})
	require.NoError(t, err)
	assert.Len(t, rows, 1)
}

func TestMaintenanceScopes(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := t.Context()
	today := planTestDay(2026, time.September, 1)

	overdue := newPlanFixture(t, tx, 90, 14, false)
	overdue.due(t, tx, planTestDay(2026, time.August, 1))

	soon := newPlanFixture(t, tx, 90, 14, true)
	soon.due(t, tx, planTestDay(2026, time.September, 10))

	later := newPlanFixture(t, tx, 90, 14, true)
	later.due(t, tx, planTestDay(2026, time.December, 1))

	plans := store.NewServicePlanStore()
	count := func(scope store.MaintenanceScope) int {
		n, err := plans.CountDue(ctx, tx, store.MaintenanceFilter{Scope: scope, Now: today})
		require.NoError(t, err)
		return n
	}

	assert.Equal(t, 1, count(store.MaintenanceScopeOverdue))
	assert.Equal(t, 1, count(store.MaintenanceScopeDueSoon), "only the one inside its own lead window")
	assert.Equal(t, 3, count(store.MaintenanceScopeAll), "everything pending, however far out")
	assert.Equal(t, 1, count(store.MaintenanceScopeUncovered),
		"the call list is near work nobody is paying for")
	assert.Equal(t, 1, count(store.MaintenanceScopeBookable),
		"covered work inside its window — the far-out one is not booked yet")
}

// A retired machine drops off every list. Nagging about a machine in a skip is
// how staff learn to ignore the whole page.
func TestRetiredMachineLeavesTheDueList(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := t.Context()
	f := newPlanFixture(t, tx, 90, 14, true)
	f.due(t, tx, planTestDay(2026, time.August, 1))

	before, err := f.plans.CountDue(ctx, tx, store.MaintenanceFilter{Now: planTestDay(2026, time.September, 1)})
	require.NoError(t, err)
	require.Equal(t, 1, before)

	_, err = store.NewEquipmentStore().UpdateStatus(ctx, tx, f.equipment.ID, domain.EquipmentStatusRetired)
	require.NoError(t, err)

	after, err := f.plans.CountDue(ctx, tx, store.MaintenanceFilter{Now: planTestDay(2026, time.September, 1)})
	require.NoError(t, err)
	assert.Equal(t, 0, after)
}

// The backfill path: a task added to a plan after machines were already on it.
func TestListMissingDueFindsTasksAddedLater(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := t.Context()
	f := newPlanFixture(t, tx, 90, 14, false)
	f.due(t, tx, planTestDay(2026, time.April, 1))

	// Nothing missing while the plan holds one task with a pending occurrence.
	missing, err := f.plans.ListMissingDue(ctx, tx)
	require.NoError(t, err)
	assert.Empty(t, missing)

	extra, err := f.plans.CreateTask(ctx, tx, store.CreateServicePlanTaskParams{
		PlanID:       f.plan.ID,
		Name:         "Descale",
		IntervalDays: 180,
		LeadDays:     30,
	})
	require.NoError(t, err)

	missing, err = f.plans.ListMissingDue(ctx, tx)
	require.NoError(t, err)
	require.Len(t, missing, 1, "the new task reaches the machine already on the plan")
	assert.Equal(t, extra.ID, missing[0].TaskID)
	assert.Equal(t, planTestDay(2026, time.January, 1).UTC(), missing[0].Anchor.UTC(),
		"with no completion to go on, the anchor is the assignment's start date")
}

// An ended assignment stops producing work for the backfill to find.
func TestListMissingDueIgnoresEndedAssignments(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := t.Context()
	f := newPlanFixture(t, tx, 90, 14, false)

	missing, err := f.plans.ListMissingDue(ctx, tx)
	require.NoError(t, err)
	require.Len(t, missing, 1, "a fresh assignment with no occurrence yet is a gap")

	require.NoError(t, f.plans.EndAssignment(ctx, tx, f.assignment.ID, time.Now()))

	missing, err = f.plans.ListMissingDue(ctx, tx)
	require.NoError(t, err)
	assert.Empty(t, missing)
}

// Closing an occurrence is scoped to pending rows, which is what makes the
// double submit safe one layer up.
func TestCloseDueOnlyClosesPending(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := t.Context()
	f := newPlanFixture(t, tx, 90, 14, false)
	d := f.due(t, tx, planTestDay(2026, time.April, 1))

	closed, err := f.plans.CloseDue(ctx, tx, d.ID, store.CloseDueParams{
		Status:      domain.MaintenanceStatusCompleted,
		CompletedOn: planTestDay(2026, time.April, 3),
	})
	require.NoError(t, err)
	assert.Equal(t, domain.MaintenanceStatusCompleted, closed.Status)

	_, err = f.plans.CloseDue(ctx, tx, d.ID, store.CloseDueParams{
		Status:      domain.MaintenanceStatusSkipped,
		CompletedOn: planTestDay(2026, time.April, 4),
	})
	assert.ErrorIs(t, err, pgx.ErrNoRows, "a closed occurrence cannot be closed again")
}

func TestPlanNamesAreUniqueCaseInsensitively(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := t.Context()
	plans := store.NewServicePlanStore()

	_, err := plans.Create(ctx, tx, store.CreateServicePlanParams{Name: "Linea PB warranty"})
	require.NoError(t, err)

	_, err = plans.Create(ctx, tx, store.CreateServicePlanParams{Name: "linea pb WARRANTY"})
	assert.Error(t, err, "two plans differing only in case is a data-entry accident, not a choice")
}

// Retiring a machine ends its schedule but must not erase what was done to it.
// Migration 077 keeps retired machines precisely so that record survives — it
// is the argument for having replaced the thing.
func TestRetiredMachineKeepsItsHistory(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := t.Context()
	f := newPlanFixture(t, tx, 90, 14, false)
	d := f.due(t, tx, planTestDay(2026, time.April, 1))

	_, err := f.plans.CloseDue(ctx, tx, d.ID, store.CloseDueParams{
		Status:      domain.MaintenanceStatusCompleted,
		CompletedOn: planTestDay(2026, time.April, 3),
	})
	require.NoError(t, err)

	_, err = store.NewEquipmentStore().UpdateStatus(ctx, tx, f.equipment.ID, domain.EquipmentStatusRetired)
	require.NoError(t, err)

	history, err := f.plans.ListDue(ctx, tx, store.MaintenanceFilter{
		EquipmentID: &f.equipment.ID,
		Scope:       store.MaintenanceScopeHistory,
		Now:         planTestDay(2026, time.September, 1),
	})
	require.NoError(t, err)
	assert.Len(t, history, 1, "what was done to the machine outlives the machine")

	pending, err := f.plans.CountDue(ctx, tx, store.MaintenanceFilter{
		EquipmentID: &f.equipment.ID,
		Now:         planTestDay(2026, time.September, 1),
	})
	require.NoError(t, err)
	assert.Equal(t, 0, pending, "but nothing is still owed on it")
}
