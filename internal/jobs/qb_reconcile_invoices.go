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

// qbReconcileBatch bounds how many open invoices a single poll run reconciles,
// so one run can't fan out an unbounded number of QB API calls. When a run hits
// the cap, the remainder is picked up on the next run (and the run logs a
// warning so silent truncation is visible).
const qbReconcileBatch = 200

// ReconcileQBInvoicesWorker sweeps open wholesale QB invoices: it is the safety
// net for missed Intuit webhooks and the detector that flips unpaid invoices to
// overdue and sends milestone past-due reminders. It is thin — it discovers the
// candidate orders, fetches each invoice from QB (outside any transaction), and
// delegates every status decision to the reconcile seam.
type ReconcileQBInvoicesWorker struct {
	river.WorkerDefaults[ReconcileQBInvoicesArgs]
	orders  *app.OrderService
	qb      quickbooks.Client
	pool    *pgxpool.Pool
	metrics *metrics.Registry
}

// NewReconcileQBInvoicesWorker creates a new ReconcileQBInvoicesWorker.
func NewReconcileQBInvoicesWorker(
	orders *app.OrderService,
	qb quickbooks.Client,
	pool *pgxpool.Pool,
	m *metrics.Registry,
) *ReconcileQBInvoicesWorker {
	return &ReconcileQBInvoicesWorker{
		orders:  orders,
		qb:      qb,
		pool:    pool,
		metrics: m,
	}
}

// Work runs one reconciliation sweep.
func (w *ReconcileQBInvoicesWorker) Work(ctx context.Context, job *river.Job[ReconcileQBInvoicesArgs]) error {
	start := time.Now()
	err := w.work(ctx)
	metrics.TrackJob(w.metrics, "qb_reconcile_invoices", start, err)
	if err != nil {
		slog.ErrorContext(ctx, "background job qb_reconcile_invoices failed",
			"job_kind", "qb_reconcile_invoices",
			"job_id", job.ID,
			"error", err.Error(),
		)
	}
	return err
}

func (w *ReconcileQBInvoicesWorker) work(ctx context.Context) error {
	orders, err := w.orders.ListWholesaleOpenInvoiceOrders(ctx, w.pool, qbReconcileBatch)
	if err != nil {
		return fmt.Errorf("list open invoice orders: %w", err)
	}
	if len(orders) == 0 {
		return nil
	}

	now := time.Now()
	var reconciled, failed int
	// Each order reconciles in its own transaction (inside ReconcileQBInvoiceByID),
	// so one bad invoice doesn't abort the whole sweep.
	for i := range orders {
		order := orders[i]
		if order.QBInvoiceID == nil {
			continue
		}
		qbInvoiceID := *order.QBInvoiceID

		facts, fErr := fetchQBInvoiceFacts(ctx, w.qb, qbInvoiceID)
		if fErr != nil {
			failed++
			slog.ErrorContext(ctx, "qb reconcile: fetch invoice failed",
				"order_id", order.ID, "qb_invoice_id", qbInvoiceID, "error", fErr.Error())
			continue
		}

		transition, rErr := w.orders.ReconcileQBInvoiceByID(ctx, w.pool, qbInvoiceID, facts, now)
		if rErr != nil {
			if errors.Is(rErr, app.ErrOrderNotFound) {
				continue
			}
			failed++
			slog.ErrorContext(ctx, "qb reconcile: reconcile failed",
				"order_id", order.ID, "qb_invoice_id", qbInvoiceID, "error", rErr.Error())
			continue
		}
		if transition != app.ReconcileNone {
			reconciled++
		}
	}

	slog.InfoContext(ctx, "qb reconcile poll complete",
		"candidates", len(orders), "reconciled", reconciled, "failed", failed)
	if len(orders) == qbReconcileBatch {
		slog.WarnContext(ctx, "qb reconcile poll hit batch cap; remaining open invoices reconcile next run",
			"cap", qbReconcileBatch)
	}
	return nil
}
