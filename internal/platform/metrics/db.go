package metrics

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RecordQuery observes one database query's duration and success label.
// It satisfies the store.QueryRecorder interface, so a *Registry can be
// passed straight into store constructors.
func (r *Registry) RecordQuery(name string, dur time.Duration, err error) {
	if r == nil {
		return
	}
	success := "true"
	if err != nil {
		success = "false"
	}
	r.DBQueryDuration.WithLabelValues(name, success).Observe(dur.Seconds())
}

// CollectPoolMetrics starts a background goroutine that scrapes pgxpool stats
// and updates the DB pool gauges. It stops when ctx is cancelled.
func CollectPoolMetrics(ctx context.Context, reg *Registry, pool *pgxpool.Pool, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		// Track previous wait count for delta calculation.
		var prevWaitCount int64

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				stat := pool.Stat()
				reg.DBPoolOpen.Set(float64(stat.TotalConns()))
				reg.DBPoolIdle.Set(float64(stat.IdleConns()))

				// EmptyAcquireCount is cumulative — report the delta.
				currentWait := stat.EmptyAcquireCount()
				if delta := currentWait - prevWaitCount; delta > 0 {
					reg.DBPoolWaitCount.Add(float64(delta))
				}
				prevWaitCount = currentWait
			}
		}
	}()

	slog.Info("db pool metrics collector started", "interval", interval)
}
