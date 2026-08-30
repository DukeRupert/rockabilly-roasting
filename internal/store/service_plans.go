package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
)

// ServicePlanStore persists preventive maintenance: the reusable plans, the
// tasks in them, the assignments onto machines, and the dated occurrences those
// assignments generate.
//
// Part of the equipment service module; see docs/equipment-service-module.md.
type ServicePlanStore struct{}

// NewServicePlanStore creates a new ServicePlanStore.
func NewServicePlanStore() *ServicePlanStore { return &ServicePlanStore{} }

// --- Plans ---

const servicePlanColumns = `id, name, description, category, active, created_at, updated_at`

func scanServicePlan(row rowScanner) (*domain.ServicePlan, error) {
	var p domain.ServicePlan
	var category *string
	if err := row.Scan(&p.ID, &p.Name, &p.Description, &category, &p.Active,
		&p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, err
	}
	if category != nil {
		p.Category = domain.EquipmentCategory(*category)
	}
	return &p, nil
}

// CreateServicePlanParams is the input for defining a maintenance plan.
type CreateServicePlanParams struct {
	Name        string
	Description string
	// Category is "" for a plan that suits any machine.
	Category domain.EquipmentCategory
}

// Create defines a plan. It starts active — a plan is written because it is
// about to be used.
func (s *ServicePlanStore) Create(ctx context.Context, tx pgx.Tx, p CreateServicePlanParams) (*domain.ServicePlan, error) {
	row := tx.QueryRow(ctx,
		`INSERT INTO service_plans (name, description, category)
		 VALUES ($1, $2, $3)
		 RETURNING `+servicePlanColumns,
		p.Name, p.Description, nullableCategory(p.Category))

	plan, err := scanServicePlan(row)
	if err != nil {
		return nil, fmt.Errorf("create service plan: %w", err)
	}
	return plan, nil
}

// nullableCategory maps the empty "any machine" category to SQL NULL.
func nullableCategory(c domain.EquipmentCategory) *string {
	if c == "" {
		return nil
	}
	v := string(c)
	return &v
}

// GetPlan returns one plan. Plans are shop-wide, so there is no scoped variant.
func (s *ServicePlanStore) GetPlan(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.ServicePlan, error) {
	row := tx.QueryRow(ctx, `SELECT `+servicePlanColumns+` FROM service_plans WHERE id = $1`, id)
	plan, err := scanServicePlan(row)
	if err != nil {
		return nil, err
	}
	return plan, nil
}

// ServicePlanFilter narrows the plan list.
type ServicePlanFilter struct {
	// ActiveOnly hides retired plans — what the assignment picker wants, and
	// not what the management page wants.
	ActiveOnly bool
	// Category "" means every plan. A category filter also returns the
	// any-machine plans, because those genuinely do apply.
	Category domain.EquipmentCategory
}

// ListPlans returns plans by name.
func (s *ServicePlanStore) ListPlans(ctx context.Context, tx pgx.Tx, f ServicePlanFilter) ([]domain.ServicePlan, error) {
	var where []string
	var args []any

	if f.ActiveOnly {
		where = append(where, "active")
	}
	if f.Category != "" {
		args = append(args, string(f.Category))
		where = append(where, fmt.Sprintf("(category IS NULL OR category = $%d)", len(args)))
	}

	query := `SELECT ` + servicePlanColumns + ` FROM service_plans`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY name"

	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list service plans: %w", err)
	}
	defer rows.Close()

	var out []domain.ServicePlan
	for rows.Next() {
		p, err := scanServicePlan(rows)
		if err != nil {
			return nil, fmt.Errorf("scan service plan: %w", err)
		}
		out = append(out, *p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list service plans: %w", err)
	}
	return out, nil
}

// UpdateServicePlanParams is the editable half of a plan.
type UpdateServicePlanParams struct {
	Name        string
	Description string
	Category    domain.EquipmentCategory
	Active      bool
}

// UpdatePlan rewrites a plan's details.
func (s *ServicePlanStore) UpdatePlan(ctx context.Context, tx pgx.Tx, id uuid.UUID, p UpdateServicePlanParams) (*domain.ServicePlan, error) {
	row := tx.QueryRow(ctx,
		`UPDATE service_plans
		 SET name = $2, description = $3, category = $4, active = $5, updated_at = now()
		 WHERE id = $1
		 RETURNING `+servicePlanColumns,
		id, p.Name, p.Description, nullableCategory(p.Category), p.Active)

	plan, err := scanServicePlan(row)
	if err != nil {
		return nil, err
	}
	return plan, nil
}

// DeletePlan removes a plan. The FK from equipment_service_plans is RESTRICT,
// so this fails rather than orphaning machines — the caller checks first and
// offers deactivation instead.
func (s *ServicePlanStore) DeletePlan(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	_, err := tx.Exec(ctx, `DELETE FROM service_plans WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete service plan: %w", err)
	}
	return nil
}

// CountAssignments is how many machines are on a plan, live or ended. Used to
// decide whether a plan can be deleted and to label the list row.
func (s *ServicePlanStore) CountAssignments(ctx context.Context, tx pgx.Tx, planID uuid.UUID) (live, total int, err error) {
	err = tx.QueryRow(ctx,
		`SELECT count(*) FILTER (WHERE ended_at IS NULL), count(*)
		 FROM equipment_service_plans WHERE plan_id = $1`, planID).Scan(&live, &total)
	if err != nil {
		return 0, 0, fmt.Errorf("count plan assignments: %w", err)
	}
	return live, total, nil
}

// CountAssignmentsByPlan is the same count for every plan at once, so the plan
// list does not run a query per row.
func (s *ServicePlanStore) CountAssignmentsByPlan(ctx context.Context, tx pgx.Tx) (map[uuid.UUID]int, error) {
	rows, err := tx.Query(ctx,
		`SELECT plan_id, count(*) FROM equipment_service_plans
		 WHERE ended_at IS NULL GROUP BY plan_id`)
	if err != nil {
		return nil, fmt.Errorf("count assignments by plan: %w", err)
	}
	defer rows.Close()

	out := make(map[uuid.UUID]int)
	for rows.Next() {
		var id uuid.UUID
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, fmt.Errorf("scan assignment count: %w", err)
		}
		out[id] = n
	}
	return out, rows.Err()
}

// --- Tasks ---

const servicePlanTaskColumns = `id, plan_id, name, instructions, interval_days, lead_days,
	                        warranty_required, sort_order, created_at, updated_at`

func scanServicePlanTask(row rowScanner) (*domain.ServicePlanTask, error) {
	var t domain.ServicePlanTask
	if err := row.Scan(&t.ID, &t.PlanID, &t.Name, &t.Instructions, &t.IntervalDays,
		&t.LeadDays, &t.WarrantyRequired, &t.SortOrder, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return nil, err
	}
	return &t, nil
}

// CreateServicePlanTaskParams is the input for adding an item to a series.
type CreateServicePlanTaskParams struct {
	PlanID           uuid.UUID
	Name             string
	Instructions     string
	IntervalDays     int
	LeadDays         int
	WarrantyRequired bool
	SortOrder        int
}

// CreateTask adds an item to a plan's series.
func (s *ServicePlanStore) CreateTask(ctx context.Context, tx pgx.Tx, p CreateServicePlanTaskParams) (*domain.ServicePlanTask, error) {
	row := tx.QueryRow(ctx,
		`INSERT INTO service_plan_tasks
		     (plan_id, name, instructions, interval_days, lead_days, warranty_required, sort_order)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING `+servicePlanTaskColumns,
		p.PlanID, p.Name, p.Instructions, p.IntervalDays, p.LeadDays, p.WarrantyRequired, p.SortOrder)

	task, err := scanServicePlanTask(row)
	if err != nil {
		return nil, fmt.Errorf("create service plan task: %w", err)
	}
	return task, nil
}

// GetTask returns one task.
func (s *ServicePlanStore) GetTask(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.ServicePlanTask, error) {
	row := tx.QueryRow(ctx, `SELECT `+servicePlanTaskColumns+` FROM service_plan_tasks WHERE id = $1`, id)
	return scanServicePlanTask(row)
}

// ListTasks returns a plan's series in the order it should be worked.
func (s *ServicePlanStore) ListTasks(ctx context.Context, tx pgx.Tx, planID uuid.UUID) ([]domain.ServicePlanTask, error) {
	rows, err := tx.Query(ctx,
		`SELECT `+servicePlanTaskColumns+` FROM service_plan_tasks
		 WHERE plan_id = $1 ORDER BY sort_order, created_at`, planID)
	if err != nil {
		return nil, fmt.Errorf("list plan tasks: %w", err)
	}
	defer rows.Close()

	var out []domain.ServicePlanTask
	for rows.Next() {
		t, err := scanServicePlanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("scan plan task: %w", err)
		}
		out = append(out, *t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list plan tasks: %w", err)
	}
	return out, nil
}

// UpdateServicePlanTaskParams is the editable half of a task.
type UpdateServicePlanTaskParams struct {
	Name             string
	Instructions     string
	IntervalDays     int
	LeadDays         int
	WarrantyRequired bool
	SortOrder        int
}

// UpdateTask rewrites a task.
func (s *ServicePlanStore) UpdateTask(ctx context.Context, tx pgx.Tx, id uuid.UUID, p UpdateServicePlanTaskParams) (*domain.ServicePlanTask, error) {
	row := tx.QueryRow(ctx,
		`UPDATE service_plan_tasks
		 SET name = $2, instructions = $3, interval_days = $4, lead_days = $5,
		     warranty_required = $6, sort_order = $7, updated_at = now()
		 WHERE id = $1
		 RETURNING `+servicePlanTaskColumns,
		id, p.Name, p.Instructions, p.IntervalDays, p.LeadDays, p.WarrantyRequired, p.SortOrder)

	return scanServicePlanTask(row)
}

// DeleteTask removes a task from a plan. The occurrences cascade with it: an
// item that is no longer part of the schedule should not still be due, and the
// completed ones described work under a task that no longer exists.
func (s *ServicePlanStore) DeleteTask(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	if _, err := tx.Exec(ctx, `DELETE FROM service_plan_tasks WHERE id = $1`, id); err != nil {
		return fmt.Errorf("delete plan task: %w", err)
	}
	return nil
}

// ListPendingForTask returns every pending occurrence of one task, across all
// the machines on the plan it belongs to.
//
// Read-then-write rather than one UPDATE, because moving an occurrence onto a
// new interval has a clamp in it (domain.RescheduledDue) that SQL would express
// far less legibly than Go — and getting it wrong books a visit somebody
// declined.
func (s *ServicePlanStore) ListPendingForTask(ctx context.Context, tx pgx.Tx, taskID uuid.UUID) ([]domain.MaintenanceDue, error) {
	rows, err := tx.Query(ctx,
		`SELECT `+strings.ReplaceAll(maintenanceDueColumns, "d.", "")+`
		 FROM service_maintenance_due
		 WHERE task_id = $1 AND status = 'pending'`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list pending for task: %w", err)
	}
	defer rows.Close()

	var out []domain.MaintenanceDue
	for rows.Next() {
		d, scanErr := scanMaintenanceDue(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan pending for task: %w", scanErr)
		}
		out = append(out, *d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list pending for task: %w", err)
	}
	return out, nil
}

// UpdateDueOn moves one pending occurrence to a new date.
func (s *ServicePlanStore) UpdateDueOn(ctx context.Context, tx pgx.Tx, id uuid.UUID, dueOn time.Time) error {
	if _, err := tx.Exec(ctx,
		`UPDATE service_maintenance_due SET due_on = $2, updated_at = now()
		 WHERE id = $1 AND status = 'pending'`, id, dueOn); err != nil {
		return fmt.Errorf("update maintenance due date: %w", err)
	}
	return nil
}

// --- Assignments ---

const assignmentColumns = `a.id, a.equipment_id, a.plan_id, a.starts_on, a.under_contract,
	                   a.contract_ends_on, a.ended_at, a.notes, a.created_at, a.updated_at`

func scanAssignment(row rowScanner) (*domain.EquipmentServicePlan, error) {
	var a domain.EquipmentServicePlan
	if err := row.Scan(&a.ID, &a.EquipmentID, &a.PlanID, &a.StartsOn, &a.UnderContract,
		&a.ContractEndsOn, &a.EndedAt, &a.Notes, &a.CreatedAt, &a.UpdatedAt, &a.PlanName); err != nil {
		return nil, err
	}
	return &a, nil
}

// AssignPlanParams is the input for putting a plan on a machine.
type AssignPlanParams struct {
	EquipmentID    uuid.UUID
	PlanID         uuid.UUID
	StartsOn       time.Time
	UnderContract  bool
	ContractEndsOn *time.Time
	Notes          string
}

// Assign puts a plan on a machine. Generating the first occurrences is the
// service's job, not this one's — it needs the plan's tasks, and this layer
// does not decide policy.
func (s *ServicePlanStore) Assign(ctx context.Context, tx pgx.Tx, p AssignPlanParams) (*domain.EquipmentServicePlan, error) {
	row := tx.QueryRow(ctx,
		`WITH ins AS (
		     INSERT INTO equipment_service_plans
		         (equipment_id, plan_id, starts_on, under_contract, contract_ends_on, notes)
		     VALUES ($1, $2, $3, $4, $5, $6)
		     RETURNING *
		 )
		 SELECT `+assignmentColumns+`, pl.name
		 FROM ins a JOIN service_plans pl ON pl.id = a.plan_id`,
		p.EquipmentID, p.PlanID, p.StartsOn, p.UnderContract, p.ContractEndsOn, p.Notes)

	a, err := scanAssignment(row)
	if err != nil {
		return nil, fmt.Errorf("assign service plan: %w", err)
	}
	return a, nil
}

// GetAssignment returns one assignment with its plan name.
func (s *ServicePlanStore) GetAssignment(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.EquipmentServicePlan, error) {
	row := tx.QueryRow(ctx,
		`SELECT `+assignmentColumns+`, pl.name
		 FROM equipment_service_plans a
		 JOIN service_plans pl ON pl.id = a.plan_id
		 WHERE a.id = $1`, id)
	return scanAssignment(row)
}

// ListAssignments returns the plans on a machine, live ones first.
func (s *ServicePlanStore) ListAssignments(ctx context.Context, tx pgx.Tx, equipmentID uuid.UUID, includeEnded bool) ([]domain.EquipmentServicePlan, error) {
	query := `SELECT ` + assignmentColumns + `, pl.name
	          FROM equipment_service_plans a
	          JOIN service_plans pl ON pl.id = a.plan_id
	          WHERE a.equipment_id = $1`
	if !includeEnded {
		query += ` AND a.ended_at IS NULL`
	}
	query += ` ORDER BY a.ended_at NULLS FIRST, pl.name`

	rows, err := tx.Query(ctx, query, equipmentID)
	if err != nil {
		return nil, fmt.Errorf("list plan assignments: %w", err)
	}
	defer rows.Close()

	var out []domain.EquipmentServicePlan
	for rows.Next() {
		a, err := scanAssignment(rows)
		if err != nil {
			return nil, fmt.Errorf("scan plan assignment: %w", err)
		}
		out = append(out, *a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list plan assignments: %w", err)
	}
	return out, nil
}

// UpdateAssignmentParams is the editable half of an assignment. The plan and
// the machine are not in it: changing either is a different assignment.
type UpdateAssignmentParams struct {
	StartsOn       time.Time
	UnderContract  bool
	ContractEndsOn *time.Time
	Notes          string
}

// UpdateAssignment rewrites an assignment's terms.
func (s *ServicePlanStore) UpdateAssignment(ctx context.Context, tx pgx.Tx, id uuid.UUID, p UpdateAssignmentParams) (*domain.EquipmentServicePlan, error) {
	row := tx.QueryRow(ctx,
		`WITH upd AS (
		     UPDATE equipment_service_plans
		     SET starts_on = $2, under_contract = $3, contract_ends_on = $4,
		         notes = $5, updated_at = now()
		     WHERE id = $1
		     RETURNING *
		 )
		 SELECT `+assignmentColumns+`, pl.name
		 FROM upd a JOIN service_plans pl ON pl.id = a.plan_id`,
		id, p.StartsOn, p.UnderContract, p.ContractEndsOn, p.Notes)

	return scanAssignment(row)
}

// EndAssignment stops an assignment generating maintenance and clears whatever
// it still had pending. The completed history stays: it is the record of what
// was done to the machine, and it outlives the arrangement that produced it.
func (s *ServicePlanStore) EndAssignment(ctx context.Context, tx pgx.Tx, id uuid.UUID, at time.Time) error {
	if _, err := tx.Exec(ctx,
		`UPDATE equipment_service_plans SET ended_at = $2, updated_at = now()
		 WHERE id = $1 AND ended_at IS NULL`, id, at); err != nil {
		return fmt.Errorf("end plan assignment: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM service_maintenance_due WHERE assignment_id = $1 AND status = 'pending'`,
		id); err != nil {
		return fmt.Errorf("clear pending maintenance: %w", err)
	}
	return nil
}

// --- Occurrences ---

const maintenanceDueColumns = `d.id, d.assignment_id, d.task_id, d.equipment_id, d.due_on, d.status,
	                      d.completed_on, d.completed_by_staff_id, d.ticket_id, d.notes,
	                      d.created_at, d.updated_at`

func scanMaintenanceDue(row rowScanner) (*domain.MaintenanceDue, error) {
	var d domain.MaintenanceDue
	var status string
	if err := row.Scan(&d.ID, &d.AssignmentID, &d.TaskID, &d.EquipmentID, &d.DueOn, &status,
		&d.CompletedOn, &d.CompletedByStaffID, &d.TicketID, &d.Notes,
		&d.CreatedAt, &d.UpdatedAt); err != nil {
		return nil, err
	}
	d.Status = domain.MaintenanceStatus(status)
	return &d, nil
}

// CreateMaintenanceDueParams is the input for writing one occurrence.
type CreateMaintenanceDueParams struct {
	AssignmentID uuid.UUID
	TaskID       uuid.UUID
	EquipmentID  uuid.UUID
	DueOn        time.Time
}

// CreateDue writes a pending occurrence, or does nothing if one already exists
// for that task on that assignment.
//
// The ON CONFLICT is what makes the daily sweep and the assignment path safe to
// run over the same data: both want "there should be a pending row here", and
// neither should care which of them got there first. Returns nil when the row
// already existed.
func (s *ServicePlanStore) CreateDue(ctx context.Context, tx pgx.Tx, p CreateMaintenanceDueParams) (*domain.MaintenanceDue, error) {
	rows, err := tx.Query(ctx,
		`INSERT INTO service_maintenance_due (assignment_id, task_id, equipment_id, due_on)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT DO NOTHING
		 RETURNING `+strings.ReplaceAll(maintenanceDueColumns, "d.", ""),
		p.AssignmentID, p.TaskID, p.EquipmentID, p.DueOn)
	if err != nil {
		return nil, fmt.Errorf("create maintenance due: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("create maintenance due: %w", err)
		}
		return nil, nil
	}
	d, err := scanMaintenanceDue(rows)
	if err != nil {
		return nil, fmt.Errorf("scan maintenance due: %w", err)
	}
	return d, nil
}

// GetDue returns one occurrence.
func (s *ServicePlanStore) GetDue(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.MaintenanceDue, error) {
	row := tx.QueryRow(ctx,
		`SELECT `+maintenanceDueColumns+` FROM service_maintenance_due d WHERE d.id = $1`, id)
	return scanMaintenanceDue(row)
}

// CloseDueParams closes an occurrence, either done or skipped.
type CloseDueParams struct {
	Status      domain.MaintenanceStatus
	CompletedOn time.Time
	StaffID     *uuid.UUID
	TicketID    *uuid.UUID
	Notes       string
}

// CloseDue marks an occurrence completed or skipped. Scoped to `status =
// 'pending'` so a double submit closes it once: the second call finds no row
// and returns pgx.ErrNoRows, which the service reads as "already done" rather
// than writing a second follow-on occurrence.
func (s *ServicePlanStore) CloseDue(ctx context.Context, tx pgx.Tx, id uuid.UUID, p CloseDueParams) (*domain.MaintenanceDue, error) {
	row := tx.QueryRow(ctx,
		`UPDATE service_maintenance_due
		 SET status = $2, completed_on = $3, completed_by_staff_id = $4,
		     ticket_id = COALESCE($5, ticket_id), notes = $6, updated_at = now()
		 WHERE id = $1 AND status = 'pending'
		 RETURNING `+strings.ReplaceAll(maintenanceDueColumns, "d.", ""),
		id, string(p.Status), p.CompletedOn, p.StaffID, p.TicketID, p.Notes)

	return scanMaintenanceDue(row)
}

// AttachTicket records that an occurrence has a ticket against it. Separate
// from closing it: booking the visit and doing the work are different days.
//
// Scoped to the ticket's own customer. The occurrence id arrives from a form
// field on the open-a-ticket page, and without this one cafe's ticket could
// mark another's maintenance booked — which there is no screen to undo.
//
// Reports pgx.ErrNoRows when nothing matched, rather than succeeding silently:
// the caller opened a ticket on the strength of this working, and an occurrence
// left unattached is one the sweep books again tomorrow.
func (s *ServicePlanStore) AttachTicket(ctx context.Context, tx pgx.Tx, id, ticketID uuid.UUID) error {
	tag, err := tx.Exec(ctx,
		`UPDATE service_maintenance_due d
		    SET ticket_id = $2, updated_at = now()
		  FROM equipment e, service_tickets t
		 WHERE d.id = $1
		   AND d.ticket_id IS NULL
		   AND e.id = d.equipment_id
		   AND t.id = $2
		   AND t.customer_id = e.customer_id`, id, ticketID)
	if err != nil {
		return fmt.Errorf("attach maintenance ticket: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// MaintenanceScope is which slice of the schedule a due list wants.
type MaintenanceScope string

const (
	// MaintenanceScopeAll — every pending occurrence, soonest first.
	MaintenanceScopeAll MaintenanceScope = ""
	// MaintenanceScopeOverdue — past its due date.
	MaintenanceScopeOverdue MaintenanceScope = "overdue"
	// MaintenanceScopeDueSoon — inside its own lead window, not yet late.
	MaintenanceScopeDueSoon MaintenanceScope = "due_soon"
	// MaintenanceScopeWarranty — overdue, warranty-required, on a machine whose
	// warranty has not yet expired. The rows worth interrupting somebody about.
	MaintenanceScopeWarranty MaintenanceScope = "warranty"
	// MaintenanceScopeUncovered — due or overdue on a machine nobody is paying
	// maintenance for. The call list.
	MaintenanceScopeUncovered MaintenanceScope = "uncovered"
	// MaintenanceScopeBookable — covered work inside its lead window with no
	// ticket against it yet. What the daily sweep opens routine tickets for.
	MaintenanceScopeBookable MaintenanceScope = "bookable"
	// MaintenanceScopeHistory — what has been done and skipped.
	MaintenanceScopeHistory MaintenanceScope = "history"
)

// MaintenanceFilter narrows the due list.
type MaintenanceFilter struct {
	Scope       MaintenanceScope
	CustomerID  *uuid.UUID
	EquipmentID *uuid.UUID
	PlanID      *uuid.UUID
	// From and To bound due_on. The calendar sets both to a month; the due list
	// leaves them zero.
	From time.Time
	To   time.Time
	// Now is the day the scopes are measured against. Passed in rather than
	// taken from the clock so a test can ask what the list looked like on a
	// given morning.
	Now   time.Time
	Limit int
}

// The scope predicates. `d.due_on < $now` and friends are written against a
// bound `now::date` so the whole list is classified on one day — a query that
// called now() per row could, at midnight, disagree with itself.
func maintenanceWhere(f MaintenanceFilter) (string, []any) {
	var where []string
	var args []any

	add := func(clause string, arg any) {
		args = append(args, arg)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}

	// The day every scope is measured against, bound once and referenced as a
	// date. Appended lazily: a scope that never mentions it would leave an
	// unused parameter in the statement, which Postgres rejects outright
	// because it cannot infer a type for it.
	now := f.Now
	if now.IsZero() {
		now = time.Now()
	}
	nowIdx := 0
	nowArg := func() string {
		if nowIdx == 0 {
			args = append(args, now)
			nowIdx = len(args)
		}
		return fmt.Sprintf("$%d::date", nowIdx)
	}

	switch f.Scope {
	case MaintenanceScopeHistory:
		where = append(where, "d.status <> 'pending'")
	case MaintenanceScopeOverdue:
		where = append(where, "d.status = 'pending'", "d.due_on < "+nowArg())
	case MaintenanceScopeDueSoon:
		where = append(where, "d.status = 'pending'",
			"d.due_on >= "+nowArg(),
			"d.due_on <= "+nowArg()+" + (t.lead_days * INTERVAL '1 day')")
	case MaintenanceScopeWarranty:
		where = append(where, "d.status = 'pending'", "t.warranty_required",
			"d.due_on < "+nowArg(),
			"e.warranty_expires_on IS NOT NULL",
			"e.warranty_expires_on >= "+nowArg())
	case MaintenanceScopeBookable:
		where = append(where, "d.status = 'pending'", "d.ticket_id IS NULL",
			"d.due_on <= "+nowArg()+" + (t.lead_days * INTERVAL '1 day')",
			"a.under_contract",
			"(a.contract_ends_on IS NULL OR a.contract_ends_on >= "+nowArg()+")")
	case MaintenanceScopeUncovered:
		where = append(where, "d.status = 'pending'",
			"d.due_on <= "+nowArg()+" + (t.lead_days * INTERVAL '1 day')",
			"(NOT a.under_contract OR (a.contract_ends_on IS NOT NULL AND a.contract_ends_on < "+nowArg()+"))")
	default:
		where = append(where, "d.status = 'pending'")
	}

	if f.CustomerID != nil {
		add("e.customer_id = $%d", *f.CustomerID)
	}
	if f.EquipmentID != nil {
		add("d.equipment_id = $%d", *f.EquipmentID)
	}
	if f.PlanID != nil {
		add("a.plan_id = $%d", *f.PlanID)
	}
	if !f.From.IsZero() {
		add("d.due_on >= $%d", f.From)
	}
	if !f.To.IsZero() {
		add("d.due_on <= $%d", f.To)
	}

	// Retired machines drop off the *pending* scopes: their schedule is over,
	// and a due list that nags about a machine in a skip is one staff learn to
	// ignore. History is the opposite case — 077 keeps retired machines
	// precisely so the record of what was done to them survives, and hiding it
	// would delete the argument for having replaced the thing.
	if f.Scope != MaintenanceScopeHistory {
		where = append(where, "e.status <> 'retired'")
	}

	return " WHERE " + strings.Join(where, " AND "), args
}

const maintenanceRowColumns = maintenanceDueColumns + `,
	t.name, t.instructions, t.interval_days, t.lead_days, t.warranty_required,
	a.plan_id, pl.name,
	a.under_contract, a.contract_ends_on,
	e.customer_id, COALESCE(NULLIF(c.company_name, ''), TRIM(c.first_name || ' ' || c.last_name), c.email),
	e.make, e.model, e.serial_number, e.status, e.warranty_expires_on, e.address_id`

const maintenanceRowFrom = ` FROM service_maintenance_due d
	JOIN service_plan_tasks t ON t.id = d.task_id
	JOIN equipment_service_plans a ON a.id = d.assignment_id
	JOIN service_plans pl ON pl.id = a.plan_id
	JOIN equipment e ON e.id = d.equipment_id
	JOIN customers c ON c.id = e.customer_id`

func scanMaintenanceRow(row rowScanner) (*domain.MaintenanceDueRow, error) {
	var r domain.MaintenanceDueRow
	var status, equipmentStatus string
	if err := row.Scan(
		&r.ID, &r.AssignmentID, &r.TaskID, &r.EquipmentID, &r.DueOn, &status,
		&r.CompletedOn, &r.CompletedByStaffID, &r.TicketID, &r.Notes, &r.CreatedAt, &r.UpdatedAt,
		&r.TaskName, &r.TaskInstructions, &r.IntervalDays, &r.LeadDays, &r.WarrantyRequired,
		&r.PlanID, &r.PlanName,
		&r.UnderContract, &r.ContractEndsOn,
		&r.CustomerID, &r.CustomerName,
		&r.EquipmentMake, &r.EquipmentModel, &r.EquipmentSerial, &equipmentStatus,
		&r.EquipmentWarrantyEnds, &r.AddressID,
	); err != nil {
		return nil, err
	}
	r.Status = domain.MaintenanceStatus(status)
	r.EquipmentStatus = domain.EquipmentStatus(equipmentStatus)
	return &r, nil
}

// ListDue returns due-list rows matching the filter, soonest first. Pending
// scopes sort by due date ascending — the thing most overdue is the thing to do
// first — and history sorts newest first.
func (s *ServicePlanStore) ListDue(ctx context.Context, tx pgx.Tx, f MaintenanceFilter) ([]domain.MaintenanceDueRow, error) {
	whereSQL, args := maintenanceWhere(f)

	query := `SELECT ` + maintenanceRowColumns + maintenanceRowFrom + whereSQL
	if f.Scope == MaintenanceScopeHistory {
		query += ` ORDER BY COALESCE(d.completed_on, d.due_on) DESC, d.created_at DESC`
	} else {
		query += ` ORDER BY d.due_on, c.company_name, c.last_name`
	}
	if f.Limit > 0 {
		args = append(args, f.Limit)
		query += fmt.Sprintf(" LIMIT $%d", len(args))
	}

	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list maintenance due: %w", err)
	}
	defer rows.Close()

	var out []domain.MaintenanceDueRow
	for rows.Next() {
		r, err := scanMaintenanceRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan maintenance due: %w", err)
		}
		out = append(out, *r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list maintenance due: %w", err)
	}
	return out, nil
}

// CountDue counts the rows a scope would return. The dashboard chip and the
// section tab want the number without the rows.
func (s *ServicePlanStore) CountDue(ctx context.Context, tx pgx.Tx, f MaintenanceFilter) (int, error) {
	whereSQL, args := maintenanceWhere(f)

	var n int
	err := tx.QueryRow(ctx, `SELECT count(*)`+maintenanceRowFrom+whereSQL, args...).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count maintenance due: %w", err)
	}
	return n, nil
}

// MissingDueRow is a live (assignment, task) pair with nothing pending against
// it — a task added to a plan after machines were already on it, or an
// occurrence lost to an ended-then-restarted arrangement.
type MissingDueRow struct {
	AssignmentID uuid.UUID
	TaskID       uuid.UUID
	EquipmentID  uuid.UUID
	IntervalDays int
	// Anchor is the day to count the interval forward from: the last completion
	// of this task on this machine, or the assignment's start date where there
	// has not been one.
	Anchor time.Time
}

// ListMissingDue finds live (assignment, task) pairs with no pending
// occurrence. The daily sweep uses it to backfill, which is what makes "add a
// task to a plan" reach the forty machines already on that plan.
func (s *ServicePlanStore) ListMissingDue(ctx context.Context, tx pgx.Tx) ([]MissingDueRow, error) {
	rows, err := tx.Query(ctx,
		`SELECT a.id, t.id, a.equipment_id, t.interval_days,
		        COALESCE(
		            (SELECT max(prev.completed_on) FROM service_maintenance_due prev
		             WHERE prev.assignment_id = a.id AND prev.task_id = t.id
		               AND prev.status = 'completed'),
		            a.starts_on
		        )
		 FROM equipment_service_plans a
		 JOIN service_plan_tasks t ON t.plan_id = a.plan_id
		 JOIN equipment e ON e.id = a.equipment_id
		 WHERE a.ended_at IS NULL
		   AND e.status <> 'retired'
		   AND NOT EXISTS (
		       SELECT 1 FROM service_maintenance_due d
		       WHERE d.assignment_id = a.id AND d.task_id = t.id AND d.status = 'pending'
		   )`)
	if err != nil {
		return nil, fmt.Errorf("list missing maintenance: %w", err)
	}
	defer rows.Close()

	var out []MissingDueRow
	for rows.Next() {
		var m MissingDueRow
		if err := rows.Scan(&m.AssignmentID, &m.TaskID, &m.EquipmentID, &m.IntervalDays, &m.Anchor); err != nil {
			return nil, fmt.Errorf("scan missing maintenance: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list missing maintenance: %w", err)
	}
	return out, nil
}
