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
	// UsageLimit defaults to single-use when empty.
	UsageLimit domain.CouponUsageLimit
	// Eligibility defaults to anyone when empty.
	Eligibility domain.DiscountEligibility
	// AppliesTo defaults to one-time orders when empty.
	AppliesTo domain.DiscountAppliesTo
}

// Create inserts a new discount and returns it.
func (s *DiscountStore) Create(ctx context.Context, tx pgx.Tx, p CreateDiscountParams) (*domain.Discount, error) {
	if p.UsageLimit == "" {
		p.UsageLimit = domain.CouponUsageSingleUse
	}
	if p.Eligibility == "" {
		p.Eligibility = domain.DiscountEligibilityAnyone
	}
	if p.AppliesTo == "" {
		p.AppliesTo = domain.DiscountAppliesToOneTime
	}
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
		UsageLimit:        string(p.UsageLimit),
		Eligibility:       string(p.Eligibility),
		AppliesTo:         string(p.AppliesTo),
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
	// Eligibility defaults to anyone when empty.
	Eligibility domain.DiscountEligibility
	// AppliesTo defaults to one-time orders when empty.
	AppliesTo domain.DiscountAppliesTo
}

// Update updates a discount and returns it.
func (s *DiscountStore) Update(ctx context.Context, tx pgx.Tx, p UpdateDiscountParams) (*domain.Discount, error) {
	if p.Eligibility == "" {
		p.Eligibility = domain.DiscountEligibilityAnyone
	}
	if p.AppliesTo == "" {
		p.AppliesTo = domain.DiscountAppliesToOneTime
	}
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
		Eligibility:       string(p.Eligibility),
		AppliesTo:         string(p.AppliesTo),
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
	                 starts_at, expires_at, active, usage_limit, eligibility, applies_to, is_referral, created_at, updated_at
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
		var discountType, usageLimit, eligibility, appliesTo string
		if err := rows.Scan(
			&d.ID, &d.Name, &d.Description, &discountType, &value, &minimumOrderCents,
			&startsAt, &expiresAt, &d.Active, &usageLimit, &eligibility, &appliesTo, &d.IsReferral, &d.CreatedAt, &d.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan discount: %w", err)
		}
		d.Type = domain.DiscountType(discountType)
		d.UsageLimit = domain.CouponUsageLimit(usageLimit)
		d.Eligibility = domain.DiscountEligibility(eligibility)
		d.AppliesTo = domain.DiscountAppliesTo(appliesTo)
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

// GetCouponCodeByOrderID returns the coupon code redeemed against an order, if
// any. Returns pgx.ErrNoRows when the order carried no coupon — the order page
// treats that as "no code", not an error.
func (s *DiscountStore) GetCouponCodeByOrderID(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) (*domain.CouponCode, error) {
	row, err := sqlcgen.New(tx).GetCouponCodeByOrderID(ctx, &orderID)
	if err != nil {
		return nil, fmt.Errorf("get coupon code by order: %w", err)
	}
	return couponCodeFromRow(row), nil
}

// ReleaseCouponCodeByOrderID reverses a coupon redemption tied to a specific
// order. Used when an order is cancelled (admin action or abandoned-checkout
// cleanup) so the code can be redeemed again. Covers both shapes: a single-use
// code is un-stamped, and a once-per-customer redemption row is deleted so the
// customer can use the offer on another order. No-op if the order redeemed
// nothing.
func (s *DiscountStore) ReleaseCouponCodeByOrderID(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) error {
	q := sqlcgen.New(tx)
	if err := q.ReleaseCouponCodeByOrderID(ctx, &orderID); err != nil {
		return fmt.Errorf("release coupon code: %w", err)
	}
	if err := q.DeleteCouponRedemptionByOrderID(ctx, orderID); err != nil {
		return fmt.Errorf("release coupon redemption: %w", err)
	}
	// The free bag the order earned its referrer goes with it, unless it has
	// already been sent.
	if err := q.VoidReferralRewardByOrderID(ctx, orderID); err != nil {
		return fmt.Errorf("void referral reward: %w", err)
	}
	return nil
}

// --- Once-per-customer redemptions ---

// CreateRedemptionParams identifies one paid order's use of a once-per-customer
// discount.
type CreateRedemptionParams struct {
	DiscountID   uuid.UUID
	CouponCodeID uuid.UUID
	CustomerID   uuid.UUID
	OrderID      uuid.UUID
}

// CreateRedemption records a once-per-customer redemption. Returns
// pgx.ErrNoRows when a unique key refused it: the customer already used this
// discount, or this order already holds its row. GetRedemptionByOrderID tells
// the two apart.
func (s *DiscountStore) CreateRedemption(ctx context.Context, tx pgx.Tx, p CreateRedemptionParams) (*domain.CouponRedemption, error) {
	row, err := sqlcgen.New(tx).CreateCouponRedemption(ctx, sqlcgen.CreateCouponRedemptionParams{
		DiscountID:   p.DiscountID,
		CouponCodeID: p.CouponCodeID,
		CustomerID:   p.CustomerID,
		OrderID:      p.OrderID,
	})
	if err != nil {
		return nil, fmt.Errorf("create coupon redemption: %w", err)
	}
	return couponRedemptionFromRow(row), nil
}

// GetRedemptionByOrderID returns the once-per-customer redemption an order
// holds, or pgx.ErrNoRows.
func (s *DiscountStore) GetRedemptionByOrderID(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) (*domain.CouponRedemption, error) {
	row, err := sqlcgen.New(tx).GetCouponRedemptionByOrderID(ctx, orderID)
	if err != nil {
		return nil, fmt.Errorf("get coupon redemption by order: %w", err)
	}
	return couponRedemptionFromRow(row), nil
}

// CustomerHasRedeemed reports whether a customer has a paid order on a
// once-per-customer discount.
func (s *DiscountStore) CustomerHasRedeemed(ctx context.Context, tx pgx.Tx, discountID, customerID uuid.UUID) (bool, error) {
	used, err := sqlcgen.New(tx).CustomerHasRedeemedDiscount(ctx, sqlcgen.CustomerHasRedeemedDiscountParams{
		DiscountID: discountID,
		CustomerID: customerID,
	})
	if err != nil {
		return false, fmt.Errorf("check coupon redemption: %w", err)
	}
	return used, nil
}

// CustomerIsSubscriber reports whether a customer counts as a member for a
// subscribers-only discount: an active subscription. Paused and past-due ones
// do not count.
func (s *DiscountStore) CustomerIsSubscriber(ctx context.Context, tx pgx.Tx, customerID uuid.UUID) (bool, error) {
	ok, err := sqlcgen.New(tx).CustomerIsSubscriber(ctx, customerID)
	if err != nil {
		return false, fmt.Errorf("check subscriber: %w", err)
	}
	return ok, nil
}

// CustomerHasEverSubscribed reports whether a customer has held a subscription
// in any status, for a new-subscribers-only discount.
func (s *DiscountStore) CustomerHasEverSubscribed(ctx context.Context, tx pgx.Tx, customerID uuid.UUID) (bool, error) {
	ok, err := sqlcgen.New(tx).CustomerHasEverSubscribed(ctx, customerID)
	if err != nil {
		return false, fmt.Errorf("check subscription history: %w", err)
	}
	return ok, nil
}

// CountRedemptions returns how many paid orders used a once-per-customer
// discount.
func (s *DiscountStore) CountRedemptions(ctx context.Context, tx pgx.Tx, discountID uuid.UUID) (int, error) {
	n, err := sqlcgen.New(tx).CountCouponRedemptionsByDiscount(ctx, discountID)
	if err != nil {
		return 0, fmt.Errorf("count coupon redemptions: %w", err)
	}
	return int(n), nil
}

// ListRedemptions returns a once-per-customer discount's most recent
// redemptions, newest first.
func (s *DiscountStore) ListRedemptions(ctx context.Context, tx pgx.Tx, discountID uuid.UUID, limit int) ([]domain.CouponRedemption, error) {
	rows, err := sqlcgen.New(tx).ListCouponRedemptionsByDiscount(ctx, sqlcgen.ListCouponRedemptionsByDiscountParams{
		DiscountID: discountID,
		Limit:      int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list coupon redemptions: %w", err)
	}
	out := make([]domain.CouponRedemption, len(rows))
	for i, r := range rows {
		out[i] = *couponRedemptionFromRow(r)
	}
	return out, nil
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
		UsageLimit:        domain.CouponUsageLimit(r.UsageLimit),
		Eligibility:       domain.DiscountEligibility(r.Eligibility),
		AppliesTo:         domain.DiscountAppliesTo(r.AppliesTo),
		IsReferral:        r.IsReferral,
		CreatedAt:         r.CreatedAt,
		UpdatedAt:         r.UpdatedAt,
	}
}

func couponRedemptionFromRow(r sqlcgen.CouponRedemption) *domain.CouponRedemption {
	return &domain.CouponRedemption{
		ID:           r.ID,
		DiscountID:   r.DiscountID,
		CouponCodeID: r.CouponCodeID,
		CustomerID:   r.CustomerID,
		OrderID:      r.OrderID,
		RedeemedAt:   r.RedeemedAt,
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

// --- Referrals ---

// GetReferralDiscount returns the discount marked as the referral offer, or
// pgx.ErrNoRows when none is.
func (s *DiscountStore) GetReferralDiscount(ctx context.Context, tx pgx.Tx) (*domain.Discount, error) {
	row, err := sqlcgen.New(tx).GetReferralDiscount(ctx)
	if err != nil {
		return nil, fmt.Errorf("get referral discount: %w", err)
	}
	return discountFromRow(row), nil
}

// SetReferralDiscount makes id the referral offer, unmarking any other. Two
// statements, unmark then mark: see MarkReferralDiscount in discounts.sql.
func (s *DiscountStore) SetReferralDiscount(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	q := sqlcgen.New(tx)
	if err := q.ClearReferralDiscount(ctx); err != nil {
		return fmt.Errorf("clear referral discount: %w", err)
	}
	if err := q.MarkReferralDiscount(ctx, id); err != nil {
		return fmt.Errorf("mark referral discount: %w", err)
	}
	return nil
}

// ClearReferralDiscount leaves no discount marked as the referral offer.
func (s *DiscountStore) ClearReferralDiscount(ctx context.Context, tx pgx.Tx) error {
	if err := sqlcgen.New(tx).ClearReferralDiscount(ctx); err != nil {
		return fmt.Errorf("clear referral discount: %w", err)
	}
	return nil
}

// GetMemberCouponCode returns the code a customer owns on a discount, or
// pgx.ErrNoRows.
func (s *DiscountStore) GetMemberCouponCode(ctx context.Context, tx pgx.Tx, discountID, customerID uuid.UUID) (*domain.CouponCode, error) {
	row, err := sqlcgen.New(tx).GetMemberCouponCode(ctx, sqlcgen.GetMemberCouponCodeParams{
		DiscountID: discountID,
		CustomerID: &customerID,
	})
	if err != nil {
		return nil, fmt.Errorf("get member coupon code: %w", err)
	}
	return couponCodeFromRow(row), nil
}

// CreateReferralRewardParams identifies the paid order a referral reward is for.
type CreateReferralRewardParams struct {
	ReferrerCustomerID uuid.UUID
	ReferredCustomerID uuid.UUID
	CouponCodeID       uuid.UUID
	OrderID            uuid.UUID
}

// CreateReferralReward records a free bag owed. Returns pgx.ErrNoRows when the
// order already has one (capture re-entered).
func (s *DiscountStore) CreateReferralReward(ctx context.Context, tx pgx.Tx, p CreateReferralRewardParams) (*domain.ReferralReward, error) {
	row, err := sqlcgen.New(tx).CreateReferralReward(ctx, sqlcgen.CreateReferralRewardParams{
		ReferrerCustomerID: p.ReferrerCustomerID,
		ReferredCustomerID: p.ReferredCustomerID,
		CouponCodeID:       p.CouponCodeID,
		OrderID:            p.OrderID,
	})
	if err != nil {
		return nil, fmt.Errorf("create referral reward: %w", err)
	}
	return referralRewardFromRow(row), nil
}

// MarkReferralRewardSent marks an owed reward sent. Returns pgx.ErrNoRows when
// it is not owed (already sent, voided, or missing).
func (s *DiscountStore) MarkReferralRewardSent(ctx context.Context, tx pgx.Tx, id uuid.UUID, staffID *uuid.UUID) (*domain.ReferralReward, error) {
	row, err := sqlcgen.New(tx).MarkReferralRewardSent(ctx, sqlcgen.MarkReferralRewardSentParams{ID: id, SentBy: staffID})
	if err != nil {
		return nil, fmt.Errorf("mark referral reward sent: %w", err)
	}
	return referralRewardFromRow(row), nil
}

// ListReferralRewards returns rewards in one status, oldest first, with the
// referrer, order and code the admin list shows.
func (s *DiscountStore) ListReferralRewards(ctx context.Context, tx pgx.Tx, status domain.ReferralRewardStatus, limit int) ([]domain.ReferralRewardRow, error) {
	rows, err := sqlcgen.New(tx).ListReferralRewardsByStatus(ctx, sqlcgen.ListReferralRewardsByStatusParams{
		Status: string(status),
		Limit:  int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list referral rewards: %w", err)
	}
	out := make([]domain.ReferralRewardRow, len(rows))
	for i, r := range rows {
		out[i] = domain.ReferralRewardRow{
			ReferralReward: domain.ReferralReward{
				ID:                 r.ID,
				ReferrerCustomerID: r.ReferrerCustomerID,
				ReferredCustomerID: r.ReferredCustomerID,
				CouponCodeID:       r.CouponCodeID,
				OrderID:            r.OrderID,
				Status:             domain.ReferralRewardStatus(r.Status),
				SentAt:             timestampFromPG(r.SentAt),
				SentBy:             r.SentBy,
				CreatedAt:          r.CreatedAt,
			},
			ReferrerEmail:     r.ReferrerEmail,
			ReferrerFirstName: r.ReferrerFirstName,
			ReferrerLastName:  r.ReferrerLastName,
			OrderNumber:       r.OrderNumber,
			Code:              r.Code,
		}
	}
	return out, nil
}

// CountReferralRewards returns how many friends a member has brought in: every
// reward that was not voided.
func (s *DiscountStore) CountReferralRewards(ctx context.Context, tx pgx.Tx, referrerID uuid.UUID) (int, error) {
	n, err := sqlcgen.New(tx).CountReferralRewardsByReferrer(ctx, referrerID)
	if err != nil {
		return 0, fmt.Errorf("count referral rewards: %w", err)
	}
	return int(n), nil
}

func referralRewardFromRow(r sqlcgen.ReferralReward) *domain.ReferralReward {
	return &domain.ReferralReward{
		ID:                 r.ID,
		ReferrerCustomerID: r.ReferrerCustomerID,
		ReferredCustomerID: r.ReferredCustomerID,
		CouponCodeID:       r.CouponCodeID,
		OrderID:            r.OrderID,
		Status:             domain.ReferralRewardStatus(r.Status),
		SentAt:             timestampFromPG(r.SentAt),
		SentBy:             r.SentBy,
		CreatedAt:          r.CreatedAt,
	}
}
