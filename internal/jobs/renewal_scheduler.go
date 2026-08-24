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

	var batchCount, soloCount, deadCardCount int
	err := store.Tx(ctx, w.pool, func(tx pgx.Tx) error {
		subs, txErr := w.subscriptionSvc.ListDueForRenewal(ctx, tx)
		if txErr != nil {
			return fmt.Errorf("list due subscriptions: %w", txErr)
		}

		// Group by (customer_id, shipping_address_id), except for subscriptions
		// carrying any memory of a permanently declined card. Whether that card is
		// still the one on file is a Stripe question, and the batch path cannot
		// ask it: it resolves one payment method for the whole group with nothing
		// to avoid. The individual renewal path resolves the method against the
		// dead card and can tell the two apart, so route them there.
		//
		// Keyed on the record rather than the latch on purpose. A subscription
		// whose latch was released still has a dead card to avoid, and routing it
		// back into a batch is precisely how that card gets charged again.
		batches := make(map[batchKey][]uuid.UUID)
		var deadCard []uuid.UUID
		for _, sub := range subs {
			if sub.DunningHasDeadCard() {
				deadCard = append(deadCard, sub.ID)
				continue
			}
			key := batchKey{
				CustomerID:        sub.CustomerID,
				ShippingAddressID: sub.ShippingAddressID,
			}
			batches[key] = append(batches[key], sub.ID)
		}

		// enqueueSolo queues one subscription's own renewal. reason only colours
		// the log line; both callers land on the identical job.
		//
		// RenewalInsertOpts, never a literal: a renewal enqueued with different
		// unique options gets a different unique_key and so deduplicates against
		// nothing. That is exactly how a scheduler rung and a staff Retry both
		// used to run, double-charging the subscription.
		enqueueSolo := func(subID uuid.UUID, reason string) error {
			res, txErr := w.client.InsertTx(ctx, tx, SubscriptionRenewalArgs{
				SubscriptionID: subID,
			}, RenewalInsertOpts())
			if txErr != nil {
				return fmt.Errorf("enqueue solo renewal: %w", txErr)
			}
			if res.UniqueSkippedAsDuplicate {
				// Not an error — something already queued a charge for this
				// subscription today, most likely a staff or customer Retry.
				// Worth saying out loud, because it means this run is riding on
				// that job rather than one of ours.
				logger.Info("solo renewal already queued",
					"subscription_id", subID, "reason", reason)
				return nil
			}
			metrics.TrackJobEnqueued(w.metrics, "subscription_renewal")
			soloCount++
			if reason == "dead_card" {
				deadCardCount++
			}
			return nil
		}

		for _, subID := range deadCard {
			if txErr := enqueueSolo(subID, "dead_card"); txErr != nil {
				return txErr
			}
		}

		for key, subIDs := range batches {
			// A group of one is not a batch. RenewBatch delegates straight to
			// RenewSubscription for a single ID anyway, so the only thing the
			// batch wrapper adds is a second job kind for the same work — and job
			// kind is part of the unique key, so wrapping it puts the renewal
			// somewhere a staff or customer Retry can never deduplicate against.
			// Sending it down the solo path instead is what makes those buttons
			// safe for the common case of one subscription per customer.
			if len(subIDs) == 1 {
				if txErr := enqueueSolo(subIDs[0], "single_subscription"); txErr != nil {
					return txErr
				}
				continue
			}

			// BatchRenewalInsertOpts keys on the customer and address rather than
			// on the membership list, so a group that grows between ticks is still
			// recognised as the same batch. Passing nil here — as this did — meant
			// no uniqueness at all, and a fresh duplicate every minute until the
			// job finished.
			res, txErr := w.client.InsertTx(ctx, tx, BatchRenewalArgs{
				SubscriptionIDs:   subIDs,
				CustomerID:        key.CustomerID,
				ShippingAddressID: key.ShippingAddressID,
			}, BatchRenewalInsertOpts())
			if txErr != nil {
				return fmt.Errorf("enqueue batch renewal: %w", txErr)
			}
			if res.UniqueSkippedAsDuplicate {
				// A batch for this customer and address is already in flight. Any
				// subscription that came due since it was queued keeps its
				// next_order_at and is picked up by the next run, once this batch
				// has settled — a minute late rather than charged twice.
				logger.Info("batch renewal already queued",
					"customer_id", key.CustomerID, "subscription_count", len(subIDs))
				continue
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
		// made the log overstate how much consolidation was happening. Most solo
		// renewals are simply customers with one subscription; the dead-card
		// subset is the one worth watching.
		logger.Info("enqueued subscription renewals",
			"batches", batchCount, "solo", soloCount, "solo_dead_card", deadCardCount)
	}
	return nil
}
