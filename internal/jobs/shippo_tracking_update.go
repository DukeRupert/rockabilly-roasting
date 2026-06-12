package jobs

import (
	"context"
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
)

// ShippoTrackingUpdateArgs carries a single Shippo track_updated event from the
// webhook handler to the worker. Status is the raw Shippo token (e.g.
// "DELIVERED"); the service maps it to a shipment status.
type ShippoTrackingUpdateArgs struct {
	TrackingNumber string `json:"tracking_number"`
	Status         string `json:"status"`
}

// Kind implements river.JobArgs.
func (ShippoTrackingUpdateArgs) Kind() string { return "shippo_tracking_update" }

// ShippoTrackingUpdateWorker applies a Shippo tracking status to the matching
// shipment. The work is idempotent (forward-only status transitions in the
// service), so River retries and Shippo's duplicate deliveries are both safe.
type ShippoTrackingUpdateWorker struct {
	river.WorkerDefaults[ShippoTrackingUpdateArgs]
	fulfillmentSvc *app.FulfillmentService
	orderSvc       *app.OrderService
	pool           *pgxpool.Pool
}

// NewShippoTrackingUpdateWorker constructs a ShippoTrackingUpdateWorker.
func NewShippoTrackingUpdateWorker(fulfillmentSvc *app.FulfillmentService, orderSvc *app.OrderService, pool *pgxpool.Pool) *ShippoTrackingUpdateWorker {
	return &ShippoTrackingUpdateWorker{fulfillmentSvc: fulfillmentSvc, orderSvc: orderSvc, pool: pool}
}

// Work applies the tracking update inside a single transaction. When the update
// marks a shipment delivered, the order's fulfillment status is reconciled in
// the same transaction so the dashboard's "shipped" bucket reflects reality —
// otherwise an order sits in shipped forever even after the carrier reports
// delivery.
func (w *ShippoTrackingUpdateWorker) Work(ctx context.Context, job *river.Job[ShippoTrackingUpdateArgs]) error {
	args := job.Args
	err := store.Tx(ctx, w.pool, func(tx pgx.Tx) error {
		shipment, applyErr := w.fulfillmentSvc.ApplyTrackingStatus(ctx, tx, args.TrackingNumber, args.Status)
		if applyErr != nil {
			return applyErr
		}
		if shipment == nil || shipment.Status != domain.ShipmentStatusDelivered {
			return nil
		}
		_, recErr := w.orderSvc.ReconcileDelivery(ctx, tx, shipment.OrderID, app.Actor{
			Type: domain.AuditActorTypeSystem,
			Name: "shippo_tracking",
		})
		return recErr
	})
	if errors.Is(err, app.ErrShipmentNotFound) {
		// A webhook for a tracking number we don't have (e.g. a shipment from
		// another system, or one not yet persisted). Nothing to retry.
		slog.Warn("shippo_tracking_update: no shipment for tracking number",
			"tracking_number", args.TrackingNumber, "status", args.Status)
		return nil
	}
	return err
}
