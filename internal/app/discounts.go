package app

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
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

// GetCouponCodeForOrder returns the coupon code this order was placed with.
// Returns nil, nil when the order carried no coupon — callers display the code
// when there is one and say nothing when there isn't.
//
// Two lookups, because a coupon is claimed at capture rather than at placement:
// an order that has not been paid for yet holds no redemption, and one whose
// redemption was lost to another customer never will. The redeemed row is
// asked first anyway — it is the one a fork with its own writer will have — and
// the coupon the order records having been placed with is the fallback. An
// order that carried no coupon has neither.
func (s *DiscountService) GetCouponCodeForOrder(ctx context.Context, tx pgx.Tx, order *domain.Order) (*domain.CouponCode, error) {
	if order == nil {
		return nil, nil
	}

	code, err := s.discounts.GetCouponCodeByOrderID(ctx, tx, order.ID)
	if err == nil {
		return code, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("get coupon code for order: %w", err)
	}

	couponID, ok := couponCodeIDFromMetadata(order.Metadata)
	if !ok {
		return nil, nil
	}
	code, err = s.discounts.GetCouponCodeByID(ctx, tx, couponID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get coupon code placed with order: %w", err)
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

// ListRedemptions returns a once-per-customer discount's most recent paid
// redemptions, newest first, and how many there are in all.
func (s *DiscountService) ListRedemptions(ctx context.Context, tx pgx.Tx, discountID uuid.UUID, limit int) ([]domain.CouponRedemption, int, error) {
	total, err := s.discounts.CountRedemptions(ctx, tx, discountID)
	if err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return nil, 0, nil
	}
	rows, err := s.discounts.ListRedemptions(ctx, tx, discountID, limit)
	if err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// CountRedemptions returns how many paid orders used a once-per-customer
// discount.
func (s *DiscountService) CountRedemptions(ctx context.Context, tx pgx.Tx, discountID uuid.UUID) (int, error) {
	return s.discounts.CountRedemptions(ctx, tx, discountID)
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
	// UsageLimit is fixed at creation; see domain.CouponUsageLimit. Empty
	// means single-use, the only shape that existed before.
	UsageLimit domain.CouponUsageLimit
	// Eligibility is who may use it; empty means anyone.
	Eligibility domain.DiscountEligibility
	// AppliesTo is the kind of order it works on; empty means one-time orders.
	AppliesTo domain.DiscountAppliesTo
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
	if p.UsageLimit == "" {
		p.UsageLimit = domain.CouponUsageSingleUse
	}
	if !p.UsageLimit.Valid() {
		return nil, ErrDiscountInvalid
	}
	if p.Eligibility == "" {
		p.Eligibility = domain.DiscountEligibilityAnyone
	}
	if !p.Eligibility.Valid() {
		return nil, ErrDiscountInvalid
	}
	if p.AppliesTo == "" {
		p.AppliesTo = domain.DiscountAppliesToOneTime
	}
	if !p.AppliesTo.Valid() {
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
		UsageLimit:        p.UsageLimit,
		Eligibility:       p.Eligibility,
		AppliesTo:         p.AppliesTo,
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
	// Eligibility is editable, unlike the usage limit: nothing is recorded
	// against it, so changing it only changes who can use the code next.
	// Empty leaves it as it is.
	Eligibility domain.DiscountEligibility
	// AppliesTo is editable for the same reason. Empty leaves it as it is.
	AppliesTo domain.DiscountAppliesTo
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
	if p.Eligibility != "" && !p.Eligibility.Valid() {
		return nil, ErrDiscountInvalid
	}
	if p.AppliesTo != "" && !p.AppliesTo.Valid() {
		return nil, ErrDiscountInvalid
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
	if p.Eligibility == "" {
		p.Eligibility = existing.Eligibility
	}
	if p.AppliesTo == "" {
		p.AppliesTo = existing.AppliesTo
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
		Eligibility:       p.Eligibility,
		AppliesTo:         p.AppliesTo,
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
		Eligibility:       discount.Eligibility,
		AppliesTo:         discount.AppliesTo,
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

// --- Referral program ---

// ReferralOffer returns the discount marked as the referral offer, or nil when
// none is.
func (s *DiscountService) ReferralOffer(ctx context.Context, tx pgx.Tx) (*domain.Discount, error) {
	d, err := s.discounts.GetReferralDiscount(ctx, tx)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return d, nil
}

// SetReferralOffer makes a discount the one members share, unmarking any
// other. What qualifies is what the members' card promises: money off a
// friend's first subscription order, as many friends as they like.
//
//   - once per customer: a member's code goes to many friends, and a
//     single-use one would stop working after the first
//   - first subscription order: the card says so, and the friend joining is
//     what earns the bag
//   - percentage or fixed amount: the card names the saving ("50% off")
func (s *DiscountService) SetReferralOffer(ctx context.Context, tx pgx.Tx, id uuid.UUID, actor Actor) error {
	d, err := s.GetDiscount(ctx, tx, id)
	if err != nil {
		return err
	}
	switch {
	case d.UsageLimit != domain.CouponUsageOncePerCustomer:
		return fmt.Errorf("a referral offer must be once per customer: %w", ErrReferralOfferInvalid)
	case d.AppliesTo != domain.DiscountAppliesToSubscriptionSignup:
		return fmt.Errorf("a referral offer must be for the first subscription order: %w", ErrReferralOfferInvalid)
	case d.Type != domain.DiscountTypePercentage && d.Type != domain.DiscountTypeFixedAmount:
		return fmt.Errorf("a referral offer must take money off: %w", ErrReferralOfferInvalid)
	}
	if d.IsReferral {
		return nil
	}
	previous, err := s.ReferralOffer(ctx, tx)
	if err != nil {
		return err
	}
	if err := s.discounts.SetReferralDiscount(ctx, tx, id); err != nil {
		return err
	}
	// The offer it replaces gets its own entry, so each discount's history
	// says when it stopped being the offer as well as when it started.
	if previous != nil {
		if err := s.auditReferralOffer(ctx, tx, previous.ID, false, actor); err != nil {
			return err
		}
	}
	return s.auditReferralOffer(ctx, tx, id, true, actor)
}

// ClearReferralOffer stops a discount being the referral offer. Members' codes
// stay on it, and stop working: an offer that is not the referral offer is an
// ordinary discount, and those codes would otherwise reward nobody.
func (s *DiscountService) ClearReferralOffer(ctx context.Context, tx pgx.Tx, id uuid.UUID, actor Actor) error {
	d, err := s.GetDiscount(ctx, tx, id)
	if err != nil {
		return err
	}
	if !d.IsReferral {
		return nil
	}
	if err := s.discounts.ClearReferralDiscount(ctx, tx); err != nil {
		return err
	}
	return s.auditReferralOffer(ctx, tx, id, false, actor)
}

func (s *DiscountService) auditReferralOffer(ctx context.Context, tx pgx.Tx, id uuid.UUID, on bool, actor Actor) error {
	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditDiscountReferralOffer,
		ResourceType: "discount",
		ResourceID:   id,
		Metadata:     map[string]any{"is_referral": on},
	}); err != nil {
		return fmt.Errorf("audit referral offer: %w", err)
	}
	return nil
}

// MemberReferralCode returns the code a member shares, and the offer it is
// on, minting the code the first time it is asked for. Nil, with no error,
// when there is nothing to share: no referral offer, an inactive one, or a
// customer who is not an active member.
func (s *DiscountService) MemberReferralCode(ctx context.Context, tx pgx.Tx, customer *domain.Customer) (*domain.CouponCode, *domain.Discount, error) {
	offer, err := s.ReferralOffer(ctx, tx)
	if err != nil || offer == nil || !discountLive(offer, time.Now()) {
		return nil, nil, err
	}
	member, err := s.discounts.CustomerIsSubscriber(ctx, tx, customer.ID)
	if err != nil || !member {
		return nil, nil, err
	}

	code, err := s.discounts.GetMemberCouponCode(ctx, tx, offer.ID, customer.ID)
	if err == nil {
		return code, offer, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, err
	}

	// Mint one. A savepoint around each insert, so a clash — another page
	// load minting this member's code, or a code string already taken — does
	// not abort the request's transaction.
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		candidate := referralCodeFor(customer.FirstName)
		sp, err := tx.Begin(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("begin referral code savepoint: %w", err)
		}
		created, err := s.discounts.CreateCouponCode(ctx, sp, store.CreateCouponCodeParams{
			DiscountID: offer.ID,
			Code:       candidate,
			CustomerID: &customer.ID,
		})
		if err == nil {
			if err := sp.Commit(ctx); err != nil {
				return nil, nil, fmt.Errorf("commit referral code: %w", err)
			}
			return created, offer, nil
		}
		lastErr = err
		_ = sp.Rollback(ctx)
		if existing, getErr := s.discounts.GetMemberCouponCode(ctx, tx, offer.ID, customer.ID); getErr == nil {
			return existing, offer, nil
		}
	}
	return nil, nil, fmt.Errorf("mint referral code for customer %s after 5 tries: %w", customer.ID, lastErr)
}

// discountLive reports whether a discount's codes can be used at now: switched
// on, started, not expired. The same timing checkCoupon holds a code to, so
// a member is never handed a code their friend would be refused.
func discountLive(d *domain.Discount, now time.Time) bool {
	if !d.Active {
		return false
	}
	if d.StartsAt != nil && now.Before(*d.StartsAt) {
		return false
	}
	if d.ExpiresAt != nil && !now.Before(*d.ExpiresAt) {
		return false
	}
	return true
}

// referralCodeAlphabet leaves out characters that read alike (0/O, 1/I/L), so
// a code said aloud or copied by hand survives.
const referralCodeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

// referralCodeFor builds a code a member can say out loud: their first name,
// letters only and capped at eight, then four random characters — LOGAN-7KQ2.
// FRIEND stands in for a name with no usable letters.
func referralCodeFor(firstName string) string {
	var name strings.Builder
	for _, r := range strings.ToUpper(firstName) {
		if r >= 'A' && r <= 'Z' {
			name.WriteRune(r)
		}
		if name.Len() == 8 {
			break
		}
	}
	prefix := name.String()
	if prefix == "" {
		prefix = "FRIEND"
	}
	suffix := make([]byte, 4)
	for i := range suffix {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(referralCodeAlphabet))))
		if err != nil {
			panic(fmt.Sprintf("crypto/rand failed: %v", err))
		}
		suffix[i] = referralCodeAlphabet[n.Int64()]
	}
	return prefix + "-" + string(suffix)
}

// ReferralFriendsCount is how many friends a member has brought in: paid
// orders on their code that were not cancelled.
func (s *DiscountService) ReferralFriendsCount(ctx context.Context, tx pgx.Tx, customerID uuid.UUID) (int, error) {
	return s.discounts.CountReferralRewards(ctx, tx, customerID)
}

// ListReferralRewards returns rewards in one status, oldest first.
func (s *DiscountService) ListReferralRewards(ctx context.Context, tx pgx.Tx, status domain.ReferralRewardStatus, limit int) ([]domain.ReferralRewardRow, error) {
	return s.discounts.ListReferralRewards(ctx, tx, status, limit)
}

// MarkReferralRewardSent records that staff sent a member's free bag.
func (s *DiscountService) MarkReferralRewardSent(ctx context.Context, tx pgx.Tx, id uuid.UUID, actor Actor) error {
	reward, err := s.discounts.MarkReferralRewardSent(ctx, tx, id, actor.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrReferralRewardNotOwed
		}
		return err
	}
	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditReferralRewardSent,
		ResourceType: "customer",
		ResourceID:   reward.ReferrerCustomerID,
		After:        reward,
		Metadata:     map[string]any{"order_id": reward.OrderID.String()},
	}); err != nil {
		return fmt.Errorf("audit referral reward sent: %w", err)
	}
	return nil
}
