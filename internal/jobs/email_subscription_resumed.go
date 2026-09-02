package jobs

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/app"
)

// SubscriptionResumedWorker delegates to SubscriptionService.SendResumeEmail.
type SubscriptionResumedWorker struct {
	river.WorkerDefaults[SubscriptionResumedArgs]
	subs *app.SubscriptionService
	pool *pgxpool.Pool
}

// NewSubscriptionResumedWorker creates a new worker.
func NewSubscriptionResumedWorker(subs *app.SubscriptionService, pool *pgxpool.Pool) *SubscriptionResumedWorker {
	return &SubscriptionResumedWorker{subs: subs, pool: pool}
}

// Work processes a subscription-resumed notification email job.
func (w *SubscriptionResumedWorker) Work(ctx context.Context, job *river.Job[SubscriptionResumedArgs]) error {
	return w.subs.SendResumeEmail(ctx, w.pool, job.Args.SubscriptionID, job.Args.CustomerID)
}
