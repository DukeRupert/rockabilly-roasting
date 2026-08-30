package web

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/auth"
	"github.com/dukerupert/hiri/internal/store"
)

// Parts and hours on a ticket. Every route here is a sub-record action posted
// from the ticket detail page and redirects straight back to it — there is no
// standalone part or time-entry screen, and there should not be.

// handleAdminServicePartAdd puts a part on a ticket.
func (d *Deps) handleAdminServicePartAdd(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ticketID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		Error(w, r, app.ErrServiceTicketNotFound)
		return
	}
	if err := r.ParseForm(); err != nil {
		Error(w, r, err)
		return
	}

	quantity := 1
	if n, convErr := strconv.Atoi(strings.TrimSpace(r.FormValue("quantity"))); convErr == nil {
		quantity = n
	}
	cost, err := parsePartCost(r.FormValue("unit_cost"))
	if err != nil {
		Error(w, r, app.ErrInvalidPartCost)
		return
	}

	if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.ServiceTicketService.AddPart(ctx, tx, app.AddPartParams{
			TicketID:      ticketID,
			Name:          r.FormValue("name"),
			PartNumber:    r.FormValue("part_number"),
			Supplier:      r.FormValue("supplier"),
			Quantity:      quantity,
			UnitCostCents: cost,
		}, staffActor(r))
		return txErr
	}); err != nil {
		Error(w, r, err)
		return
	}

	redirectFlash(w, r, ticketPath(ticketID), "Part added.")
}

// parsePartCost reads the optional per-unit cost off a part form, in cents.
//
// A blank cost is zero, not a rejection: plenty of parts come off a shelf the
// shop already paid for, and refusing to record the part because nobody knows
// what it cost would lose the more useful fact. The cost input is optional in
// the form, so blank is the ordinary case and not an edge one.
//
// This exists because parseDollarsCents — shared with the settings screens,
// where a missing amount genuinely is a mistake — returns an error for the
// empty string. The handler used to call it directly under a comment promising
// the opposite, so every part added without a price was dropped with the toast
// "a part cannot cost less than nothing". Deciding the blank case here keeps
// that helper right for its other callers, and makes the rule a named thing
// that can be tested without a database.
//
// A non-empty value that is not a number, or is negative, is still an error:
// somebody typing "abc" or "-5" into the price box has made a mistake worth
// reporting.
func parsePartCost(raw string) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, nil
	}
	return parseDollarsCents(raw)
}

// handleAdminServicePartStatus advances a part along ordered → arrived → fitted.
func (d *Deps) handleAdminServicePartStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ticketID, partID, ok := d.ticketAndChildID(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		Error(w, r, err)
		return
	}

	status := domain.ServicePartStatus(r.FormValue("status"))

	var part *domain.ServicePart
	if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		part, txErr = d.ServiceTicketService.SetPartStatus(ctx, tx, ticketID, partID, status, time.Time{}, staffActor(r))
		return txErr
	}); err != nil {
		Error(w, r, err)
		return
	}

	redirectFlash(w, r, ticketPath(ticketID),
		part.Name+" is "+strings.ToLower(part.Status.Label())+".")
}

// handleAdminServicePartDelete removes a mistyped part line.
func (d *Deps) handleAdminServicePartDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ticketID, partID, ok := d.ticketAndChildID(w, r)
	if !ok {
		return
	}

	if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.ServiceTicketService.RemovePart(ctx, tx, ticketID, partID, staffActor(r))
	}); err != nil {
		Error(w, r, err)
		return
	}

	redirectFlash(w, r, ticketPath(ticketID), "Part removed.")
}

// handleAdminServiceTimeLog records a stint of work.
func (d *Deps) handleAdminServiceTimeLog(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ticketID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		Error(w, r, app.ErrServiceTicketNotFound)
		return
	}
	if err := r.ParseForm(); err != nil {
		Error(w, r, err)
		return
	}

	minutes, convErr := strconv.Atoi(strings.TrimSpace(r.FormValue("minutes")))
	if convErr != nil {
		Error(w, r, app.ErrInvalidTimeMinutes)
		return
	}

	// Whoever did the work, defaulting to whoever is logged in. A manager
	// writing up the van crew picks somebody else; the tech logging their own
	// stint leaves it alone.
	staffID := parseOptionalUUID(r.FormValue("staff_id"))
	if staffID == nil {
		if staff, ok := auth.StaffFromContext(ctx); ok {
			id := staff.ID
			staffID = &id
		}
	}
	if staffID == nil {
		Error(w, r, app.ErrPermissionDenied)
		return
	}

	params := app.LogTimeParams{
		TicketID: ticketID,
		StaffID:  *staffID,
		Kind:     domain.ServiceTimeKind(r.FormValue("kind")),
		Minutes:  minutes,
		Billable: r.FormValue("billable") == "1",
		Note:     r.FormValue("note"),
	}
	if on := parseOptionalDate(r.FormValue("performed_on")); on != nil {
		params.PerformedOn = *on
	}

	var entry *domain.ServiceTimeEntry
	if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		entry, txErr = d.ServiceTicketService.LogTime(ctx, tx, params, staffActor(r))
		return txErr
	}); err != nil {
		Error(w, r, err)
		return
	}

	redirectFlash(w, r, ticketPath(ticketID), formatTimeFlash(entry))
}

// handleAdminServiceTimeDelete removes a logged stint.
func (d *Deps) handleAdminServiceTimeDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ticketID, entryID, ok := d.ticketAndChildID(w, r)
	if !ok {
		return
	}

	if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.ServiceTicketService.RemoveTimeEntry(ctx, tx, ticketID, entryID, staffActor(r))
	}); err != nil {
		Error(w, r, err)
		return
	}

	redirectFlash(w, r, ticketPath(ticketID), "Time entry removed.")
}

// --- helpers ---

func ticketPath(id uuid.UUID) string {
	return "/admin/service/tickets/" + id.String()
}

// ticketAndChildID parses both ids off a sub-record route. Whether the child
// actually belongs to that ticket is checked in the service, which is where the
// database is — this only rejects the ids that are not ids.
func (d *Deps) ticketAndChildID(w http.ResponseWriter, r *http.Request) (ticketID, childID uuid.UUID, ok bool) {
	ticketID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		Error(w, r, app.ErrServiceTicketNotFound)
		return uuid.Nil, uuid.Nil, false
	}
	childID, err = uuid.Parse(r.PathValue("childID"))
	if err != nil {
		Error(w, r, app.ErrServicePartNotFound)
		return uuid.Nil, uuid.Nil, false
	}
	return ticketID, childID, true
}

func formatTimeFlash(e *domain.ServiceTimeEntry) string {
	unit := "minutes"
	if e.Minutes == 1 {
		unit = "minute"
	}
	if e.Billable {
		return strconv.Itoa(e.Minutes) + " billable " + unit + " logged."
	}
	return strconv.Itoa(e.Minutes) + " " + unit + " logged."
}

// handleAdminServiceTimeReprice sets what one recorded hour cost.
//
// The deliberate counterpart to the rate snapshot: a settings change no longer
// re-prices past work, so an hour logged before the shop had a rate — or at one
// since decided wrong — is corrected here, one row at a time and audited.
func (d *Deps) handleAdminServiceTimeReprice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ticketID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		Error(w, r, app.ErrServiceTicketNotFound)
		return
	}
	entryID, err := uuid.Parse(r.PathValue("childID"))
	if err != nil {
		Error(w, r, app.ErrServiceTimeEntryNotFound)
		return
	}
	if err := r.ParseForm(); err != nil {
		Error(w, r, err)
		return
	}

	// Blank returns the hour to unpriced, which is how a wrongly-costed one is
	// taken back out of the money figures without deleting the hours.
	rate, err := parseOptionalRate(r.FormValue("rate"))
	if err != nil {
		redirectFlashError(w, r, ticketPath(ticketID), "That is not an hourly rate.")
		return
	}

	var entry *domain.ServiceTimeEntry
	if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		entry, txErr = d.ServiceTicketService.RepriceTimeEntry(ctx, tx, ticketID, entryID, rate, staffActor(r))
		return txErr
	}); err != nil {
		// A rate out of range is a typo, not a crash: say so on the ticket the
		// operator is standing on rather than replacing it with an error page.
		d.redirectOrFail(w, r, ticketPath(ticketID), err)
		return
	}

	if !entry.Costed() {
		redirectFlash(w, r, ticketPath(ticketID), "Hour left unpriced — its minutes still count, its cost does not.")
		return
	}
	redirectFlash(w, r, ticketPath(ticketID),
		fmt.Sprintf("Priced at %s an hour — %s.", formatCentsPlain(*entry.RateCents), formatCentsPlain(entry.CostCents())))
}

// formatCentsPlain renders cents as dollars for a flash message.
func formatCentsPlain(cents int) string {
	return fmt.Sprintf("$%d.%02d", cents/100, cents%100)
}
