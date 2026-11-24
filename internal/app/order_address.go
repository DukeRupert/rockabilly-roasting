package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
)

// ShippingAddressChange is what ChangeShippingAddress did.
type ShippingAddressChange struct {
	Order *domain.Order
	// MovedSubscriptions lists the subscriptions that now renew to the new
	// address. Empty unless the caller asked to move them.
	MovedSubscriptions []uuid.UUID
}

// ShippingAddressEditable reports why an order's ship-to cannot change, or nil
// when it can. The order page asks before offering the form, and
// ChangeShippingAddress asks again inside its own transaction.
//
// The address stays editable after the bag is packed ("fulfilled"): packing
// is exactly when staff read the label and catch a typo. It locks once the
// order has left the shop, once postage is bought (the label has the old
// address printed on it; refund it first), and while the order waits on an
// open delivery route (the stop carries its own copy of the address).
func (s *OrderService) ShippingAddressEditable(ctx context.Context, tx pgx.Tx, order *domain.Order) error {
	if order.CustomerID == nil {
		return ErrOrderAddressLocked
	}
	switch order.Status {
	case domain.OrderStatusCancelled, domain.OrderStatusRefunded, domain.OrderStatusComplete:
		return ErrOrderAddressLocked
	}
	switch order.FulfillmentStatus {
	case domain.FulfillmentStatusUnfulfilled, domain.FulfillmentStatusFulfilled:
	default:
		return ErrOrderAddressLocked
	}

	// Fail closed without the shipments store, as ConvertShippedOrderToLocal
	// does: unable to tell whether postage is live, refuse.
	if s.shipments == nil {
		return fmt.Errorf("shipping address editable: shipments store not configured: %w", ErrOrderAddressLocked)
	}
	shipments, err := s.shipments.ListShipmentsByOrder(ctx, tx, order.ID)
	if err != nil {
		return fmt.Errorf("list shipments for address change: %w", err)
	}
	for _, sh := range shipments {
		if sh.BlocksRebuy() {
			return ErrOrderHasActiveLabel
		}
	}

	onRoute, err := s.routes.OrderHasPendingStop(ctx, tx, order.ID)
	if err != nil {
		return fmt.Errorf("check route for address change: %w", err)
	}
	if onRoute {
		return ErrOrderOnRoute
	}
	return nil
}

// LiveSubscriptionsAtShippingAddress counts the order customer's subscriptions
// that renew to the order's current ship-to. The order page offers to move
// them along with the order, because a typo in the address almost always came
// from the same checkout that started them.
func (s *OrderService) LiveSubscriptionsAtShippingAddress(ctx context.Context, tx pgx.Tx, order *domain.Order) (int, error) {
	if order.CustomerID == nil {
		return 0, nil
	}
	if s.subscriptions == nil {
		return 0, fmt.Errorf("live subscriptions at address: subscriptions store not configured")
	}
	return s.subscriptions.CountLiveSubscriptionsAtAddress(ctx, tx, *order.CustomerID, order.ShippingAddressID)
}

// ChangeShippingAddress points an order at another of its customer's
// addresses, and with moveSubscriptions also moves every live subscription
// that renewed to the old address.
//
// It repoints rather than editing the address row in place. The old row is
// shared: past orders that shipped there point at it, and rewriting it would
// change where the records say those orders went.
//
// The billing address and every money figure are left alone. Shipping and tax
// were charged against the old address; a refund of any difference is a Stripe
// call that cannot run inside this transaction, so staff issue it separately.
// No customer email is sent.
//
// Pointing the order at the address it already has is a no-op that records
// nothing.
func (s *OrderService) ChangeShippingAddress(ctx context.Context, tx pgx.Tx, orderID, addressID uuid.UUID, moveSubscriptions bool, actor Actor) (*ShippingAddressChange, error) {
	order, err := s.orders.GetOrderByIDAsStaff(ctx, tx, orderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrOrderNotFound
		}
		return nil, fmt.Errorf("get order for address change: %w", err)
	}
	if err := s.ShippingAddressEditable(ctx, tx, order); err != nil {
		return nil, err
	}
	from := order.ShippingAddressID
	if addressID == from {
		return &ShippingAddressChange{Order: order}, nil
	}
	if moveSubscriptions && s.subscriptions == nil {
		return nil, fmt.Errorf("change shipping address: subscriptions store not configured")
	}

	order, err = s.orders.UpdateOrderShippingAddress(ctx, tx, orderID, addressID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// The order exists (it was just read), so the address is not this
			// customer's.
			return nil, ErrAddressNotFound
		}
		return nil, fmt.Errorf("change shipping address: %w", err)
	}

	var moved []uuid.UUID
	if moveSubscriptions {
		moved, err = s.subscriptions.MoveLiveSubscriptionsToAddress(ctx, tx, *order.CustomerID, from, addressID)
		if err != nil {
			return nil, fmt.Errorf("change shipping address: %w", err)
		}
	}

	movedIDs := make([]string, len(moved))
	for i, id := range moved {
		movedIDs[i] = id.String()
	}
	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditOrderShippingAddressChanged,
		ResourceType: "order",
		ResourceID:   orderID,
		After:        order,
		Metadata: map[string]any{
			"from_address_id":     from.String(),
			"to_address_id":       addressID.String(),
			"moved_subscriptions": movedIDs,
		},
	}); err != nil {
		return nil, fmt.Errorf("audit shipping address changed: %w", err)
	}
	for _, id := range moved {
		if err := s.audit.Record(ctx, tx, audit.AuditEntry{
			ActorType:    actor.Type,
			ActorID:      actor.ID,
			ActorName:    actor.Name,
			Action:       audit.AuditSubscriptionAddressChanged,
			ResourceType: "subscription",
			ResourceID:   id,
			Metadata: map[string]any{
				"from_address_id": from.String(),
				"to_address_id":   addressID.String(),
				"order_id":        orderID.String(),
			},
		}); err != nil {
			return nil, fmt.Errorf("audit subscription address changed: %w", err)
		}
	}

	return &ShippingAddressChange{Order: order, MovedSubscriptions: moved}, nil
}
