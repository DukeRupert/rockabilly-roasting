package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/shipping"
)

// shippoTrackingActor attributes shipment status changes driven by Shippo
// tracking webhooks. These are system-initiated (no staff member clicked a
// button), mirroring qbReconcileActor in orders_qb.go.
var shippoTrackingActor = Actor{Type: domain.AuditActorTypeSystem, Name: "shippo_tracking"}

// shipmentStatusFromShippoTracking maps a Shippo tracking_status token to our
// shipment status. The bool is false for tokens we don't act on (UNKNOWN,
// PRE_TRANSIT) — a shipment already sits at label_created once its label is
// bought, so those carry no new information.
func shipmentStatusFromShippoTracking(token string) (domain.ShipmentStatus, bool) {
	switch token {
	case shipping.ShippoTrackTransit:
		return domain.ShipmentStatusInTransit, true
	case shipping.ShippoTrackDelivered:
		return domain.ShipmentStatusDelivered, true
	case shipping.ShippoTrackReturned, shipping.ShippoTrackFailure:
		return domain.ShipmentStatusException, true
	default:
		return "", false
	}
}

// ApplyTrackingStatus advances the shipment with the given tracking number to
// the status implied by a Shippo tracking token. It is forward-only (see
// ShipmentStatus.CanAdvanceTo): a token that maps to the current or an earlier
// status is ignored, so replayed or out-of-order webhooks are safe no-ops.
//
// Returns the updated shipment when a transition was applied, or (nil, nil)
// when the token wasn't actionable or the status didn't move. Returns
// ErrShipmentNotFound when no shipment carries the tracking number — the caller
// (a webhook-driven job) should log and drop that rather than retry.
func (s *FulfillmentService) ApplyTrackingStatus(ctx context.Context, tx pgx.Tx, trackingNumber, shippoToken string) (*domain.Shipment, error) {
	target, ok := shipmentStatusFromShippoTracking(shippoToken)
	if !ok {
		return nil, nil
	}

	shipment, err := s.shipments.GetShipmentByTrackingNumber(ctx, tx, trackingNumber)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrShipmentNotFound
		}
		return nil, fmt.Errorf("lookup shipment for tracking %s: %w", trackingNumber, err)
	}

	if !shipment.Status.CanAdvanceTo(target) {
		return nil, nil
	}

	fromStatus := shipment.Status

	var updated *domain.Shipment
	if target == domain.ShipmentStatusDelivered {
		if err := s.shipments.UpdateShipmentDelivered(ctx, tx, shipment.ID); err != nil {
			return nil, fmt.Errorf("mark delivered: %w", err)
		}
		updated, err = s.shipments.GetShipmentByIDAsStaff(ctx, tx, shipment.ID)
		if err != nil {
			return nil, fmt.Errorf("reload shipment: %w", err)
		}
	} else {
		updated, err = s.shipments.UpdateShipmentStatus(ctx, tx, shipment.ID, target)
		if err != nil {
			return nil, fmt.Errorf("update shipment status: %w", err)
		}
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    shippoTrackingActor.Type,
		ActorID:      shippoTrackingActor.ID,
		ActorName:    shippoTrackingActor.Name,
		Action:       audit.AuditShipmentStatusUpdated,
		ResourceType: "shipment",
		ResourceID:   shipment.ID,
		After:        updated,
		Metadata: map[string]any{
			"from_status":   string(fromStatus),
			"to_status":     string(target),
			"shippo_status": shippoToken,
		},
	}); err != nil {
		return nil, fmt.Errorf("audit shipment status updated: %w", err)
	}

	return updated, nil
}
