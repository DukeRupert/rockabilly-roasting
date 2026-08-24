package jobs

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/app"
)

// SubscriptionPastDueWorker delegates to SubscriptionService.SendPastDueEmail.
type SubscriptionPastDueWorker struct {
	river.WorkerDefaults[SubscriptionPastDueArgs]
	subs *app.SubscriptionService
	pool *pgxpool.Pool
}

// NewSubscriptionPastDueWorker creates a new worker.
func NewSubscriptionPastDueWorker(subs *app.SubscriptionService, pool *pgxpool.Pool) *SubscriptionPastDueWorker {
	return &SubscriptionPastDueWorker{subs: subs, pool: pool}
}

// Work processes a past-due notification email job.
func (w *SubscriptionPastDueWorker) Work(ctx context.Context, job *river.Job[SubscriptionPastDueArgs]) error {
	return w.subs.SendPastDueEmail(ctx, w.pool, job.Args.SubscriptionID, job.Args.CustomerID, job.Args.Stage)
}
