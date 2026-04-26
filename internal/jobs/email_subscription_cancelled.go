package jobs

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/app"
)

// SubscriptionCancelledWorker delegates to SubscriptionService.SendCancellationEmail.
type SubscriptionCancelledWorker struct {
	river.WorkerDefaults[SubscriptionCancelledArgs]
	subs *app.SubscriptionService
	pool *pgxpool.Pool
}

// NewSubscriptionCancelledWorker creates a new worker.
func NewSubscriptionCancelledWorker(subs *app.SubscriptionService, pool *pgxpool.Pool) *SubscriptionCancelledWorker {
	return &SubscriptionCancelledWorker{subs: subs, pool: pool}
}

// Work processes a cancellation confirmation email job.
func (w *SubscriptionCancelledWorker) Work(ctx context.Context, job *river.Job[SubscriptionCancelledArgs]) error {
	return w.subs.SendCancellationEmail(ctx, w.pool, job.Args.SubscriptionID, job.Args.CustomerID)
}
