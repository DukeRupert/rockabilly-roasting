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

// abandonedOrderAge is how long a pre-paid-intent order can sit in
// pending+awaiting before the cleanup worker cancels it. Stripe auto-cancels
// most async PIs at 48h; this catches card-path orders where the customer
// closed the browser before confirming and Stripe never moves the PI.
const abandonedOrderAge = 24 * time.Hour

// abandonedOrderBatch caps how many orders a single worker run cancels.
// Limits the per-tick blast radius if something is wrong.
const abandonedOrderBatch = 100

// AbandonedOrderCleanupWorker scans for orders that were pre-created at PI
// time but never moved past pending+awaiting (customer abandoned checkout
// before confirming payment) and cancels them. Cancellation releases any
// held coupon redemption so the code can be reused.
type AbandonedOrderCleanupWorker struct {
	river.WorkerDefaults[AbandonedOrderCleanupArgs]
	orderSvc *app.OrderService
	pool     *pgxpool.Pool
}

// NewAbandonedOrderCleanupWorker creates a new AbandonedOrderCleanupWorker.
func NewAbandonedOrderCleanupWorker(orderSvc *app.OrderService, pool *pgxpool.Pool) *AbandonedOrderCleanupWorker {
	return &AbandonedOrderCleanupWorker{orderSvc: orderSvc, pool: pool}
}

// Work scans for abandoned orders and cancels each in its own transaction so
// a single bad order doesn't block the rest of the batch.
func (w *AbandonedOrderCleanupWorker) Work(ctx context.Context, _ *river.Job[AbandonedOrderCleanupArgs]) error {
	logger := slog.Default()
	cutoff := time.Now().Add(-abandonedOrderAge)

	var ids []uuid.UUID
	err := store.Tx(ctx, w.pool, func(tx pgx.Tx) error {
		var txErr error
		ids, txErr = w.orderSvc.ListAbandonedOrderIDs(ctx, tx, cutoff, abandonedOrderBatch)
		return txErr
	})
	if err != nil {
		return fmt.Errorf("list abandoned orders: %w", err)
	}
	if len(ids) == 0 {
		return nil
	}

	cancelled := 0
	for _, id := range ids {
		if err := store.Tx(ctx, w.pool, func(tx pgx.Tx) error {
			_, cancelErr := w.orderSvc.CancelOrder(ctx, tx, id, app.Actor{
				Type: domain.AuditActorTypeSystem,
				Name: "abandoned_order_cleanup",
			})
			return cancelErr
		}); err != nil {
			if errors.Is(err, app.ErrOrderNotCancellable) || errors.Is(err, app.ErrOrderNotFound) {
				// Race: order moved out of cancellable state between list and
				// cancel (e.g. webhook arrived). Not an error.
				continue
			}
			logger.Error("abandoned order cleanup: cancel failed", "order_id", id, "error", err)
			continue
		}
		cancelled++
	}
	if cancelled > 0 {
		logger.Info("abandoned order cleanup", "cancelled", cancelled, "scanned", len(ids))
	}
	return nil
}
