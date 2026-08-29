package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/store"
)

// EquipmentService owns the register of machines a shop maintains for its
// customers — what is installed where, who owns it, and whether it is still in
// service.
//
// Part of the equipment service module; see docs/equipment-service-module.md.
// Nothing here checks whether the module is switched on: that is decided once,
// at the router, by requireModule. A service that re-asked would be answering a
// question its callers have already answered, and would tempt someone to skip
// the middleware.
type EquipmentService struct {
	equipment *store.EquipmentStore
	audit     *audit.AuditWriter
}

// NewEquipmentService creates a new EquipmentService.
func NewEquipmentService(equipment *store.EquipmentStore, auditWriter *audit.AuditWriter) *EquipmentService {
	return &EquipmentService{equipment: equipment, audit: auditWriter}
}

// RegisterEquipmentParams is the input for adding a machine to the register.
type RegisterEquipmentParams = store.CreateEquipmentParams

// EditEquipmentParams is the input for editing one.
type EditEquipmentParams = store.UpdateEquipmentParams

// Register adds a machine to the register.
func (s *EquipmentService) Register(ctx context.Context, tx pgx.Tx, p RegisterEquipmentParams, actor Actor) (*domain.Equipment, error) {
	p.Make = strings.TrimSpace(p.Make)
	p.Model = strings.TrimSpace(p.Model)
	p.SerialNumber = strings.TrimSpace(p.SerialNumber)

	if err := validateEquipment(p.Make, p.Category, p.Ownership); err != nil {
		return nil, err
	}

	equipment, err := s.equipment.Create(ctx, tx, p)
	if err != nil {
		return nil, err
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditEquipmentCreated,
		ResourceType: "equipment",
		ResourceID:   equipment.ID,
		Metadata: map[string]any{
			"customer_id": equipment.CustomerID.String(),
			"machine":     equipment.Description(),
			"category":    string(equipment.Category),
			"ownership":   string(equipment.Ownership),
			"serial":      equipment.SerialNumber,
		},
	}); err != nil {
		return nil, fmt.Errorf("audit equipment created: %w", err)
	}

	return equipment, nil
}

// Edit rewrites a machine's details.
func (s *EquipmentService) Edit(ctx context.Context, tx pgx.Tx, id uuid.UUID, p EditEquipmentParams, actor Actor) (*domain.Equipment, error) {
	p.Make = strings.TrimSpace(p.Make)
	p.Model = strings.TrimSpace(p.Model)
	p.SerialNumber = strings.TrimSpace(p.SerialNumber)

	if err := validateEquipment(p.Make, p.Category, p.Ownership); err != nil {
		return nil, err
	}

	before, err := s.GetByID(ctx, tx, id)
	if err != nil {
		return nil, err
	}

	equipment, err := s.equipment.Update(ctx, tx, id, p)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrEquipmentNotFound
		}
		return nil, err
	}

	// The before/after machine description is the useful half of the record:
	// a serial corrected or a loaner reclassified as customer-owned changes who
	// pays for the next repair, and that is worth being able to attribute.
	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditEquipmentUpdated,
		ResourceType: "equipment",
		ResourceID:   equipment.ID,
		Metadata: map[string]any{
			"machine":       equipment.Description(),
			"was":           before.Description(),
			"ownership":     string(equipment.Ownership),
			"was_ownership": string(before.Ownership),
			"serial":        equipment.SerialNumber,
			"was_serial":    before.SerialNumber,
		},
	}); err != nil {
		return nil, fmt.Errorf("audit equipment updated: %w", err)
	}

	return equipment, nil
}

// SetStatus moves a machine between in service, in the shop, and retired.
//
// Each destination gets its own audit action so the timeline can label and
// colour them apart — see audit/actions.go.
func (s *EquipmentService) SetStatus(ctx context.Context, tx pgx.Tx, id uuid.UUID, status domain.EquipmentStatus, actor Actor) (*domain.Equipment, error) {
	if !status.Valid() {
		return nil, ErrInvalidEquipmentStatus
	}

	before, err := s.GetByID(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if before.Status == status {
		// Nothing changed. Returning early keeps the timeline free of entries
		// that say a machine was retired twice.
		return before, nil
	}

	equipment, err := s.equipment.UpdateStatus(ctx, tx, id, status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrEquipmentNotFound
		}
		return nil, err
	}

	action := audit.AuditEquipmentReturnedToService
	switch status {
	case domain.EquipmentStatusInShop:
		action = audit.AuditEquipmentSentToShop
	case domain.EquipmentStatusRetired:
		action = audit.AuditEquipmentRetired
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       action,
		ResourceType: "equipment",
		ResourceID:   equipment.ID,
		Metadata: map[string]any{
			"machine": equipment.Description(),
			"from":    string(before.Status),
			"to":      string(equipment.Status),
		},
	}); err != nil {
		return nil, fmt.Errorf("audit equipment status: %w", err)
	}

	return equipment, nil
}

// GetByID returns one machine. Staff only.
func (s *EquipmentService) GetByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Equipment, error) {
	equipment, err := s.equipment.GetByID(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrEquipmentNotFound
		}
		return nil, err
	}
	return equipment, nil
}

// Get returns one machine belonging to a customer — the portal's read.
func (s *EquipmentService) Get(ctx context.Context, tx pgx.Tx, id, customerID uuid.UUID) (*domain.Equipment, error) {
	equipment, err := s.equipment.Get(ctx, tx, id, customerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Deliberately the same error a missing machine gives. A customer
			// probing ids must not be able to tell "not yours" from "not there".
			return nil, ErrEquipmentNotFound
		}
		return nil, err
	}
	return equipment, nil
}

// List returns machines matching the filter.
func (s *EquipmentService) List(ctx context.Context, tx pgx.Tx, f store.EquipmentFilter) ([]domain.Equipment, error) {
	return s.equipment.List(ctx, tx, f)
}

// ListForCustomer returns a customer's machines, for the card on their detail
// page and for the portal. Retired ones are left out: this is a list of what is
// on their counter now.
func (s *EquipmentService) ListForCustomer(ctx context.Context, tx pgx.Tx, customerID uuid.UUID) ([]domain.Equipment, error) {
	return s.equipment.List(ctx, tx, store.EquipmentFilter{CustomerID: &customerID})
}

func validateEquipment(make string, category domain.EquipmentCategory, ownership domain.EquipmentOwnership) error {
	if make == "" {
		return ErrEquipmentMakeRequired
	}
	if !category.Valid() {
		return ErrInvalidEquipmentCategory
	}
	if !ownership.Valid() {
		return ErrInvalidEquipmentOwnership
	}
	return nil
}

// ListWithCustomer returns register rows with each machine's owner attached.
func (s *EquipmentService) ListWithCustomer(ctx context.Context, tx pgx.Tx, f store.EquipmentFilter) ([]domain.EquipmentWithCustomer, error) {
	return s.equipment.ListWithCustomer(ctx, tx, f)
}
