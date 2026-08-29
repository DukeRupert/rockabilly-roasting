package web

import (
	"context"
	"errors"
	"fmt"
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

// The equipment register: /admin/service/equipment. Part of the equipment
// service module — every route here sits behind requireModule, so on a shop
// that does not service machines none of it exists.
//
// See docs/equipment-service-module.md for the design.

// handleAdminEquipmentList renders the register.
func (d *Deps) handleAdminEquipmentList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()

	filter := store.EquipmentFilter{
		Category:       domain.EquipmentCategory(q.Get("category")),
		Ownership:      domain.EquipmentOwnership(q.Get("ownership")),
		Search:         strings.TrimSpace(q.Get("q")),
		IncludeRetired: q.Get("retired") == "1",
		// The register is a working list, not a report. A cap keeps one very
		// large shop from rendering a four-thousand-row table; the filters are
		// how you find a specific machine.
		Limit: 250,
	}
	// A filter value the database would reject is dropped rather than passed
	// on: a hand-edited query string should show everything, not 500.
	if !filter.Category.Valid() {
		filter.Category = ""
	}
	if !filter.Ownership.Valid() {
		filter.Ownership = ""
	}

	var rows []domain.EquipmentWithCustomer
	var nav admin.ServiceNav
	if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		rows, txErr = d.EquipmentService.ListWithCustomer(ctx, tx, filter)
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
	props := admin.EquipmentListProps{
		Equipment:      rows,
		Nav:            nav,
		Category:       string(filter.Category),
		Ownership:      string(filter.Ownership),
		Search:         filter.Search,
		IncludeRetired: filter.IncludeRetired,
		MerchantTZ:     d.MerchantTZ,
		StaffName:      staffName,
		StaffRole:      staffRole,
		CanWrite:       staffCan(r, auth.PermWriteService),
		Flash:          settingsFlash(r),
	}

	if IsHTMX(r) {
		admin.EquipmentListContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.EquipmentList(props).Render(ctx, w) //nolint:errcheck
}

// handleAdminEquipmentNew renders the add form.
func (d *Deps) handleAdminEquipmentNew(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	props := admin.EquipmentFormProps{Values: admin.EquipmentFormValuesFrom(nil)}
	// Opened from a customer's page, the customer comes prefilled.
	if raw := r.URL.Query().Get("customer_id"); raw != "" {
		if id, err := uuid.Parse(raw); err == nil {
			props.CustomerID = id
			props.Values.CustomerID = id.String()
		}
	}

	if err := d.fillEquipmentFormOptions(ctx, &props); err != nil {
		Error(w, r, err)
		return
	}

	props.StaffName, props.StaffRole = staffNameRole(r)
	d.renderEquipmentForm(w, r, props)
}

// handleAdminEquipmentEdit renders the edit form.
func (d *Deps) handleAdminEquipmentEdit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		Error(w, r, app.ErrEquipmentNotFound)
		return
	}

	var equipment *domain.Equipment
	if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		equipment, txErr = d.EquipmentService.GetByID(ctx, tx, id)
		return txErr
	}); err != nil {
		Error(w, r, err)
		return
	}

	props := admin.EquipmentFormProps{
		Equipment:  equipment,
		CustomerID: equipment.CustomerID,
		Values:     admin.EquipmentFormValuesFrom(equipment),
	}
	if err := d.fillEquipmentFormOptions(ctx, &props); err != nil {
		Error(w, r, err)
		return
	}

	props.StaffName, props.StaffRole = staffNameRole(r)
	d.renderEquipmentForm(w, r, props)
}

// handleAdminEquipmentCreate registers a machine.
func (d *Deps) handleAdminEquipmentCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := r.ParseForm(); err != nil {
		Error(w, r, err)
		return
	}

	values := equipmentFormValues(r)
	customerID, err := uuid.Parse(values.CustomerID)
	if err != nil {
		d.rejectEquipmentForm(w, r, nil, values, "Choose the customer this machine belongs to.")
		return
	}

	params := app.RegisterEquipmentParams{
		CustomerID:        customerID,
		AddressID:         parseOptionalUUID(values.AddressID),
		Category:          domain.EquipmentCategory(values.Category),
		Make:              values.Make,
		Model:             values.Model,
		SerialNumber:      values.SerialNumber,
		Ownership:         domain.EquipmentOwnership(values.Ownership),
		InstalledOn:       parseOptionalDate(values.InstalledOn),
		WarrantyExpiresOn: parseOptionalDate(values.WarrantyExpiresOn),
		Notes:             strings.TrimSpace(values.Notes),
	}

	var created *domain.Equipment
	if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		created, txErr = d.EquipmentService.Register(ctx, tx, params, staffActor(r))
		return txErr
	}); err != nil {
		if msg, ok := equipmentValidationMessage(err); ok {
			d.rejectEquipmentForm(w, r, nil, values, msg)
			return
		}
		Error(w, r, err)
		return
	}

	redirectFlash(w, r, "/admin/service/equipment/"+created.ID.String(),
		created.Description()+" is on the register.")
}

// handleAdminEquipmentUpdate saves an edit.
func (d *Deps) handleAdminEquipmentUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		Error(w, r, app.ErrEquipmentNotFound)
		return
	}
	if err := r.ParseForm(); err != nil {
		Error(w, r, err)
		return
	}

	values := equipmentFormValues(r)
	params := app.EditEquipmentParams{
		AddressID:         parseOptionalUUID(values.AddressID),
		Category:          domain.EquipmentCategory(values.Category),
		Make:              values.Make,
		Model:             values.Model,
		SerialNumber:      values.SerialNumber,
		Ownership:         domain.EquipmentOwnership(values.Ownership),
		InstalledOn:       parseOptionalDate(values.InstalledOn),
		WarrantyExpiresOn: parseOptionalDate(values.WarrantyExpiresOn),
		Notes:             strings.TrimSpace(values.Notes),
	}

	var updated *domain.Equipment
	if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		updated, txErr = d.EquipmentService.Edit(ctx, tx, id, params, staffActor(r))
		return txErr
	}); err != nil {
		if msg, ok := equipmentValidationMessage(err); ok {
			// Re-read the machine so the rejected form still knows which one it
			// is editing; without it the retry posts to the create route.
			var existing *domain.Equipment
			_ = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
				var txErr error
				existing, txErr = d.EquipmentService.GetByID(ctx, tx, id)
				return txErr
			})
			d.rejectEquipmentForm(w, r, existing, values, msg)
			return
		}
		Error(w, r, err)
		return
	}

	redirectFlash(w, r, "/admin/service/equipment/"+updated.ID.String(), "Saved.")
}

// handleAdminEquipmentStatus moves a machine between in service, in the shop
// and retired.
func (d *Deps) handleAdminEquipmentStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		Error(w, r, app.ErrEquipmentNotFound)
		return
	}
	if err := r.ParseForm(); err != nil {
		Error(w, r, err)
		return
	}

	status := domain.EquipmentStatus(r.FormValue("status"))

	var updated *domain.Equipment
	if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		updated, txErr = d.EquipmentService.SetStatus(ctx, tx, id, status, staffActor(r))
		return txErr
	}); err != nil {
		Error(w, r, err)
		return
	}

	var msg string
	switch updated.Status {
	case domain.EquipmentStatusRetired:
		msg = updated.Description() + " is retired. Its history stays."
	case domain.EquipmentStatusInShop:
		msg = updated.Description() + " is marked as in the shop."
	default:
		msg = updated.Description() + " is back in service."
	}
	redirectFlash(w, r, "/admin/service/equipment/"+updated.ID.String(), msg)
}

// handleAdminEquipmentShow renders one machine.
func (d *Deps) handleAdminEquipmentShow(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		Error(w, r, app.ErrEquipmentNotFound)
		return
	}

	var equipment *domain.Equipment
	var customerName, siteLabel string
	var activity []domain.AuditEntry
	var tickets []admin.ServiceTicketRow

	if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		equipment, txErr = d.EquipmentService.GetByID(ctx, tx, id)
		if txErr != nil {
			return txErr
		}

		customer, txErr := d.CustomerService.GetCustomer(ctx, tx, equipment.CustomerID)
		if txErr != nil {
			return txErr
		}
		customerName = customerDisplayName(customer)

		if equipment.AddressID != nil {
			addresses, addrErr := d.CustomerService.ListAddresses(ctx, tx, equipment.CustomerID)
			if addrErr != nil {
				return addrErr
			}
			for _, a := range addresses {
				if a.ID == *equipment.AddressID {
					siteLabel = addressOneLine(a)
					break
				}
			}
		}

		activity, txErr = d.AuditQueryService.ListByResource(ctx, tx, "equipment", equipment.ID)
		if txErr != nil {
			return txErr
		}

		// Every ticket against this machine, closed ones included — a repair
		// history that hides finished work cannot make the case for replacing
		// a machine.
		machineID := equipment.ID
		history, txErr := d.ServiceTicketService.List(ctx, tx, store.ServiceTicketFilter{EquipmentID: &machineID})
		if txErr != nil {
			return txErr
		}
		tickets = admin.ServiceTicketRowsFrom(history, nil, nil, nil, staleCutoff())
		return nil
	}); err != nil {
		Error(w, r, err)
		return
	}

	staffName, staffRole := staffNameRole(r)
	props := admin.EquipmentShowProps{
		Equipment:    *equipment,
		CustomerName: customerName,
		SiteLabel:    siteLabel,
		Activity:     activity,
		Tickets:      tickets,
		MerchantTZ:   d.MerchantTZ,
		StaffName:    staffName,
		StaffRole:    staffRole,
		CanWrite:     staffCan(r, auth.PermWriteService),
		Flash:        settingsFlash(r),
	}

	if IsHTMX(r) {
		admin.EquipmentShowContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.EquipmentShow(props).Render(ctx, w) //nolint:errcheck
}

// --- helpers ---

func (d *Deps) renderEquipmentForm(w http.ResponseWriter, r *http.Request, props admin.EquipmentFormProps) {
	if IsHTMX(r) {
		admin.EquipmentFormContent(props).Render(r.Context(), w) //nolint:errcheck
		return
	}
	admin.EquipmentForm(props).Render(r.Context(), w) //nolint:errcheck
}

// rejectEquipmentForm re-renders the form with what was typed still in it. A
// rejected save must not cost the operator the other eight fields.
func (d *Deps) rejectEquipmentForm(w http.ResponseWriter, r *http.Request, equipment *domain.Equipment, values admin.EquipmentFormValues, msg string) {
	props := admin.EquipmentFormProps{
		Equipment: equipment,
		Values:    values,
		Error:     msg,
	}
	if equipment != nil {
		props.CustomerID = equipment.CustomerID
	}
	if err := d.fillEquipmentFormOptions(r.Context(), &props); err != nil {
		Error(w, r, err)
		return
	}
	props.StaffName, props.StaffRole = staffNameRole(r)
	w.WriteHeader(http.StatusBadRequest)
	d.renderEquipmentForm(w, r, props)
}

// fillEquipmentFormOptions loads the customer and site pickers.
//
// Addresses are only loaded once a customer is known — there is no useful
// site list across every customer, and offering one would let a machine be
// filed at an address belonging to somebody else.
func (d *Deps) fillEquipmentFormOptions(ctx context.Context, props *admin.EquipmentFormProps) error {
	return store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		// Every customer, by name. At this shop's scale that is a couple of
		// hundred options, which a <select> handles; if a merchant ever grows
		// past what a dropdown can hold, this wants the typeahead the order
		// pages use (CustomerService.SuggestCustomers), not a bigger cap.
		customers, err := d.CustomerService.ListCustomers(ctx, tx, store.CustomerFilter{
			Sort:  store.CustomerSortNameAsc,
			Limit: 1000,
		})
		if err != nil {
			return err
		}
		props.Customers = make([]admin.EquipmentOption, 0, len(customers))
		for _, c := range customers {
			props.Customers = append(props.Customers, admin.EquipmentOption{
				ID:    c.ID,
				Label: customerDisplayName(&c),
			})
		}

		if props.CustomerID == uuid.Nil {
			return nil
		}
		addresses, err := d.CustomerService.ListAddresses(ctx, tx, props.CustomerID)
		if err != nil {
			return err
		}
		props.Addresses = make([]admin.EquipmentOption, 0, len(addresses))
		for _, a := range addresses {
			props.Addresses = append(props.Addresses, admin.EquipmentOption{
				ID:    a.ID,
				Label: addressOneLine(a),
			})
		}
		return nil
	})
}

func equipmentFormValues(r *http.Request) admin.EquipmentFormValues {
	return admin.EquipmentFormValues{
		CustomerID:        r.FormValue("customer_id"),
		AddressID:         r.FormValue("address_id"),
		Category:          r.FormValue("category"),
		Make:              r.FormValue("make"),
		Model:             r.FormValue("model"),
		SerialNumber:      r.FormValue("serial_number"),
		Ownership:         r.FormValue("ownership"),
		InstalledOn:       r.FormValue("installed_on"),
		WarrantyExpiresOn: r.FormValue("warranty_expires_on"),
		Notes:             r.FormValue("notes"),
	}
}

// equipmentValidationMessage separates "you typed something wrong" from "the
// database fell over". The first re-renders the form; the second is a 500.
func equipmentValidationMessage(err error) (string, bool) {
	switch {
	case errors.Is(err, app.ErrEquipmentMakeRequired),
		errors.Is(err, app.ErrInvalidEquipmentCategory),
		errors.Is(err, app.ErrInvalidEquipmentOwnership):
		return err.Error(), true
	}
	return "", false
}

func parseOptionalUUID(raw string) *uuid.UUID {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return nil
	}
	return &id
}

// parseOptionalDate reads a browser <input type="date"> value. An unparseable
// or blank value becomes nil, which for a warranty reads as "no cover" — the
// safe direction, since the reverse quotes a repair as covered when it is not.
func parseOptionalDate(raw string) *time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	t, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return nil
	}
	return &t
}

func addressOneLine(a domain.Address) string {
	parts := []string{a.Line1}
	if a.Line2 != nil && strings.TrimSpace(*a.Line2) != "" {
		parts = append(parts, strings.TrimSpace(*a.Line2))
	}
	parts = append(parts, fmt.Sprintf("%s, %s %s", a.City, a.State, a.PostalCode))
	return strings.Join(parts, ", ")
}
