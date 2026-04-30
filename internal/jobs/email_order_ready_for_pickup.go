package jobs

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/app"
)

// OrderReadyForPickupEmailWorker delegates to
// OrderService.SendOrderReadyForPickupEmail.
type OrderReadyForPickupEmailWorker struct {
	river.WorkerDefaults[OrderReadyForPickupEmailArgs]
	orders *app.OrderService
	pool   *pgxpool.Pool
}

// NewOrderReadyForPickupEmailWorker creates a new worker.
func NewOrderReadyForPickupEmailWorker(orders *app.OrderService, pool *pgxpool.Pool) *OrderReadyForPickupEmailWorker {
	return &OrderReadyForPickupEmailWorker{orders: orders, pool: pool}
}

// Work processes a "ready for pickup" email job.
func (w *OrderReadyForPickupEmailWorker) Work(ctx context.Context, job *river.Job[OrderReadyForPickupEmailArgs]) error {
	return w.orders.SendOrderReadyForPickupEmail(ctx, w.pool, job.Args.OrderID, job.Args.CustomerID)
}
