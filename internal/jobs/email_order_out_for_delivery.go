package jobs

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/app"
)

// OrderOutForDeliveryEmailWorker delegates to
// OrderService.SendOrderOutForDeliveryEmail.
type OrderOutForDeliveryEmailWorker struct {
	river.WorkerDefaults[OrderOutForDeliveryEmailArgs]
	orders *app.OrderService
	pool   *pgxpool.Pool
}

// NewOrderOutForDeliveryEmailWorker creates a new worker.
func NewOrderOutForDeliveryEmailWorker(orders *app.OrderService, pool *pgxpool.Pool) *OrderOutForDeliveryEmailWorker {
	return &OrderOutForDeliveryEmailWorker{orders: orders, pool: pool}
}

// Work processes an "out for local delivery" email job.
func (w *OrderOutForDeliveryEmailWorker) Work(ctx context.Context, job *river.Job[OrderOutForDeliveryEmailArgs]) error {
	return w.orders.SendOrderOutForDeliveryEmail(ctx, w.pool, job.Args.OrderID, job.Args.CustomerID)
}
