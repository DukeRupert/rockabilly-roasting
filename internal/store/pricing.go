package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store/sqlcgen"
)

// PricingStore provides database access for prices and price sets.
type PricingStore struct{}

// NewPricingStore creates a new PricingStore.
func NewPricingStore() *PricingStore {
	return &PricingStore{}
}

// GetBasePrice returns the base price for a variant in a given currency.
// Base price = no price list, no customer group, no min quantity.
func (s *PricingStore) GetBasePrice(ctx context.Context, tx pgx.Tx, variantID uuid.UUID, currencyCode string) (*domain.Price, error) {
	row, err := sqlcgen.New(tx).GetBasePrice(ctx, sqlcgen.GetBasePriceParams{
		VariantID:    variantID,
		CurrencyCode: currencyCode,
	})
	if err != nil {
		return nil, fmt.Errorf("get base price for variant %s: %w", variantID, err)
	}
	return priceFromRow(row), nil
}

// GetOrCreatePriceSet returns the price set for a variant, creating one if it doesn't exist.
func (s *PricingStore) GetOrCreatePriceSet(ctx context.Context, tx pgx.Tx, variantID uuid.UUID) (*domain.PriceSet, error) {
	row, err := sqlcgen.New(tx).GetPriceSetByVariant(ctx, variantID)
	if err == nil {
		return &domain.PriceSet{ID: row.ID, VariantID: row.VariantID}, nil
	}
	// Create if not found
	row, err = sqlcgen.New(tx).CreatePriceSet(ctx, sqlcgen.CreatePriceSetParams{
		ID:        uuid.New(),
		VariantID: variantID,
	})
	if err != nil {
		return nil, fmt.Errorf("create price set for variant %s: %w", variantID, err)
	}
	return &domain.PriceSet{ID: row.ID, VariantID: row.VariantID}, nil
}

// SetBasePrice deletes any existing base price and inserts a new one.
func (s *PricingStore) SetBasePrice(ctx context.Context, tx pgx.Tx, priceSetID uuid.UUID, amount int, currencyCode string) (*domain.Price, error) {
	q := sqlcgen.New(tx)

	// Delete existing base price for this price set + currency
	if err := q.DeleteBasePrice(ctx, sqlcgen.DeleteBasePriceParams{
		PriceSetID:   priceSetID,
		CurrencyCode: currencyCode,
	}); err != nil {
		return nil, fmt.Errorf("delete existing base price: %w", err)
	}

	// Insert new price
	row, err := q.UpsertBasePrice(ctx, sqlcgen.UpsertBasePriceParams{
		ID:           uuid.New(),
		PriceSetID:   priceSetID,
		Amount:       int32(amount),
		CurrencyCode: currencyCode,
	})
	if err != nil {
		return nil, fmt.Errorf("insert base price: %w", err)
	}
	return priceFromRow(row), nil
}

// ListBasePricesByProduct returns base prices for all variants of a product, keyed by variant ID.
func (s *PricingStore) ListBasePricesByProduct(ctx context.Context, tx pgx.Tx, productID uuid.UUID, currencyCode string) (map[uuid.UUID]int, error) {
	rows, err := sqlcgen.New(tx).ListBasePricesByProduct(ctx, sqlcgen.ListBasePricesByProductParams{
		ProductID:    productID,
		CurrencyCode: currencyCode,
	})
	if err != nil {
		return nil, fmt.Errorf("list base prices for product %s: %w", productID, err)
	}
	prices := make(map[uuid.UUID]int, len(rows))
	for _, r := range rows {
		prices[r.VariantID] = int(r.Amount)
	}
	return prices, nil
}

func priceFromRow(r sqlcgen.Price) *domain.Price {
	return &domain.Price{
		ID:              r.ID,
		PriceSetID:      r.PriceSetID,
		Amount:          int(r.Amount),
		CurrencyCode:    r.CurrencyCode,
		MinQuantity:     int32PtrToIntPtr(r.MinQuantity),
		MaxQuantity:     int32PtrToIntPtr(r.MaxQuantity),
		CustomerGroupID: r.CustomerGroupID,
		PriceListID:     r.PriceListID,
		StartsAt:        timestampFromPG(r.StartsAt),
		EndsAt:          timestampFromPG(r.EndsAt),
	}
}
