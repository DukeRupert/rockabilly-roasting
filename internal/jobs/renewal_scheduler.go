package jobs

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/store"
)

// RenewalSchedulerArgs triggers a scan for subscriptions due for renewal.
type RenewalSchedulerArgs struct{}

// Kind returns the job kind identifier.
func (RenewalSchedulerArgs) Kind() string { return "renewal_scheduler" }

// RenewalSchedulerWorker finds due subscriptions and enqueues individual renewal jobs.
type RenewalSchedulerWorker struct {
	river.WorkerDefaults[RenewalSchedulerArgs]
	subscriptionSvc *app.SubscriptionService
	pool            *pgxpool.Pool
	client          *river.Client[pgx.Tx]
}

// NewRenewalSchedulerWorker creates a new RenewalSchedulerWorker.
func NewRenewalSchedulerWorker(
	subscriptionSvc *app.SubscriptionService,
	pool *pgxpool.Pool,
	client *river.Client[pgx.Tx],
) *RenewalSchedulerWorker {
	return &RenewalSchedulerWorker{
		subscriptionSvc: subscriptionSvc,
		pool:            pool,
		client:          client,
	}
}

// Work scans for due subscriptions and enqueues renewal jobs.
func (w *RenewalSchedulerWorker) Work(ctx context.Context, _ *river.Job[RenewalSchedulerArgs]) error {
	logger := slog.Default()

	var count int
	err := store.Tx(ctx, w.pool, func(tx pgx.Tx) error {
		subs, txErr := w.subscriptionSvc.ListDueForRenewal(ctx, tx)
		if txErr != nil {
			return fmt.Errorf("list due subscriptions: %w", txErr)
		}

		for _, sub := range subs {
			_, txErr = w.client.InsertTx(ctx, tx, SubscriptionRenewalArgs{
				SubscriptionID: sub.ID,
			}, nil)
			if txErr != nil {
				return fmt.Errorf("enqueue renewal for %s: %w", sub.ID, txErr)
			}
			count++
		}

		return nil
	})
	if err != nil {
		return err
	}

	if count > 0 {
		logger.Info("enqueued subscription renewals", "count", count)
	}
	return nil
}
