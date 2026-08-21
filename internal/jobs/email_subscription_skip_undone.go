package jobs

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/app"
)

// SubscriptionSkipUndoneWorker delegates to SubscriptionService.SendSkipUndoneEmail.
type SubscriptionSkipUndoneWorker struct {
	river.WorkerDefaults[SubscriptionSkipUndoneArgs]
	subs *app.SubscriptionService
	pool *pgxpool.Pool
}

// NewSubscriptionSkipUndoneWorker creates a new worker.
func NewSubscriptionSkipUndoneWorker(subs *app.SubscriptionService, pool *pgxpool.Pool) *SubscriptionSkipUndoneWorker {
	return &SubscriptionSkipUndoneWorker{subs: subs, pool: pool}
}

// Work processes a skip-undone notification email job.
func (w *SubscriptionSkipUndoneWorker) Work(ctx context.Context, job *river.Job[SubscriptionSkipUndoneArgs]) error {
	return w.subs.SendSkipUndoneEmail(ctx, w.pool, job.Args.SubscriptionID, job.Args.CustomerID, job.Args.SkippedTo)
}
