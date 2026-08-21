package jobs

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/app"
)

// SubscriptionSkippedWorker delegates to SubscriptionService.SendSkipEmail.
type SubscriptionSkippedWorker struct {
	river.WorkerDefaults[SubscriptionSkippedArgs]
	subs *app.SubscriptionService
	pool *pgxpool.Pool
}

// NewSubscriptionSkippedWorker creates a new worker.
func NewSubscriptionSkippedWorker(subs *app.SubscriptionService, pool *pgxpool.Pool) *SubscriptionSkippedWorker {
	return &SubscriptionSkippedWorker{subs: subs, pool: pool}
}

// Work processes a skip notification email job.
func (w *SubscriptionSkippedWorker) Work(ctx context.Context, job *river.Job[SubscriptionSkippedArgs]) error {
	return w.subs.SendSkipEmail(ctx, w.pool, job.Args.SubscriptionID, job.Args.CustomerID, job.Args.SkippedCount)
}
