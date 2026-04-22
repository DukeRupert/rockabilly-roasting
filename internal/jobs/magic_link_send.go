package jobs

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/app"
)

// MagicLinkSendWorker delegates to AuthService.SendMagicLink. All loading,
// rendering, sending, audit and metrics live in the app layer.
type MagicLinkSendWorker struct {
	river.WorkerDefaults[MagicLinkSendArgs]
	auth *app.AuthService
	pool *pgxpool.Pool
}

// NewMagicLinkSendWorker creates a new MagicLinkSendWorker.
func NewMagicLinkSendWorker(auth *app.AuthService, pool *pgxpool.Pool) *MagicLinkSendWorker {
	return &MagicLinkSendWorker{auth: auth, pool: pool}
}

// Work processes a magic link send job.
func (w *MagicLinkSendWorker) Work(ctx context.Context, job *river.Job[MagicLinkSendArgs]) error {
	return w.auth.SendMagicLink(ctx, w.pool, job.Args.CustomerID, job.Args.RawToken, job.Args.Next)
}
