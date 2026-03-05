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
