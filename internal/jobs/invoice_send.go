package jobs

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/app"
)

// InvoiceSendWorker delegates to InvoiceService.SendInvoice.
type InvoiceSendWorker struct {
	river.WorkerDefaults[InvoiceSendArgs]
	invoices *app.InvoiceService
	pool     *pgxpool.Pool
}

// NewInvoiceSendWorker creates a new InvoiceSendWorker.
func NewInvoiceSendWorker(invoices *app.InvoiceService, pool *pgxpool.Pool) *InvoiceSendWorker {
	return &InvoiceSendWorker{invoices: invoices, pool: pool}
}

// Work processes an invoice send job.
func (w *InvoiceSendWorker) Work(ctx context.Context, job *river.Job[InvoiceSendArgs]) error {
	return w.invoices.SendInvoice(ctx, w.pool, job.Args.InvoiceID)
}
