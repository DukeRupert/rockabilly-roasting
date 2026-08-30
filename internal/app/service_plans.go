package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
)

// ServicePlanService owns preventive maintenance: the plans a shop writes, the
// machines they are assigned to, and the dated occurrences those assignments
// generate.
//
// The rule the whole service turns on: every write that closes an occurrence
// also writes the next one, in the same transaction. A schedule with a gap in
// it is worse than no schedule — it looks like everything is up to date.
//
// Part of the equipment service module; see docs/equipment-service-module.md.
// Nothing here checks whether the module is switched on: that is decided once,
// at the router, by requireModule.
type ServicePlanService struct {
	plans     *store.ServicePlanStore
	equipment *store.EquipmentStore
	audit     *audit.AuditWriter

	// Wired by WithScheduling, and only needed by the daily sweep: the module
	// toggle it obeys, the ticket service it books covered work through, and
	// the gauges it publishes. Nil everywhere else, and every use is guarded.
	tickets *ServiceTicketService
	modules *ModuleService
	metrics *metrics.Registry
}

// NewServicePlanService creates a new ServicePlanService.
func NewServicePlanService(plans *store.ServicePlanStore, equipment *store.EquipmentStore, auditWriter *audit.AuditWriter) *ServicePlanService {
	return &ServicePlanService{plans: plans, equipment: equipment, audit: auditWriter}
}

// pgUniqueViolation is the SQLSTATE Postgres returns when an INSERT collides
// with a unique index — a duplicate plan name, or the same plan assigned twice
// to one machine. Both are user mistakes with a sentence to say about them, not
// internal errors.
const pgUniqueViolation = "23505"

// isUniqueViolation reports whether err is that collision.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation
}

// maxPlanInterval caps a task's interval at ten years. Not a business rule —
// a guard against a typo (3650 for 365) putting a machine's next service
// beyond anyone's working life, where nothing would ever surface it again.
const maxPlanInterval = 3650

// --- Plans ---

// CreateServicePlanParams is the input for defining a maintenance plan.
type CreateServicePlanParams = store.CreateServicePlanParams

// CreatePlan defines a plan.
func (s *ServicePlanService) CreatePlan(ctx context.Context, tx pgx.Tx, p CreateServicePlanParams, actor Actor) (*domain.ServicePlan, error) {
	p.Name = strings.TrimSpace(p.Name)
	p.Description = strings.TrimSpace(p.Description)

	if p.Name == "" {
		return nil, ErrPlanNameRequired
	}
	if p.Category != "" && !p.Category.Valid() {
		return nil, ErrInvalidEquipmentCategory
	}

	plan, err := s.plans.Create(ctx, tx, p)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrPlanNameTaken
		}
		return nil, err
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditServicePlanCreated,
		ResourceType: "service_plan",
		ResourceID:   plan.ID,
		Metadata: map[string]any{
			"name":     plan.Name,
			"category": string(plan.Category),
		},
	}); err != nil {
		return nil, fmt.Errorf("audit service plan created: %w", err)
	}

	return plan, nil
}

// EditServicePlanParams is the editable half of a plan.
type EditServicePlanParams = store.UpdateServicePlanParams

// EditPlan rewrites a plan's details. Editing the plan reaches every machine on
// it, which is the point of a plan being a template.
func (s *ServicePlanService) EditPlan(ctx context.Context, tx pgx.Tx, id uuid.UUID, p EditServicePlanParams, actor Actor) (*domain.ServicePlan, error) {
	p.Name = strings.TrimSpace(p.Name)
	p.Description = strings.TrimSpace(p.Description)

	if p.Name == "" {
		return nil, ErrPlanNameRequired
	}
	if p.Category != "" && !p.Category.Valid() {
		return nil, ErrInvalidEquipmentCategory
	}

	before, err := s.GetPlan(ctx, tx, id)
	if err != nil {
		return nil, err
	}

	plan, err := s.plans.UpdatePlan(ctx, tx, id, p)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPlanNotFound
		}
		if isUniqueViolation(err) {
			return nil, ErrPlanNameTaken
		}
		return nil, err
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditServicePlanUpdated,
		ResourceType: "service_plan",
		ResourceID:   plan.ID,
		Metadata: map[string]any{
			"name":       plan.Name,
			"was":        before.Name,
			"active":     plan.Active,
			"was_active": before.Active,
		},
	}); err != nil {
		return nil, fmt.Errorf("audit service plan updated: %w", err)
	}

	return plan, nil
}

// DeletePlan removes a plan that has never been used. A plan with machines on
// it — live or historical — is deactivated instead: deleting it would take the
// maintenance history of every one of those machines with it.
func (s *ServicePlanService) DeletePlan(ctx context.Context, tx pgx.Tx, id uuid.UUID, actor Actor) error {
	plan, err := s.GetPlan(ctx, tx, id)
	if err != nil {
		return err
	}

	_, total, err := s.plans.CountAssignments(ctx, tx, id)
	if err != nil {
		return err
	}
	if total > 0 {
		return ErrPlanInUse
	}

	if err := s.plans.DeletePlan(ctx, tx, id); err != nil {
		return err
	}

	return s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditServicePlanDeleted,
		ResourceType: "service_plan",
		ResourceID:   id,
		Metadata:     map[string]any{"name": plan.Name},
	})
}

// GetPlan returns one plan, without its tasks.
func (s *ServicePlanService) GetPlan(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.ServicePlan, error) {
	plan, err := s.plans.GetPlan(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPlanNotFound
		}
		return nil, err
	}
	return plan, nil
}

// GetPlanWithTasks returns a plan and its series — what the plan page renders
// and what the assignment path needs to generate the first occurrences.
func (s *ServicePlanService) GetPlanWithTasks(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.ServicePlan, error) {
	plan, err := s.GetPlan(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	tasks, err := s.plans.ListTasks(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	plan.Tasks = tasks
	return plan, nil
}

// ListPlans returns plans by name.
func (s *ServicePlanService) ListPlans(ctx context.Context, tx pgx.Tx, f store.ServicePlanFilter) ([]domain.ServicePlan, error) {
	return s.plans.ListPlans(ctx, tx, f)
}

// CountAssignments is how many machines are on one plan — live, and ever. The
// second number is what decides whether the plan can be deleted.
func (s *ServicePlanService) CountAssignments(ctx context.Context, tx pgx.Tx, planID uuid.UUID) (live, total int, err error) {
	return s.plans.CountAssignments(ctx, tx, planID)
}

// CountAssignmentsByPlan is how many live machines sit on each plan.
func (s *ServicePlanService) CountAssignmentsByPlan(ctx context.Context, tx pgx.Tx) (map[uuid.UUID]int, error) {
	return s.plans.CountAssignmentsByPlan(ctx, tx)
}

// --- Tasks ---

// AddPlanTaskParams is the input for adding an item to a plan's series.
type AddPlanTaskParams = store.CreateServicePlanTaskParams

// AddTask adds an item to a plan's series.
//
// It does not backfill occurrences onto the machines already on the plan. The
// daily sweep does that, from ListMissingDue — one path that finds every gap
// beats two paths that each find some of them, and a task added at 4pm is due
// no sooner for having been materialised at 4pm.
func (s *ServicePlanService) AddTask(ctx context.Context, tx pgx.Tx, p AddPlanTaskParams, actor Actor) (*domain.ServicePlanTask, error) {
	p.Name = strings.TrimSpace(p.Name)
	p.Instructions = strings.TrimSpace(p.Instructions)

	if err := validatePlanTask(p.Name, p.IntervalDays, p.LeadDays); err != nil {
		return nil, err
	}

	plan, err := s.GetPlan(ctx, tx, p.PlanID)
	if err != nil {
		return nil, err
	}

	task, err := s.plans.CreateTask(ctx, tx, p)
	if err != nil {
		return nil, err
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditServicePlanTaskAdded,
		ResourceType: "service_plan",
		ResourceID:   plan.ID,
		Metadata: map[string]any{
			"plan":              plan.Name,
			"task":              task.Name,
			"task_id":           task.ID.String(),
			"interval_days":     task.IntervalDays,
			"warranty_required": task.WarrantyRequired,
		},
	}); err != nil {
		return nil, fmt.Errorf("audit plan task added: %w", err)
	}

	return task, nil
}

// EditPlanTaskParams is the editable half of a task.
type EditPlanTaskParams = store.UpdateServicePlanTaskParams

// GetTaskOnPlan returns a task, refusing one that belongs to a different plan.
// The ids arrive from an editable URL, so the pairing is checked rather than
// assumed.
func (s *ServicePlanService) GetTaskOnPlan(ctx context.Context, tx pgx.Tx, planID, taskID uuid.UUID) (*domain.ServicePlanTask, error) {
	task, err := s.plans.GetTask(ctx, tx, taskID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPlanTaskNotFound
		}
		return nil, err
	}
	if task.PlanID != planID {
		return nil, ErrPlanTaskNotFound
	}
	return task, nil
}

// EditTask rewrites a task. A changed interval is pushed onto every pending
// occurrence of the task, re-measured from whatever each one was anchored to —
// otherwise the plan would say "every 60 days" while forty machines quietly
// stayed on the old ninety.
func (s *ServicePlanService) EditTask(ctx context.Context, tx pgx.Tx, id uuid.UUID, p EditPlanTaskParams, now time.Time, actor Actor) (*domain.ServicePlanTask, error) {
	p.Name = strings.TrimSpace(p.Name)
	p.Instructions = strings.TrimSpace(p.Instructions)

	if err := validatePlanTask(p.Name, p.IntervalDays, p.LeadDays); err != nil {
		return nil, err
	}

	before, err := s.plans.GetTask(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPlanTaskNotFound
		}
		return nil, err
	}

	task, err := s.plans.UpdateTask(ctx, tx, id, p)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPlanTaskNotFound
		}
		return nil, err
	}

	rescheduled := 0
	if before.IntervalDays != task.IntervalDays {
		rescheduled, err = s.rescheduleTask(ctx, tx, task.ID, before.IntervalDays, task.IntervalDays, now)
		if err != nil {
			return nil, err
		}
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditServicePlanTaskUpdated,
		ResourceType: "service_plan",
		ResourceID:   task.PlanID,
		Metadata: map[string]any{
			"task":              task.Name,
			"task_id":           task.ID.String(),
			"was":               before.Name,
			"interval_days":     task.IntervalDays,
			"was_interval_days": before.IntervalDays,
			"rescheduled":       rescheduled,
		},
	}); err != nil {
		return nil, fmt.Errorf("audit plan task updated: %w", err)
	}

	return task, nil
}

// rescheduleTask moves every pending occurrence of a task onto a new interval.
//
// Each is shifted by the difference and clamped so a future occurrence cannot
// land in the past — see domain.RescheduledDue for why that matters.
func (s *ServicePlanService) rescheduleTask(ctx context.Context, tx pgx.Tx, taskID uuid.UUID, oldInterval, newInterval int, now time.Time) (int, error) {
	pending, err := s.plans.ListPendingForTask(ctx, tx, taskID)
	if err != nil {
		return 0, err
	}
	// Counting moves, not examinations. Work already owed stays where it is, so
	// forty overdue machines used to audit "rescheduled: 40" having moved none
	// — a number somebody would later read as evidence that dates changed.
	moved := 0
	for _, d := range pending {
		next := domain.RescheduledDue(d.DueOn, oldInterval, newInterval, now)
		if next.Equal(d.DueOn.UTC().Truncate(24 * time.Hour)) {
			continue
		}
		if err := s.plans.UpdateDueOn(ctx, tx, d.ID, next); err != nil {
			return 0, err
		}
		moved++
	}
	return moved, nil
}

// RemoveTask takes an item out of a plan's series. Its occurrences go with it —
// pending and historical alike, by the cascade — because a completed record of
// a task that is no longer in the plan describes work nobody can now interpret.
func (s *ServicePlanService) RemoveTask(ctx context.Context, tx pgx.Tx, planID, id uuid.UUID, actor Actor) error {
	task, err := s.plans.GetTask(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrPlanTaskNotFound
		}
		return err
	}
	// The id arrives from an editable URL. A task on another plan must not be
	// deletable through this one — and the confirm dialog the staffer just read
	// described a different task.
	if task.PlanID != planID {
		return ErrPlanTaskNotFound
	}

	if err := s.plans.DeleteTask(ctx, tx, id); err != nil {
		return err
	}

	return s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditServicePlanTaskRemoved,
		ResourceType: "service_plan",
		ResourceID:   task.PlanID,
		Metadata: map[string]any{
			"task":    task.Name,
			"task_id": task.ID.String(),
		},
	})
}

// validatePlanTask guards the fields a task cannot do without.
func validatePlanTask(name string, intervalDays, leadDays int) error {
	if name == "" {
		return ErrPlanTaskNameRequired
	}
	if intervalDays < 1 || intervalDays > maxPlanInterval {
		return ErrPlanIntervalInvalid
	}
	// A lead longer than the interval would put a task inside its own warning
	// window the moment it was completed, so every task would always read as
	// due soon and the list would say nothing.
	if leadDays < 0 || leadDays >= intervalDays {
		return ErrPlanLeadInvalid
	}
	return nil
}

// --- Assignments ---

// AssignServicePlanParams is the input for putting a plan on a machine.
type AssignServicePlanParams = store.AssignPlanParams

// AssignPlan puts a plan on a machine and generates its first occurrences.
//
// The occurrences are written here rather than left to the sweep because the
// staffer who just assigned a plan expects to see the schedule it produced —
// finding out tomorrow whether the anchor date was right is not a workflow.
func (s *ServicePlanService) AssignPlan(ctx context.Context, tx pgx.Tx, p AssignServicePlanParams, actor Actor) (*domain.EquipmentServicePlan, error) {
	if p.StartsOn.IsZero() {
		return nil, ErrPlanStartRequired
	}
	if p.ContractEndsOn != nil && p.ContractEndsOn.Before(p.StartsOn) {
		return nil, ErrPlanContractEndsBeforeStart
	}
	p.Notes = strings.TrimSpace(p.Notes)

	equipment, err := s.equipment.GetByID(ctx, tx, p.EquipmentID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrEquipmentNotFound
		}
		return nil, err
	}
	// A retired machine is not coming back. Putting it on a schedule would
	// generate maintenance nobody will ever do, which is exactly the noise the
	// due list has to stay free of to be worth opening.
	if equipment.Status == domain.EquipmentStatusRetired {
		return nil, ErrEquipmentRetired
	}

	plan, err := s.GetPlanWithTasks(ctx, tx, p.PlanID)
	if err != nil {
		return nil, err
	}
	if !plan.Active {
		return nil, ErrPlanInactive
	}
	// A plan with no tasks generates nothing. Silently assigning it would leave
	// staff believing a machine was covered when it was not.
	if len(plan.Tasks) == 0 {
		return nil, ErrPlanHasNoTasks
	}

	assignment, err := s.plans.Assign(ctx, tx, p)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrPlanAlreadyAssigned
		}
		return nil, err
	}

	for _, task := range plan.Tasks {
		if _, err := s.plans.CreateDue(ctx, tx, store.CreateMaintenanceDueParams{
			AssignmentID: assignment.ID,
			TaskID:       task.ID,
			EquipmentID:  assignment.EquipmentID,
			DueOn:        domain.FirstDueOn(assignment.StartsOn, task.IntervalDays),
		}); err != nil {
			return nil, err
		}
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditServicePlanAssigned,
		ResourceType: "equipment",
		ResourceID:   assignment.EquipmentID,
		Metadata: map[string]any{
			"plan":           plan.Name,
			"plan_id":        plan.ID.String(),
			"assignment_id":  assignment.ID.String(),
			"machine":        equipment.Description(),
			"customer_id":    equipment.CustomerID.String(),
			"starts_on":      assignment.StartsOn.Format(time.DateOnly),
			"under_contract": assignment.UnderContract,
			"tasks":          len(plan.Tasks),
		},
	}); err != nil {
		return nil, fmt.Errorf("audit plan assigned: %w", err)
	}

	return assignment, nil
}

// EditPlanAssignmentParams is the editable half of an assignment.
type EditPlanAssignmentParams = store.UpdateAssignmentParams

// EditAssignment rewrites an assignment's terms — the anchor date and whether
// the customer is paying for the work.
//
// Moving the anchor does not move occurrences that have already been generated.
// The anchor's job is to seed the schedule; once a task has been done, the
// schedule is anchored to that instead, and rewriting history from a corrected
// start date would move dates the shop has already told a customer about.
func (s *ServicePlanService) EditAssignment(ctx context.Context, tx pgx.Tx, id uuid.UUID, p EditPlanAssignmentParams, actor Actor) (*domain.EquipmentServicePlan, error) {
	if p.StartsOn.IsZero() {
		return nil, ErrPlanStartRequired
	}
	if p.ContractEndsOn != nil && p.ContractEndsOn.Before(p.StartsOn) {
		return nil, ErrPlanContractEndsBeforeStart
	}
	p.Notes = strings.TrimSpace(p.Notes)

	before, err := s.GetAssignment(ctx, tx, id)
	if err != nil {
		return nil, err
	}

	assignment, err := s.plans.UpdateAssignment(ctx, tx, id, p)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPlanAssignmentNotFound
		}
		return nil, err
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditServicePlanAssignmentUpdated,
		ResourceType: "equipment",
		ResourceID:   assignment.EquipmentID,
		Metadata: map[string]any{
			"plan":               assignment.PlanName,
			"assignment_id":      assignment.ID.String(),
			"under_contract":     assignment.UnderContract,
			"was_under_contract": before.UnderContract,
			"starts_on":          assignment.StartsOn.Format(time.DateOnly),
		},
	}); err != nil {
		return nil, fmt.Errorf("audit plan assignment updated: %w", err)
	}

	return assignment, nil
}

// EndAssignment takes a machine off a plan. Pending occurrences go — except any
// already carrying a ticket, which survive so the visit somebody booked is not
// orphaned — and the record of what was done under the arrangement stays.
func (s *ServicePlanService) EndAssignment(ctx context.Context, tx pgx.Tx, equipmentID, id uuid.UUID, now time.Time, actor Actor) error {
	assignment, err := s.GetAssignment(ctx, tx, id)
	if err != nil {
		return err
	}
	// Scoped to the machine in the path: another machine's schedule must not be
	// stoppable through this one's page.
	if assignment.EquipmentID != equipmentID {
		return ErrPlanAssignmentNotFound
	}
	if !assignment.Live() {
		return ErrPlanAssignmentEnded
	}

	if err := s.plans.EndAssignment(ctx, tx, id, now); err != nil {
		return err
	}

	return s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditServicePlanUnassigned,
		ResourceType: "equipment",
		ResourceID:   assignment.EquipmentID,
		Metadata: map[string]any{
			"plan":          assignment.PlanName,
			"plan_id":       assignment.PlanID.String(),
			"assignment_id": assignment.ID.String(),
		},
	})
}

// GetAssignment returns one assignment.
func (s *ServicePlanService) GetAssignment(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.EquipmentServicePlan, error) {
	a, err := s.plans.GetAssignment(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPlanAssignmentNotFound
		}
		return nil, err
	}
	return a, nil
}

// ListAssignments returns the plans on a machine.
func (s *ServicePlanService) ListAssignments(ctx context.Context, tx pgx.Tx, equipmentID uuid.UUID, includeEnded bool) ([]domain.EquipmentServicePlan, error) {
	return s.plans.ListAssignments(ctx, tx, equipmentID, includeEnded)
}

// --- Occurrences ---

// CompleteDueParams records that a scheduled piece of maintenance was done.
//
// No ticket field: an occurrence worked on a ticket is already attached to it,
// by the sweep when it books one or by the ticket form when a staffer opens one
// from the due list. A second way to set it would be a second way to get it
// wrong.
type CompleteDueParams struct {
	// CompletedOn is when the work actually happened, which is routinely not
	// today: a tech logs Tuesday's visit on Thursday, and the next occurrence
	// has to count from Tuesday.
	CompletedOn time.Time
	Notes       string
}

// CompleteDue marks an occurrence done and writes the next one, in the same
// transaction. The pairing is the invariant the schedule rests on: a completion
// that did not produce a successor is a machine that silently falls off the
// schedule at the moment it was last serviced.
func (s *ServicePlanService) CompleteDue(ctx context.Context, tx pgx.Tx, id uuid.UUID, p CompleteDueParams, actor Actor) (*domain.MaintenanceDue, error) {
	if p.CompletedOn.IsZero() {
		return nil, ErrMaintenanceDateRequired
	}

	return s.closeDue(ctx, tx, id, domain.MaintenanceStatusCompleted, p.CompletedOn,
		strings.TrimSpace(p.Notes), actor)
}

// SkipDue marks an occurrence deliberately not done and writes the next one on
// the original cadence — a skipped backflush does not move next month's.
func (s *ServicePlanService) SkipDue(ctx context.Context, tx pgx.Tx, id uuid.UUID, on time.Time, notes string, actor Actor) (*domain.MaintenanceDue, error) {
	if on.IsZero() {
		return nil, ErrMaintenanceDateRequired
	}
	return s.closeDue(ctx, tx, id, domain.MaintenanceStatusSkipped, on, strings.TrimSpace(notes), actor)
}

// closeDue is the shared body of CompleteDue and SkipDue: close this
// occurrence, work out when the task comes round again, write that.
func (s *ServicePlanService) closeDue(ctx context.Context, tx pgx.Tx, id uuid.UUID, status domain.MaintenanceStatus, on time.Time, notes string, actor Actor) (*domain.MaintenanceDue, error) {
	before, err := s.plans.GetDue(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrMaintenanceNotFound
		}
		return nil, err
	}
	if before.Status != domain.MaintenanceStatusPending {
		return nil, ErrMaintenanceAlreadyClosed
	}

	task, err := s.plans.GetTask(ctx, tx, before.TaskID)
	if err != nil {
		return nil, err
	}
	assignment, err := s.GetAssignment(ctx, tx, before.AssignmentID)
	if err != nil {
		return nil, err
	}

	var staffID *uuid.UUID
	if actor.Type == domain.AuditActorTypeStaff {
		staffID = actor.ID
	}

	closed, err := s.plans.CloseDue(ctx, tx, id, store.CloseDueParams{
		Status:      status,
		CompletedOn: on,
		StaffID:     staffID,
		Notes:       notes,
	})
	if err != nil {
		// The UPDATE is scoped to pending rows, so no row here means a
		// concurrent request closed it between the read above and this write.
		// Reporting that as "already closed" is both true and idempotent — the
		// caller's intent has been satisfied, just not by them.
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrMaintenanceAlreadyClosed
		}
		return nil, err
	}

	// Only a live assignment gets a successor.
	//
	// Belt and braces: ending an assignment deletes its unbooked pending rows,
	// so GetDue above already fails for those and this is unreachable through
	// that path. It is reachable for an occurrence that was booked — those
	// survive EndAssignment so the ticket is not orphaned — and there, closing
	// the work out must not restart a schedule nobody is on.
	if assignment.Live() {
		next := domain.NextDueAfterCompletion(on, task.IntervalDays)
		if status == domain.MaintenanceStatusSkipped {
			next = domain.NextDueAfterSkip(before.DueOn, on, task.IntervalDays)
		}
		successor, err := s.plans.CreateDue(ctx, tx, store.CreateMaintenanceDueParams{
			AssignmentID: before.AssignmentID,
			TaskID:       before.TaskID,
			EquipmentID:  before.EquipmentID,
			DueOn:        next,
		})
		if err != nil {
			return nil, err
		}
		// CreateDue returns nil when the pending unique index already held a
		// row. Reaching that here would mean this occurrence was closed without
		// its predecessor's successor ever being written — the one way the
		// close-writes-the-successor invariant can fail silently. It cannot
		// happen while CloseDue runs first, and asserting it is how we find out
		// if that ever stops being true.
		if successor == nil {
			return nil, fmt.Errorf("close maintenance %s: successor already existed", id)
		}
	}

	action := audit.AuditMaintenanceCompleted
	if status == domain.MaintenanceStatusSkipped {
		action = audit.AuditMaintenanceSkipped
	}
	metadata := map[string]any{
		"task":          task.Name,
		"plan":          assignment.PlanName,
		"assignment_id": assignment.ID.String(),
		"due_on":        before.DueOn.Format(time.DateOnly),
		"on":            on.Format(time.DateOnly),
	}
	if before.TicketID != nil {
		metadata["ticket_id"] = before.TicketID.String()
	}
	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       action,
		ResourceType: "equipment",
		ResourceID:   before.EquipmentID,
		Metadata:     metadata,
	}); err != nil {
		return nil, fmt.Errorf("audit maintenance closed: %w", err)
	}

	return closed, nil
}

// ListDue returns due-list rows matching the filter.
func (s *ServicePlanService) ListDue(ctx context.Context, tx pgx.Tx, f store.MaintenanceFilter) ([]domain.MaintenanceDueRow, error) {
	return s.plans.ListDue(ctx, tx, f)
}

// CountDue counts the rows a scope would return — the dashboard chip and the
// section tab badge.
func (s *ServicePlanService) CountDue(ctx context.Context, tx pgx.Tx, f store.MaintenanceFilter) (int, error) {
	return s.plans.CountDue(ctx, tx, f)
}

// AttachTicket records the ticket a due item is being handled on, so the due
// list can stop asking about work that is already booked.
//
// Fails rather than shrugging when nothing matched — a tampered or stale
// occurrence id, one already booked, or one belonging to another customer. The
// caller is inside the transaction that opened the ticket, so refusing here
// rolls that back too, which is the right outcome: a ticket claiming to cover
// maintenance it is not attached to is worse than no ticket.
func (s *ServicePlanService) AttachTicket(ctx context.Context, tx pgx.Tx, id, ticketID uuid.UUID) error {
	if err := s.plans.AttachTicket(ctx, tx, id, ticketID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrMaintenanceNotFound
		}
		return err
	}
	return nil
}
