package jobs

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/app"
)

// BatchRenewalWorker processes batched subscription renewal jobs.
type BatchRenewalWorker struct {
	river.WorkerDefaults[BatchRenewalArgs]
	renewalSvc *app.RenewalService
	pool       *pgxpool.Pool
}

// NewBatchRenewalWorker creates a new BatchRenewalWorker.
func NewBatchRenewalWorker(renewalSvc *app.RenewalService, pool *pgxpool.Pool) *BatchRenewalWorker {
	return &BatchRenewalWorker{
		renewalSvc: renewalSvc,
		pool:       pool,
	}
}

// Work processes a batch of subscription renewals into a single order.
func (w *BatchRenewalWorker) Work(ctx context.Context, job *river.Job[BatchRenewalArgs]) error {
	if len(job.Args.SubscriptionIDs) == 0 {
		return river.JobCancel(fmt.Errorf("empty subscription batch"))
	}

	_, err := w.renewalSvc.RenewBatch(ctx, w.pool, job.Args.SubscriptionIDs)
	if err != nil {
		if errors.Is(err, app.ErrSubscriptionNotActive) || errors.Is(err, app.ErrSubscriptionNotFound) {
			return river.JobCancel(fmt.Errorf("batch renewal: %w", err))
		}
		return fmt.Errorf("batch renewal: %w", err)
	}
	return nil
}
