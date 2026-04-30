package jobs

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/app"
)

// OrderShippedEmailWorker delegates to OrderService.SendOrderShippedEmail.
type OrderShippedEmailWorker struct {
	river.WorkerDefaults[OrderShippedEmailArgs]
	orders *app.OrderService
	pool   *pgxpool.Pool
}

// NewOrderShippedEmailWorker creates a new OrderShippedEmailWorker.
func NewOrderShippedEmailWorker(orders *app.OrderService, pool *pgxpool.Pool) *OrderShippedEmailWorker {
	return &OrderShippedEmailWorker{orders: orders, pool: pool}
}

// Work processes an "order shipped" email job.
func (w *OrderShippedEmailWorker) Work(ctx context.Context, job *river.Job[OrderShippedEmailArgs]) error {
	return w.orders.SendOrderShippedEmail(ctx, w.pool, job.Args.OrderID, job.Args.CustomerID, job.Args.ShipmentID)
}
