package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
)

// ServiceTicketService owns the repair workflow: opening a ticket, moving it
// through its states, and — the part that earns the module — keeping track of
// when anybody last spoke to the customer about it.
//
// Part of the equipment service module; see docs/equipment-service-module.md.
// As with EquipmentService, nothing here re-asks whether the module is on: the
// router decided that once, before the request got this far.
type ServiceTicketService struct {
	tickets   *store.ServiceTicketStore
	equipment *store.EquipmentStore
	audit     *audit.AuditWriter

	// Set by WithNotifications, and used only by the jobs that send mail — the
	// daily stale sweep and the ticket-opened notice. Nil on a service built
	// for request handling, which is why both check before reaching for any of
	// them.
	email     EmailEnv
	customers *store.CustomerStore
	modules   *ModuleService
	metrics   *metrics.Registry

	// Set by WithSettings. Holds the labour rates the cost reports cost hours
	// at; nil-safe, and a service without it reports hours and parts with no
	// money column — which is also what happens when the rate is simply unset.
	settings *store.SettingsStore
}

// NewServiceTicketService creates a new ServiceTicketService.
func NewServiceTicketService(tickets *store.ServiceTicketStore, equipment *store.EquipmentStore, auditWriter *audit.AuditWriter) *ServiceTicketService {
	return &ServiceTicketService{tickets: tickets, equipment: equipment, audit: auditWriter}
}

// WithNotifications attaches what the module's background jobs need: somewhere
// to send staff mail, customer names to put in it, the module registry to check
// whether this instance runs the module at all, and the metrics to publish.
// Must be called before SweepStaleTickets or SendTicketOpenedNotice; safe to
// call at wiring time.
//
// Separate from the constructor because every request-path caller needs none of
// it — only the workers read any of these.
func (s *ServiceTicketService) WithNotifications(env EmailEnv, customers *store.CustomerStore, modules *ModuleService, m *metrics.Registry) *ServiceTicketService {
	s.email = env
	s.customers = customers
	s.modules = modules
	s.metrics = m
	return s
}

// WithSettings attaches the store-wide settings the cost reports read their
// labour rates from.
//
// Separate from WithNotifications because the two have nothing to do with each
// other: one is what the background jobs need, this is what a report needs, and
// a caller wiring only one should not have to supply the other's collaborators.
func (s *ServiceTicketService) WithSettings(settings *store.SettingsStore) *ServiceTicketService {
	s.settings = settings
	return s
}

// LaborRates is what an hour of the crew's time costs the shop.
//
// Unset rates are not an error — they are the ordinary state of a shop that has
// not decided, and every surface that would show money hides it instead.
func (s *ServiceTicketService) LaborRates(ctx context.Context, tx pgx.Tx) (domain.ServiceLaborRates, error) {
	if s.settings == nil {
		return domain.ServiceLaborRates{}, nil
	}
	return s.settings.GetServiceLaborRates(ctx, tx)
}

// SetLaborRates records what the crew's time costs.
//
// The outgoing rates go into the audit metadata: every cost figure the reports
// have ever shown was computed from whatever was set at the time, so "why did
// this account's cost jump in March" is a question only the change history can
// answer.
func (s *ServiceTicketService) SetLaborRates(ctx context.Context, tx pgx.Tx, rates domain.ServiceLaborRates, actor Actor) error {
	if s.settings == nil {
		return fmt.Errorf("service labor rates: settings store not wired")
	}
	if err := validateLaborRates(rates); err != nil {
		return err
	}

	previous, err := s.settings.GetServiceLaborRates(ctx, tx)
	if err != nil {
		return err
	}
	if err := s.settings.UpdateServiceLaborRates(ctx, tx, rates); err != nil {
		return err
	}

	return s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditServiceLaborRatesUpdated,
		ResourceType: "store_settings",
		Metadata: map[string]any{
			"labor_rate_cents":      rateForAudit(rates.LaborCentsPerHour),
			"travel_rate_cents":     rateForAudit(rates.TravelCentsPerHour),
			"was_labor_rate_cents":  rateForAudit(previous.LaborCentsPerHour),
			"was_travel_rate_cents": rateForAudit(previous.TravelCentsPerHour),
		},
	})
}

// maxLaborRateCents caps an hourly rate at $10,000. Not a business rule — a
// guard against somebody typing cents into a dollars field and putting a
// six-figure number on every account in the report.
const maxLaborRateCents = 1_000_000

// validateLaborRates guards the two fields.
func validateLaborRates(rates domain.ServiceLaborRates) error {
	for _, r := range []*int{rates.LaborCentsPerHour, rates.TravelCentsPerHour} {
		if r == nil {
			continue
		}
		if *r < 0 || *r > maxLaborRateCents {
			return ErrLaborRateInvalid
		}
	}
	// A travel rate on its own costs nothing: travel falls back to the labour
	// rate, and with no labour rate there is no money column for it to appear
	// in. Saying so beats silently accepting a setting that does nothing.
	if rates.LaborCentsPerHour == nil && rates.TravelCentsPerHour != nil {
		return ErrTravelRateWithoutLabor
	}
	return nil
}

// rateForAudit renders a nullable rate for an audit record, where "unset" and
// "zero" have to stay distinguishable.
func rateForAudit(cents *int) any {
	if cents == nil {
		return nil
	}
	return *cents
}

// OpenTicketParams is the input for raising a ticket.
type OpenTicketParams struct {
	CustomerID  uuid.UUID
	EquipmentID *uuid.UUID
	AddressID   *uuid.UUID
	Title       string
	Description string
	Severity    domain.ServiceSeverity
	// Exactly one of these identifies who raised it. Both nil means a job did.
	OpenedByStaffID        *uuid.UUID
	OpenedByCustomerUserID *uuid.UUID
	AssignedStaffID        *uuid.UUID
	ScheduledFor           *time.Time
	Billable               bool
}

// Open raises a ticket.
//
// When it names a machine, the machine's address is inherited unless one was
// given — a tech reading the ticket needs to know which shop to drive to, and
// re-typing it is how that goes wrong.
func (s *ServiceTicketService) Open(ctx context.Context, tx pgx.Tx, p OpenTicketParams, actor Actor) (*domain.ServiceTicket, error) {
	p.Title = strings.TrimSpace(p.Title)
	p.Description = strings.TrimSpace(p.Description)

	if p.Title == "" {
		return nil, ErrServiceTicketTitleRequired
	}
	if p.Severity == "" {
		p.Severity = domain.ServiceSeverityRoutine
	}
	if !p.Severity.Valid() {
		return nil, ErrInvalidServiceSeverity
	}

	if p.EquipmentID != nil {
		machine, err := s.equipment.GetByID(ctx, tx, *p.EquipmentID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrEquipmentNotFound
			}
			return nil, err
		}
		// One cafe's repair history must not end up on another's page.
		if machine.CustomerID != p.CustomerID {
			return nil, ErrTicketEquipmentMismatch
		}
		if p.AddressID == nil {
			p.AddressID = machine.AddressID
		}
	}

	ticket, err := s.tickets.Create(ctx, tx, store.CreateServiceTicketParams{
		Number:                 generateTicketNumber(),
		CustomerID:             p.CustomerID,
		EquipmentID:            p.EquipmentID,
		AddressID:              p.AddressID,
		Title:                  p.Title,
		Description:            p.Description,
		Severity:               p.Severity,
		OpenedByStaffID:        p.OpenedByStaffID,
		OpenedByCustomerUserID: p.OpenedByCustomerUserID,
		AssignedStaffID:        p.AssignedStaffID,
		ScheduledFor:           p.ScheduledFor,
		Billable:               p.Billable,
	})
	if err != nil {
		return nil, err
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditServiceTicketOpened,
		ResourceType: "service_ticket",
		ResourceID:   ticket.ID,
		Metadata: map[string]any{
			"number":      ticket.Number,
			"customer_id": ticket.CustomerID.String(),
			"severity":    string(ticket.Severity),
			"title":       ticket.Title,
		},
	}); err != nil {
		return nil, fmt.Errorf("audit service ticket opened: %w", err)
	}

	return ticket, nil
}

// SetStatus moves a ticket through the workflow.
func (s *ServiceTicketService) SetStatus(ctx context.Context, tx pgx.Tx, id uuid.UUID, status domain.ServiceTicketStatus, resolution string, actor Actor) (*domain.ServiceTicket, error) {
	if !status.Valid() {
		return nil, ErrInvalidServiceTicketStatus
	}

	before, err := s.GetByID(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if before.Status == status {
		return before, nil
	}

	ticket, err := s.tickets.UpdateStatus(ctx, tx, id, status, strings.TrimSpace(resolution))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrServiceTicketNotFound
		}
		return nil, err
	}

	// Three named actions out of seven states: the ones whose timeline entry
	// deserves its own colour. Everything else is a status change with the
	// detail in metadata.
	action := audit.AuditServiceTicketStatus
	switch {
	case status == domain.ServiceTicketStatusResolved:
		action = audit.AuditServiceTicketResolved
	case status == domain.ServiceTicketStatusCancelled:
		action = audit.AuditServiceTicketCancelled
	case !before.Status.Open() && status.Open():
		action = audit.AuditServiceTicketReopened
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       action,
		ResourceType: "service_ticket",
		ResourceID:   ticket.ID,
		Metadata: map[string]any{
			"number": ticket.Number,
			"from":   string(before.Status),
			"to":     string(ticket.Status),
		},
	}); err != nil {
		return nil, fmt.Errorf("audit service ticket status: %w", err)
	}

	return ticket, nil
}

// Assign sets or clears the staff member responsible.
func (s *ServiceTicketService) Assign(ctx context.Context, tx pgx.Tx, id uuid.UUID, staffID *uuid.UUID, staffName string, actor Actor) (*domain.ServiceTicket, error) {
	ticket, err := s.tickets.Assign(ctx, tx, id, staffID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrServiceTicketNotFound
		}
		return nil, err
	}

	assignedTo := "nobody"
	if staffName != "" {
		assignedTo = staffName
	}
	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditServiceTicketAssigned,
		ResourceType: "service_ticket",
		ResourceID:   ticket.ID,
		Metadata: map[string]any{
			"number":      ticket.Number,
			"assigned_to": assignedTo,
		},
	}); err != nil {
		return nil, fmt.Errorf("audit service ticket assigned: %w", err)
	}

	return ticket, nil
}

// AddNoteParams is the input for one timeline entry.
type AddNoteParams struct {
	TicketID        uuid.UUID
	Kind            domain.ServiceNoteKind
	Body            string
	OccurredAt      time.Time
	StaffID         *uuid.UUID
	CustomerUserID  *uuid.UUID
	CustomerVisible bool
}

// AddNote records something that was said or done, and moves the ticket's
// last-contact clock when the entry is a communication.
//
// The clock is the reason this method is not just a store call. Which kinds
// count is domain.ServiceNoteKind.IsContact — an internal working note
// deliberately does not, because "chased the supplier again" written to
// yourself is not telling the cafe anything, and letting it reset the clock
// would silence the flag in exactly the case it exists to catch.
func (s *ServiceTicketService) AddNote(ctx context.Context, tx pgx.Tx, p AddNoteParams, actor Actor) (*domain.ServiceTicketNote, error) {
	p.Body = strings.TrimSpace(p.Body)
	if p.Body == "" {
		return nil, ErrEmptyServiceNote
	}
	if !p.Kind.Valid() {
		return nil, ErrInvalidServiceNoteKind
	}

	ticket, err := s.GetByID(ctx, tx, p.TicketID)
	if err != nil {
		return nil, err
	}

	note, err := s.tickets.CreateNote(ctx, tx, store.CreateNoteParams{
		TicketID:        p.TicketID,
		Kind:            p.Kind,
		Body:            p.Body,
		OccurredAt:      p.OccurredAt,
		StaffID:         p.StaffID,
		CustomerUserID:  p.CustomerUserID,
		CustomerVisible: p.CustomerVisible,
	})
	if err != nil {
		return nil, err
	}

	if p.Kind.IsContact() {
		// TouchContact only moves the clock forward, so Tuesday's call written
		// up on Thursday cannot drag it backwards.
		if err := s.tickets.TouchContact(ctx, tx, p.TicketID, note.OccurredAt); err != nil {
			return nil, err
		}
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditServiceTicketNoteAdded,
		ResourceType: "service_ticket",
		ResourceID:   ticket.ID,
		Metadata: map[string]any{
			"number":           ticket.Number,
			"kind":             string(note.Kind),
			"is_contact":       note.Kind.IsContact(),
			"customer_visible": note.CustomerVisible,
		},
	}); err != nil {
		return nil, fmt.Errorf("audit service ticket note: %w", err)
	}

	return note, nil
}

// GetByID returns one ticket. Staff only.
func (s *ServiceTicketService) GetByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.ServiceTicket, error) {
	ticket, err := s.tickets.GetByID(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrServiceTicketNotFound
		}
		return nil, err
	}
	return ticket, nil
}

// Get returns one ticket belonging to a customer — the portal's read. A ticket
// that is not theirs is reported as missing, not as forbidden.
func (s *ServiceTicketService) Get(ctx context.Context, tx pgx.Tx, id, customerID uuid.UUID) (*domain.ServiceTicket, error) {
	ticket, err := s.tickets.Get(ctx, tx, id, customerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrServiceTicketNotFound
		}
		return nil, err
	}
	return ticket, nil
}

// List returns tickets matching the filter.
func (s *ServiceTicketService) List(ctx context.Context, tx pgx.Tx, f store.ServiceTicketFilter) ([]domain.ServiceTicket, error) {
	return s.tickets.List(ctx, tx, f)
}

// ListStale returns the open tickets nobody has spoken to the customer about
// since the cutoff, quietest first.
func (s *ServiceTicketService) ListStale(ctx context.Context, tx pgx.Tx, cutoff time.Time, limit int) ([]domain.ServiceTicket, error) {
	return s.tickets.List(ctx, tx, store.ServiceTicketFilter{StaleBefore: &cutoff, Limit: limit})
}

// ListNotes returns a ticket's timeline entries. customerVisibleOnly is what
// the portal passes.
func (s *ServiceTicketService) ListNotes(ctx context.Context, tx pgx.Tx, ticketID uuid.UUID, customerVisibleOnly bool) ([]domain.ServiceTicketNote, error) {
	return s.tickets.ListNotes(ctx, tx, ticketID, customerVisibleOnly)
}

// LastServiceByEquipment returns when work on each of a customer's machines was
// last finished, keyed by equipment id. Machines never serviced are absent.
func (s *ServiceTicketService) LastServiceByEquipment(ctx context.Context, tx pgx.Tx, customerID uuid.UUID) (map[uuid.UUID]time.Time, error) {
	return s.tickets.LastServiceByEquipment(ctx, tx, customerID)
}

// Totals rolls up a ticket's parts cost and logged hours.
func (s *ServiceTicketService) Totals(ctx context.Context, tx pgx.Tx, ticketID uuid.UUID) (domain.ServiceTotals, error) {
	return s.tickets.Totals(ctx, tx, ticketID)
}

// generateTicketNumber mints the human handle for a ticket.
//
// Deliberately the same shape as generateOrderNumber: random rather than
// sequential, so it leaks no volume and needs no counter, and UNIQUE in the
// database so a re-run or an import cannot collide. Ten hex characters is not
// something anyone will read down a phone — but a second numbering scheme in
// one codebase costs more than that is worth, and staff click the row.
func generateTicketNumber() string {
	b := make([]byte, 5)
	if _, err := rand.Read(b); err != nil {
		// Same fallback as orders: a timestamp is still unique enough to insert.
		return fmt.Sprintf("SVC-%d", time.Now().UnixMilli())
	}
	return "SVC-" + strings.ToUpper(hex.EncodeToString(b))
}

// serviceCostWindows are the three periods every roll-up reports.
//
// Three at once rather than a period selector. The question is never "what did
// the last quarter cost" on its own — it is whether this machine is getting
// worse, and that is a comparison. A toggle would make somebody click three
// times and hold the numbers in their head to answer it.
//
// 90 days is the quarter the merchant actually asks about, 12 months absorbs
// the seasonality of a cafe, and all-time is the argument for replacing a
// machine.
var serviceCostWindows = []struct {
	label string
	days  int
}{
	{"Last 90 days", 90},
	{"Last 12 months", 365},
	{"All time", 0},
}

// CostForEquipment rolls up what one machine has cost, over each window.
func (s *ServiceTicketService) CostForEquipment(ctx context.Context, tx pgx.Tx, equipmentID uuid.UUID, now time.Time) ([]domain.ServiceCostWindow, error) {
	return s.costWindows(ctx, tx, store.ServiceCostFilter{EquipmentID: &equipmentID}, now)
}

// CostForCustomer rolls up what one account has cost, over each window. It
// includes work on tickets that never named a machine — those hours went into
// the account just the same.
func (s *ServiceTicketService) CostForCustomer(ctx context.Context, tx pgx.Tx, customerID uuid.UUID, now time.Time) ([]domain.ServiceCostWindow, error) {
	return s.costWindows(ctx, tx, store.ServiceCostFilter{CustomerID: &customerID}, now)
}

// costWindows runs the roll-up once per window.
//
// The windows are measured from a calendar day rather than an instant, so the
// numbers on a card do not shift between two refreshes of the same page — and
// so a test can ask what the card said on a given morning.
func (s *ServiceTicketService) costWindows(ctx context.Context, tx pgx.Tx, f store.ServiceCostFilter, now time.Time) ([]domain.ServiceCostWindow, error) {
	today := now.UTC().Truncate(24 * time.Hour)

	out := make([]domain.ServiceCostWindow, 0, len(serviceCostWindows))
	for _, w := range serviceCostWindows {
		scoped := f
		if w.days > 0 {
			scoped.Since = today.AddDate(0, 0, -w.days)
		}
		summary, err := s.tickets.CostSummary(ctx, tx, scoped)
		if err != nil {
			return nil, err
		}
		out = append(out, domain.ServiceCostWindow{Label: w.label, Since: scoped.Since, Summary: summary})
	}
	return out, nil
}

// accountCostLimit caps the cross-account table. Rows only exist for accounts
// that had work in the window, so this is far above any real result — it is a
// guard against a page rendering ten thousand rows, not a business rule. When
// it bites, the caller is told rather than shown a silently short table.
const accountCostLimit = 200

// CostByAccount ranks accounts by what servicing them has taken over a window.
//
// sinceDays of zero means all time. The window is measured from a calendar day
// so the table does not shift between two refreshes of the same page.
func (s *ServiceTicketService) CostByAccount(ctx context.Context, tx pgx.Tx, sinceDays int, sort domain.ServiceAccountCostSort, now time.Time) (domain.ServiceAccountReport, error) {
	rates, err := s.LaborRates(ctx, tx)
	if err != nil {
		return domain.ServiceAccountReport{}, err
	}

	report := domain.ServiceAccountReport{Sort: sort, Rates: rates}
	if sinceDays > 0 {
		report.Since = now.UTC().Truncate(24*time.Hour).AddDate(0, 0, -sinceDays)
	}

	rows, err := s.tickets.CostByCustomer(ctx, tx, store.ServiceAccountCostFilter{
		Since: report.Since,
		Sort:  sort,
		// One over the cap, so a full page can be told from an overflowing one
		// without a second counting query.
		Limit: accountCostLimit + 1,
	})
	if err != nil {
		return domain.ServiceAccountReport{}, err
	}

	if len(rows) > accountCostLimit {
		rows = rows[:accountCostLimit]
		report.Truncated = true
	}
	report.Rows = rows

	for _, r := range rows {
		report.Total.PartsCostCents += r.Summary.PartsCostCents
		report.Total.PartCount += r.Summary.PartCount
		report.Total.LaborMinutes += r.Summary.LaborMinutes
		report.Total.TravelMinutes += r.Summary.TravelMinutes
		report.Total.BillableMinutes += r.Summary.BillableMinutes
		report.Total.LaborCostCents += r.Summary.LaborCostCents
		report.Total.UncostedMinutes += r.Summary.UncostedMinutes
		report.Total.Visits += r.Summary.Visits
	}

	return report, nil
}
