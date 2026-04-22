package jobs

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/app"
)

// WholesaleApplicationNotifyWorker delegates to WholesaleService.SendApplicationNotice.
type WholesaleApplicationNotifyWorker struct {
	river.WorkerDefaults[WholesaleApplicationNotifyArgs]
	wholesale *app.WholesaleService
	pool      *pgxpool.Pool
}

// NewWholesaleApplicationNotifyWorker creates a new WholesaleApplicationNotifyWorker.
func NewWholesaleApplicationNotifyWorker(wholesale *app.WholesaleService, pool *pgxpool.Pool) *WholesaleApplicationNotifyWorker {
	return &WholesaleApplicationNotifyWorker{wholesale: wholesale, pool: pool}
}

// Work processes a wholesale application notification job.
func (w *WholesaleApplicationNotifyWorker) Work(ctx context.Context, job *river.Job[WholesaleApplicationNotifyArgs]) error {
	return w.wholesale.SendApplicationNotice(ctx, w.pool, job.Args.CustomerID)
}
