package jobs

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/app"
)

// InvoicePastDueEmailWorker delegates to OrderService.SendInvoicePastDueEmail.
type InvoicePastDueEmailWorker struct {
	river.WorkerDefaults[EmailInvoicePastDueArgs]
	orders *app.OrderService
	pool   *pgxpool.Pool
}

// NewInvoicePastDueEmailWorker creates a new InvoicePastDueEmailWorker.
func NewInvoicePastDueEmailWorker(orders *app.OrderService, pool *pgxpool.Pool) *InvoicePastDueEmailWorker {
	return &InvoicePastDueEmailWorker{orders: orders, pool: pool}
}

// Work sends the past-due reminder email for the milestone in the job args.
func (w *InvoicePastDueEmailWorker) Work(ctx context.Context, job *river.Job[EmailInvoicePastDueArgs]) error {
	return w.orders.SendInvoicePastDueEmail(ctx, w.pool, job.Args.OrderID, job.Args.CustomerID, job.Args.Stage)
}
