package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/platform/quickbooks"
)

// ProcessQBInvoiceUpdateWorker handles a QB webhook notification about an
// invoice update. It fetches the invoice from QB (outside any transaction) and
// hands the facts to the reconcile seam, which is the single writer of the
// order's payment status. The worker stays thin: fetch, reconcile, log.
type ProcessQBInvoiceUpdateWorker struct {
	river.WorkerDefaults[ProcessQBInvoiceUpdateArgs]
	orders  *app.OrderService
	qb      quickbooks.Client
	pool    *pgxpool.Pool
	metrics *metrics.Registry
}

// NewProcessQBInvoiceUpdateWorker creates a new ProcessQBInvoiceUpdateWorker.
func NewProcessQBInvoiceUpdateWorker(
	orders *app.OrderService,
	qb quickbooks.Client,
	pool *pgxpool.Pool,
	m *metrics.Registry,
) *ProcessQBInvoiceUpdateWorker {
	return &ProcessQBInvoiceUpdateWorker{
		orders:  orders,
		qb:      qb,
		pool:    pool,
		metrics: m,
	}
}

// Work processes a single ProcessQBInvoiceUpdate job.
func (w *ProcessQBInvoiceUpdateWorker) Work(ctx context.Context, job *river.Job[ProcessQBInvoiceUpdateArgs]) error {
	start := time.Now()

	err := w.work(ctx, job)

	metrics.TrackJob(w.metrics, "qb_process_invoice_update", start, err)
	if err != nil {
		slog.ErrorContext(ctx, "background job qb_process_invoice_update failed",
			"job_kind", "qb_process_invoice_update",
			"job_id", job.ID,
			"qb_invoice_id", job.Args.QBInvoiceID,
			"error", err.Error(),
		)
		if !quickbooks.IsRetryable(err) {
			return river.JobCancel(fmt.Errorf("process qb invoice update %s: %w", job.Args.QBInvoiceID, err))
		}
	}
	return err
}

func (w *ProcessQBInvoiceUpdateWorker) work(ctx context.Context, job *river.Job[ProcessQBInvoiceUpdateArgs]) error {
	facts, err := fetchQBInvoiceFacts(ctx, w.qb, job.Args.QBInvoiceID)
	if err != nil {
		return err
	}

	transition, err := w.orders.ReconcileQBInvoiceByID(ctx, w.pool, job.Args.QBInvoiceID, facts, time.Now())
	if err != nil {
		// A webhook can arrive for an invoice we didn't create (or one whose
		// order was purged). Nothing to reconcile — don't retry forever.
		if errors.Is(err, app.ErrOrderNotFound) {
			slog.WarnContext(ctx, "qb webhook: no order for invoice, skipping",
				"qb_invoice_id", job.Args.QBInvoiceID)
			return nil
		}
		return fmt.Errorf("reconcile qb invoice: %w", err)
	}

	if transition != app.ReconcileNone {
		slog.InfoContext(ctx, "qb invoice reconciled",
			"qb_invoice_id", job.Args.QBInvoiceID,
			"transition", string(transition),
		)
	}
	return nil
}
