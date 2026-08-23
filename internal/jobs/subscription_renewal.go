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
		metrics.TrackJob(w.metrics, "subscription_renewal", start, err)
		slog.ErrorContext(ctx, "background job subscription_renewal failed",
			"job_kind", "subscription_renewal",
			"job_id", job.ID,
			"attempt", job.Attempt,
			"subscription_id", job.Args.SubscriptionID,
			"error", err.Error(),
		)
		// Permanently discard jobs for subscriptions that are no longer renewable
		if errors.Is(err, app.ErrSubscriptionNotActive) || errors.Is(err, app.ErrSubscriptionNotFound) {
			return river.JobCancel(fmt.Errorf("renew subscription %s: %w", job.Args.SubscriptionID, err))
		}
		// A declined charge is terminal for this job: RenewSubscription has
		// already advanced the dunning state (next retry scheduled, or expired
		// at the cap). The renewal scheduler owns the next attempt, so River
		// must not retry — that would double-charge the schedule.
		if errors.Is(err, app.ErrRenewalPaymentDeclined) {
			return river.JobCancel(fmt.Errorf("renew subscription %s: %w", job.Args.SubscriptionID, err))
		}
		return fmt.Errorf("renew subscription %s: %w", job.Args.SubscriptionID, err)
	}
	metrics.TrackJob(w.metrics, "subscription_renewal", start, nil)

	// Track renewal result for subscription health metrics.
	w.metrics.SubscriptionRenewals.WithLabelValues("success").Inc()

	return nil
}
