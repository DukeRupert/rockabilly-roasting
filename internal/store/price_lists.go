package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store/sqlcgen"
)

// PriceListStore provides database access for price lists.
type PriceListStore struct{}

// NewPriceListStore creates a new PriceListStore.
func NewPriceListStore() *PriceListStore {
	return &PriceListStore{}
}

// Create inserts a new price list and returns it.
func (s *PriceListStore) Create(ctx context.Context, tx pgx.Tx, name string, listType domain.PriceListType, status domain.PriceListStatus) (*domain.PriceList, error) {
	row, err := sqlcgen.New(tx).CreatePriceList(ctx, sqlcgen.CreatePriceListParams{
		ID:     uuid.New(),
		Name:   name,
		Type:   string(listType),
		Status: string(status),
	})
	if err != nil {
		return nil, fmt.Errorf("insert price list: %w", err)
	}
	return priceListFromRow(row), nil
}

// GetByID returns a price list by ID.
func (s *PriceListStore) GetByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.PriceList, error) {
	row, err := sqlcgen.New(tx).GetPriceListByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get price list %s: %w", id, err)
	}
	return priceListFromRow(row), nil
}

// List returns all price lists ordered by name.
func (s *PriceListStore) List(ctx context.Context, tx pgx.Tx) ([]domain.PriceList, error) {
	rows, err := sqlcgen.New(tx).ListPriceLists(ctx)
	if err != nil {
		return nil, fmt.Errorf("list price lists: %w", err)
	}
	lists := make([]domain.PriceList, len(rows))
	for i, r := range rows {
		lists[i] = *priceListFromRow(r)
	}
	return lists, nil
}

// Update changes the name and status of a price list.
func (s *PriceListStore) Update(ctx context.Context, tx pgx.Tx, id uuid.UUID, name string, status domain.PriceListStatus) (*domain.PriceList, error) {
	row, err := sqlcgen.New(tx).UpdatePriceList(ctx, sqlcgen.UpdatePriceListParams{
		ID:     id,
		Name:   name,
		Status: string(status),
	})
	if err != nil {
		return nil, fmt.Errorf("update price list %s: %w", id, err)
	}
	return priceListFromRow(row), nil
}

// Delete removes a price list by ID.
// Customers referencing this list have their price_list_id nulled (FK ON DELETE SET NULL);
// any prices on this list are removed (FK ON DELETE CASCADE).
func (s *PriceListStore) Delete(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	if err := sqlcgen.New(tx).DeletePriceList(ctx, id); err != nil {
		return fmt.Errorf("delete price list: %w", err)
	}
	return nil
}

// CountCustomers returns the number of customers assigned to the given price list.
func (s *PriceListStore) CountCustomers(ctx context.Context, tx pgx.Tx, id uuid.UUID) (int, error) {
	n, err := sqlcgen.New(tx).CountCustomersByPriceList(ctx, &id)
	if err != nil {
		return 0, fmt.Errorf("count customers on price list %s: %w", id, err)
	}
	return int(n), nil
}

func priceListFromRow(r sqlcgen.PriceList) *domain.PriceList {
	return &domain.PriceList{
		ID:     r.ID,
		Name:   r.Name,
		Type:   domain.PriceListType(r.Type),
		Status: domain.PriceListStatus(r.Status),
	}
}
