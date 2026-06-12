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

// CreateDiscountParams holds the validated inputs for creating a discount
// with its first coupon code.
type CreateDiscountParams struct {
	Name              string
	Description       *string
	Type              domain.DiscountType
	Value             int
	MinimumOrderCents *int
	ExpiresAt         *time.Time
	Code              string
}

// CreateWithCode creates a discount and its first coupon code in one step.
// Codes are normalized to uppercase so customers can type them in any case
// (lookup at apply time normalizes the same way).
func (s *DiscountService) CreateWithCode(ctx context.Context, tx pgx.Tx, p CreateDiscountParams, actor Actor) (*domain.Discount, error) {
	p.Name = strings.TrimSpace(p.Name)
	p.Code = strings.ToUpper(strings.TrimSpace(p.Code))
	if p.Name == "" || p.Code == "" {
		return nil, ErrDiscountInvalid
	}
	switch p.Type {
	case domain.DiscountTypePercentage:
		if p.Value < 1 || p.Value > 100 {
			return nil, ErrDiscountInvalid
		}
	case domain.DiscountTypeFixedAmount:
		if p.Value < 1 {
			return nil, ErrDiscountInvalid
		}
	default:
		// free_shipping exists in the schema but checkout doesn't honor it
		// yet — refuse to create discounts the buy path can't apply.
		return nil, ErrDiscountInvalid
	}

	if _, err := s.discounts.GetCouponCodeByCode(ctx, tx, p.Code); err == nil {
		return nil, ErrCouponCodeExists
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("check coupon code: %w", err)
	}

	discount, err := s.discounts.Create(ctx, tx, store.CreateDiscountParams{
		Name:              p.Name,
		Description:       p.Description,
		Type:              p.Type,
		Value:             p.Value,
		MinimumOrderCents: p.MinimumOrderCents,
		ExpiresAt:         p.ExpiresAt,
		Active:            true,
	})
	if err != nil {
		return nil, fmt.Errorf("create discount: %w", err)
	}

	code, err := s.discounts.CreateCouponCode(ctx, tx, store.CreateCouponCodeParams{
		DiscountID: discount.ID,
		Code:       p.Code,
	})
	if err != nil {
		return nil, fmt.Errorf("create coupon code: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditDiscountCreated,
		ResourceType: "discount",
		ResourceID:   discount.ID,
		After:        discount,
		Metadata:     map[string]any{"coupon_code": code.Code},
	}); err != nil {
		return nil, fmt.Errorf("audit discount created: %w", err)
	}

	return discount, nil
}

// SetActive activates or deactivates a discount. Deactivating immediately
// stops new applications of its coupon codes; carts that already hold the
// code lose the discount at payment-intent time (the totals calculation
// re-checks Active).
func (s *DiscountService) SetActive(ctx context.Context, tx pgx.Tx, id uuid.UUID, active bool, actor Actor) error {
	discount, err := s.GetDiscount(ctx, tx, id)
	if err != nil {
		return err
	}
	if discount.Active == active {
		return nil
	}

	updated, err := s.discounts.Update(ctx, tx, store.UpdateDiscountParams{
		ID:                discount.ID,
		Name:              discount.Name,
		Description:       discount.Description,
		Type:              discount.Type,
		Value:             discount.Value,
		MinimumOrderCents: discount.MinimumOrderCents,
		StartsAt:          discount.StartsAt,
		ExpiresAt:         discount.ExpiresAt,
		Active:            active,
	})
	if err != nil {
		return fmt.Errorf("update discount active: %w", err)
	}

	action := audit.AuditDiscountDeactivated
	if active {
		action = audit.AuditDiscountUpdated
	}
	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       action,
		ResourceType: "discount",
		ResourceID:   discount.ID,
		After:        updated,
		Metadata:     map[string]any{"was_active": discount.Active},
	}); err != nil {
		return fmt.Errorf("audit discount active: %w", err)
	}
	return nil
}
