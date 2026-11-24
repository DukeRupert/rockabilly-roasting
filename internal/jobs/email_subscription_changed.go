package jobs

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/app"
)

// SubscriptionChangedWorker delegates to SubscriptionService.SendChangeEmail.
type SubscriptionChangedWorker struct {
	river.WorkerDefaults[SubscriptionChangedArgs]
	subs *app.SubscriptionService
	pool *pgxpool.Pool
}

// NewSubscriptionChangedWorker creates a new worker.
func NewSubscriptionChangedWorker(subs *app.SubscriptionService, pool *pgxpool.Pool) *SubscriptionChangedWorker {
	return &SubscriptionChangedWorker{subs: subs, pool: pool}
}

// Work sends one subscription-changed email.
func (w *SubscriptionChangedWorker) Work(ctx context.Context, job *river.Job[SubscriptionChangedArgs]) error {
	return w.subs.SendChangeEmail(ctx, w.pool, app.SubscriptionChangeNotice(job.Args))
}
