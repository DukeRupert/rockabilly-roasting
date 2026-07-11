package jobs

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/app"
)

// QBInvoiceAlertEmailWorker delegates to OrderService.SendQBInvoiceAlertEmail.
type QBInvoiceAlertEmailWorker struct {
	river.WorkerDefaults[EmailQBInvoiceAlertArgs]
	orders *app.OrderService
	pool   *pgxpool.Pool
}

// NewQBInvoiceAlertEmailWorker creates a new QBInvoiceAlertEmailWorker.
func NewQBInvoiceAlertEmailWorker(orders *app.OrderService, pool *pgxpool.Pool) *QBInvoiceAlertEmailWorker {
	return &QBInvoiceAlertEmailWorker{orders: orders, pool: pool}
}

// Work notifies staff that a QB invoicing job failed permanently.
func (w *QBInvoiceAlertEmailWorker) Work(ctx context.Context, job *river.Job[EmailQBInvoiceAlertArgs]) error {
	return w.orders.SendQBInvoiceAlertEmail(ctx, w.pool, job.Args.OrderID, job.Args.FailedKind, job.Args.Cause)
}
