package jobs

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/app"
)

// EmailVerifySendWorker delegates to AuthService.SendVerificationEmail. All
// loading, rendering, sending, audit and metrics live in the app layer.
type EmailVerifySendWorker struct {
	river.WorkerDefaults[EmailVerifySendArgs]
	auth *app.AuthService
	pool *pgxpool.Pool
}

// NewEmailVerifySendWorker creates a new EmailVerifySendWorker.
func NewEmailVerifySendWorker(auth *app.AuthService, pool *pgxpool.Pool) *EmailVerifySendWorker {
	return &EmailVerifySendWorker{auth: auth, pool: pool}
}

// Work processes an email verification send job.
func (w *EmailVerifySendWorker) Work(ctx context.Context, job *river.Job[EmailVerifySendArgs]) error {
	return w.auth.SendVerificationEmail(ctx, w.pool, job.Args.CustomerID, job.Args.RawToken, job.Args.Next)
}
