package jobs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/platform/metrics"
)

// BatchRenewalWorker processes batched subscription renewal jobs.
type BatchRenewalWorker struct {
	river.WorkerDefaults[BatchRenewalArgs]
	renewalSvc *app.RenewalService
	pool       *pgxpool.Pool
	metrics    *metrics.Registry
}

// NewBatchRenewalWorker creates a new BatchRenewalWorker.
func NewBatchRenewalWorker(renewalSvc *app.RenewalService, pool *pgxpool.Pool, m *metrics.Registry) *BatchRenewalWorker {
	return &BatchRenewalWorker{
		renewalSvc: renewalSvc,
		pool:       pool,
		metrics:    m,
	}
}

// Work processes a batch of subscription renewals into a single order.
func (w *BatchRenewalWorker) Work(ctx context.Context, job *river.Job[BatchRenewalArgs]) error {
	start := time.Now()

	if len(job.Args.SubscriptionIDs) == 0 {
		metrics.TrackJob(w.metrics, "batch_renewal", start, nil)
		return river.JobCancel(fmt.Errorf("empty subscription batch"))
	}

	_, err := w.renewalSvc.RenewBatch(ctx, w.pool, job.Args.SubscriptionIDs)
	metrics.TrackJob(w.metrics, "batch_renewal", start, err)

	if err != nil {
		if errors.Is(err, app.ErrSubscriptionNotActive) || errors.Is(err, app.ErrSubscriptionNotFound) {
			return river.JobCancel(fmt.Errorf("batch renewal: %w", err))
		}
		w.metrics.SubscriptionRenewals.WithLabelValues("failed").Inc()
		return fmt.Errorf("batch renewal: %w", err)
	}

	w.metrics.SubscriptionRenewals.WithLabelValues("success").Inc()
	return nil
}
