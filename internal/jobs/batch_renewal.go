package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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

	// River does not run the ErrorHandler for a cancelled job, so anything worth
	// alerting on has to be logged here — see jobs.ErrorHandler. An empty batch
	// means the scheduler built one, which is a bug in the scheduler rather than
	// a subscription that moved on.
	if len(job.Args.SubscriptionIDs) == 0 {
		metrics.TrackJob(w.metrics, "batch_renewal", start, nil)
		slog.ErrorContext(ctx, "background job batch_renewal received an empty batch",
			"job_kind", "batch_renewal",
			"job_id", job.ID,
		)
		return river.JobCancel(fmt.Errorf("empty subscription batch"))
	}

	_, err := w.renewalSvc.RenewBatch(ctx, w.pool, job.Args.SubscriptionIDs)
	metrics.TrackJob(w.metrics, "batch_renewal", start, err)

	if err != nil {
		// Both cancels below are terminal-but-expected, so they log at Warn:
		// the slog handler files Warn as a Sentry breadcrumb rather than paging
		// anyone. A subscription that was cancelled between scheduling and
		// running is the system working, and a declined charge is dunning's
		// business, not an outage.
		if errors.Is(err, app.ErrSubscriptionNotActive) || errors.Is(err, app.ErrSubscriptionNotFound) {
			slog.WarnContext(ctx, "background job batch_renewal cancelled: subscription no longer renewable",
				"job_kind", "batch_renewal",
				"job_id", job.ID,
				"subscription_ids", job.Args.SubscriptionIDs,
				"error", err.Error(),
			)
			return river.JobCancel(fmt.Errorf("batch renewal: %w", err))
		}
		// Declined charge: dunning state already advanced for every sub in the
		// batch. The scheduler owns retries, so don't let River retry the job.
		if errors.Is(err, app.ErrRenewalPaymentDeclined) {
			slog.WarnContext(ctx, "background job batch_renewal cancelled: payment declined, dunning advanced",
				"job_kind", "batch_renewal",
				"job_id", job.ID,
				"subscription_ids", job.Args.SubscriptionIDs,
				"error", err.Error(),
			)
			return river.JobCancel(fmt.Errorf("batch renewal: %w", err))
		}
		w.metrics.SubscriptionRenewals.WithLabelValues("failed").Inc()
		return fmt.Errorf("batch renewal: %w", err)
	}

	w.metrics.SubscriptionRenewals.WithLabelValues("success").Inc()
	return nil
}
