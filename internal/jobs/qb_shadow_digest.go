package jobs

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/app"
)

// qbShadowDigestWindowDays is the period each digest covers. It matches the
// weekly schedule: a digest that summarised more than the gap since the last
// one would report the same invoice twice.
const qbShadowDigestWindowDays = 7

// QBShadowDigestWorker delegates to OrderService.SendQBShadowDigestEmail.
//
// Thin by design, like the other periodic workers: the decision about whether
// a proof period is still running lives in the service, so flipping to live
// silences the digest without touching the scheduler.
type QBShadowDigestWorker struct {
	river.WorkerDefaults[QBShadowDigestArgs]
	orders *app.OrderService
	pool   *pgxpool.Pool
}

// NewQBShadowDigestWorker creates a new QBShadowDigestWorker.
func NewQBShadowDigestWorker(orders *app.OrderService, pool *pgxpool.Pool) *QBShadowDigestWorker {
	return &QBShadowDigestWorker{orders: orders, pool: pool}
}

// Work emails staff a summary of what QuickBooks billing would have done.
func (w *QBShadowDigestWorker) Work(ctx context.Context, job *river.Job[QBShadowDigestArgs]) error {
	return w.orders.SendQBShadowDigestEmail(ctx, w.pool, qbShadowDigestWindowDays)
}
