package jobs

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/store"
)

// enqueueQBInvoiceAlert queues the staff notification for a permanently failed
// QB invoicing job — the order will not be billed until someone intervenes, so
// the failure must not live only in logs. Called from a worker's cancel path
// in its own short tx. Best effort: a failure to enqueue is logged and never
// masks the original error.
func enqueueQBInvoiceAlert(ctx context.Context, pool *pgxpool.Pool, riverClient *river.Client[pgx.Tx], orderID uuid.UUID, failedKind string, cause error) {
	err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		_, txErr := riverClient.InsertTx(ctx, tx, EmailQBInvoiceAlertArgs{
			OrderID:    orderID,
			FailedKind: failedKind,
			Cause:      cause.Error(),
		}, nil)
		return txErr
	})
	if err != nil {
		slog.ErrorContext(ctx, "qb: failed to enqueue invoice failure alert",
			"order_id", orderID,
			"failed_kind", failedKind,
			"error", err.Error(),
		)
	}
}
