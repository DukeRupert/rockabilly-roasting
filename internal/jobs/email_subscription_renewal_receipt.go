package jobs

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/app"
)

// SubscriptionRenewalReceiptWorker delegates to OrderService.SendRenewalReceiptEmail.
type SubscriptionRenewalReceiptWorker struct {
	river.WorkerDefaults[SubscriptionRenewalReceiptArgs]
	orders *app.OrderService
	pool   *pgxpool.Pool
}

// NewSubscriptionRenewalReceiptWorker creates a new worker.
func NewSubscriptionRenewalReceiptWorker(orders *app.OrderService, pool *pgxpool.Pool) *SubscriptionRenewalReceiptWorker {
	return &SubscriptionRenewalReceiptWorker{orders: orders, pool: pool}
}

// Work processes a renewal receipt email job.
func (w *SubscriptionRenewalReceiptWorker) Work(ctx context.Context, job *river.Job[SubscriptionRenewalReceiptArgs]) error {
	return w.orders.SendRenewalReceiptEmail(ctx, w.pool, job.Args.OrderID, job.Args.CustomerID)
}
