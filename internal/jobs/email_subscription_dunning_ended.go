package jobs

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/app"
)

// SubscriptionDunningEndedWorker delegates to SubscriptionService.SendDunningEndedEmail.
type SubscriptionDunningEndedWorker struct {
	river.WorkerDefaults[SubscriptionDunningEndedArgs]
	subs *app.SubscriptionService
	pool *pgxpool.Pool
}

// NewSubscriptionDunningEndedWorker creates a new worker.
func NewSubscriptionDunningEndedWorker(subs *app.SubscriptionService, pool *pgxpool.Pool) *SubscriptionDunningEndedWorker {
	return &SubscriptionDunningEndedWorker{subs: subs, pool: pool}
}

// Work processes a subscription-ended (dunning-exhausted) email job.
func (w *SubscriptionDunningEndedWorker) Work(ctx context.Context, job *river.Job[SubscriptionDunningEndedArgs]) error {
	return w.subs.SendDunningEndedEmail(ctx, w.pool, job.Args.SubscriptionID, job.Args.CustomerID)
}
