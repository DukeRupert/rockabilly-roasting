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

	var batchCount, soloCount int
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
			// ByArgs alone dedupes across all time, including against completed
			// jobs still inside River's retention window, so yesterday's manual
			// retry could swallow today's rung. Scoping to the day bounds that to
			// a retry from the last 24 hours — which is a charge attempt for this
			// same subscription, so riding on it is correct rather than a loss.
			// The skip is logged below so it is visible either way.
			res, txErr := w.client.InsertTx(ctx, tx, SubscriptionRenewalArgs{
				SubscriptionID: subID,
			}, &river.InsertOpts{UniqueOpts: river.UniqueOpts{
				ByArgs:   true,
				ByPeriod: 24 * time.Hour,
			}})
			if txErr != nil {
				return fmt.Errorf("enqueue hard-declined renewal: %w", txErr)
			}
			if res.UniqueSkippedAsDuplicate {
				// Not an error — something already queued a charge for this
				// subscription today. Worth saying out loud, because it means
				// this rung is riding on that job rather than one of ours.
				logger.Info("hard-declined renewal already queued", "subscription_id", subID)
				continue
			}
			metrics.TrackJobEnqueued(w.metrics, "subscription_renewal")
			soloCount++
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

	if batchCount > 0 || soloCount > 0 {
		// Counted separately: a solo renewal is not a batch, and conflating them
		// made the log overstate how much consolidation was happening.
		logger.Info("enqueued subscription renewals",
			"batches", batchCount, "solo_hard_declined", soloCount)
	}
	return nil
}
