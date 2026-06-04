package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
)

// defaultPriceListSettings is the slice of SettingsStore that PriceListService
// needs to read and write the store-wide default wholesale price list.
// *store.SettingsStore satisfies it.
type defaultPriceListSettings interface {
	GetDefaultWholesalePriceListID(ctx context.Context, tx pgx.Tx) (*uuid.UUID, error)
	SetDefaultWholesalePriceListID(ctx context.Context, tx pgx.Tx, id *uuid.UUID) error
}

// PriceListService manages price lists.
type PriceListService struct {
	lists    *store.PriceListStore
	settings defaultPriceListSettings
	audit    *audit.AuditWriter
	metrics  *metrics.Registry
}

// NewPriceListService creates a new PriceListService.
func NewPriceListService(lists *store.PriceListStore, auditWriter *audit.AuditWriter, m *metrics.Registry) *PriceListService {
	return &PriceListService{
		lists:   lists,
		audit:   auditWriter,
		metrics: m,
	}
}

// WithSettings wires the store-settings dependency, enabling the default
// wholesale price list getter/setter.
func (s *PriceListService) WithSettings(settings defaultPriceListSettings) *PriceListService {
	s.settings = settings
	return s
}

// --- Reads ---

// List returns all price lists ordered by name.
func (s *PriceListService) List(ctx context.Context, tx pgx.Tx) ([]domain.PriceList, error) {
	lists, err := s.lists.List(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("list price lists: %w", err)
	}
	return lists, nil
}

// Get returns a price list by ID. Returns ErrPriceListNotFound on miss.
func (s *PriceListService) Get(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.PriceList, error) {
	pl, err := s.lists.GetByID(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPriceListNotFound
		}
		return nil, fmt.Errorf("get price list: %w", err)
	}
	return pl, nil
}

// CountCustomers returns the number of customers currently assigned to the list.
func (s *PriceListService) CountCustomers(ctx context.Context, tx pgx.Tx, id uuid.UUID) (int, error) {
	n, err := s.lists.CountCustomers(ctx, tx, id)
	if err != nil {
		return 0, fmt.Errorf("count customers on price list: %w", err)
	}
	return n, nil
}

// GetDefaultWholesale returns the store-wide default wholesale price list, or nil
// if none is configured. Wholesale customers without an explicitly-assigned list
// resolve their prices against this default.
func (s *PriceListService) GetDefaultWholesale(ctx context.Context, tx pgx.Tx) (*uuid.UUID, error) {
	if s.settings == nil {
		return nil, nil
	}
	id, err := s.settings.GetDefaultWholesalePriceListID(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("get default wholesale price list: %w", err)
	}
	return id, nil
}

// --- Writes ---

// SetDefaultWholesale sets (or clears, when id is nil) the store-wide default
// wholesale price list and records an audit entry. A non-nil id must reference an
// existing price list; otherwise ErrPriceListNotFound is returned.
func (s *PriceListService) SetDefaultWholesale(ctx context.Context, tx pgx.Tx, id *uuid.UUID, actor Actor) error {
	if s.settings == nil {
		return fmt.Errorf("set default wholesale price list: settings not configured")
	}

	if id != nil {
		if _, err := s.lists.GetByID(ctx, tx, *id); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrPriceListNotFound
			}
			return fmt.Errorf("verify price list exists: %w", err)
		}
	}

	if err := s.settings.SetDefaultWholesalePriceListID(ctx, tx, id); err != nil {
		return fmt.Errorf("set default wholesale price list: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditDefaultWholesalePriceListUpdated,
		ResourceType: "store_settings",
		ResourceID:   uuid.Nil,
		After:        map[string]any{"default_wholesale_price_list_id": id},
	}); err != nil {
		return fmt.Errorf("audit default wholesale price list updated: %w", err)
	}

	return nil
}

// Create creates a new price list and records an audit entry.
func (s *PriceListService) Create(ctx context.Context, tx pgx.Tx, name string, listType domain.PriceListType, status domain.PriceListStatus, actor Actor) (*domain.PriceList, error) {
	if listType == "" {
		listType = domain.PriceListTypeOverride
	}
	if status == "" {
		status = domain.PriceListStatusActive
	}

	pl, err := s.lists.Create(ctx, tx, name, listType, status)
	if err != nil {
		return nil, fmt.Errorf("create price list: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditPriceListCreated,
		ResourceType: "price_list",
		ResourceID:   pl.ID,
		After:        pl,
	}); err != nil {
		return nil, fmt.Errorf("audit price list created: %w", err)
	}

	return pl, nil
}

// Update changes the name and status of a price list and records an audit entry.
func (s *PriceListService) Update(ctx context.Context, tx pgx.Tx, id uuid.UUID, name string, status domain.PriceListStatus, actor Actor) (*domain.PriceList, error) {
	pl, err := s.lists.Update(ctx, tx, id, name, status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPriceListNotFound
		}
		return nil, fmt.Errorf("update price list: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditPriceListUpdated,
		ResourceType: "price_list",
		ResourceID:   pl.ID,
		After:        pl,
	}); err != nil {
		return nil, fmt.Errorf("audit price list updated: %w", err)
	}

	return pl, nil
}

// Delete removes a price list and records an audit entry. The FK on
// customers.price_list_id is ON DELETE SET NULL, and prices.price_list_id is
// ON DELETE CASCADE — both happen automatically.
func (s *PriceListService) Delete(ctx context.Context, tx pgx.Tx, id uuid.UUID, actor Actor) error {
	if err := s.lists.Delete(ctx, tx, id); err != nil {
		return fmt.Errorf("delete price list: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditPriceListDeleted,
		ResourceType: "price_list",
		ResourceID:   id,
	}); err != nil {
		return fmt.Errorf("audit price list deleted: %w", err)
	}

	return nil
}
