package web

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/auth"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/storefront"
)

// The wholesale portal's half of the equipment service module
// (/wholesale/account/equipment): the machines we look after for this cafe, and
// the form that turns a 6am phone call into a ticket.
//
// These routes sit behind requireApprovedWholesale *and* requireModule (see
// router.go), so every handler here can assume an approved wholesale account is
// in context and that this shop actually runs the module. Every read is scoped
// by the customer from that session — EquipmentStore.Get and
// ServiceTicketStore.Get take a customerID for exactly this reason, and the
// unscoped GetByID variants must never appear in this file.

// openTicketDisplayLimit caps how many live tickets the page draws. A cafe with
// more than this many open at once has a relationship problem that a longer
// list will not fix.
const openTicketDisplayLimit = 25

// --- Equipment list ---

func (d *Deps) handleWholesaleEquipment(w http.ResponseWriter, r *http.Request) {
	d.renderWholesaleEquipment(w, r, storefront.WholesaleEquipmentProps{
		Success: r.URL.Query().Get("success"),
		Error:   r.URL.Query().Get("error"),
	})
}

// renderWholesaleEquipment loads the machines, their last service date, and
// every open ticket against them, then renders the page.
//
// One pass of four queries rather than a per-machine walk: the equipment list,
// the grouped last-service dates, the open tickets, and then the customer-visible
// notes for each of those tickets. Only the last is per-row, and it is bounded
// by openTicketDisplayLimit.
func (d *Deps) renderWholesaleEquipment(w http.ResponseWriter, r *http.Request, overrides storefront.WholesaleEquipmentProps) {
	ctx := r.Context()
	customer, _ := auth.CustomerFromContext(ctx)

	var (
		machines    []domain.Equipment
		lastService map[uuid.UUID]time.Time
		tickets     []domain.ServiceTicket
		notes       map[uuid.UUID][]domain.ServiceTicketNote
	)

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		machines, txErr = d.EquipmentService.ListForCustomer(ctx, tx, customer.ID)
		if txErr != nil {
			return txErr
		}
		lastService, txErr = d.ServiceTicketService.LastServiceByEquipment(ctx, tx, customer.ID)
		if txErr != nil {
			return txErr
		}
		tickets, txErr = d.ServiceTicketService.List(ctx, tx, store.ServiceTicketFilter{
			CustomerID: &customer.ID,
			OpenOnly:   true,
			Limit:      openTicketDisplayLimit,
		})
		if txErr != nil {
			return txErr
		}
		notes = make(map[uuid.UUID][]domain.ServiceTicketNote, len(tickets))
		for _, t := range tickets {
			// customerVisibleOnly — the internal working notes on a ticket are
			// staff's alone, and this is the one call site where forgetting
			// that would put them in front of the cafe.
			list, err := d.ServiceTicketService.ListNotes(ctx, tx, t.ID, true)
			if err != nil {
				return err
			}
			notes[t.ID] = list
		}
		return nil
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	props := overrides
	props.Customer = customer
	props.CompanyName = wholesaleCompanyName(customer)
	props.CartCount = d.wholesaleCartItemCount(r)
	props.Machines, props.OtherTickets = groupMachineTickets(machines, lastService, tickets, notes)

	if IsHTMX(r) {
		storefront.WholesaleEquipmentContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	storefront.WholesaleEquipmentPage(props).Render(ctx, w) //nolint:errcheck
}

// groupMachineTickets files each open ticket under the machine it is about, and
// returns the ones that name no machine separately.
//
// Staff can raise a ticket against an account without picking a machine, and
// those must still be visible here — a customer who cannot see the ticket they
// were told about will ring up to ask about it, which is the phone call this
// whole page exists to prevent.
func groupMachineTickets(
	machines []domain.Equipment,
	lastService map[uuid.UUID]time.Time,
	tickets []domain.ServiceTicket,
	notes map[uuid.UUID][]domain.ServiceTicketNote,
) ([]storefront.WholesaleMachineView, []storefront.WholesaleTicketView) {
	views := make([]storefront.WholesaleMachineView, 0, len(machines))
	index := make(map[uuid.UUID]int, len(machines))

	for _, m := range machines {
		v := storefront.WholesaleMachineView{Equipment: m}
		if at, ok := lastService[m.ID]; ok {
			at := at
			v.LastServiced = &at
		}
		index[m.ID] = len(views)
		views = append(views, v)
	}

	var orphans []storefront.WholesaleTicketView
	for _, t := range tickets {
		view := storefront.WholesaleTicketView{Ticket: t, Notes: notes[t.ID]}
		// A ticket against a retired machine has no card to sit on —
		// ListForCustomer leaves retired machines out — so it falls through to
		// the same place as one that never named a machine at all.
		if t.EquipmentID != nil {
			if i, ok := index[*t.EquipmentID]; ok {
				views[i].OpenTickets = append(views[i].OpenTickets, view)
				continue
			}
		}
		orphans = append(orphans, view)
	}
	return views, orphans
}

// --- Report a problem ---

// handleWholesaleEquipmentReport opens a ticket from the customer's report.
//
// Everything lands in one transaction: the ticket, the note carrying their own
// words, and the job that mails the crew. If the enqueue fails the ticket rolls
// back with it, because a report nobody is told about is worse than a report
// that visibly did not go through.
func (d *Deps) handleWholesaleEquipmentReport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customer, _ := auth.CustomerFromContext(ctx)

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		equipmentRedirect(w, r, "error", "That machine could not be found.")
		return
	}
	if err := r.ParseForm(); err != nil {
		Error(w, r, err)
		return
	}

	description := strings.TrimSpace(r.FormValue("description"))
	bestTime := strings.TrimSpace(r.FormValue("best_time"))
	down := r.FormValue("down") == "1"

	if description == "" {
		d.renderWholesaleEquipment(w, r, storefront.WholesaleEquipmentProps{
			Error:       "Tell us what the machine is doing — even one line helps.",
			ReportingID: id.String(),
			ReportDown:  down,
			ReportTime:  bestTime,
		})
		return
	}

	severity := domain.ServiceSeverityDegraded
	if down {
		severity = domain.ServiceSeverityDown
	}

	// The best time to reach them belongs in the ticket body, not in a column:
	// it is true of this report and not of the machine, and a tech reading the
	// ticket on their phone needs it in the same block of text as the fault.
	body := description
	if bestTime != "" {
		body += "\n\nBest time to reach us: " + bestTime
	}

	// The invited teammate who filed it, where there is one. Nil for the
	// account's primary sign-in, which is not a customer_users row at all — see
	// the multi-login seam.
	var openedBy *uuid.UUID
	if actingUser, ok := auth.CustomerUserFromContext(ctx); ok {
		openedBy = &actingUser.ID
	}

	var ticket *domain.ServiceTicket
	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		// scoping: the customer-scoped Get is the ownership check. A machine
		// belonging to another cafe reads as missing, not as forbidden.
		machine, txErr := d.EquipmentService.Get(ctx, tx, id, customer.ID)
		if txErr != nil {
			return txErr
		}

		ticket, txErr = d.ServiceTicketService.Open(ctx, tx, app.OpenTicketParams{
			CustomerID:  customer.ID,
			EquipmentID: &machine.ID,
			// Nobody is going to make a cafe write a subject line at 6am, so
			// the title is built from what they did tell us. It is what the
			// queue shows as a column of one-liners.
			Title:                  machine.Description() + " — " + strings.ToLower(severity.Label()),
			Description:            body,
			Severity:               severity,
			OpenedByCustomerUserID: openedBy,
		}, customerActor(r))
		if txErr != nil {
			return txErr
		}

		// Customer-visible on purpose: showing their own words back on the page
		// is how "did they get my message" answers itself without a phone call.
		// customer_report also counts as contact, so the staleness clock starts
		// from the report rather than from silence.
		if _, txErr = d.ServiceTicketService.AddNote(ctx, tx, app.AddNoteParams{
			TicketID:        ticket.ID,
			Kind:            domain.ServiceNoteKindCustomerReport,
			Body:            body,
			CustomerUserID:  openedBy,
			CustomerVisible: true,
		}, customerActor(r)); txErr != nil {
			return txErr
		}

		return d.Enqueuer.EnqueueServiceTicketOpened(ctx, tx, ticket.ID, down)
	})
	if err != nil {
		if msg, ok := reportErrorMessage(err); ok {
			equipmentRedirect(w, r, "error", msg)
			return
		}
		Error(w, r, err)
		return
	}

	// After the commit, never inside it.
	d.Metrics.ServiceTicketsOpened.WithLabelValues("customer", string(severity)).Inc()

	msg := "Thanks — " + ticket.Number + " is with the crew. We'll be in touch."
	if down {
		msg = "Got it — " + ticket.Number + " is flagged as down and the crew have been paged."
	}
	equipmentRedirect(w, r, "success", msg)
}

// equipmentRedirect sends the browser back to the equipment page with a flash
// message. POSTs redirect rather than re-render so a refresh cannot file the
// same report twice.
func equipmentRedirect(w http.ResponseWriter, r *http.Request, key, msg string) {
	target := "/wholesale/account/equipment?" + key + "=" + url.QueryEscape(msg)
	if IsHTMX(r) {
		w.Header().Set("HX-Redirect", target)
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// reportErrorMessage maps the expected failures to copy for the page. Returns
// ok=false for anything unexpected, which the caller surfaces as a real error
// rather than reassuring the customer that a broken thing worked.
func reportErrorMessage(err error) (string, bool) {
	switch {
	case errors.Is(err, app.ErrEquipmentNotFound), errors.Is(err, app.ErrTicketEquipmentMismatch):
		return "That machine could not be found on your account.", true
	case errors.Is(err, app.ErrEmptyServiceNote), errors.Is(err, app.ErrServiceTicketTitleRequired):
		return "Tell us what the machine is doing — even one line helps.", true
	default:
		return "", false
	}
}
