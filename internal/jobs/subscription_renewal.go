package jobs

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/app"
)

// SubscriptionRenewalWorker processes subscription renewal jobs.
type SubscriptionRenewalWorker struct {
	river.WorkerDefaults[SubscriptionRenewalArgs]
	renewalSvc *app.RenewalService
	pool       *pgxpool.Pool
}

// NewSubscriptionRenewalWorker creates a new SubscriptionRenewalWorker.
func NewSubscriptionRenewalWorker(renewalSvc *app.RenewalService, pool *pgxpool.Pool) *SubscriptionRenewalWorker {
	return &SubscriptionRenewalWorker{
		renewalSvc: renewalSvc,
		pool:       pool,
	}
}

// Work processes a single subscription renewal.
func (w *SubscriptionRenewalWorker) Work(ctx context.Context, job *river.Job[SubscriptionRenewalArgs]) error {
	_, err := w.renewalSvc.RenewSubscription(ctx, w.pool, job.Args.SubscriptionID)
	if err != nil {
		return fmt.Errorf("renew subscription %s: %w", job.Args.SubscriptionID, err)
	}
	return nil
}
