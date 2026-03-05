package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
)

// PricingService contains business logic for pricing.
type PricingService struct {
	pricing *store.PricingStore
}

// NewPricingService creates a new PricingService.
func NewPricingService(pricing *store.PricingStore) *PricingService {
	return &PricingService{pricing: pricing}
}

// GetBasePrice returns the base price for a variant in a given currency.
func (s *PricingService) GetBasePrice(ctx context.Context, tx pgx.Tx, variantID uuid.UUID, currencyCode string) (*domain.Price, error) {
	price, err := s.pricing.GetBasePrice(ctx, tx, variantID, currencyCode)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPriceNotFound
		}
		return nil, fmt.Errorf("get base price: %w", err)
	}
	return price, nil
}
