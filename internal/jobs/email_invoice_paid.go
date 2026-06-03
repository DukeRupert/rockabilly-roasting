package jobs

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/app"
)

// InvoicePaidEmailWorker delegates to OrderService.SendInvoicePaidEmail.
type InvoicePaidEmailWorker struct {
	river.WorkerDefaults[EmailInvoicePaidArgs]
	orders *app.OrderService
	pool   *pgxpool.Pool
}

// NewInvoicePaidEmailWorker creates a new InvoicePaidEmailWorker.
func NewInvoicePaidEmailWorker(orders *app.OrderService, pool *pgxpool.Pool) *InvoicePaidEmailWorker {
	return &InvoicePaidEmailWorker{orders: orders, pool: pool}
}

// Work sends the invoice payment-confirmation email.
func (w *InvoicePaidEmailWorker) Work(ctx context.Context, job *river.Job[EmailInvoicePaidArgs]) error {
	return w.orders.SendInvoicePaidEmail(ctx, w.pool, job.Args.OrderID, job.Args.CustomerID)
}
