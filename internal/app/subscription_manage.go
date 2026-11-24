package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/store"
)

// Customers managing their own subscriptions: changing what an item is, how
// many and how often, and adding a coffee to a box. Every change applies from
// the next renewal and nothing is charged when it is made, because a renewal
// prices from the subscription row as it stands (base price less the plan
// discount, times quantity). See docs/subscriptions-module.md.

// maxBoxLines caps how many subscriptions one box may hold, the same cap a
// signup has.
const maxBoxLines = 10

// EditSubscriptionParams is a customer's edit to one subscription. A nil field
// is left as it is.
type EditSubscriptionParams struct {
	Quantity  *int
	VariantID *uuid.UUID
	PlanID    *uuid.UUID
}

// SubscriptionEdit is what EditSubscription changed. Before and After are the
// whole row either side; the flags say which fields moved. Nothing moved when
// all three are false, and then no audit entry was written.
type SubscriptionEdit struct {
	Before          *domain.Subscription
	After           *domain.Subscription
	QuantityChanged bool
	VariantChanged  bool
	PlanChanged     bool
}

// Changed reports whether the edit moved anything.
func (e *SubscriptionEdit) Changed() bool {
	return e.QuantityChanged || e.VariantChanged || e.PlanChanged
}

// EditSubscription applies a customer's edit to their own subscription, all of
// it or none of it, from the next renewal.
//
//   - Quantity is 1 to 10.
//   - A variant is another of the same coffee (a grind or a size), on sale to
//     retail and on subscription. Its own price applies from the next renewal;
//     unlike staff ChangeVariant, a price change is allowed, because the
//     customer is shown the new price before they save.
//   - A plan changes how often, after the shipment already booked: that one
//     ships on its date, and the new interval counts from there. Staff
//     ChangePlan recomputes the date from the period start instead, which can
//     put it in the past and charge at the next run; a customer must not get
//     a charge they did not see coming.
//
// Errors: ErrSubscriptionNotFound when the customer has no such subscription;
// ErrSubscriptionPastDue when it has an unpaid charge to settle first;
// ErrSubscriptionNotEditable when it is cancelled or expired;
// ErrSubscriptionRenewing while a renewal holds it.
func (s *SubscriptionService) EditSubscription(ctx context.Context, tx pgx.Tx, id, customerID uuid.UUID, p EditSubscriptionParams, actor Actor) (*SubscriptionEdit, error) {
	sub, err := s.subscriptions.LockForCustomer(ctx, tx, id, customerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrSubscriptionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("edit subscription: %w", err)
	}
	if err := customerCanChange(sub, time.Now()); err != nil {
		return nil, err
	}

	edit := &SubscriptionEdit{Before: sub, After: sub}
	quantityChanged := p.Quantity != nil && *p.Quantity != sub.Quantity
	variantChanged := p.VariantID != nil && *p.VariantID != sub.VariantID
	planChanged := p.PlanID != nil && *p.PlanID != sub.PlanID

	// Check everything before writing anything.
	if quantityChanged && (*p.Quantity < 1 || *p.Quantity > 10) {
		return nil, ErrInvalidQuantity
	}
	var oldVariant, newVariant *domain.Variant
	var oldPrice, newPrice int
	if variantChanged {
		oldVariant, oldPrice, err = s.variantWithPrice(ctx, tx, sub.VariantID)
		if err != nil {
			return nil, fmt.Errorf("edit subscription: current variant: %w", err)
		}
		newVariant, err = s.subscribableVariant(ctx, tx, *p.VariantID)
		if err != nil {
			return nil, err
		}
		if newVariant.ProductID != oldVariant.ProductID {
			return nil, ErrVariantNotOnSameProduct
		}
		_, newPrice, err = s.variantWithPrice(ctx, tx, newVariant.ID)
		if err != nil {
			return nil, err
		}
	}
	var oldPlan, newPlan *domain.SubscriptionPlan
	if planChanged {
		oldPlan, err = s.subscriptions.GetPlanByID(ctx, tx, sub.PlanID)
		if err != nil {
			return nil, fmt.Errorf("edit subscription: current plan: %w", err)
		}
		newPlan, err = s.activePlan(ctx, tx, *p.PlanID)
		if err != nil {
			return nil, err
		}
	}

	meta := func(m map[string]any) map[string]any {
		m["customer_edit"] = true
		return m
	}
	record := func(action string, after *domain.Subscription, m map[string]any) error {
		return s.audit.Record(ctx, tx, audit.AuditEntry{
			ActorType:    actor.Type,
			ActorID:      actor.ID,
			ActorName:    actor.Name,
			Action:       action,
			ResourceType: "subscription",
			ResourceID:   id,
			After:        after,
			Metadata:     meta(m),
		})
	}

	if quantityChanged {
		edit.After, err = s.subscriptions.UpdateQuantity(ctx, tx, id, *p.Quantity)
		if err != nil {
			return nil, fmt.Errorf("edit subscription: %w", err)
		}
		if err := record(audit.AuditSubscriptionQuantityChanged, edit.After, map[string]any{
			"old_quantity": sub.Quantity,
			"new_quantity": *p.Quantity,
		}); err != nil {
			return nil, fmt.Errorf("audit subscription quantity changed: %w", err)
		}
		edit.QuantityChanged = true
	}
	if variantChanged {
		edit.After, err = s.subscriptions.UpdateVariant(ctx, tx, id, newVariant.ID)
		if err != nil {
			return nil, fmt.Errorf("edit subscription: %w", err)
		}
		if err := record(audit.AuditSubscriptionVariantChanged, edit.After, map[string]any{
			"old_variant_id":  oldVariant.ID,
			"new_variant_id":  newVariant.ID,
			"old_variant_sku": oldVariant.SKU,
			"new_variant_sku": newVariant.SKU,
			"old_price":       oldPrice,
			"new_price":       newPrice,
		}); err != nil {
			return nil, fmt.Errorf("audit subscription variant changed: %w", err)
		}
		edit.VariantChanged = true
	}
	if planChanged {
		// The booked shipment keeps its date; only the interval after it moves.
		edit.After, err = s.subscriptions.UpdatePlan(ctx, tx, id, newPlan.ID, sub.CurrentPeriodEnd, sub.NextOrderAt)
		if err != nil {
			return nil, fmt.Errorf("edit subscription: %w", err)
		}
		if err := record(audit.AuditSubscriptionPlanChanged, edit.After, map[string]any{
			"old_plan_id":        oldPlan.ID,
			"new_plan_id":        newPlan.ID,
			"old_plan_name":      oldPlan.Name,
			"new_plan_name":      newPlan.Name,
			"old_interval":       string(oldPlan.Interval),
			"new_interval":       string(newPlan.Interval),
			"old_interval_count": oldPlan.IntervalCount,
			"new_interval_count": newPlan.IntervalCount,
			"next_order_at":      sub.NextOrderAt,
		}); err != nil {
			return nil, fmt.Errorf("audit subscription plan changed: %w", err)
		}
		edit.PlanChanged = true
	}
	return edit, nil
}

// AddToBoxParams is a coffee a customer adds to one of their boxes.
type AddToBoxParams struct {
	Key       domain.SubscriptionBoxKey
	VariantID uuid.UUID
	PlanID    uuid.UUID
	Quantity  int
}

// AddToBox adds a coffee to one of a customer's boxes as a new subscription,
// due at the box's instant at the box's address. Nothing is charged now: the
// new subscription is charged and shipped with the rest of the box at its next
// renewal, so there is no short first period to prorate. Its first period runs
// from now to that renewal.
//
// It inherits free renewal shipping only when every member of the box has it:
// a batch waives shipping only when all its members are grandfathered, so a
// new member without the flag would start charging shipping on the whole box.
//
// The box's members are locked first, so a renewal cannot claim the box
// between the checks and the insert.
//
// Errors: ErrSubscriptionChanged when the box is not there any more (it renewed
// or was skipped since the page was drawn); ErrSubscriptionPastDue when a
// member has an unpaid charge; ErrSubscriptionRenewing while the box is due or
// being renewed; ErrBoxFull at maxBoxLines; ErrAlreadyInBox when the box holds
// that coffee on that plan already; the variant and plan errors of
// subscribableVariant and activePlan; ErrInvalidQuantity.
func (s *SubscriptionService) AddToBox(ctx context.Context, tx pgx.Tx, customerID uuid.UUID, p AddToBoxParams, actor Actor) (*domain.Subscription, error) {
	if p.Quantity < 1 || p.Quantity > 10 {
		return nil, ErrInvalidQuantity
	}
	members, err := s.subscriptions.LockBox(ctx, tx, customerID, p.Key)
	if err != nil {
		return nil, fmt.Errorf("add to box: %w", err)
	}
	if len(members) == 0 {
		return nil, ErrSubscriptionChanged
	}
	now := time.Now()
	if !p.Key.NextOrderAt.After(now) {
		return nil, ErrSubscriptionRenewing
	}
	grandfathered := true
	for i := range members {
		m := &members[i]
		if err := customerCanChange(m, now); err != nil {
			return nil, err
		}
		if m.VariantID == p.VariantID && m.PlanID == p.PlanID {
			return nil, ErrAlreadyInBox
		}
		grandfathered = grandfathered && m.ShippingGrandfathered()
	}
	if len(members) >= maxBoxLines {
		return nil, ErrBoxFull
	}
	if _, err := s.subscribableVariant(ctx, tx, p.VariantID); err != nil {
		return nil, err
	}
	if _, _, err := s.variantWithPrice(ctx, tx, p.VariantID); err != nil {
		return nil, err
	}
	if _, err := s.activePlan(ctx, tx, p.PlanID); err != nil {
		return nil, err
	}

	metadata := map[string]any{"added_to_box": true}
	if grandfathered {
		metadata[domain.SubscriptionMetaShippingGrandfathered] = true
	}
	sub, err := s.subscriptions.Create(ctx, tx, store.CreateSubscriptionParams{
		CustomerID:         customerID,
		PlanID:             p.PlanID,
		VariantID:          p.VariantID,
		Quantity:           p.Quantity,
		Status:             domain.SubscriptionStatusActive,
		ShippingAddressID:  p.Key.ShippingAddressID,
		CurrentPeriodStart: now,
		CurrentPeriodEnd:   p.Key.NextOrderAt,
		NextOrderAt:        p.Key.NextOrderAt,
		Metadata:           metadata,
	})
	if err != nil {
		return nil, fmt.Errorf("add to box: %w", err)
	}

	auditMeta := boxAuditMeta(p.Key)
	auditMeta["added_to_box"] = true
	auditMeta["customer_edit"] = true
	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditSubscriptionCreated,
		ResourceType: "subscription",
		ResourceID:   sub.ID,
		After:        sub,
		Metadata:     auditMeta,
	}); err != nil {
		return nil, fmt.Errorf("audit subscription added to box: %w", err)
	}
	return sub, nil
}

// customerCanChange reports why a customer may not change a subscription now,
// or nil. Only active and paused subscriptions can be changed; a past-due one
// has a charge to settle first, as with the whole-box actions.
func customerCanChange(sub *domain.Subscription, now time.Time) error {
	switch sub.Status {
	case domain.SubscriptionStatusActive, domain.SubscriptionStatusPaused:
	case domain.SubscriptionStatusPastDue:
		return ErrSubscriptionPastDue
	default:
		return ErrSubscriptionNotEditable
	}
	if renewalHeld(sub, now) {
		return ErrSubscriptionRenewing
	}
	// Due and not yet claimed: the next renewal run takes it as it stands. A
	// renewal that timed out at Stripe releases its claim with nothing
	// recorded, and its retry charges under the same idempotency key, which
	// does not cover the amount: an edit in between would make every retry a
	// reused key with different parameters, the card charged once and no
	// order written. AddToBox refuses a due box the same way. A paused
	// subscription is not renewed, whatever its next_order_at says.
	if sub.Status == domain.SubscriptionStatusActive && !sub.NextOrderAt.After(now) {
		return ErrSubscriptionRenewing
	}
	return nil
}

// renewalHeld reports whether a renewal is working on sub: it holds a live
// claim, or has charged and not yet written the order. A claim older than the
// lease belongs to a renewal whose process died, and the next renewal takes it
// over, so it does not hold the subscription.
func renewalHeld(sub *domain.Subscription, now time.Time) bool {
	if sub.RenewalPaymentIntentID != nil {
		return true
	}
	return sub.RenewalClaimedAt != nil && sub.RenewalClaimedAt.After(now.Add(-renewalClaimLease))
}

// subscribableVariant returns a variant a customer may subscribe to: not
// archived, on sale to retail, on an active product marked subscribable. The
// same rules the /subscribe picker lists by.
//
// Errors: ErrVariantNotFound, ErrVariantArchived, ErrVariantNotInChannel,
// ErrVariantNotSubscribable.
func (s *SubscriptionService) subscribableVariant(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Variant, error) {
	if s.catalog == nil {
		return nil, fmt.Errorf("subscribable variant: service not wired with catalog")
	}
	v, err := s.catalog.GetVariantByID(ctx, tx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrVariantNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("subscribable variant: %w", err)
	}
	if v.ArchivedAt != nil {
		return nil, ErrVariantArchived
	}
	if !v.Available(domain.ChannelRetail) {
		return nil, ErrVariantNotInChannel
	}
	product, err := s.catalog.GetProductByID(ctx, tx, v.ProductID)
	if err != nil {
		return nil, fmt.Errorf("subscribable variant: product: %w", err)
	}
	if !product.Subscribable || product.Status != domain.ProductStatusActive {
		return nil, ErrVariantNotSubscribable
	}
	return v, nil
}

// variantWithPrice returns a variant and its base price in cents, the price a
// renewal charges before the plan discount. ErrPriceNotFound when it has none.
func (s *SubscriptionService) variantWithPrice(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Variant, int, error) {
	if s.catalog == nil || s.pricing == nil {
		return nil, 0, fmt.Errorf("variant price: service not wired with catalog/pricing")
	}
	v, err := s.catalog.GetVariantByID(ctx, tx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, ErrVariantNotFound
	}
	if err != nil {
		return nil, 0, fmt.Errorf("variant price: %w", err)
	}
	price, err := s.pricing.GetBasePrice(ctx, tx, id, "USD")
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, ErrPriceNotFound
	}
	if err != nil {
		return nil, 0, fmt.Errorf("variant price: %w", err)
	}
	return v, price.Amount, nil
}

// activePlan returns a plan a customer may choose. Errors:
// ErrSubscriptionPlanNotFound, ErrSubscriptionPlanInactive.
func (s *SubscriptionService) activePlan(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.SubscriptionPlan, error) {
	plan, err := s.subscriptions.GetPlanByID(ctx, tx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrSubscriptionPlanNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("active plan: %w", err)
	}
	if !plan.IsActive {
		return nil, ErrSubscriptionPlanInactive
	}
	return plan, nil
}

// SubscriptionChangeNotice is what a customer changed, captured when they
// changed it, for the email that tells them. Old and new values are carried
// rather than read back when the email is sent, so a second change a moment
// later cannot rewrite what the first email says. A field that did not change
// is zero (quantities) or nil (ids). Added marks a coffee added to a box; it has
// only new values.
type SubscriptionChangeNotice struct {
	SubscriptionID uuid.UUID
	CustomerID     uuid.UUID
	Added          bool
	OldQuantity    int
	NewQuantity    int
	OldVariantID   *uuid.UUID
	NewVariantID   *uuid.UUID
	OldPlanID      *uuid.UUID
	NewPlanID      *uuid.UUID
}

// Notice is the change email's record of this edit.
func (e *SubscriptionEdit) Notice() SubscriptionChangeNotice {
	n := SubscriptionChangeNotice{SubscriptionID: e.After.ID, CustomerID: e.After.CustomerID}
	if e.QuantityChanged {
		n.OldQuantity, n.NewQuantity = e.Before.Quantity, e.After.Quantity
	}
	if e.VariantChanged {
		n.OldVariantID, n.NewVariantID = &e.Before.VariantID, &e.After.VariantID
	}
	if e.PlanChanged {
		n.OldPlanID, n.NewPlanID = &e.Before.PlanID, &e.After.PlanID
	}
	return n
}

// AddedNotice is the change email's record of a coffee added to a box.
func AddedNotice(sub *domain.Subscription) SubscriptionChangeNotice {
	return SubscriptionChangeNotice{
		SubscriptionID: sub.ID,
		CustomerID:     sub.CustomerID,
		Added:          true,
		NewQuantity:    sub.Quantity,
		NewVariantID:   &sub.VariantID,
		NewPlanID:      &sub.PlanID,
	}
}
