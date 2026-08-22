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

// GetCouponCodeForOrder returns the coupon code redeemed against an order.
// Returns nil, nil when the order carried no coupon — callers display the code
// when there is one and say nothing when there isn't.
func (s *DiscountService) GetCouponCodeForOrder(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) (*domain.CouponCode, error) {
	code, err := s.discounts.GetCouponCodeByOrderID(ctx, tx, orderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get coupon code for order: %w", err)
	}
	return code, nil
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
	if err := validateDiscountShape(p.Type, p.Value); err != nil {
		return nil, err
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

// validateDiscountShape checks a discount's type/value pair. Shared by create
// and edit so an edit cannot put a discount into a state create would refuse.
//
// free_shipping exists in the schema but checkout does not honor it, so it is
// rejected here rather than allowed to sit in the catalog looking functional.
func validateDiscountShape(t domain.DiscountType, value int) error {
	switch t {
	case domain.DiscountTypePercentage:
		if value < 1 || value > 100 {
			return ErrDiscountInvalid
		}
	case domain.DiscountTypeFixedAmount:
		if value < 1 {
			return ErrDiscountInvalid
		}
	default:
		return ErrDiscountInvalid
	}
	return nil
}

// EditDiscountParams holds the editable fields of an existing discount.
//
// Active is deliberately absent: activation stays on SetActive so the list's
// Activate/Deactivate buttons remain the single way it changes, and saving the
// edit form cannot flip a live discount off by accident.
type EditDiscountParams struct {
	Name              string
	Description       *string
	Type              domain.DiscountType
	Value             int
	MinimumOrderCents *int
	StartsAt          *time.Time
	ExpiresAt         *time.Time
}

// Update edits an existing discount.
//
// Nothing here rewrites history: an order's discount is frozen as an
// adjustment row at checkout, so changing the rule changes what future
// customers get and leaves past orders alone. Coupon codes are not editable —
// a code is the thing customers have already been given, so a new code means a
// new discount.
func (s *DiscountService) Update(ctx context.Context, tx pgx.Tx, id uuid.UUID, p EditDiscountParams, actor Actor) (*domain.Discount, error) {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		return nil, ErrDiscountInvalid
	}
	if err := validateDiscountShape(p.Type, p.Value); err != nil {
		return nil, err
	}
	if p.StartsAt != nil && p.ExpiresAt != nil && !p.ExpiresAt.After(*p.StartsAt) {
		return nil, fmt.Errorf("expiry must fall after the start date: %w", ErrDiscountInvalid)
	}

	existing, err := s.discounts.GetByID(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrDiscountNotFound
		}
		return nil, fmt.Errorf("get discount for update: %w", err)
	}

	discount, err := s.discounts.Update(ctx, tx, store.UpdateDiscountParams{
		ID:                id,
		Name:              p.Name,
		Description:       p.Description,
		Type:              p.Type,
		Value:             p.Value,
		MinimumOrderCents: p.MinimumOrderCents,
		StartsAt:          p.StartsAt,
		ExpiresAt:         p.ExpiresAt,
		Active:            existing.Active,
	})
	if err != nil {
		return nil, fmt.Errorf("update discount: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditDiscountUpdated,
		ResourceType: "discount",
		ResourceID:   id,
		After:        discount,
	}); err != nil {
		return nil, fmt.Errorf("audit discount updated: %w", err)
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
