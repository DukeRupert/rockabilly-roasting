package app

import (
	"context"
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

// Parts and hours on a service ticket — the two halves of "what did this repair
// actually cost". Methods hang off ServiceTicketService because neither is
// meaningful apart from its ticket.
//
// Part of the equipment service module; see docs/equipment-service-module.md.

// AddPartParams is the input for putting a part on a ticket.
type AddPartParams struct {
	TicketID uuid.UUID
	// VariantID links the line to the catalog for the minority of shops that
	// stock common parts. Nothing in the admin sets it yet — see AddPart.
	VariantID     *uuid.UUID
	Name          string
	PartNumber    string
	Supplier      string
	Quantity      int
	UnitCostCents int
}

// AddPart puts a part on a ticket, in status needed.
//
// The catalog link stays unused for now, deliberately. Most parts are ordered
// ad hoc from a distributor and will never be a product here, and demanding a
// catalog entry before you can write down "group head gasket, $4" guarantees
// nobody writes it down. The column is there for the shop that stocks its own;
// wiring a picker — and the stock decrement that would have to come with it —
// waits until one asks.
func (s *ServiceTicketService) AddPart(ctx context.Context, tx pgx.Tx, p AddPartParams, actor Actor) (*domain.ServicePart, error) {
	p.Name = strings.TrimSpace(p.Name)
	p.PartNumber = strings.TrimSpace(p.PartNumber)
	p.Supplier = strings.TrimSpace(p.Supplier)

	if p.Name == "" {
		return nil, ErrPartNameRequired
	}
	if p.Quantity < 1 {
		return nil, ErrInvalidPartQuantity
	}
	if p.UnitCostCents < 0 {
		return nil, ErrInvalidPartCost
	}

	ticket, err := s.GetByID(ctx, tx, p.TicketID)
	if err != nil {
		return nil, err
	}

	part, err := s.tickets.CreatePart(ctx, tx, store.CreatePartParams{
		TicketID:      p.TicketID,
		VariantID:     p.VariantID,
		Name:          p.Name,
		PartNumber:    p.PartNumber,
		Supplier:      p.Supplier,
		Quantity:      p.Quantity,
		UnitCostCents: p.UnitCostCents,
	})
	if err != nil {
		return nil, err
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditServicePartAdded,
		ResourceType: "service_ticket",
		ResourceID:   ticket.ID,
		Metadata: map[string]any{
			"number":     ticket.Number,
			"part":       part.Name,
			"quantity":   part.Quantity,
			"cost_cents": part.TotalCostCents(),
		},
	}); err != nil {
		return nil, fmt.Errorf("audit service part added: %w", err)
	}

	return part, nil
}

// SetPartStatus advances a part — needed, ordered, received, installed.
//
// on is the day it happened; zero means today. The store stamps the date that
// belongs to the new status and keeps the earlier ones, so a fitted part still
// remembers when it was ordered — which is the answer to "why did this repair
// take three weeks".
func (s *ServiceTicketService) SetPartStatus(ctx context.Context, tx pgx.Tx, ticketID, partID uuid.UUID, status domain.ServicePartStatus, on time.Time, actor Actor) (*domain.ServicePart, error) {
	if !status.Valid() {
		return nil, ErrInvalidPartStatus
	}

	ticket, err := s.GetByID(ctx, tx, ticketID)
	if err != nil {
		return nil, err
	}
	before, err := s.partOnTicket(ctx, tx, ticketID, partID)
	if err != nil {
		return nil, err
	}
	if before.Status == status {
		return before, nil
	}

	part, err := s.tickets.UpdatePartStatus(ctx, tx, partID, status, on)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrServicePartNotFound
		}
		return nil, err
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditServicePartStatus,
		ResourceType: "service_ticket",
		ResourceID:   ticket.ID,
		Metadata: map[string]any{
			"number": ticket.Number,
			"part":   part.Name,
			"from":   string(before.Status),
			"to":     string(part.Status),
		},
	}); err != nil {
		return nil, fmt.Errorf("audit service part status: %w", err)
	}

	return part, nil
}

// RemovePart deletes a part line.
//
// For a mistyped entry. A part that was genuinely ordered and then cancelled is
// history worth keeping — leave it and say so in a note.
func (s *ServiceTicketService) RemovePart(ctx context.Context, tx pgx.Tx, ticketID, partID uuid.UUID, actor Actor) error {
	ticket, err := s.GetByID(ctx, tx, ticketID)
	if err != nil {
		return err
	}
	part, err := s.partOnTicket(ctx, tx, ticketID, partID)
	if err != nil {
		return err
	}

	if err := s.tickets.DeletePart(ctx, tx, partID); err != nil {
		return err
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditServicePartRemoved,
		ResourceType: "service_ticket",
		ResourceID:   ticket.ID,
		Metadata: map[string]any{
			"number":     ticket.Number,
			"part":       part.Name,
			"quantity":   part.Quantity,
			"cost_cents": part.TotalCostCents(),
		},
	}); err != nil {
		return fmt.Errorf("audit service part removed: %w", err)
	}
	return nil
}

// ListParts returns a ticket's parts, oldest first.
func (s *ServiceTicketService) ListParts(ctx context.Context, tx pgx.Tx, ticketID uuid.UUID) ([]domain.ServicePart, error) {
	return s.tickets.ListParts(ctx, tx, ticketID)
}

// partOnTicket fetches a part and checks it belongs to the ticket in the URL.
//
// Without this, a part id from one ticket could be posted to another's route
// and would happily update — an id from anywhere in the table is otherwise
// accepted, and the audit record would name the wrong ticket.
func (s *ServiceTicketService) partOnTicket(ctx context.Context, tx pgx.Tx, ticketID, partID uuid.UUID) (*domain.ServicePart, error) {
	parts, err := s.tickets.ListParts(ctx, tx, ticketID)
	if err != nil {
		return nil, err
	}
	for i := range parts {
		if parts[i].ID == partID {
			return &parts[i], nil
		}
	}
	return nil, ErrServicePartNotFound
}

// LogTimeParams is the input for recording a stint of work.
type LogTimeParams struct {
	TicketID uuid.UUID
	StaffID  uuid.UUID
	Kind     domain.ServiceTimeKind
	Minutes  int
	// PerformedOn is the day the work happened; zero means today.
	PerformedOn time.Time
	Billable    bool
	Note        string
}

// LogTime records minutes against a ticket.
func (s *ServiceTicketService) LogTime(ctx context.Context, tx pgx.Tx, p LogTimeParams, actor Actor) (*domain.ServiceTimeEntry, error) {
	if p.Minutes < 1 {
		return nil, ErrInvalidTimeMinutes
	}
	if p.Kind == "" {
		p.Kind = domain.ServiceTimeKindLabor
	}
	if !p.Kind.Valid() {
		return nil, ErrInvalidServiceTimeKind
	}

	ticket, err := s.GetByID(ctx, tx, p.TicketID)
	if err != nil {
		return nil, err
	}

	entry, err := s.tickets.CreateTimeEntry(ctx, tx, store.CreateTimeEntryParams{
		TicketID:    p.TicketID,
		StaffID:     p.StaffID,
		Kind:        p.Kind,
		Minutes:     p.Minutes,
		PerformedOn: p.PerformedOn,
		Billable:    p.Billable,
		Note:        strings.TrimSpace(p.Note),
	})
	if err != nil {
		return nil, err
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditServiceTimeLogged,
		ResourceType: "service_ticket",
		ResourceID:   ticket.ID,
		Metadata: map[string]any{
			"number":   ticket.Number,
			"minutes":  entry.Minutes,
			"kind":     string(entry.Kind),
			"billable": entry.Billable,
		},
	}); err != nil {
		return nil, fmt.Errorf("audit service time logged: %w", err)
	}

	return entry, nil
}

// RemoveTimeEntry deletes a logged stint. There is no edit: retyping ninety
// minutes is cheaper than an update path nobody can audit.
func (s *ServiceTicketService) RemoveTimeEntry(ctx context.Context, tx pgx.Tx, ticketID, entryID uuid.UUID, actor Actor) error {
	ticket, err := s.GetByID(ctx, tx, ticketID)
	if err != nil {
		return err
	}
	entry, err := s.timeEntryOnTicket(ctx, tx, ticketID, entryID)
	if err != nil {
		return err
	}

	if err := s.tickets.DeleteTimeEntry(ctx, tx, entryID); err != nil {
		return err
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditServiceTimeRemoved,
		ResourceType: "service_ticket",
		ResourceID:   ticket.ID,
		Metadata: map[string]any{
			"number":  ticket.Number,
			"minutes": entry.Minutes,
			"kind":    string(entry.Kind),
		},
	}); err != nil {
		return fmt.Errorf("audit service time removed: %w", err)
	}
	return nil
}

// ListTimeEntries returns a ticket's logged time, oldest first.
func (s *ServiceTicketService) ListTimeEntries(ctx context.Context, tx pgx.Tx, ticketID uuid.UUID) ([]domain.ServiceTimeEntry, error) {
	return s.tickets.ListTimeEntries(ctx, tx, ticketID)
}

// timeEntryOnTicket is timeEntry's half of partOnTicket — same reason.
func (s *ServiceTicketService) timeEntryOnTicket(ctx context.Context, tx pgx.Tx, ticketID, entryID uuid.UUID) (*domain.ServiceTimeEntry, error) {
	entries, err := s.tickets.ListTimeEntries(ctx, tx, ticketID)
	if err != nil {
		return nil, err
	}
	for i := range entries {
		if entries[i].ID == entryID {
			return &entries[i], nil
		}
	}
	return nil, ErrServiceTimeEntryNotFound
}
