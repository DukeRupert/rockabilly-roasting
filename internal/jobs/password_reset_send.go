package jobs

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/app"
)

// PasswordResetSendWorker delegates to AuthService.SendPasswordResetEmail. All
// loading, rendering, sending, audit and metrics live in the app layer.
type PasswordResetSendWorker struct {
	river.WorkerDefaults[PasswordResetSendArgs]
	auth *app.AuthService
	pool *pgxpool.Pool
}

// NewPasswordResetSendWorker creates a new PasswordResetSendWorker.
func NewPasswordResetSendWorker(auth *app.AuthService, pool *pgxpool.Pool) *PasswordResetSendWorker {
	return &PasswordResetSendWorker{auth: auth, pool: pool}
}

// Work processes a password reset send job.
func (w *PasswordResetSendWorker) Work(ctx context.Context, job *river.Job[PasswordResetSendArgs]) error {
	return w.auth.SendPasswordResetEmail(ctx, w.pool, job.Args.CustomerID, job.Args.RawToken)
}
