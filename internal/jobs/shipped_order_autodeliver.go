package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
)

// autoDeliverAge is how long an order may sit in fulfillment_status='shipped'
// before the sweep assumes the package arrived and marks it delivered. Carrier
// delivery is never reported for these — legacy orders predating the current
// shipping integration have no shipments rows, and live orders can miss the
// Shippo tracking webhook — so without this backstop they pile up in the
// fulfillment dashboard's "shipped" bucket forever.
const autoDeliverAge = 7 * 24 * time.Hour

// autoDeliverBatch caps how many orders one sweep marks delivered. Set high
// enough to clear the existing backlog of long-shipped orders in a single run.
const autoDeliverBatch = 500

// ShippedOrderAutoDeliverArgs is the periodic auto-deliver sweep job.
type ShippedOrderAutoDeliverArgs struct{}

// Kind implements river.JobArgs.
func (ShippedOrderAutoDeliverArgs) Kind() string { return "shipped_order_auto_deliver" }

// ShippedOrderAutoDeliverWorker marks long-shipped orders delivered. It is the
// time-based safety net paired with the Shippo tracking cascade (which delivers
// orders precisely when the carrier reports it): the cascade can only act on
// orders that flow through Shippo, so this sweep covers everything else.
type ShippedOrderAutoDeliverWorker struct {
	river.WorkerDefaults[ShippedOrderAutoDeliverArgs]
	orderSvc *app.OrderService
	pool     *pgxpool.Pool
}

// NewShippedOrderAutoDeliverWorker creates a new ShippedOrderAutoDeliverWorker.
func NewShippedOrderAutoDeliverWorker(orderSvc *app.OrderService, pool *pgxpool.Pool) *ShippedOrderAutoDeliverWorker {
	return &ShippedOrderAutoDeliverWorker{orderSvc: orderSvc, pool: pool}
}

// Work scans for long-shipped orders and marks each delivered in its own
// transaction so one bad row doesn't block the rest of the batch.
func (w *ShippedOrderAutoDeliverWorker) Work(ctx context.Context, _ *river.Job[ShippedOrderAutoDeliverArgs]) error {
	logger := slog.Default()
	cutoff := time.Now().Add(-autoDeliverAge)

	var ids []uuid.UUID
	err := store.Tx(ctx, w.pool, func(tx pgx.Tx) error {
		var txErr error
		ids, txErr = w.orderSvc.ListOrderIDsToAutoDeliver(ctx, tx, cutoff, autoDeliverBatch)
		return txErr
	})
	if err != nil {
		return fmt.Errorf("list orders to auto-deliver: %w", err)
	}
	if len(ids) == 0 {
		return nil
	}

	delivered := 0
	for _, id := range ids {
		if err := store.Tx(ctx, w.pool, func(tx pgx.Tx) error {
			_, markErr := w.orderSvc.MarkOrderDelivered(ctx, tx, id, app.Actor{
				Type: domain.AuditActorTypeSystem,
				Name: "shipped_order_auto_deliver",
			})
			return markErr
		}); err != nil {
			if errors.Is(err, app.ErrInvalidOrderStatus) || errors.Is(err, app.ErrOrderNotFound) {
				// Race: a tracking webhook (or staff) moved the order out of
				// shipped between the list and the update. Not an error.
				continue
			}
			logger.Error("auto-deliver: mark delivered failed", "order_id", id, "error", err)
			continue
		}
		delivered++
	}
	if delivered > 0 {
		logger.Info("shipped order auto-deliver", "delivered", delivered, "scanned", len(ids))
	}
	return nil
}
