package jobs

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/app"
)

// SubscriptionConfirmEmailWorker delegates to SubscriptionService.SendConfirmationEmail.
type SubscriptionConfirmEmailWorker struct {
	river.WorkerDefaults[SubscriptionConfirmEmailArgs]
	subs *app.SubscriptionService
	pool *pgxpool.Pool
}

// NewSubscriptionConfirmEmailWorker creates a new SubscriptionConfirmEmailWorker.
func NewSubscriptionConfirmEmailWorker(subs *app.SubscriptionService, pool *pgxpool.Pool) *SubscriptionConfirmEmailWorker {
	return &SubscriptionConfirmEmailWorker{subs: subs, pool: pool}
}

// Work processes a subscription confirmation email job.
func (w *SubscriptionConfirmEmailWorker) Work(ctx context.Context, job *river.Job[SubscriptionConfirmEmailArgs]) error {
	return w.subs.SendConfirmationEmail(ctx, w.pool, job.Args.SubscriptionID, job.Args.CustomerID)
}
