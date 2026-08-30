package domain

import (
	"strconv"
	"time"

	"github.com/google/uuid"
)

// Preventive maintenance for the equipment service module: a named series of
// tasks (a *plan*) assigned to a machine from an anchor date, which generates
// dated occurrences that come due, get done, and re-anchor.
//
// See docs/equipment-service-module.md. Nothing here talks to the module
// toggle: that is decided once, at the router.

// ServicePlan is a reusable maintenance series — usually a manufacturer's
// warranty schedule, occasionally a shop's own house standard.
type ServicePlan struct {
	ID          uuid.UUID
	Name        string
	Description string
	// Category is the kind of machine the plan is written for, or "" for any.
	// Advisory: it sorts the assignment picker, it does not forbid the odd case.
	Category EquipmentCategory
	// Active false hides the plan from the picker without disturbing the
	// machines already on it.
	Active    bool
	CreatedAt time.Time
	UpdatedAt time.Time

	// Tasks is populated by the loaders that need the whole series — the plan
	// page and the assignment path. Empty on list rows.
	Tasks []ServicePlanTask
}

// TaskCount is how many items are in the series, for a list row that has been
// loaded with its tasks.
func (p ServicePlan) TaskCount() int { return len(p.Tasks) }

// ServicePlanTask is one item in the series: what gets done, and how often.
type ServicePlanTask struct {
	ID     uuid.UUID
	PlanID uuid.UUID
	Name   string
	// Instructions is what the tech actually does. It is copied into the
	// description of any ticket opened for this task, so the person on site has
	// the procedure without opening the plan.
	Instructions string
	// IntervalDays is the gap between occurrences.
	IntervalDays int
	// LeadDays is how far ahead of the due date the task starts asking for
	// attention. A yearly full service needs weeks of notice to get booked; a
	// monthly backflush needs days.
	LeadDays int
	// WarrantyRequired marks the tasks that keep a manufacturer's warranty
	// alive. An overdue one on a machine still inside its warranty is the
	// loudest row on the due list.
	WarrantyRequired bool
	SortOrder        int
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// IntervalLabel is the interval in the words a person would use — "every 30
// days" reads worse than "every month" for the common values, and worse than
// nothing at all for the odd ones.
func (t ServicePlanTask) IntervalLabel() string {
	switch t.IntervalDays {
	case 1:
		return "Daily"
	case 7:
		return "Weekly"
	case 14:
		return "Every 2 weeks"
	case 30, 31:
		return "Monthly"
	case 60:
		return "Every 2 months"
	case 90, 91:
		return "Quarterly"
	case 180, 182:
		return "Every 6 months"
	case 365:
		return "Yearly"
	case 730:
		return "Every 2 years"
	}
	return "Every " + plural(t.IntervalDays, "day")
}

// plural renders "1 day" / "30 days".
func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return strconv.Itoa(n) + " " + unit + "s"
}

// EquipmentServicePlan is a plan put on a machine from a date.
type EquipmentServicePlan struct {
	ID          uuid.UUID
	EquipmentID uuid.UUID
	PlanID      uuid.UUID
	// StartsOn anchors the schedule: every task's first occurrence counts its
	// interval forward from this day. Staff set it to the install date or to
	// the last known full service, which is what puts a mid-life machine on a
	// believable schedule instead of making everything due at once.
	StartsOn time.Time
	// UnderContract is whether the customer pays for this maintenance. It
	// decides what a due task does — book itself as a ticket, or land on the
	// call list.
	UnderContract bool
	// ContractEndsOn is when the contract lapses, where it is a term. Past it
	// the assignment keeps generating due items; they just stop booking
	// themselves.
	ContractEndsOn *time.Time
	// EndedAt stops the assignment generating work without deleting what was
	// done under it.
	EndedAt   *time.Time
	Notes     string
	CreatedAt time.Time
	UpdatedAt time.Time

	// PlanName is carried on loaded rows so a machine's plan list can be
	// rendered without a lookup per row.
	PlanName string
}

// Live reports whether the assignment is still generating maintenance.
func (a EquipmentServicePlan) Live() bool { return a.EndedAt == nil }

// Covered reports whether the customer is paying for this maintenance on the
// given day. A contract with a past end date is not a contract — and quoting
// work as covered when the term has run out is the expensive direction of the
// mistake, so an unset ContractEndsOn means open-ended and a set one is
// honoured through the whole of its last day.
func (a EquipmentServicePlan) Covered(on time.Time) bool {
	if !a.UnderContract {
		return false
	}
	if a.ContractEndsOn == nil {
		return true
	}
	return !calendarDay(on).After(calendarDay(*a.ContractEndsOn))
}

// MaintenanceStatus is where an occurrence got to.
type MaintenanceStatus string

const (
	// MaintenanceStatusPending — waiting to be done. Exactly one per task per
	// assignment exists at a time.
	MaintenanceStatusPending MaintenanceStatus = "pending"
	// MaintenanceStatusCompleted — done. The next occurrence is anchored to the
	// day it was actually done, not to the day it was due.
	MaintenanceStatusCompleted MaintenanceStatus = "completed"
	// MaintenanceStatusSkipped — deliberately not done this cycle. The next
	// occurrence keeps the original cadence: skipping is not a reason to let
	// the whole schedule drift.
	MaintenanceStatusSkipped MaintenanceStatus = "skipped"
)

// Label is the human name for a maintenance status.
func (s MaintenanceStatus) Label() string {
	switch s {
	case MaintenanceStatusPending:
		return "Due"
	case MaintenanceStatusCompleted:
		return "Done"
	case MaintenanceStatusSkipped:
		return "Skipped"
	}
	return string(s)
}

// Valid reports whether s is a known maintenance status.
func (s MaintenanceStatus) Valid() bool {
	switch s {
	case MaintenanceStatusPending, MaintenanceStatusCompleted, MaintenanceStatusSkipped:
		return true
	}
	return false
}

// MaintenanceDue is one occurrence of one task on one machine.
type MaintenanceDue struct {
	ID           uuid.UUID
	AssignmentID uuid.UUID
	TaskID       uuid.UUID
	EquipmentID  uuid.UUID
	DueOn        time.Time
	Status       MaintenanceStatus
	CompletedOn  *time.Time
	// CompletedByStaffID is who marked it done, where a person did. Nil when
	// the row was closed by a job.
	CompletedByStaffID *uuid.UUID
	// TicketID is the ticket the work was done on, where there was one. Set
	// both by the sweep that books contract work and by a staffer closing an
	// item against a ticket they already had open.
	TicketID  *uuid.UUID
	Notes     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// MaintenanceUrgency is how loudly a pending occurrence should ask for
// attention. Derived, never stored: it changes every midnight, and a stored
// copy would be wrong by morning.
type MaintenanceUrgency string

const (
	// MaintenanceOverdue — the due date has passed.
	MaintenanceOverdue MaintenanceUrgency = "overdue"
	// MaintenanceDueSoon — inside the task's lead window.
	MaintenanceDueSoon MaintenanceUrgency = "due_soon"
	// MaintenanceUpcoming — on the schedule, not yet worth a phone call.
	MaintenanceUpcoming MaintenanceUrgency = "upcoming"
)

// Label is the human name for an urgency.
func (u MaintenanceUrgency) Label() string {
	switch u {
	case MaintenanceOverdue:
		return "Overdue"
	case MaintenanceDueSoon:
		return "Due soon"
	case MaintenanceUpcoming:
		return "Upcoming"
	}
	return string(u)
}

// MaintenanceDueRow is a due-list line: the occurrence plus everything needed
// to render it and act on it, joined once in the query rather than per row.
//
// The due list spans every customer and every machine — it is the view the
// whole feature exists to produce — so it carries the machine, the account, and
// the plan task with it.
type MaintenanceDueRow struct {
	MaintenanceDue

	// The task, as it stands now. Intervals get edited; the row shows current
	// truth rather than what was true when the occurrence was written.
	TaskName         string
	TaskInstructions string
	IntervalDays     int
	LeadDays         int
	WarrantyRequired bool

	PlanID   uuid.UUID
	PlanName string

	// UnderContract and ContractEndsOn come from the assignment. The due list
	// splits on them: contract work is booked, uncovered work is sold.
	UnderContract  bool
	ContractEndsOn *time.Time

	CustomerID   uuid.UUID
	CustomerName string

	EquipmentMake         string
	EquipmentModel        string
	EquipmentSerial       string
	EquipmentStatus       EquipmentStatus
	EquipmentWarrantyEnds *time.Time
	AddressID             *uuid.UUID
}

// MachineDescription is the machine in one line, matching Equipment.Description.
func (r MaintenanceDueRow) MachineDescription() string {
	if r.EquipmentModel == "" {
		return r.EquipmentMake
	}
	return r.EquipmentMake + " " + r.EquipmentModel
}

// IntervalLabel is the task's cadence in words, on the same terms as
// ServicePlanTask.IntervalLabel — the row carries the interval but not the task.
func (r MaintenanceDueRow) IntervalLabel() string {
	return ServicePlanTask{IntervalDays: r.IntervalDays}.IntervalLabel()
}

// Urgency classifies the row against a day. Both sides are collapsed to a
// calendar day: due_on is a date, and a task due today should read as due
// today for the whole of today rather than flipping to overdue at midnight UTC.
func (r MaintenanceDueRow) Urgency(on time.Time) MaintenanceUrgency {
	today := calendarDay(on)
	due := calendarDay(r.DueOn)
	switch {
	case due.Before(today):
		return MaintenanceOverdue
	case !due.After(today.AddDate(0, 0, r.LeadDays)):
		return MaintenanceDueSoon
	}
	return MaintenanceUpcoming
}

// DaysUntil is how many days remain until the row is due — negative once it is
// overdue. Used for the "3 days late" / "in 12 days" line on the due list.
func (r MaintenanceDueRow) DaysUntil(on time.Time) int {
	return int(calendarDay(r.DueOn).Sub(calendarDay(on)).Hours() / 24)
}

// WarrantyAtRisk is the row that justifies the whole feature: a warranty-
// required task that is overdue on a machine whose warranty has not yet run
// out. Missing it is what costs the customer their cover, and it is the one
// thing on the due list worth interrupting somebody about.
//
// A machine with no recorded warranty date is never at risk — an unknown
// warranty is not a warranty, the same rule Equipment.UnderWarranty follows.
func (r MaintenanceDueRow) WarrantyAtRisk(on time.Time) bool {
	if !r.WarrantyRequired || r.Status != MaintenanceStatusPending {
		return false
	}
	if r.Urgency(on) != MaintenanceOverdue {
		return false
	}
	if r.EquipmentWarrantyEnds == nil {
		return false
	}
	return !calendarDay(on).After(calendarDay(*r.EquipmentWarrantyEnds))
}

// Covered reports whether the customer pays for this occurrence, on the same
// terms as EquipmentServicePlan.Covered.
func (r MaintenanceDueRow) Covered(on time.Time) bool {
	if !r.UnderContract {
		return false
	}
	if r.ContractEndsOn == nil {
		return true
	}
	return !calendarDay(on).After(calendarDay(*r.ContractEndsOn))
}

// NeedsSelling is the other half of the split: work that is due or overdue on a
// machine nobody is paying maintenance for. These are the rows staff ring the
// customer about — "this is due, and it is what keeps your warranty; call us to
// get it booked."
func (r MaintenanceDueRow) NeedsSelling(on time.Time) bool {
	if r.Status != MaintenanceStatusPending || r.Covered(on) {
		return false
	}
	return r.Urgency(on) != MaintenanceUpcoming
}

// BookableOn reports whether the sweep should open a routine ticket for this
// occurrence today: covered work, inside its lead window, not already booked.
//
// Uncovered work is never booked automatically. Opening a ticket commits the
// shop to a visit the customer has not agreed to pay for, and the whole point
// of the contract flag is that those two cases are handled differently.
func (r MaintenanceDueRow) BookableOn(on time.Time) bool {
	if r.Status != MaintenanceStatusPending || r.TicketID != nil {
		return false
	}
	if r.EquipmentStatus == EquipmentStatusRetired {
		return false
	}
	if !r.Covered(on) {
		return false
	}
	return r.Urgency(on) != MaintenanceUpcoming
}

// NextDueAfterCompletion is when a task comes round again once it has been done
// on a day. Anchored to the day the work actually happened: a machine serviced
// three weeks late is not due again three weeks early.
func NextDueAfterCompletion(completedOn time.Time, intervalDays int) time.Time {
	return calendarDay(completedOn).AddDate(0, 0, intervalDays)
}

// NextDueAfterSkip is when a skipped task comes round again. Anchored to the
// date it was *due*, not to today: skipping one backflush should not shift the
// whole cadence forward, and a schedule that drifts every time somebody clicks
// skip stops matching the manufacturer's document it was copied from.
//
// The result is pushed forward whole intervals until it is in the future, so
// skipping a badly overdue item does not immediately produce another overdue
// one — which would leave the row unclearable.
func NextDueAfterSkip(dueOn, on time.Time, intervalDays int) time.Time {
	next := calendarDay(dueOn).AddDate(0, 0, intervalDays)
	today := calendarDay(on)
	// Bounded by construction: intervalDays is CHECKed > 0 in the database and
	// validated in the service, so each step moves at least a day forward.
	for !next.After(today) {
		next = next.AddDate(0, 0, intervalDays)
	}
	return next
}

// RescheduledDue is where a pending occurrence moves when its task's interval
// changes.
//
// Shifted rather than recomputed from an anchor. The occurrence's current date
// was produced by adding the old interval to *something* — a completion, a
// skipped due date, the assignment's start — so subtracting the old interval
// and adding the new one keeps it on the same cadence without having to work
// out which of those it was. Reconstructing the anchor from completions alone
// is what made a skipped occurrence jump backwards: a skip leaves no
// completed_on to find.
//
// Two rules, both about not letting an interval edit rewrite what is owed now:
//
//   - Work already owed does not move at all — overdue *or due today*. The test
//     is "is this owed now", not "is this late": due-today is MaintenanceDueSoon
//     and BookableOn returns true for it, so the sweep would book it tonight.
//     Lengthening an interval must not clear it — shifting a task due today
//     from weekly to yearly would push it a year out and take a
//     warranty-critical job off the list nobody would then think to look for.
//     The new interval governs the *next* occurrence, which is measured from
//     whenever this one is finally done.
//   - Work still in the future is shifted, then stepped forward whole intervals
//     if that lands it in the past. Past-due covered work is inside the sweep's
//     booking window, so without this a shortened interval would open a real
//     customer ticket for a visit somebody had just declined.
func RescheduledDue(currentDue time.Time, oldInterval, newInterval int, on time.Time) time.Time {
	due := calendarDay(currentDue)
	today := calendarDay(on)

	// Already owed: leave it precisely where it is. Due-today counts — it is
	// work the sweep would book tonight, so an interval edit must not defer it.
	if !due.After(today) {
		return due
	}

	shifted := due.AddDate(0, 0, newInterval-oldInterval)
	if newInterval < 1 {
		return shifted
	}
	// Bounded: newInterval is at least one day, so each step moves forward.
	for shifted.Before(today) {
		shifted = shifted.AddDate(0, 0, newInterval)
	}
	return shifted
}

// FirstDueOn is when a task first comes due on a freshly assigned plan: one
// interval after the anchor.
//
// Unlike a skip, this is *not* pushed into the future. Assigning a plan with an
// anchor of "last serviced two years ago" is exactly how a shop discovers that
// a machine is overdue, and quietly moving that date forward would hide the
// finding the assignment was made to surface.
func FirstDueOn(startsOn time.Time, intervalDays int) time.Time {
	return calendarDay(startsOn).AddDate(0, 0, intervalDays)
}
