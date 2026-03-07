package metrics

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TrackJob records duration and success/failure for a River job execution.
// Call at the start of a job worker's Work method:
//
//	func (w *MyWorker) Work(ctx context.Context, job *river.Job[MyArgs]) error {
//	    start := time.Now()
//	    err := w.doWork(ctx, job)
//	    metrics.TrackJob(w.metrics, "my_job_kind", start, err)
//	    return err
//	}
func TrackJob(reg *Registry, kind string, start time.Time, err error) {
	if reg == nil {
		return
	}
	success := "true"
	if err != nil {
		success = "false"
		reg.RiverJobsFailed.WithLabelValues(kind).Inc()
	} else {
		reg.RiverJobsCompleted.WithLabelValues(kind).Inc()
	}
	reg.RiverJobDuration.WithLabelValues(kind, success).
		Observe(time.Since(start).Seconds())
}

// TrackJobEnqueued increments the enqueue counter for a job kind.
func TrackJobEnqueued(reg *Registry, kind string) {
	if reg == nil {
		return
	}
	reg.RiverJobsEnqueued.WithLabelValues(kind).Inc()
}

// CollectRiverMetrics starts a background goroutine that queries River's
// internal tables for queue depth and updates the pending gauge.
// It stops when ctx is cancelled.
func CollectRiverMetrics(ctx context.Context, reg *Registry, pool *pgxpool.Pool, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				collectRiverQueueDepth(ctx, reg, pool)
			}
		}
	}()

	slog.Info("river metrics collector started", "interval", interval)
}

func collectRiverQueueDepth(ctx context.Context, reg *Registry, pool *pgxpool.Pool) {
	rows, err := pool.Query(ctx, `
		SELECT kind, state, COUNT(*)
		FROM river_job
		WHERE state IN ('available', 'running', 'retryable', 'scheduled')
		GROUP BY kind, state
	`)
	if err != nil {
		slog.Error("river metrics: query queue depth", "error", err)
		return
	}
	defer rows.Close()

	// Reset all pending gauges to zero before updating so jobs that
	// drained to zero are reflected.
	reg.RiverJobsPending.Reset()

	for rows.Next() {
		var kind, state string
		var count int64
		if err := rows.Scan(&kind, &state, &count); err != nil {
			slog.Error("river metrics: scan row", "error", err)
			continue
		}
		reg.RiverJobsPending.WithLabelValues(kind, state).Set(float64(count))
	}
}
