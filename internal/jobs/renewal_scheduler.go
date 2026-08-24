package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
)

// RenewalSchedulerArgs triggers a scan for subscriptions due for renewal.
type RenewalSchedulerArgs struct{}

// Kind returns the job kind identifier.
func (RenewalSchedulerArgs) Kind() string { return "renewal_scheduler" }

// RenewalSchedulerWorker finds due subscriptions and enqueues batched renewal jobs.
type RenewalSchedulerWorker struct {
	river.WorkerDefaults[RenewalSchedulerArgs]
	subscriptionSvc *app.SubscriptionService
	pool            *pgxpool.Pool
	client          *river.Client[pgx.Tx]
	metrics         *metrics.Registry
}

// NewRenewalSchedulerWorker creates a new RenewalSchedulerWorker.
func NewRenewalSchedulerWorker(
	subscriptionSvc *app.SubscriptionService,
	pool *pgxpool.Pool,
	client *river.Client[pgx.Tx],
	m *metrics.Registry,
) *RenewalSchedulerWorker {
	return &RenewalSchedulerWorker{
		subscriptionSvc: subscriptionSvc,
		pool:            pool,
		client:          client,
		metrics:         m,
	}
}

// batchKey groups subscriptions by customer and shipping address so they
// can be consolidated into a single renewal order.
type batchKey struct {
	CustomerID        uuid.UUID
	ShippingAddressID uuid.UUID
}

// Work scans for due subscriptions, groups them by customer + address,
// and enqueues one batch renewal job per group.
func (w *RenewalSchedulerWorker) Work(ctx context.Context, _ *river.Job[RenewalSchedulerArgs]) error {
	start := time.Now()
	logger := slog.Default()

	var batchCount int
	err := store.Tx(ctx, w.pool, func(tx pgx.Tx) error {
		subs, txErr := w.subscriptionSvc.ListDueForRenewal(ctx, tx)
		if txErr != nil {
			return fmt.Errorf("list due subscriptions: %w", txErr)
		}

		// Group by (customer_id, shipping_address_id), except for subscriptions
		// carrying a hard-decline latch. Whether that latch has been released
		// depends on which card is currently on file, and answering that means
		// calling Stripe — which the batch path cannot do at the point it decides
		// what to charge. The individual renewal path resolves the payment method
		// first and can compare it against the card that died, so route them
		// there and let each one be judged on its own.
		batches := make(map[batchKey][]uuid.UUID)
		var solo []uuid.UUID
		for _, sub := range subs {
			if sub.DunningHardDeclined() {
				solo = append(solo, sub.ID)
				continue
			}
			key := batchKey{
				CustomerID:        sub.CustomerID,
				ShippingAddressID: sub.ShippingAddressID,
			}
			batches[key] = append(batches[key], sub.ID)
		}

		for _, subID := range solo {
			if _, txErr = w.client.InsertTx(ctx, tx, SubscriptionRenewalArgs{
				SubscriptionID: subID,
			}, &river.InsertOpts{UniqueOpts: river.UniqueOpts{ByArgs: true}}); txErr != nil {
				return fmt.Errorf("enqueue hard-declined renewal: %w", txErr)
			}
			metrics.TrackJobEnqueued(w.metrics, "subscription_renewal")
			batchCount++
		}

		// Enqueue one batch job per group
		for _, subIDs := range batches {
			_, txErr = w.client.InsertTx(ctx, tx, BatchRenewalArgs{
				SubscriptionIDs: subIDs,
			}, nil)
			if txErr != nil {
				return fmt.Errorf("enqueue batch renewal: %w", txErr)
			}
			metrics.TrackJobEnqueued(w.metrics, "batch_renewal")
			batchCount++
		}

		return nil
	})

	metrics.TrackJob(w.metrics, "renewal_scheduler", start, err)

	if err != nil {
		return err
	}

	if batchCount > 0 {
		logger.Info("enqueued subscription renewal batches", "count", batchCount)
	}
	return nil
}
