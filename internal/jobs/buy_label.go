package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/shipping"
	"github.com/dukerupert/hiri/internal/store"
)

// BuyLabelWorker orchestrates the two-phase label purchase flow:
//
//  1. Read tx — load order, addresses, weights, box preset; build LabelRequest
//  2. Provider call (no tx) — purchase the label
//  3. Write tx — persist shipment + audit + enqueue StoreLabelToR2 job
//
// Phase 1 errors that are deterministic (no box preset, no shippable items,
// missing variant weight) terminate the job with a non-retryable error so a
// retry won't keep re-failing on the same bad state. Phase 2 errors retry
// per River's default backoff (Shippo flakiness is the expected case).
type BuyLabelWorker struct {
	river.WorkerDefaults[BuyLabelArgs]
	fulfillmentSvc *app.FulfillmentService
	pool           *pgxpool.Pool
	riverClient    *river.Client[pgx.Tx]
}

// NewBuyLabelWorker constructs a BuyLabelWorker.
func NewBuyLabelWorker(
	fulfillmentSvc *app.FulfillmentService,
	pool *pgxpool.Pool,
	riverClient *river.Client[pgx.Tx],
) *BuyLabelWorker {
	return &BuyLabelWorker{
		fulfillmentSvc: fulfillmentSvc,
		pool:           pool,
		riverClient:    riverClient,
	}
}

// Work processes a single BuyLabel job.
func (w *BuyLabelWorker) Work(ctx context.Context, job *river.Job[BuyLabelArgs]) error {
	args := job.Args
	actor := app.Actor{
		Type: domain.AuditActorType(args.ActorType),
		ID:   args.ActorID,
		Name: args.ActorName,
	}

	// Phase 1: read tx — assemble the label request.
	var req shipping.LabelRequest
	err := store.Tx(ctx, w.pool, func(tx pgx.Tx) error {
		var txErr error
		req, txErr = w.fulfillmentSvc.PrepareLabelRequest(ctx, tx, args.OrderID, args.ServiceCode)
		return txErr
	})
	if err != nil {
		// Deterministic preparation failures aren't worth retrying — they
		// require an operator to fix data (add a box preset, set variant
		// weight, etc.). Cancel the job so it doesn't bounce forever.
		if isDeterministicPrepFailure(err) {
			slog.Error("buy_label: prep failed permanently",
				"order_id", args.OrderID, "error", err)
			return river.JobCancel(fmt.Errorf("prepare label request: %w", err))
		}
		return fmt.Errorf("prepare label request: %w", err)
	}

	// Phase 2: external provider call — no tx held.
	result, err := w.fulfillmentSvc.PurchaseLabel(ctx, req)
	if err != nil {
		return fmt.Errorf("purchase label: %w", err)
	}

	// Phase 3: write tx — persist shipment, audit, enqueue R2 sync.
	return store.Tx(ctx, w.pool, func(tx pgx.Tx) error {
		shipment, txErr := w.fulfillmentSvc.PersistShipmentLabel(ctx, tx, args.OrderID, req, *result, actor)
		if txErr != nil {
			return fmt.Errorf("persist shipment: %w", txErr)
		}

		labelURL := ""
		if shipment.LabelURL != nil {
			labelURL = *shipment.LabelURL
		}
		_, txErr = w.riverClient.InsertTx(ctx, tx, StoreLabelToR2Args{
			ShipmentID: shipment.ID,
			LabelURL:   labelURL,
		}, nil)
		if txErr != nil {
			return fmt.Errorf("enqueue r2 sync: %w", txErr)
		}
		return nil
	})
}

// isDeterministicPrepFailure reports whether an error from PrepareLabelRequest
// will recur on retry without operator intervention.
func isDeterministicPrepFailure(err error) bool {
	return errors.Is(err, app.ErrNoBoxPreset) ||
		errors.Is(err, app.ErrShipmentNoPhysicalItems) ||
		errors.Is(err, app.ErrShipmentWeightUnknown)
}
