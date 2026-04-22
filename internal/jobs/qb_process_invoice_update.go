package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/platform/quickbooks"
	"github.com/dukerupert/hiri/internal/store"
)

// ProcessQBInvoiceUpdateWorker handles a QB webhook notification about an invoice update.
// It fetches the invoice from QB, checks if fully paid, and updates the order.
type ProcessQBInvoiceUpdateWorker struct {
	river.WorkerDefaults[ProcessQBInvoiceUpdateArgs]
	orders      *store.OrderStore
	qb          quickbooks.Client
	audit       *audit.AuditWriter
	pool        *pgxpool.Pool
	riverClient *river.Client[pgx.Tx]
	metrics     *metrics.Registry
}

// NewProcessQBInvoiceUpdateWorker creates a new ProcessQBInvoiceUpdateWorker.
func NewProcessQBInvoiceUpdateWorker(
	orders *store.OrderStore,
	qb quickbooks.Client,
	auditWriter *audit.AuditWriter,
	pool *pgxpool.Pool,
	riverClient *river.Client[pgx.Tx],
	m *metrics.Registry,
) *ProcessQBInvoiceUpdateWorker {
	return &ProcessQBInvoiceUpdateWorker{
		orders:      orders,
		qb:          qb,
		audit:       auditWriter,
		pool:        pool,
		riverClient: riverClient,
		metrics:     m,
	}
}

// Work processes a single ProcessQBInvoiceUpdate job.
func (w *ProcessQBInvoiceUpdateWorker) Work(ctx context.Context, job *river.Job[ProcessQBInvoiceUpdateArgs]) error {
	start := time.Now()

	err := w.work(ctx, job)

	metrics.TrackJob(w.metrics, "qb_process_invoice_update", start, err)
	if err != nil {
		slog.ErrorContext(ctx, "job failed",
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
	// Fetch current invoice state from QB to confirm it's actually paid
	invoice, err := w.qb.GetInvoice(ctx, job.Args.QBInvoiceID)
	if err != nil {
		return fmt.Errorf("qb fetch invoice: %w", err)
	}

	if invoice.Balance != 0 {
		// Not fully paid yet — partial payment or other update, ignore
		slog.InfoContext(ctx, "qb invoice not fully paid, skipping",
			"qb_invoice_id", job.Args.QBInvoiceID,
			"balance", invoice.Balance,
		)
		return nil
	}

	// Look up Hiri order by QB invoice ID
	var order *domain.Order
	err = store.Tx(ctx, w.pool, func(tx pgx.Tx) error {
		var txErr error
		order, txErr = w.orders.GetOrderByQBInvoiceIDAsStaff(ctx, tx, job.Args.QBInvoiceID)
		return txErr
	})
	if err != nil {
		return fmt.Errorf("qb order lookup by invoice id: %w", err)
	}

	// Idempotency: already marked paid
	if order.PaymentStatus == domain.PaymentStatusCaptured {
		return nil
	}

	// Update order payment status in a transaction with audit record
	err = store.Tx(ctx, w.pool, func(tx pgx.Tx) error {
		if _, txErr := w.orders.UpdateOrderPaymentStatus(ctx, tx, order.ID, domain.PaymentStatusCaptured); txErr != nil {
			return txErr
		}

		return w.audit.Record(ctx, tx, audit.AuditEntry{
			ActorType:    domain.AuditActorTypeSystem,
			ActorName:    "qb_webhook",
			Action:       audit.AuditOrderPaymentCaptured,
			ResourceType: "order",
			ResourceID:   order.ID,
			Metadata: map[string]any{
				"qb_invoice_id":  job.Args.QBInvoiceID,
				"payment_method": "ach",
				"river_job_id":   job.ID,
			},
		})
	})
	if err != nil {
		return err
	}

	// Notify customer — enqueue email job
	return store.Tx(ctx, w.pool, func(tx pgx.Tx) error {
		_, txErr := w.riverClient.InsertTx(ctx, tx, EmailInvoicePaidArgs{
			OrderID:    order.ID,
			CustomerID: *order.CustomerID,
		}, nil)
		return txErr
	})
}
