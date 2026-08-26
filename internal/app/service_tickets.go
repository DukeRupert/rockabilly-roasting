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
}

// NewServiceTicketService creates a new ServiceTicketService.
func NewServiceTicketService(tickets *store.ServiceTicketStore, equipment *store.EquipmentStore, auditWriter *audit.AuditWriter) *ServiceTicketService {
	return &ServiceTicketService{tickets: tickets, equipment: equipment, audit: auditWriter}
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
