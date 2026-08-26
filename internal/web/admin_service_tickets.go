package web

import (
	"context"
	"errors"
	"net/http"
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

// The ticket queue: /admin/service. Part of the equipment service module, so
// every route here sits behind requireModule.
//
// See docs/equipment-service-module.md for the design.

// staleCutoff is the moment before which an open ticket counts as having gone
// quiet. One place computes it so the list flag, the tab badge and the detail
// banner cannot disagree about what "a week" means.
func staleCutoff() time.Time {
	return time.Now().Add(-domain.DefaultStaleContactWindow)
}

// serviceNav builds the section strip, including the count of tickets nobody
// has spoken to the customer about. Every page in the section pays for this
// read so a quiet ticket cannot hide behind a tab nobody clicked.
func (d *Deps) serviceNav(ctx context.Context, tx pgx.Tx) (admin.ServiceNav, error) {
	stale, err := d.ServiceTicketService.ListStale(ctx, tx, staleCutoff(), 0)
	if err != nil {
		return admin.ServiceNav{}, err
	}
	return admin.ServiceNav{StaleCount: len(stale)}, nil
}

// handleAdminServiceTicketList renders the queue.
func (d *Deps) handleAdminServiceTicketList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()

	scope := q.Get("scope")
	filter := store.ServiceTicketFilter{
		Status:   domain.ServiceTicketStatus(q.Get("status")),
		Severity: domain.ServiceSeverity(q.Get("severity")),
		Limit:    250,
	}
	if !filter.Status.Valid() {
		filter.Status = ""
	}
	if !filter.Severity.Valid() {
		filter.Severity = ""
	}

	switch scope {
	case "mine":
		if staff, ok := auth.StaffFromContext(ctx); ok {
			id := staff.ID
			filter.AssignedTo = &id
		}
		filter.OpenOnly = true
	case "stale":
		cutoff := staleCutoff()
		filter.StaleBefore = &cutoff
	case "closed":
		// An explicit status filter wins; otherwise "closed" means resolved,
		// which is what people mean when they go looking for finished work.
		if filter.Status == "" {
			filter.Status = domain.ServiceTicketStatusResolved
		}
	default:
		scope = ""
		// The default view is the queue: work that is not finished. A status
		// filter naming a closed state overrides it, or picking "Resolved" from
		// the dropdown would return nothing.
		if filter.Status == "" || filter.Status.Open() {
			filter.OpenOnly = true
		}
	}

	var (
		tickets []domain.ServiceTicket
		nav     admin.ServiceNav
		rows    []admin.ServiceTicketRow
	)
	if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		tickets, txErr = d.ServiceTicketService.List(ctx, tx, filter)
		if txErr != nil {
			return txErr
		}
		nav, txErr = d.serviceNav(ctx, tx)
		if txErr != nil {
			return txErr
		}
		customers, machines, staff, txErr := d.ticketRowLookups(ctx, tx, tickets)
		if txErr != nil {
			return txErr
		}
		rows = admin.ServiceTicketRowsFrom(tickets, customers, machines, staff, staleCutoff())
		return nil
	}); err != nil {
		Error(w, r, err)
		return
	}

	staffName, staffRole := staffNameRole(r)
	props := admin.ServiceTicketListProps{
		Rows:       rows,
		Nav:        nav,
		Status:     string(filter.Status),
		Severity:   string(filter.Severity),
		Scope:      scope,
		MerchantTZ: d.MerchantTZ,
		StaffName:  staffName,
		StaffRole:  staffRole,
		CanWrite:   staffCan(r, auth.PermWriteService),
		Flash:      settingsFlash(r),
	}

	if IsHTMX(r) {
		admin.ServiceTicketListContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.ServiceTicketList(props).Render(ctx, w) //nolint:errcheck
}

// ticketRowLookups resolves the ids on a page of tickets to names in three
// queries, not three per row.
func (d *Deps) ticketRowLookups(ctx context.Context, tx pgx.Tx, tickets []domain.ServiceTicket) (
	customers map[uuid.UUID]string, machines map[uuid.UUID]string, staff map[uuid.UUID]string, err error,
) {
	customers = map[uuid.UUID]string{}
	machines = map[uuid.UUID]string{}
	staff = map[uuid.UUID]string{}
	if len(tickets) == 0 {
		return customers, machines, staff, nil
	}

	// The customer and equipment lists are small enough at this scale to read
	// whole and index in memory; a per-ticket lookup would be a query storm on
	// a 250-row page.
	allCustomers, err := d.CustomerService.ListCustomers(ctx, tx, store.CustomerFilter{Limit: 5000})
	if err != nil {
		return nil, nil, nil, err
	}
	for i := range allCustomers {
		customers[allCustomers[i].ID] = customerDisplayName(&allCustomers[i])
	}

	allEquipment, err := d.EquipmentService.List(ctx, tx, store.EquipmentFilter{IncludeRetired: true})
	if err != nil {
		return nil, nil, nil, err
	}
	for _, e := range allEquipment {
		machines[e.ID] = e.Description()
	}

	allStaff, err := d.StaffService.List(ctx, tx, staffRosterLimit, 0)
	if err != nil {
		return nil, nil, nil, err
	}
	for _, s := range allStaff {
		staff[s.ID] = s.Name
	}

	return customers, machines, staff, nil
}

// handleAdminServiceTicketShow renders one ticket.
func (d *Deps) handleAdminServiceTicketShow(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		Error(w, r, app.ErrServiceTicketNotFound)
		return
	}

	var (
		ticket   *domain.ServiceTicket
		props    admin.ServiceTicketShowProps
		notes    []domain.ServiceTicketNote
		activity []domain.AuditEntry
		staffBy  = map[uuid.UUID]string{}
	)

	if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		ticket, txErr = d.ServiceTicketService.GetByID(ctx, tx, id)
		if txErr != nil {
			return txErr
		}

		customer, txErr := d.CustomerService.GetCustomer(ctx, tx, ticket.CustomerID)
		if txErr != nil {
			return txErr
		}
		props.CustomerName = customerDisplayName(customer)

		if ticket.EquipmentID != nil {
			machine, mErr := d.EquipmentService.GetByID(ctx, tx, *ticket.EquipmentID)
			if mErr != nil && !errors.Is(mErr, app.ErrEquipmentNotFound) {
				return mErr
			}
			if machine != nil {
				props.MachineName = machine.Description()
				props.MachineID = &machine.ID
			}
		}

		if ticket.AddressID != nil {
			addresses, aErr := d.CustomerService.ListAddresses(ctx, tx, ticket.CustomerID)
			if aErr != nil {
				return aErr
			}
			for _, a := range addresses {
				if a.ID == *ticket.AddressID {
					props.SiteLabel = addressOneLine(a)
					break
				}
			}
		}

		allStaff, sErr := d.StaffService.List(ctx, tx, staffRosterLimit, 0)
		if sErr != nil {
			return sErr
		}
		for _, s := range allStaff {
			staffBy[s.ID] = s.Name
			props.Staff = append(props.Staff, admin.StaffOption{ID: s.ID, Name: s.Name})
		}
		if ticket.AssignedStaffID != nil {
			props.AssigneeName = staffBy[*ticket.AssignedStaffID]
		}

		notes, txErr = d.ServiceTicketService.ListNotes(ctx, tx, ticket.ID, false)
		if txErr != nil {
			return txErr
		}
		activity, txErr = d.AuditQueryService.ListByResource(ctx, tx, "service_ticket", ticket.ID)
		if txErr != nil {
			return txErr
		}
		props.Totals, txErr = d.ServiceTicketService.Totals(ctx, tx, ticket.ID)
		return txErr
	}); err != nil {
		Error(w, r, err)
		return
	}

	// Note authors are resolved from the staff map already loaded above; a
	// customer-written note is labelled by the account it came from, since the
	// portal user's name is not worth a second query on a staff-facing page.
	authorName := func(n domain.ServiceTicketNote) string {
		if n.StaffID != nil {
			if name := staffBy[*n.StaffID]; name != "" {
				return name
			}
			return "Staff"
		}
		if n.CustomerUserID != nil {
			return props.CustomerName
		}
		return "System"
	}

	props.Ticket = *ticket
	props.Timeline = admin.ServiceTimelineEntries(notes, activity, authorName)
	props.Stale = ticket.StaleSince(staleCutoff())
	props.MerchantTZ = d.MerchantTZ
	props.StaffName, props.StaffRole = staffNameRole(r)
	props.CanWrite = staffCan(r, auth.PermWriteService)
	props.Flash = settingsFlash(r)

	if IsHTMX(r) {
		admin.ServiceTicketShowContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.ServiceTicketShow(props).Render(ctx, w) //nolint:errcheck
}

// handleAdminServiceTicketNew renders the open-a-ticket form.
func (d *Deps) handleAdminServiceTicketNew(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	props := admin.ServiceTicketNewProps{}
	if raw := r.URL.Query().Get("customer_id"); raw != "" {
		props.Values.CustomerID = raw
	}
	if raw := r.URL.Query().Get("equipment_id"); raw != "" {
		props.Values.EquipmentID = raw
	}

	if err := d.fillTicketFormOptions(ctx, &props); err != nil {
		Error(w, r, err)
		return
	}

	props.StaffName, props.StaffRole = staffNameRole(r)
	d.renderTicketForm(w, r, props)
}

// handleAdminServiceTicketMachines serves the machine picker for one customer.
// Swapped in by htmx when the customer select changes, so the list can never
// offer another cafe's equipment.
func (d *Deps) handleAdminServiceTicketMachines(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var options []admin.EquipmentOption
	if raw := r.URL.Query().Get("customer_id"); raw != "" {
		if id, err := uuid.Parse(raw); err == nil {
			var equipment []domain.Equipment
			if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
				var txErr error
				equipment, txErr = d.EquipmentService.ListForCustomer(ctx, tx, id)
				return txErr
			}); err != nil {
				Error(w, r, err)
				return
			}
			options = admin.MachineOptionsFrom(equipment)
		}
	}

	admin.ServiceTicketMachinePicker(options, "").Render(ctx, w) //nolint:errcheck
}

// handleAdminServiceTicketCreate opens a ticket.
func (d *Deps) handleAdminServiceTicketCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := r.ParseForm(); err != nil {
		Error(w, r, err)
		return
	}

	values := admin.ServiceTicketFormValues{
		CustomerID:  r.FormValue("customer_id"),
		EquipmentID: r.FormValue("equipment_id"),
		Title:       r.FormValue("title"),
		Description: r.FormValue("description"),
		Severity:    r.FormValue("severity"),
		AssignedTo:  r.FormValue("assigned_staff_id"),
		Billable:    r.FormValue("billable"),
	}

	customerID, err := uuid.Parse(values.CustomerID)
	if err != nil {
		d.rejectTicketForm(w, r, values, "Choose the customer this is about.")
		return
	}

	staff, _ := auth.StaffFromContext(ctx)
	openedBy := staff.ID

	params := app.OpenTicketParams{
		CustomerID:      customerID,
		EquipmentID:     parseOptionalUUID(values.EquipmentID),
		Title:           values.Title,
		Description:     values.Description,
		Severity:        domain.ServiceSeverity(values.Severity),
		OpenedByStaffID: &openedBy,
		AssignedStaffID: parseOptionalUUID(values.AssignedTo),
		Billable:        values.Billable == "1",
	}

	var ticket *domain.ServiceTicket
	if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		ticket, txErr = d.ServiceTicketService.Open(ctx, tx, params, staffActor(r))
		return txErr
	}); err != nil {
		if msg, ok := ticketValidationMessage(err); ok {
			d.rejectTicketForm(w, r, values, msg)
			return
		}
		Error(w, r, err)
		return
	}

	redirectFlash(w, r, "/admin/service/tickets/"+ticket.ID.String(), ticket.Number+" is open.")
}

// handleAdminServiceTicketStatus moves a ticket.
func (d *Deps) handleAdminServiceTicketStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		Error(w, r, app.ErrServiceTicketNotFound)
		return
	}
	if err := r.ParseForm(); err != nil {
		Error(w, r, err)
		return
	}

	status := domain.ServiceTicketStatus(r.FormValue("status"))
	resolution := r.FormValue("resolution")

	var ticket *domain.ServiceTicket
	if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		ticket, txErr = d.ServiceTicketService.SetStatus(ctx, tx, id, status, resolution, staffActor(r))
		return txErr
	}); err != nil {
		Error(w, r, err)
		return
	}

	redirectFlash(w, r, "/admin/service/tickets/"+ticket.ID.String(),
		ticket.Number+" is now "+strings.ToLower(ticket.Status.Label())+".")
}

// handleAdminServiceTicketAssign sets who is responsible.
func (d *Deps) handleAdminServiceTicketAssign(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		Error(w, r, app.ErrServiceTicketNotFound)
		return
	}
	if err := r.ParseForm(); err != nil {
		Error(w, r, err)
		return
	}

	staffID := parseOptionalUUID(r.FormValue("staff_id"))

	var ticket *domain.ServiceTicket
	var staffName string
	if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		if staffID != nil {
			member, sErr := d.StaffService.Get(ctx, tx, *staffID)
			if sErr != nil {
				return sErr
			}
			staffName = member.Name
		}
		var txErr error
		ticket, txErr = d.ServiceTicketService.Assign(ctx, tx, id, staffID, staffName, staffActor(r))
		return txErr
	}); err != nil {
		Error(w, r, err)
		return
	}

	msg := ticket.Number + " is unassigned."
	if staffName != "" {
		msg = ticket.Number + " is assigned to " + staffName + "."
	}
	redirectFlash(w, r, "/admin/service/tickets/"+ticket.ID.String(), msg)
}

// handleAdminServiceTicketNote adds a timeline entry.
func (d *Deps) handleAdminServiceTicketNote(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		Error(w, r, app.ErrServiceTicketNotFound)
		return
	}
	if err := r.ParseForm(); err != nil {
		Error(w, r, err)
		return
	}

	staff, _ := auth.StaffFromContext(ctx)
	staffID := staff.ID

	params := app.AddNoteParams{
		TicketID:        id,
		Kind:            domain.ServiceNoteKind(r.FormValue("kind")),
		Body:            r.FormValue("body"),
		StaffID:         &staffID,
		CustomerVisible: r.FormValue("customer_visible") == "1",
	}
	// A date without a time lands at midnight, which would sort a call logged
	// today below everything else that happened today. Only honour the field
	// when it names a different day from today.
	if on := parseOptionalDate(r.FormValue("occurred_on")); on != nil {
		if !sameDay(*on, time.Now()) {
			params.OccurredAt = *on
		}
	}

	var note *domain.ServiceTicketNote
	if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		note, txErr = d.ServiceTicketService.AddNote(ctx, tx, params, staffActor(r))
		return txErr
	}); err != nil {
		Error(w, r, err)
		return
	}

	msg := "Note saved."
	if note.Kind.IsContact() {
		msg = note.Kind.Label() + " logged. The quiet flag is cleared."
	}
	redirectFlash(w, r, "/admin/service/tickets/"+id.String(), msg)
}

// --- helpers ---

// staffRosterLimit caps the roster reads that fill assign pickers and resolve
// assignee names. A single-merchant shop's team is a dozen people; this is a
// backstop, not a paging control.
const staffRosterLimit = 500

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

func (d *Deps) renderTicketForm(w http.ResponseWriter, r *http.Request, props admin.ServiceTicketNewProps) {
	if IsHTMX(r) {
		admin.ServiceTicketNewContent(props).Render(r.Context(), w) //nolint:errcheck
		return
	}
	admin.ServiceTicketNew(props).Render(r.Context(), w) //nolint:errcheck
}

func (d *Deps) rejectTicketForm(w http.ResponseWriter, r *http.Request, values admin.ServiceTicketFormValues, msg string) {
	props := admin.ServiceTicketNewProps{Values: values, Error: msg}
	if err := d.fillTicketFormOptions(r.Context(), &props); err != nil {
		Error(w, r, err)
		return
	}
	props.StaffName, props.StaffRole = staffNameRole(r)
	w.WriteHeader(http.StatusBadRequest)
	d.renderTicketForm(w, r, props)
}

func (d *Deps) fillTicketFormOptions(ctx context.Context, props *admin.ServiceTicketNewProps) error {
	return store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		customers, err := d.CustomerService.ListCustomers(ctx, tx, store.CustomerFilter{
			Sort:  store.CustomerSortNameAsc,
			Limit: 1000,
		})
		if err != nil {
			return err
		}
		props.Customers = make([]admin.EquipmentOption, 0, len(customers))
		for i := range customers {
			props.Customers = append(props.Customers, admin.EquipmentOption{
				ID:    customers[i].ID,
				Label: customerDisplayName(&customers[i]),
			})
		}

		allStaff, err := d.StaffService.List(ctx, tx, staffRosterLimit, 0)
		if err != nil {
			return err
		}
		for _, s := range allStaff {
			props.Staff = append(props.Staff, admin.StaffOption{ID: s.ID, Name: s.Name})
		}

		props.Nav, err = d.serviceNav(ctx, tx)
		if err != nil {
			return err
		}

		// Machines only once a customer is chosen — the picker is refreshed by
		// htmx after that, and an unfiltered list would offer other cafes'.
		if props.Values.CustomerID != "" {
			if id, parseErr := uuid.Parse(props.Values.CustomerID); parseErr == nil {
				equipment, eErr := d.EquipmentService.ListForCustomer(ctx, tx, id)
				if eErr != nil {
					return eErr
				}
				props.Machines = admin.MachineOptionsFrom(equipment)
			}
		}
		return nil
	})
}

func ticketValidationMessage(err error) (string, bool) {
	switch {
	case errors.Is(err, app.ErrServiceTicketTitleRequired),
		errors.Is(err, app.ErrInvalidServiceSeverity),
		errors.Is(err, app.ErrTicketEquipmentMismatch),
		errors.Is(err, app.ErrEquipmentNotFound):
		return err.Error(), true
	}
	return "", false
}
