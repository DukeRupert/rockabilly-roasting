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
				collectRenewalBlocked(ctx, reg, pool)
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

// collectRenewalBlocked counts subscriptions that look live but carry a past
// ends_at, so ListSubscriptionsDueForRenewal can never select them.
//
// The predicate is the scheduler's own third clause, inverted. It is repeated
// here as SQL rather than filtered in Go because the whole point is to catch
// rows the application never loads — a blocked subscription is, by definition,
// one no code path is looking at.
//
// A non-zero value means somebody is being silently not-billed. It stayed at
// three for months with nobody aware, which is why this is a gauge and not a
// log line.
func collectRenewalBlocked(ctx context.Context, reg *Registry, pool *pgxpool.Pool) {
	var count int64
	err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM subscriptions
		WHERE status IN ('active', 'past_due')
		  AND ends_at IS NOT NULL
		  AND ends_at <= now()
	`).Scan(&count)
	if err != nil {
		// Deliberately not zeroed on error: a gauge that drops to zero because
		// the query failed reads as "all clear", which is the one wrong answer.
		slog.Error("subscription metrics: query renewal-blocked", "error", err)
		return
	}
	reg.SubscriptionsRenewalBlocked.Set(float64(count))
}
