package jobs

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/app"
)

// WholesaleApprovedWorker delegates to WholesaleService.SendApprovalEmail.
type WholesaleApprovedWorker struct {
	river.WorkerDefaults[WholesaleApprovedArgs]
	wholesale *app.WholesaleService
	pool      *pgxpool.Pool
}

// NewWholesaleApprovedWorker creates a new WholesaleApprovedWorker.
func NewWholesaleApprovedWorker(wholesale *app.WholesaleService, pool *pgxpool.Pool) *WholesaleApprovedWorker {
	return &WholesaleApprovedWorker{wholesale: wholesale, pool: pool}
}

// Work processes a wholesale approval notification job.
func (w *WholesaleApprovedWorker) Work(ctx context.Context, job *river.Job[WholesaleApprovedArgs]) error {
	return w.wholesale.SendApprovalEmail(ctx, w.pool, job.Args.CustomerID)
}
