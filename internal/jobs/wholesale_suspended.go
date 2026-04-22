package jobs

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/app"
)

// WholesaleSuspendedWorker delegates to WholesaleService.SendSuspensionEmail.
type WholesaleSuspendedWorker struct {
	river.WorkerDefaults[WholesaleSuspendedArgs]
	wholesale *app.WholesaleService
	pool      *pgxpool.Pool
}

// NewWholesaleSuspendedWorker creates a new WholesaleSuspendedWorker.
func NewWholesaleSuspendedWorker(wholesale *app.WholesaleService, pool *pgxpool.Pool) *WholesaleSuspendedWorker {
	return &WholesaleSuspendedWorker{wholesale: wholesale, pool: pool}
}

// Work processes a wholesale suspension notification job.
func (w *WholesaleSuspendedWorker) Work(ctx context.Context, job *river.Job[WholesaleSuspendedArgs]) error {
	return w.wholesale.SendSuspensionEmail(ctx, w.pool, job.Args.CustomerID)
}
