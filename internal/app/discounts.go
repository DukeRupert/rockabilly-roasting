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

// DiscountService contains business logic for discounts and coupon codes.
type DiscountService struct {
	discounts *store.DiscountStore
	audit     *audit.AuditWriter
	metrics   *metrics.Registry
}

// NewDiscountService creates a new DiscountService.
func NewDiscountService(discounts *store.DiscountStore, audit *audit.AuditWriter, metrics *metrics.Registry) *DiscountService {
	return &DiscountService{
		discounts: discounts,
		audit:     audit,
		metrics:   metrics,
	}
}

// GetDiscount returns a discount by ID.
func (s *DiscountService) GetDiscount(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Discount, error) {
	d, err := s.discounts.GetByID(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrDiscountNotFound
		}
		return nil, fmt.Errorf("get discount: %w", err)
	}
	return d, nil
}

// ListDiscounts returns discounts matching the given filter.
func (s *DiscountService) ListDiscounts(ctx context.Context, tx pgx.Tx, f store.DiscountFilter) ([]domain.Discount, error) {
	discounts, err := s.discounts.List(ctx, tx, f)
	if err != nil {
		return nil, fmt.Errorf("list discounts: %w", err)
	}
	return discounts, nil
}

// ListCouponCodes returns all coupon codes for a discount.
func (s *DiscountService) ListCouponCodes(ctx context.Context, tx pgx.Tx, discountID uuid.UUID) ([]domain.CouponCode, error) {
	codes, err := s.discounts.ListCouponCodesByDiscount(ctx, tx, discountID)
	if err != nil {
		return nil, fmt.Errorf("list coupon codes: %w", err)
	}
	return codes, nil
}
