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

// SetBasePrice sets the base price (in cents) for a variant.
// Creates a price set if one doesn't exist for the variant.
func (s *PricingService) SetBasePrice(ctx context.Context, tx pgx.Tx, variantID uuid.UUID, amountCents int, currencyCode string) (*domain.Price, error) {
	if amountCents < 0 {
		return nil, ErrInvalidPrice
	}

	ps, err := s.pricing.GetOrCreatePriceSet(ctx, tx, variantID)
	if err != nil {
		return nil, fmt.Errorf("get or create price set: %w", err)
	}

	price, err := s.pricing.SetBasePrice(ctx, tx, ps.ID, amountCents, currencyCode)
	if err != nil {
		return nil, fmt.Errorf("set base price: %w", err)
	}
	return price, nil
}

// GetOrCreatePriceSet returns the price set for a variant, creating one if needed.
func (s *PricingService) GetOrCreatePriceSet(ctx context.Context, tx pgx.Tx, variantID uuid.UUID) (*domain.PriceSet, error) {
	ps, err := s.pricing.GetOrCreatePriceSet(ctx, tx, variantID)
	if err != nil {
		return nil, fmt.Errorf("get or create price set: %w", err)
	}
	return ps, nil
}

// ListBasePricesByProduct returns base prices for all variants of a product, keyed by variant ID.
func (s *PricingService) ListBasePricesByProduct(ctx context.Context, tx pgx.Tx, productID uuid.UUID, currencyCode string) (map[uuid.UUID]int, error) {
	prices, err := s.pricing.ListBasePricesByProduct(ctx, tx, productID, currencyCode)
	if err != nil {
		return nil, fmt.Errorf("list base prices: %w", err)
	}
	return prices, nil
}
