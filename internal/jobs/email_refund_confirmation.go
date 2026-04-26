package jobs

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/app"
)

// RefundConfirmationWorker delegates to OrderService.SendRefundConfirmationEmail.
type RefundConfirmationWorker struct {
	river.WorkerDefaults[RefundConfirmationArgs]
	orders *app.OrderService
	pool   *pgxpool.Pool
}

// NewRefundConfirmationWorker creates a new worker.
func NewRefundConfirmationWorker(orders *app.OrderService, pool *pgxpool.Pool) *RefundConfirmationWorker {
	return &RefundConfirmationWorker{orders: orders, pool: pool}
}

// Work processes a refund confirmation email job.
func (w *RefundConfirmationWorker) Work(ctx context.Context, job *river.Job[RefundConfirmationArgs]) error {
	return w.orders.SendRefundConfirmationEmail(ctx, w.pool, job.Args.OrderID, job.Args.CustomerID, job.Args.RefundAmount)
}
