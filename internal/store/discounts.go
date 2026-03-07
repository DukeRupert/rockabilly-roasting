package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store/sqlcgen"
)

// DiscountStore provides database access for discounts and coupon codes.
type DiscountStore struct{}

// NewDiscountStore creates a new DiscountStore.
func NewDiscountStore() *DiscountStore {
	return &DiscountStore{}
}

// --- Discount CRUD ---

// CreateDiscountParams holds the fields needed to create a discount.
type CreateDiscountParams struct {
	Name              string
	Description       *string
	Type              domain.DiscountType
	Value             int
	MinimumOrderCents *int
	StartsAt          *time.Time
	ExpiresAt         *time.Time
	Active            bool
}

// Create inserts a new discount and returns it.
func (s *DiscountStore) Create(ctx context.Context, tx pgx.Tx, p CreateDiscountParams) (*domain.Discount, error) {
	row, err := sqlcgen.New(tx).CreateDiscount(ctx, sqlcgen.CreateDiscountParams{
		ID:                uuid.New(),
		Name:              p.Name,
		Description:       p.Description,
		Type:              string(p.Type),
		Value:             int32(p.Value),
		MinimumOrderCents: intPtrToInt32Ptr(p.MinimumOrderCents),
		StartsAt:          timestampToPG(p.StartsAt),
		ExpiresAt:         timestampToPG(p.ExpiresAt),
		Active:            p.Active,
	})
	if err != nil {
		return nil, fmt.Errorf("insert discount: %w", err)
	}
	return discountFromRow(row), nil
}

// GetByID returns a discount by ID.
func (s *DiscountStore) GetByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Discount, error) {
	row, err := sqlcgen.New(tx).GetDiscountByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get discount %s: %w", id, err)
	}
	return discountFromRow(row), nil
}

// UpdateDiscountParams holds the fields to update a discount.
type UpdateDiscountParams struct {
	ID                uuid.UUID
	Name              string
	Description       *string
	Type              domain.DiscountType
	Value             int
	MinimumOrderCents *int
	StartsAt          *time.Time
	ExpiresAt         *time.Time
	Active            bool
}

// Update updates a discount and returns it.
func (s *DiscountStore) Update(ctx context.Context, tx pgx.Tx, p UpdateDiscountParams) (*domain.Discount, error) {
	row, err := sqlcgen.New(tx).UpdateDiscount(ctx, sqlcgen.UpdateDiscountParams{
		ID:                p.ID,
		Name:              p.Name,
		Description:       p.Description,
		Type:              string(p.Type),
		Value:             int32(p.Value),
		MinimumOrderCents: intPtrToInt32Ptr(p.MinimumOrderCents),
		StartsAt:          timestampToPG(p.StartsAt),
		ExpiresAt:         timestampToPG(p.ExpiresAt),
		Active:            p.Active,
	})
	if err != nil {
		return nil, fmt.Errorf("update discount: %w", err)
	}
	return discountFromRow(row), nil
}

// Delete removes a discount by ID.
func (s *DiscountStore) Delete(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	if err := sqlcgen.New(tx).DeleteDiscount(ctx, id); err != nil {
		return fmt.Errorf("delete discount: %w", err)
	}
	return nil
}

// DiscountFilter holds optional filters for listing discounts.
type DiscountFilter struct {
	Active *bool
	Limit  int
	Offset int
}

// List returns discounts matching the given filter (hand-written for dynamic WHERE).
func (s *DiscountStore) List(ctx context.Context, tx pgx.Tx, f DiscountFilter) ([]domain.Discount, error) {
	query := `SELECT id, name, description, type, value, minimum_order_cents,
	                 starts_at, expires_at, active, created_at, updated_at
	          FROM discounts WHERE true`
	args := []any{}
	argN := 1

	if f.Active != nil {
		query += fmt.Sprintf(" AND active = $%d", argN)
		args = append(args, *f.Active)
		argN++
	}

	query += " ORDER BY created_at DESC"

	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	query += fmt.Sprintf(" LIMIT $%d", argN)
	args = append(args, limit)
	argN++

	if f.Offset > 0 {
		query += fmt.Sprintf(" OFFSET $%d", argN)
		args = append(args, f.Offset)
	}

	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list discounts: %w", err)
	}
	defer rows.Close()

	var discounts []domain.Discount
	for rows.Next() {
		var d domain.Discount
		var value int32
		var startsAt, expiresAt pgtype.Timestamptz
		var minimumOrderCents *int32
		var discountType string
		if err := rows.Scan(
			&d.ID, &d.Name, &d.Description, &discountType, &value, &minimumOrderCents,
			&startsAt, &expiresAt, &d.Active, &d.CreatedAt, &d.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan discount: %w", err)
		}
		d.Type = domain.DiscountType(discountType)
		d.Value = int(value)
		d.MinimumOrderCents = int32PtrToIntPtr(minimumOrderCents)
		d.StartsAt = timestampFromPG(startsAt)
		d.ExpiresAt = timestampFromPG(expiresAt)
		discounts = append(discounts, d)
	}
	return discounts, rows.Err()
}

// --- Coupon Code CRUD ---

// CreateCouponCodeParams holds the fields needed to create a coupon code.
type CreateCouponCodeParams struct {
	DiscountID uuid.UUID
	Code       string
	CustomerID *uuid.UUID
}

// CreateCouponCode inserts a new coupon code and returns it.
func (s *DiscountStore) CreateCouponCode(ctx context.Context, tx pgx.Tx, p CreateCouponCodeParams) (*domain.CouponCode, error) {
	row, err := sqlcgen.New(tx).CreateCouponCode(ctx, sqlcgen.CreateCouponCodeParams{
		ID:         uuid.New(),
		DiscountID: p.DiscountID,
		Code:       p.Code,
		CustomerID: p.CustomerID,
	})
	if err != nil {
		return nil, fmt.Errorf("insert coupon code: %w", err)
	}
	return couponCodeFromRow(row), nil
}

// GetCouponCodeByCode returns a coupon code by its code string.
func (s *DiscountStore) GetCouponCodeByCode(ctx context.Context, tx pgx.Tx, code string) (*domain.CouponCode, error) {
	row, err := sqlcgen.New(tx).GetCouponCodeByCode(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("get coupon code: %w", err)
	}
	return couponCodeFromRow(row), nil
}

// GetCouponCodeByID returns a coupon code by ID.
func (s *DiscountStore) GetCouponCodeByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.CouponCode, error) {
	row, err := sqlcgen.New(tx).GetCouponCodeByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get coupon code %s: %w", id, err)
	}
	return couponCodeFromRow(row), nil
}

// ListCouponCodesByDiscount returns all coupon codes for a discount.
func (s *DiscountStore) ListCouponCodesByDiscount(ctx context.Context, tx pgx.Tx, discountID uuid.UUID) ([]domain.CouponCode, error) {
	rows, err := sqlcgen.New(tx).ListCouponCodesByDiscount(ctx, discountID)
	if err != nil {
		return nil, fmt.Errorf("list coupon codes: %w", err)
	}
	codes := make([]domain.CouponCode, len(rows))
	for i, r := range rows {
		codes[i] = *couponCodeFromRow(r)
	}
	return codes, nil
}

// MarkCouponCodeRedeemed marks a coupon code as redeemed by a customer.
func (s *DiscountStore) MarkCouponCodeRedeemed(ctx context.Context, tx pgx.Tx, id uuid.UUID, redeemedBy *uuid.UUID) error {
	err := sqlcgen.New(tx).MarkCouponCodeRedeemed(ctx, sqlcgen.MarkCouponCodeRedeemedParams{
		ID:         id,
		RedeemedBy: redeemedBy,
	})
	if err != nil {
		return fmt.Errorf("mark coupon redeemed: %w", err)
	}
	return nil
}

// RedeemCouponCode atomically marks a coupon as redeemed.
// Returns the coupon if successful, or pgx.ErrNoRows if already redeemed (race condition).
func (s *DiscountStore) RedeemCouponCode(ctx context.Context, tx pgx.Tx, couponID uuid.UUID, customerID *uuid.UUID, orderID uuid.UUID) (*domain.CouponCode, error) {
	row, err := sqlcgen.New(tx).RedeemCouponCode(ctx, sqlcgen.RedeemCouponCodeParams{
		ID:                couponID,
		RedeemedBy:        customerID,
		RedeemedByOrderID: &orderID,
	})
	if err != nil {
		return nil, fmt.Errorf("redeem coupon code: %w", err)
	}
	return couponCodeFromRow(row), nil
}

// DeleteCouponCode removes a coupon code by ID.
func (s *DiscountStore) DeleteCouponCode(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	if err := sqlcgen.New(tx).DeleteCouponCode(ctx, id); err != nil {
		return fmt.Errorf("delete coupon code: %w", err)
	}
	return nil
}

// --- Row converters ---

func discountFromRow(r sqlcgen.Discount) *domain.Discount {
	return &domain.Discount{
		ID:                r.ID,
		Name:              r.Name,
		Description:       r.Description,
		Type:              domain.DiscountType(r.Type),
		Value:             int(r.Value),
		MinimumOrderCents: int32PtrToIntPtr(r.MinimumOrderCents),
		StartsAt:          timestampFromPG(r.StartsAt),
		ExpiresAt:         timestampFromPG(r.ExpiresAt),
		Active:            r.Active,
		CreatedAt:         r.CreatedAt,
		UpdatedAt:         r.UpdatedAt,
	}
}

func couponCodeFromRow(r sqlcgen.CouponCode) *domain.CouponCode {
	return &domain.CouponCode{
		ID:                r.ID,
		DiscountID:        r.DiscountID,
		Code:              r.Code,
		CustomerID:        r.CustomerID,
		RedeemedAt:        timestampFromPG(r.RedeemedAt),
		RedeemedBy:        r.RedeemedBy,
		RedeemedByOrderID: r.RedeemedByOrderID,
		CreatedAt:         r.CreatedAt,
	}
}
