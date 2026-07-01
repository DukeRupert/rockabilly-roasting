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
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/shipping"
	"github.com/dukerupert/hiri/internal/store"
)

// refundPollInterval is how long to wait between polls while a refund is still
// resolving. Shippo settles within ~14 days, so there's no value in polling
// aggressively — a few times a day is ample.
const refundPollInterval = 6 * time.Hour

// refundPollTimeout bounds how long a refund may sit unresolved before the job
// gives up and marks it failed. Shippo documents resolution within 14 days;
// the extra day is slack for clock skew and carrier lag.
const refundPollTimeout = 15 * 24 * time.Hour

// PollLabelRefundWorker resolves an asynchronous carrier label refund. It polls
// the provider for the refund state and, once terminal, records it on the
// shipment. While the refund is still pending it snoozes and re-polls. Snoozing
// (rather than returning) means polling never consumes River retry attempts —
// only the refundPollTimeout bound ends an unresolved poll.
type PollLabelRefundWorker struct {
	river.WorkerDefaults[PollLabelRefundArgs]
	fulfillmentSvc *app.FulfillmentService
	pool           *pgxpool.Pool
}

// NewPollLabelRefundWorker constructs a PollLabelRefundWorker.
func NewPollLabelRefundWorker(fulfillmentSvc *app.FulfillmentService, pool *pgxpool.Pool) *PollLabelRefundWorker {
	return &PollLabelRefundWorker{fulfillmentSvc: fulfillmentSvc, pool: pool}
}

// Work polls one refund and either resolves it or snoozes for another pass.
func (w *PollLabelRefundWorker) Work(ctx context.Context, job *river.Job[PollLabelRefundArgs]) error {
	args := job.Args

	// External call (no tx): fetch current refund state from the carrier.
	res, err := w.fulfillmentSvc.GetRefundStatus(ctx, args.RefundID)
	if err != nil {
		// A provider/network hiccup shouldn't burn a retry attempt — snooze and
		// try again. Only the age-out bound below ends an unresolved refund.
		slog.Warn("poll_label_refund: provider poll failed, snoozing",
			"shipment_id", args.ShipmentID, "refund_id", args.RefundID, "error", err)
		return river.JobSnooze(refundPollInterval)
	}

	switch res.State {
	case shipping.RefundSuccess:
		return w.resolve(ctx, job, domain.RefundStatusRefunded)
	case shipping.RefundError:
		return w.resolve(ctx, job, domain.RefundStatusFailed)
	default: // shipping.RefundPending
		// Give up if the request has aged past the carrier's resolution window.
		shipment, gErr := w.loadShipment(ctx, args.ShipmentID)
		if gErr != nil {
			return fmt.Errorf("load shipment: %w", gErr)
		}
		if shipment.RefundRequestedAt != nil && time.Since(*shipment.RefundRequestedAt) > refundPollTimeout {
			slog.Warn("poll_label_refund: refund timed out, marking failed",
				"shipment_id", args.ShipmentID, "refund_id", args.RefundID)
			return w.resolve(ctx, job, domain.RefundStatusFailed)
		}
		return river.JobSnooze(refundPollInterval)
	}
}

// resolve settles the shipment's refund status in a write tx, attributing the
// change to the system actor and tagging the audit entry with the job id.
func (w *PollLabelRefundWorker) resolve(ctx context.Context, job *river.Job[PollLabelRefundArgs], status domain.RefundStatus) error {
	actor := app.Actor{
		Type: domain.SystemActor.Type,
		Name: domain.SystemActor.Name,
	}
	return store.Tx(ctx, w.pool, func(tx pgx.Tx) error {
		return w.fulfillmentSvc.ResolveRefund(ctx, tx, job.Args.ShipmentID, status, actor, map[string]any{
			"river_job_id": job.ID,
			"refund_id":    job.Args.RefundID,
		})
	})
}

// loadShipment reads a shipment in a short-lived read tx.
func (w *PollLabelRefundWorker) loadShipment(ctx context.Context, shipmentID uuid.UUID) (*domain.Shipment, error) {
	var shipment *domain.Shipment
	err := store.Tx(ctx, w.pool, func(tx pgx.Tx) error {
		var txErr error
		shipment, txErr = w.fulfillmentSvc.GetShipment(ctx, tx, shipmentID)
		return txErr
	})
	return shipment, err
}
