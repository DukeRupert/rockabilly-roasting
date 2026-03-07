package metrics

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// CollectSubscriptionMetrics starts a background goroutine that queries
// subscription status counts and updates the gauges.
// It stops when ctx is cancelled.
func CollectSubscriptionMetrics(ctx context.Context, reg *Registry, pool *pgxpool.Pool, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				collectSubscriptionCounts(ctx, reg, pool)
			}
		}
	}()

	slog.Info("subscription metrics collector started", "interval", interval)
}

func collectSubscriptionCounts(ctx context.Context, reg *Registry, pool *pgxpool.Pool) {
	rows, err := pool.Query(ctx, `
		SELECT status, COUNT(*)
		FROM subscriptions
		GROUP BY status
	`)
	if err != nil {
		slog.Error("subscription metrics: query counts", "error", err)
		return
	}
	defer rows.Close()

	// Reset to zero before updating.
	reg.SubscriptionsActive.Set(0)
	reg.SubscriptionsPaused.Set(0)
	reg.SubscriptionsCancelled.Set(0)

	for rows.Next() {
		var status string
		var count int64
		if err := rows.Scan(&status, &count); err != nil {
			slog.Error("subscription metrics: scan row", "error", err)
			continue
		}
		switch status {
		case "active":
			reg.SubscriptionsActive.Set(float64(count))
		case "paused":
			reg.SubscriptionsPaused.Set(float64(count))
		case "cancelled":
			reg.SubscriptionsCancelled.Set(float64(count))
		}
	}
}
