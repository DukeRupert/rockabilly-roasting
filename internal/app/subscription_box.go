package app

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
)

// Whole-box skip and pause. A box is derived — the customer's subscriptions
// that renew at one instant at one address, see domain.GroupIntoBoxes — so
// these find its members afresh each time, and each member is skipped or
// paused by the same per-subscription method a single row uses, inside the
// caller's transaction. No new audit actions: each member's entry is the
// ordinary one, marked with the box it went with.

// boxMembers returns the customer's subscriptions in the box key names, or
// ErrSubscriptionNotFound when there are none.
//
// Read only through the customer-scoped list. The per-member methods below read
// each row again with GetByIDAsStaff, which is unscoped, and that is safe only
// because every id they are handed came from this list: a key naming another
// customer's address matches nothing here, and so touches nothing.
func (s *SubscriptionService) boxMembers(ctx context.Context, tx pgx.Tx, customerID uuid.UUID, key domain.SubscriptionBoxKey) ([]domain.Subscription, error) {
	subs, err := s.ListSubscriptionsByCustomer(ctx, tx, customerID)
	if err != nil {
		return nil, err
	}
	for _, box := range domain.GroupIntoBoxes(subs, time.UTC) {
		if box.Key.ShippingAddressID != key.ShippingAddressID || !box.Key.NextOrderAt.Equal(key.NextOrderAt) {
			continue
		}
		members := make([]domain.Subscription, len(box.Members))
		for i, m := range box.Members {
			members[i] = m.Subscription
		}
		return members, nil
	}
	return nil, ErrSubscriptionNotFound
}

func boxAuditMeta(key domain.SubscriptionBoxKey) map[string]any {
	return map[string]any{
		"box":                     true,
		"box_shipping_address_id": key.ShippingAddressID.String(),
		"box_next_order_at":       key.NextOrderAt,
	}
}

// SkipBox moves every member of a box to resumeOn, so they still ship
// together afterwards.
//
// By date only. Skipping by a number of shipments walks each member's own
// cadence, and a weekly and a four-weekly that coincide today would land on
// different days — the box split by the action meant to keep it whole.
//
// Every member must be skippable, or none is skipped: a past-due member has an
// unpaid charge to settle first, and skipping the rest would leave the box
// split with nothing said about it. Checked before anything is written.
func (s *SubscriptionService) SkipBox(ctx context.Context, tx pgx.Tx, customerID uuid.UUID, key domain.SubscriptionBoxKey, resumeOn time.Time, actor Actor) ([]*domain.Subscription, error) {
	members, err := s.boxMembers(ctx, tx, customerID, key)
	if err != nil {
		return nil, err
	}
	for _, m := range members {
		if !canSkipSubscription(m.Status) {
			return nil, ErrSubscriptionNotSkippable
		}
	}
	params := SkipSubscriptionParams{ResumeOn: &resumeOn}
	meta := boxAuditMeta(key)
	out := make([]*domain.Subscription, len(members))
	for i, m := range members {
		sub, err := s.skipSubscription(ctx, tx, m.ID, params, actor, meta)
		if err != nil {
			return nil, fmt.Errorf("skip box member %s: %w", m.ID, err)
		}
		out[i] = sub
	}
	return out, nil
}

// PauseBox pauses every member of a box. As with SkipBox, one member that
// cannot be paused — past due, with an unpaid charge — refuses the whole box
// before anything is written.
func (s *SubscriptionService) PauseBox(ctx context.Context, tx pgx.Tx, customerID uuid.UUID, key domain.SubscriptionBoxKey, actor Actor) ([]*domain.Subscription, error) {
	members, err := s.boxMembers(ctx, tx, customerID, key)
	if err != nil {
		return nil, err
	}
	for _, m := range members {
		if !canPauseSubscription(m.Status) {
			return nil, ErrSubscriptionNotPausable
		}
	}
	meta := boxAuditMeta(key)
	out := make([]*domain.Subscription, len(members))
	for i, m := range members {
		sub, err := s.pauseSubscription(ctx, tx, m.ID, nil, actor, meta)
		if err != nil {
			return nil, fmt.Errorf("pause box member %s: %w", m.ID, err)
		}
		out[i] = sub
	}
	return out, nil
}
