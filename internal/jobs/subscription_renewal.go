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

// SubscriptionRenewalWorker processes subscription renewal jobs.
type SubscriptionRenewalWorker struct {
	river.WorkerDefaults[SubscriptionRenewalArgs]
	renewalSvc *app.RenewalService
	pool       *pgxpool.Pool
	metrics    *metrics.Registry
}

// NewSubscriptionRenewalWorker creates a new SubscriptionRenewalWorker.
func NewSubscriptionRenewalWorker(renewalSvc *app.RenewalService, pool *pgxpool.Pool, m *metrics.Registry) *SubscriptionRenewalWorker {
	return &SubscriptionRenewalWorker{
		renewalSvc: renewalSvc,
		pool:       pool,
		metrics:    m,
	}
}

// Work processes a single subscription renewal.
func (w *SubscriptionRenewalWorker) Work(ctx context.Context, job *river.Job[SubscriptionRenewalArgs]) error {
	start := time.Now()
	_, err := w.renewalSvc.RenewSubscription(ctx, w.pool, job.Args.SubscriptionID)
	if err != nil {
		attrs := []any{
			"job_kind", "subscription_renewal",
			"job_id", job.ID,
			"attempt", job.Attempt,
			"subscription_id", job.Args.SubscriptionID,
			"error", err.Error(),
		}

		// Two of these are terminal-but-expected, and they log at Warn — a
		// breadcrumb, not a page. A subscription cancelled between scheduling
		// and running is the system working; a declined card is dunning's
		// business, and RenewSubscription has already advanced it (next retry
		// scheduled, or expired at the cap). The scheduler owns the next
		// attempt, so River must not retry either — that would double-charge
		// the schedule.
		//
		// They are counted as cancellations rather than job failures for the
		// same reason: river_jobs_failed_total is where someone looks to see if
		// a worker is broken, and a declined card on that line makes the graph
		// worth less. The renewal outcome itself is already counted by
		// RenewalService against subscription_renewals_total.
		//
		// This has to be decided here rather than left to jobs.ErrorHandler:
		// River does not run the handler for a cancelled job. The batch worker
		// makes the same call on the same three sentinels; if that judgement
		// ever changes, change it in both or the two paths disagree about what
		// a declined card is worth.
		switch {
		case errors.Is(err, app.ErrSubscriptionNotActive), errors.Is(err, app.ErrSubscriptionNotFound):
			metrics.TrackJobCancelled(w.metrics, "subscription_renewal", start)
			slog.WarnContext(ctx, "background job subscription_renewal cancelled: subscription no longer renewable", attrs...)
			return river.JobCancel(fmt.Errorf("renew subscription %s: %w", job.Args.SubscriptionID, err))
		case errors.Is(err, app.ErrRenewalPaymentDeclined):
			metrics.TrackJobCancelled(w.metrics, "subscription_renewal", start)
			slog.WarnContext(ctx, "background job subscription_renewal cancelled: payment declined, dunning advanced", attrs...)
			return river.JobCancel(fmt.Errorf("renew subscription %s: %w", job.Args.SubscriptionID, err))
		}

		// Anything else is unexpected but retryable, so it logs like every other
		// retryable failure: Warn here, and jobs.ErrorHandler pages if the job
		// runs out of attempts. This was the last worker in the package still
		// raising an event on attempt 1 of a failure the system was about to
		// retry.
		metrics.TrackJob(w.metrics, "subscription_renewal", start, err)
		logWorkerFailure(ctx, "subscription_renewal", false, attrs...)
		return fmt.Errorf("renew subscription %s: %w", job.Args.SubscriptionID, err)
	}
	metrics.TrackJob(w.metrics, "subscription_renewal", start, nil)

	// Track renewal result for subscription health metrics.
	w.metrics.SubscriptionRenewals.WithLabelValues("success").Inc()

	return nil
}
