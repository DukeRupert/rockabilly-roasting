package jobs

import (
	"context"
	"errors"
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

// SendQBInvoiceWorker has QBO email an invoice to the customer. It runs as its
// own job (chained by CreateQBInvoice in the tx that persists the invoice ID)
// so a send failure retries independently and can never re-create the invoice.
type SendQBInvoiceWorker struct {
	river.WorkerDefaults[SendQBInvoiceArgs]
	qb          quickbooks.Client
	audit       *audit.AuditWriter
	pool        *pgxpool.Pool
	riverClient *river.Client[pgx.Tx]
	metrics     *metrics.Registry
}

// NewSendQBInvoiceWorker creates a new SendQBInvoiceWorker.
func NewSendQBInvoiceWorker(
	qb quickbooks.Client,
	auditWriter *audit.AuditWriter,
	pool *pgxpool.Pool,
	riverClient *river.Client[pgx.Tx],
	m *metrics.Registry,
) *SendQBInvoiceWorker {
	return &SendQBInvoiceWorker{
		qb:          qb,
		audit:       auditWriter,
		pool:        pool,
		riverClient: riverClient,
		metrics:     m,
	}
}

// Work processes a single SendQBInvoice job.
func (w *SendQBInvoiceWorker) Work(ctx context.Context, job *river.Job[SendQBInvoiceArgs]) error {
	start := time.Now()

	err := w.work(ctx, job)

	metrics.TrackJob(w.metrics, "qb_send_invoice", start, err)
	if err != nil {
		slog.ErrorContext(ctx, "background job qb_send_invoice failed",
			"job_kind", "qb_send_invoice",
			"job_id", job.ID,
			"order_id", job.Args.OrderID,
			"qb_invoice_id", job.Args.QBInvoiceID,
			"error", err.Error(),
		)
		if !quickbooks.IsRetryable(err) {
			enqueueQBInvoiceAlert(ctx, w.pool, w.riverClient, job.Args.OrderID, "qb_send_invoice", err)
			return river.JobCancel(fmt.Errorf("send qb invoice %s: %w", job.Args.QBInvoiceID, err))
		}
		if job.Attempt >= job.MaxAttempts {
			// Final retry burned — River discards the job after this return,
			// so alert now or the failure is silent.
			enqueueQBInvoiceAlert(ctx, w.pool, w.riverClient, job.Args.OrderID, "qb_send_invoice", err)
		}
	}
	return err
}

func (w *SendQBInvoiceWorker) work(ctx context.Context, job *river.Job[SendQBInvoiceArgs]) error {
	// Idempotency: QB records EmailStatus once an invoice has been emailed, so
	// a retry after a successful send (e.g. the audit write below failed)
	// doesn't email the customer twice.
	inv, err := w.qb.GetInvoice(ctx, job.Args.QBInvoiceID)
	if err != nil {
		if errors.Is(err, quickbooks.ErrNotFound) {
			// Deleted in QB before we could send — nothing to email; the
			// reconcile poll reverts the order so a fresh invoice can be cut.
			slog.WarnContext(ctx, "qb send invoice: invoice gone, skipping",
				"order_id", job.Args.OrderID, "qb_invoice_id", job.Args.QBInvoiceID)
			return nil
		}
		return fmt.Errorf("qb get invoice: %w", err)
	}
	if inv.EmailStatus == quickbooks.EmailStatusSent {
		return nil
	}

	// External call outside any transaction.
	if err := w.qb.SendInvoice(ctx, job.Args.QBInvoiceID); err != nil {
		return fmt.Errorf("qb send invoice: %w", err)
	}

	return store.Tx(ctx, w.pool, func(tx pgx.Tx) error {
		return w.audit.Record(ctx, tx, audit.AuditEntry{
			ActorType:    domain.AuditActorTypeSystem,
			ActorName:    "qb_send_invoice",
			Action:       audit.AuditQBInvoiceEmailed,
			ResourceType: "order",
			ResourceID:   job.Args.OrderID,
			Metadata: map[string]any{
				"qb_invoice_id": job.Args.QBInvoiceID,
				"river_job_id":  job.ID,
			},
		})
	})
}
