package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/auth"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/admin"
)

// Preventive maintenance: the plans, the due list, and the calendar. Part of
// the equipment service module — every route here sits behind requireModule.
//
// See docs/equipment-service-module.md for the design.

// maintenanceListLimit caps the due list. Like the register, it is a working
// list rather than a report: the scope tabs are how you narrow it, and a shop
// with a thousand pending occurrences has a scheduling problem no page length
// will fix.
const maintenanceListLimit = 250

// merchantToday is the calendar day the due list is measured against, in the
// merchant's own zone.
//
// Which day it is matters here in a way it does not elsewhere: "overdue" flips
// at midnight, and a shop in Los Angeles reading UTC would see maintenance go
// red most of the afternoon before it actually was. Collapsed to a UTC midnight
// once the date is known, so it compares cleanly against the `date` columns
// pgx hands back the same way.
func (d *Deps) merchantToday() time.Time {
	loc := d.MerchantTZ
	if loc == nil {
		loc = time.UTC
	}
	y, m, day := time.Now().In(loc).Date()
	return time.Date(y, m, day, 0, 0, 0, 0, time.UTC)
}

// --- The due list ---

// handleAdminMaintenanceList renders what the shop owes.
func (d *Deps) handleAdminMaintenanceList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	today := d.merchantToday()

	scope := r.URL.Query().Get("scope")
	filter := store.MaintenanceFilter{
		Scope: maintenanceScope(scope),
		Now:   today,
		Limit: maintenanceListLimit,
	}
	// A hand-edited scope shows everything rather than 500. Echo back the
	// normalised value so the tab strip does not highlight a tab that is not
	// the one being shown.
	if filter.Scope == store.MaintenanceScopeAll {
		scope = ""
	}

	var rows []domain.MaintenanceDueRow
	var counts admin.MaintenanceCounts
	var nav admin.ServiceNav
	if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		rows, txErr = d.ServicePlanService.ListDue(ctx, tx, filter)
		if txErr != nil {
			return txErr
		}
		counts, txErr = d.maintenanceCounts(ctx, tx, today)
		if txErr != nil {
			return txErr
		}
		nav, txErr = d.serviceNav(ctx, tx)
		return txErr
	}); err != nil {
		Error(w, r, err)
		return
	}

	staffName, staffRole := staffNameRole(r)
	props := admin.MaintenanceDueProps{
		Rows:       rows,
		Nav:        nav,
		Scope:      scope,
		Counts:     counts,
		Today:      today,
		MerchantTZ: d.MerchantTZ,
		StaffName:  staffName,
		StaffRole:  staffRole,
		CanWrite:   staffCan(r, auth.PermWriteService),
		Flash:      settingsFlash(r),
	}

	if IsHTMX(r) {
		admin.MaintenanceDueContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.MaintenanceDue(props).Render(ctx, w) //nolint:errcheck
}

// maintenanceScope maps a query-string scope onto a store scope, dropping
// anything it does not recognise.
func maintenanceScope(raw string) store.MaintenanceScope {
	switch store.MaintenanceScope(raw) {
	case store.MaintenanceScopeOverdue:
		return store.MaintenanceScopeOverdue
	case store.MaintenanceScopeDueSoon:
		return store.MaintenanceScopeDueSoon
	case store.MaintenanceScopeWarranty:
		return store.MaintenanceScopeWarranty
	case store.MaintenanceScopeUncovered:
		return store.MaintenanceScopeUncovered
	case store.MaintenanceScopeHistory:
		return store.MaintenanceScopeHistory
	}
	return store.MaintenanceScopeAll
}

// maintenanceCounts fills the tab badges.
func (d *Deps) maintenanceCounts(ctx context.Context, tx pgx.Tx, today time.Time) (admin.MaintenanceCounts, error) {
	var counts admin.MaintenanceCounts
	for _, pair := range []struct {
		scope store.MaintenanceScope
		into  *int
	}{
		{store.MaintenanceScopeOverdue, &counts.Overdue},
		{store.MaintenanceScopeWarranty, &counts.Warranty},
		{store.MaintenanceScopeUncovered, &counts.Uncovered},
	} {
		n, err := d.ServicePlanService.CountDue(ctx, tx, store.MaintenanceFilter{Scope: pair.scope, Now: today})
		if err != nil {
			return admin.MaintenanceCounts{}, err
		}
		*pair.into = n
	}
	return counts, nil
}

// handleAdminMaintenanceComplete logs a piece of scheduled work as done.
func (d *Deps) handleAdminMaintenanceComplete(w http.ResponseWriter, r *http.Request) {
	d.closeMaintenance(w, r, false)
}

// handleAdminMaintenanceSkip records a cycle as deliberately not done.
func (d *Deps) handleAdminMaintenanceSkip(w http.ResponseWriter, r *http.Request) {
	d.closeMaintenance(w, r, true)
}

// closeMaintenance is the shared body of the two row actions. Both close an
// occurrence and both come back to the tab the staffer was on — losing their
// filter every time they log a job would make working through a list of twenty
// unbearable.
func (d *Deps) closeMaintenance(w http.ResponseWriter, r *http.Request, skip bool) {
	ctx := r.Context()
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		Error(w, r, app.ErrMaintenanceNotFound)
		return
	}
	if err := r.ParseForm(); err != nil {
		Error(w, r, err)
		return
	}

	back := maintenancePath(r.FormValue("scope"))

	on, err := parseMaintenanceDay(r.FormValue("on"), d.merchantToday())
	if err != nil {
		redirectFlashError(w, r, back, "That is not a date the shop recognises.")
		return
	}

	if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		if skip {
			_, txErr = d.ServicePlanService.SkipDue(ctx, tx, id, on, r.FormValue("notes"), staffActor(r))
			return txErr
		}
		_, txErr = d.ServicePlanService.CompleteDue(ctx, tx, id, app.CompleteDueParams{
			CompletedOn: on,
			Notes:       r.FormValue("notes"),
		}, staffActor(r))
		return txErr
	}); err != nil {
		// The double submit is the common failure and it is not really one:
		// the item is closed, which is what the staffer wanted. Say so plainly
		// instead of showing them an error page.
		if errors.Is(err, app.ErrMaintenanceAlreadyClosed) {
			redirectFlash(w, r, back, "That one was already logged.")
			return
		}
		Error(w, r, err)
		return
	}

	if skip {
		redirectFlash(w, r, back, "Skipped — the next one stays on the original schedule.")
		return
	}
	redirectFlash(w, r, back, "Logged. The next one is scheduled from that date.")
}

// maintenancePath rebuilds the due-list URL for a scope, so a redirect lands
// back on the tab the action was taken from.
func maintenancePath(scope string) string {
	if maintenanceScope(scope) == store.MaintenanceScopeAll {
		return "/admin/service/maintenance"
	}
	return "/admin/service/maintenance?scope=" + url.QueryEscape(scope)
}

// parseMaintenanceDay reads a date input, falling back to today when the field
// was left empty. A `date` is parsed in UTC to match how the column comes back.
func parseMaintenanceDay(raw string, fallback time.Time) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback, nil
	}
	return time.Parse("2006-01-02", raw)
}

// --- The calendar ---

// handleAdminMaintenanceCalendar renders a month of scheduled work.
func (d *Deps) handleAdminMaintenanceCalendar(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	today := d.merchantToday()

	month := parseCalendarMonth(r.URL.Query().Get("month"), today)
	// The grid starts on the Monday on or before the first and runs six weeks,
	// which covers every possible month layout. Leading and trailing days still
	// show their work: maintenance due on the 31st of last month does not stop
	// mattering because somebody paged forward.
	gridStart := startOfWeek(month)
	gridEnd := gridStart.AddDate(0, 0, 41)

	var rows []domain.MaintenanceDueRow
	var nav admin.ServiceNav
	if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		rows, txErr = d.ServicePlanService.ListDue(ctx, tx, store.MaintenanceFilter{
			From: gridStart,
			To:   gridEnd,
			Now:  today,
			// No limit: a month is already bounded, and a calendar that
			// silently dropped the last few days of it would be worse than
			// useless — it would look complete.
		})
		if txErr != nil {
			return txErr
		}
		nav, txErr = d.serviceNav(ctx, tx)
		return txErr
	}); err != nil {
		Error(w, r, err)
		return
	}

	staffName, staffRole := staffNameRole(r)
	props := admin.MaintenanceCalendarProps{
		Month:     month,
		Days:      buildCalendarDays(gridStart, month, rows),
		Nav:       nav,
		Today:     today,
		StaffName: staffName,
		StaffRole: staffRole,
		Flash:     settingsFlash(r),
	}

	if IsHTMX(r) {
		admin.MaintenanceCalendarContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.MaintenanceCalendar(props).Render(ctx, w) //nolint:errcheck
}

// parseCalendarMonth reads ?month=2026-09, falling back to the month today is
// in. A bad value lands on this month rather than erroring: a mistyped URL
// should show a calendar.
func parseCalendarMonth(raw string, today time.Time) time.Time {
	if raw != "" {
		if t, err := time.Parse("2006-01", raw); err == nil {
			return t.UTC()
		}
	}
	y, m, _ := today.UTC().Date()
	return time.Date(y, m, 1, 0, 0, 0, 0, time.UTC)
}

// startOfWeek walks back to the Monday on or before a day. A work calendar
// starts on the day the shop opens, not on Sunday.
func startOfWeek(day time.Time) time.Time {
	offset := (int(day.UTC().Weekday()) + 6) % 7
	return day.UTC().AddDate(0, 0, -offset)
}

// buildCalendarDays lays the rows into a six-week grid.
//
// Built here rather than in the template so it can be tested without rendering
// HTML — the off-by-one in a calendar grid is the classic bug, and it is much
// cheaper to catch in a table test than by counting cells in a screenshot.
func buildCalendarDays(gridStart, month time.Time, rows []domain.MaintenanceDueRow) []admin.MaintenanceCalendarDay {
	byDay := make(map[string][]domain.MaintenanceDueRow, len(rows))
	for _, row := range rows {
		key := row.DueOn.UTC().Format("2006-01-02")
		byDay[key] = append(byDay[key], row)
	}

	days := make([]admin.MaintenanceCalendarDay, 0, 42)
	for i := 0; i < 42; i++ {
		date := gridStart.AddDate(0, 0, i)
		days = append(days, admin.MaintenanceCalendarDay{
			Date:    date,
			InMonth: date.Month() == month.Month() && date.Year() == month.Year(),
			Rows:    byDay[date.Format("2006-01-02")],
		})
	}
	return days
}

// --- Plans ---

// handleAdminServicePlanList renders the plan index.
func (d *Deps) handleAdminServicePlanList(w http.ResponseWriter, r *http.Request) {
	props, err := d.planListProps(r)
	if err != nil {
		Error(w, r, err)
		return
	}

	if IsHTMX(r) {
		admin.ServicePlanListContent(props).Render(r.Context(), w) //nolint:errcheck
		return
	}
	admin.ServicePlanList(props).Render(r.Context(), w) //nolint:errcheck
}

// planListProps reads everything the index draws. Shared with the rejected-form
// path, so a bounced create redraws the same page rather than a thinner one.
func (d *Deps) planListProps(r *http.Request) (admin.ServicePlanListProps, error) {
	ctx := r.Context()

	var plans []domain.ServicePlan
	var machines map[uuid.UUID]int
	var nav admin.ServiceNav
	if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		plans, txErr = d.ServicePlanService.ListPlans(ctx, tx, store.ServicePlanFilter{})
		if txErr != nil {
			return txErr
		}
		// The list says how many tasks each plan holds, so each one is loaded
		// with its series. Plans are a handful of rows written by hand, not a
		// catalogue — this is not the query worth optimising.
		for i := range plans {
			full, planErr := d.ServicePlanService.GetPlanWithTasks(ctx, tx, plans[i].ID)
			if planErr != nil {
				return planErr
			}
			plans[i] = *full
		}
		machines, txErr = d.ServicePlanService.CountAssignmentsByPlan(ctx, tx)
		if txErr != nil {
			return txErr
		}
		nav, txErr = d.serviceNav(ctx, tx)
		return txErr
	}); err != nil {
		return admin.ServicePlanListProps{}, err
	}

	staffName, staffRole := staffNameRole(r)
	return admin.ServicePlanListProps{
		Plans:     plans,
		Nav:       nav,
		Machines:  machines,
		Values:    admin.ServicePlanFormValuesFrom(nil),
		StaffName: staffName,
		StaffRole: staffRole,
		CanWrite:  staffCan(r, auth.PermWriteService),
		Flash:     settingsFlash(r),
	}, nil
}

// handleAdminServicePlanCreate writes a new plan.
func (d *Deps) handleAdminServicePlanCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := r.ParseForm(); err != nil {
		Error(w, r, err)
		return
	}

	values := admin.ServicePlanFormValues{
		Name:        r.FormValue("name"),
		Description: r.FormValue("description"),
		Category:    r.FormValue("category"),
		Active:      true,
	}

	var plan *domain.ServicePlan
	if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		plan, txErr = d.ServicePlanService.CreatePlan(ctx, tx, app.CreateServicePlanParams{
			Name:        values.Name,
			Description: values.Description,
			Category:    domain.EquipmentCategory(values.Category),
		}, staffActor(r))
		return txErr
	}); err != nil {
		// Re-render in place rather than redirecting. The commonest rejection
		// is a duplicate name, and bouncing back to an empty form would throw
		// away a description somebody had just written.
		d.renderPlanList(w, r, values, err.Error())
		return
	}

	// Straight to the plan: an empty plan is not usable, and the next thing to
	// do is add the first task to it.
	redirectFlash(w, r, planPath(plan.ID), "Plan created. Add the jobs it is made of.")
}

// renderPlanList redraws the index with a rejected create form open on it.
func (d *Deps) renderPlanList(w http.ResponseWriter, r *http.Request, values admin.ServicePlanFormValues, msg string) {
	props, err := d.planListProps(r)
	if err != nil {
		Error(w, r, err)
		return
	}
	props.Values = values
	props.Error = msg
	props.ShowForm = true

	if IsHTMX(r) {
		admin.ServicePlanListContent(props).Render(r.Context(), w) //nolint:errcheck
		return
	}
	admin.ServicePlanList(props).Render(r.Context(), w) //nolint:errcheck
}

// handleAdminServicePlanShow renders one plan and its series.
func (d *Deps) handleAdminServicePlanShow(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		Error(w, r, app.ErrPlanNotFound)
		return
	}
	d.renderPlanShow(w, r, id, admin.NewPlanTaskFormValues(), "")
}

// renderPlanShow draws the plan page, optionally with a rejected task form on
// it. One path for both so a bounced submission gets the whole page back.
func (d *Deps) renderPlanShow(w http.ResponseWriter, r *http.Request, id uuid.UUID, values admin.PlanTaskFormValues, msg string) {
	ctx := r.Context()

	var plan *domain.ServicePlan
	var live, total int
	var nav admin.ServiceNav
	if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		plan, txErr = d.ServicePlanService.GetPlanWithTasks(ctx, tx, id)
		if txErr != nil {
			return txErr
		}
		live, total, txErr = d.ServicePlanService.CountAssignments(ctx, tx, id)
		if txErr != nil {
			return txErr
		}
		nav, txErr = d.serviceNav(ctx, tx)
		return txErr
	}); err != nil {
		Error(w, r, err)
		return
	}

	staffName, staffRole := staffNameRole(r)
	props := admin.ServicePlanShowProps{
		Plan:          *plan,
		Nav:           nav,
		LiveMachines:  live,
		TotalMachines: total,
		Values:        values,
		Error:         msg,
		StaffName:     staffName,
		StaffRole:     staffRole,
		CanWrite:      staffCan(r, auth.PermWriteService),
		Flash:         settingsFlash(r),
	}

	if IsHTMX(r) {
		admin.ServicePlanShowContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.ServicePlanShow(props).Render(ctx, w) //nolint:errcheck
}

// handleAdminServicePlanUpdate rewrites a plan's details.
func (d *Deps) handleAdminServicePlanUpdate(w http.ResponseWriter, r *http.Request) {
	d.updatePlan(w, r, func(p *app.EditServicePlanParams, r *http.Request) {
		p.Name = r.FormValue("name")
		p.Description = r.FormValue("description")
		p.Category = domain.EquipmentCategory(r.FormValue("category"))
	}, "Plan updated.")
}

// handleAdminServicePlanRetire takes a plan out of the assignment picker.
func (d *Deps) handleAdminServicePlanRetire(w http.ResponseWriter, r *http.Request) {
	d.updatePlan(w, r, func(p *app.EditServicePlanParams, _ *http.Request) {
		p.Active = false
	}, "Plan retired. Machines already on it carry on as normal.")
}

// handleAdminServicePlanReactivate puts it back.
func (d *Deps) handleAdminServicePlanReactivate(w http.ResponseWriter, r *http.Request) {
	d.updatePlan(w, r, func(p *app.EditServicePlanParams, _ *http.Request) {
		p.Active = true
	}, "Plan back in use.")
}

// updatePlan is the shared read-modify-write behind the three plan edits.
//
// It reads the current plan first and applies the change on top, so the retire
// and reactivate buttons do not have to carry every other field as hidden
// inputs — which is how a stale form quietly reverts somebody else's edit.
func (d *Deps) updatePlan(w http.ResponseWriter, r *http.Request, apply func(*app.EditServicePlanParams, *http.Request), msg string) {
	ctx := r.Context()
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		Error(w, r, app.ErrPlanNotFound)
		return
	}
	if err := r.ParseForm(); err != nil {
		Error(w, r, err)
		return
	}

	if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		current, txErr := d.ServicePlanService.GetPlan(ctx, tx, id)
		if txErr != nil {
			return txErr
		}
		params := app.EditServicePlanParams{
			Name:        current.Name,
			Description: current.Description,
			Category:    current.Category,
			Active:      current.Active,
		}
		apply(&params, r)

		_, txErr = d.ServicePlanService.EditPlan(ctx, tx, id, params, staffActor(r))
		return txErr
	}); err != nil {
		redirectFlashError(w, r, planPath(id), err.Error())
		return
	}

	redirectFlash(w, r, planPath(id), msg)
}

// handleAdminServicePlanDelete removes a plan nothing has ever used.
func (d *Deps) handleAdminServicePlanDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		Error(w, r, app.ErrPlanNotFound)
		return
	}

	if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.ServicePlanService.DeletePlan(ctx, tx, id, staffActor(r))
	}); err != nil {
		redirectFlashError(w, r, planPath(id), err.Error())
		return
	}

	redirectFlash(w, r, "/admin/service/plans", "Plan deleted.")
}

// handleAdminServicePlanTaskAdd adds a job to a plan's series.
func (d *Deps) handleAdminServicePlanTaskAdd(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	planID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		Error(w, r, app.ErrPlanNotFound)
		return
	}
	if err := r.ParseForm(); err != nil {
		Error(w, r, err)
		return
	}

	values := admin.PlanTaskFormValues{
		Name:             r.FormValue("name"),
		Instructions:     r.FormValue("instructions"),
		IntervalDays:     strings.TrimSpace(r.FormValue("interval_days")),
		LeadDays:         strings.TrimSpace(r.FormValue("lead_days")),
		WarrantyRequired: r.FormValue("warranty_required") == "1",
	}

	interval, err := strconv.Atoi(values.IntervalDays)
	if err != nil {
		d.renderPlanShow(w, r, planID, values, app.ErrPlanIntervalInvalid.Error())
		return
	}
	// A blank notice period is none, not a rejection: plenty of jobs are done
	// on the day and want no warning at all.
	lead := 0
	if values.LeadDays != "" {
		lead, err = strconv.Atoi(values.LeadDays)
		if err != nil {
			d.renderPlanShow(w, r, planID, values, app.ErrPlanLeadInvalid.Error())
			return
		}
	}

	if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		// New tasks go on the end of the series, which is the order somebody
		// writing a plan types them in.
		plan, txErr := d.ServicePlanService.GetPlanWithTasks(ctx, tx, planID)
		if txErr != nil {
			return txErr
		}
		_, txErr = d.ServicePlanService.AddTask(ctx, tx, app.AddPlanTaskParams{
			PlanID:           planID,
			Name:             values.Name,
			Instructions:     values.Instructions,
			IntervalDays:     interval,
			LeadDays:         lead,
			WarrantyRequired: values.WarrantyRequired,
			SortOrder:        len(plan.Tasks),
		}, staffActor(r))
		return txErr
	}); err != nil {
		// Instructions can be several lines of procedure. Losing them to a
		// mistyped interval would be a good way to teach somebody not to write
		// them in the first place.
		d.renderPlanShow(w, r, planID, values, err.Error())
		return
	}

	redirectFlash(w, r, planPath(planID), "Added to the series. Machines on this plan pick it up overnight.")
}

// handleAdminServicePlanTaskDelete takes a job out of a series.
func (d *Deps) handleAdminServicePlanTaskDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	planID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		Error(w, r, app.ErrPlanNotFound)
		return
	}
	taskID, err := uuid.Parse(r.PathValue("childID"))
	if err != nil {
		Error(w, r, app.ErrPlanTaskNotFound)
		return
	}

	if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.ServicePlanService.RemoveTask(ctx, tx, planID, taskID, staffActor(r))
	}); err != nil {
		redirectFlashError(w, r, planPath(planID), err.Error())
		return
	}

	redirectFlash(w, r, planPath(planID), "Removed from the series.")
}

func planPath(id uuid.UUID) string { return "/admin/service/plans/" + id.String() }

// --- Assignments, from the machine's page ---

// handleAdminEquipmentPlanAssign puts a machine on a plan.
func (d *Deps) handleAdminEquipmentPlanAssign(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	equipmentID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		Error(w, r, app.ErrEquipmentNotFound)
		return
	}
	if err := r.ParseForm(); err != nil {
		Error(w, r, err)
		return
	}
	back := equipmentPath(equipmentID)

	planID, err := uuid.Parse(r.FormValue("plan_id"))
	if err != nil {
		redirectFlashError(w, r, back, app.ErrPlanNotFound.Error())
		return
	}
	startsOn, err := parseMaintenanceDay(r.FormValue("starts_on"), time.Time{})
	if err != nil || startsOn.IsZero() {
		redirectFlashError(w, r, back, app.ErrPlanStartRequired.Error())
		return
	}
	var contractEnds *time.Time
	if raw := strings.TrimSpace(r.FormValue("contract_ends_on")); raw != "" {
		end, parseErr := time.Parse("2006-01-02", raw)
		if parseErr != nil {
			redirectFlashError(w, r, back, "That contract end date is not a date.")
			return
		}
		contractEnds = &end
	}

	if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.ServicePlanService.AssignPlan(ctx, tx, app.AssignServicePlanParams{
			EquipmentID:    equipmentID,
			PlanID:         planID,
			StartsOn:       startsOn,
			UnderContract:  r.FormValue("under_contract") == "1",
			ContractEndsOn: contractEnds,
			Notes:          r.FormValue("notes"),
		}, staffActor(r))
		return txErr
	}); err != nil {
		redirectFlashError(w, r, back, err.Error())
		return
	}

	redirectFlash(w, r, back, "On the plan. Its schedule is below.")
}

// handleAdminEquipmentPlanEnd takes a machine off a plan.
func (d *Deps) handleAdminEquipmentPlanEnd(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	equipmentID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		Error(w, r, app.ErrEquipmentNotFound)
		return
	}
	assignmentID, err := uuid.Parse(r.PathValue("childID"))
	if err != nil {
		Error(w, r, app.ErrPlanAssignmentNotFound)
		return
	}
	back := equipmentPath(equipmentID)

	if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.ServicePlanService.EndAssignment(ctx, tx, equipmentID, assignmentID, time.Now(), staffActor(r))
	}); err != nil {
		redirectFlashError(w, r, back, err.Error())
		return
	}

	redirectFlash(w, r, back, "Off the plan. What was already done stays on the record.")
}

func equipmentPath(id uuid.UUID) string { return "/admin/service/equipment/" + id.String() }

// equipmentMaintenanceProps loads the maintenance card on a machine's page.
//
// Lives here rather than in admin_equipment.go so the whole preventive
// maintenance feature stays in one file — the machine page is a consumer of it,
// not its owner.
func (d *Deps) equipmentMaintenanceProps(ctx context.Context, tx pgx.Tx, equipment *domain.Equipment, today time.Time) (admin.EquipmentMaintenanceProps, error) {
	props := admin.EquipmentMaintenanceProps{Today: today}

	assignments, err := d.ServicePlanService.ListAssignments(ctx, tx, equipment.ID, false)
	if err != nil {
		return props, err
	}
	props.Assignments = assignments

	machineID := equipment.ID
	upcoming, err := d.ServicePlanService.ListDue(ctx, tx, store.MaintenanceFilter{
		EquipmentID: &machineID,
		Now:         today,
	})
	if err != nil {
		return props, err
	}
	props.Upcoming = upcoming

	// Only active plans the machine is not already on. Offering a plan it is
	// on would produce a duplicate the database then rejects, which is a
	// worse way to learn the same fact.
	onAlready := make(map[uuid.UUID]bool, len(assignments))
	for _, a := range assignments {
		onAlready[a.PlanID] = true
	}
	plans, err := d.ServicePlanService.ListPlans(ctx, tx, store.ServicePlanFilter{
		ActiveOnly: true,
		Category:   equipment.Category,
	})
	if err != nil {
		return props, err
	}
	for _, plan := range plans {
		if !onAlready[plan.ID] {
			props.AvailablePlans = append(props.AvailablePlans, plan)
		}
	}

	// How many active plans the category filter left out, so an empty picker
	// can tell "you have written none" from "none of yours suit this machine".
	if len(props.AvailablePlans) == 0 {
		all, allErr := d.ServicePlanService.ListPlans(ctx, tx, store.ServicePlanFilter{ActiveOnly: true})
		if allErr != nil {
			return props, allErr
		}
		props.OtherCategoryPlans = len(all) - len(plans)
	}

	return props, nil
}

// --- The cross-account cost report ---

// handleAdminServiceCosts renders what servicing each account has taken.
func (d *Deps) handleAdminServiceCosts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()

	days := serviceCostDays(q.Get("days"))
	sort, explicit := serviceCostSort(q)

	var report domain.ServiceAccountReport
	var nav admin.ServiceNav
	if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		// Cost is the ranking the report exists to produce, so it leads unless
		// the reader asked for another. CostByAccount falls the choice back to
		// hours when there is no money to rank on, and echoes back whichever
		// ran — so the strip highlights the tab actually being shown.
		if !explicit {
			sort = domain.ServiceAccountCostByCost
		}
		report, txErr = d.ServiceTicketService.CostByAccount(ctx, tx, days, sort, time.Now())
		if txErr != nil {
			return txErr
		}
		nav, txErr = d.serviceNav(ctx, tx)
		return txErr
	}); err != nil {
		Error(w, r, err)
		return
	}

	staffName, staffRole := staffNameRole(r)
	props := admin.ServiceCostsProps{
		Report:    report,
		Nav:       nav,
		Days:      days,
		StaffName: staffName,
		StaffRole: staffRole,
		Flash:     settingsFlash(r),
	}

	if IsHTMX(r) {
		admin.ServiceCostsContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.ServiceCosts(props).Render(ctx, w) //nolint:errcheck
}

// serviceCostSort reads the ranking off the query string, and says whether the
// reader picked one at all.
//
// The distinction matters: an absent sort means "give me the best default",
// which depends on whether a labour rate exists, and that is not known here. A
// hand-edited one falls back rather than erroring — a mistyped URL should still
// show the report.
func serviceCostSort(q url.Values) (domain.ServiceAccountCostSort, bool) {
	raw, ok := q["sort"]
	if !ok || len(raw) == 0 {
		return domain.ServiceAccountCostByHours, false
	}
	sort := domain.ServiceAccountCostSort(raw[0])
	if !sort.Valid() {
		return domain.ServiceAccountCostByHours, false
	}
	return sort, true
}

// serviceCostDays reads the window off the query string. Only the three the
// page offers are accepted — an arbitrary ?days= would be a window nothing in
// the control strip could highlight, so the reader could not tell what they
// were looking at.
//
// The default is 90 days: the quarter is the period a merchant actually argues
// about, and all-time flatters a long-standing account by spreading its hours
// over years.
func serviceCostDays(raw string) int {
	switch raw {
	case "0":
		return 0
	case "365":
		return 365
	}
	return 90
}

// handleAdminServicePlanTaskUpdate rewrites one job in a plan's series.
//
// A changed interval reaches every machine on the plan, which is the point of a
// plan being a template rather than a thing you copy.
func (d *Deps) handleAdminServicePlanTaskUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	planID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		Error(w, r, app.ErrPlanNotFound)
		return
	}
	taskID, err := uuid.Parse(r.PathValue("childID"))
	if err != nil {
		Error(w, r, app.ErrPlanTaskNotFound)
		return
	}
	if err := r.ParseForm(); err != nil {
		Error(w, r, err)
		return
	}

	interval, err := strconv.Atoi(strings.TrimSpace(r.FormValue("interval_days")))
	if err != nil {
		redirectFlashError(w, r, planPath(planID), app.ErrPlanIntervalInvalid.Error())
		return
	}
	lead := 0
	if raw := strings.TrimSpace(r.FormValue("lead_days")); raw != "" {
		lead, err = strconv.Atoi(raw)
		if err != nil {
			redirectFlashError(w, r, planPath(planID), app.ErrPlanLeadInvalid.Error())
			return
		}
	}

	if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		// Scoped to the plan in the path: a task on another plan must not be
		// editable through this one.
		task, txErr := d.ServicePlanService.GetTaskOnPlan(ctx, tx, planID, taskID)
		if txErr != nil {
			return txErr
		}
		_, txErr = d.ServicePlanService.EditTask(ctx, tx, taskID, app.EditPlanTaskParams{
			Name:             r.FormValue("name"),
			Instructions:     r.FormValue("instructions"),
			IntervalDays:     interval,
			LeadDays:         lead,
			WarrantyRequired: r.FormValue("warranty_required") == "1",
			SortOrder:        task.SortOrder,
		}, d.merchantToday(), staffActor(r))
		return txErr
	}); err != nil {
		redirectFlashError(w, r, planPath(planID), err.Error())
		return
	}

	redirectFlash(w, r, planPath(planID), "Job updated. Machines on this plan are rescheduled to match.")
}

// handleAdminEquipmentPlanUpdate changes the terms a machine is on a plan under.
//
// The contract flag is the one that matters: a cafe that signs up halfway
// through should not have to be taken off the plan and put back on, which would
// throw away the schedule it has built up.
func (d *Deps) handleAdminEquipmentPlanUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	equipmentID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		Error(w, r, app.ErrEquipmentNotFound)
		return
	}
	assignmentID, err := uuid.Parse(r.PathValue("childID"))
	if err != nil {
		Error(w, r, app.ErrPlanAssignmentNotFound)
		return
	}
	if err := r.ParseForm(); err != nil {
		Error(w, r, err)
		return
	}
	back := equipmentPath(equipmentID)

	startsOn, err := parseMaintenanceDay(r.FormValue("starts_on"), time.Time{})
	if err != nil || startsOn.IsZero() {
		redirectFlashError(w, r, back, app.ErrPlanStartRequired.Error())
		return
	}
	var contractEnds *time.Time
	if raw := strings.TrimSpace(r.FormValue("contract_ends_on")); raw != "" {
		end, parseErr := time.Parse("2006-01-02", raw)
		if parseErr != nil {
			redirectFlashError(w, r, back, "That contract end date is not a date.")
			return
		}
		contractEnds = &end
	}

	if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		// Scoped to the machine in the path, like ending one is.
		assignment, txErr := d.ServicePlanService.GetAssignment(ctx, tx, assignmentID)
		if txErr != nil {
			return txErr
		}
		if assignment.EquipmentID != equipmentID {
			return app.ErrPlanAssignmentNotFound
		}
		_, txErr = d.ServicePlanService.EditAssignment(ctx, tx, assignmentID, app.EditPlanAssignmentParams{
			StartsOn:       startsOn,
			UnderContract:  r.FormValue("under_contract") == "1",
			ContractEndsOn: contractEnds,
			Notes:          r.FormValue("notes"),
		}, staffActor(r))
		return txErr
	}); err != nil {
		redirectFlashError(w, r, back, err.Error())
		return
	}

	redirectFlash(w, r, back, "Terms updated.")
}
