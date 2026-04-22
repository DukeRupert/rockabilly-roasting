package jobs

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/app"
)

// OrderConfirmEmailWorker delegates to OrderService.SendConfirmationEmail.
type OrderConfirmEmailWorker struct {
	river.WorkerDefaults[OrderConfirmEmailArgs]
	orders *app.OrderService
	pool   *pgxpool.Pool
}

// NewOrderConfirmEmailWorker creates a new OrderConfirmEmailWorker.
func NewOrderConfirmEmailWorker(orders *app.OrderService, pool *pgxpool.Pool) *OrderConfirmEmailWorker {
	return &OrderConfirmEmailWorker{orders: orders, pool: pool}
}

// Work processes an order confirmation email job.
func (w *OrderConfirmEmailWorker) Work(ctx context.Context, job *river.Job[OrderConfirmEmailArgs]) error {
	return w.orders.SendConfirmationEmail(ctx, w.pool, job.Args.OrderID, job.Args.CustomerID)
}
