package metrics

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TrackQuery records duration and success for a named database query.
// Use with defer at the store layer:
//
//	func (s *Store) ListOrders(ctx context.Context, tx pgx.Tx) (result []Order, err error) {
//	    defer metrics.TrackQuery(s.metrics, "orders.list", time.Now(), &err)
//	    ...
//	}
func TrackQuery(reg *Registry, name string, start time.Time, err *error) {
	if reg == nil {
		return
	}
	success := "true"
	if err != nil && *err != nil {
		success = "false"
	}
	reg.DBQueryDuration.WithLabelValues(name, success).
		Observe(time.Since(start).Seconds())
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
