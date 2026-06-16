package jobs

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/app"
)

// WhiteLabelInviteWorker delegates to WhiteLabelService.SendInviteEmail.
type WhiteLabelInviteWorker struct {
	river.WorkerDefaults[WhiteLabelInviteArgs]
	whiteLabel *app.WhiteLabelService
	pool       *pgxpool.Pool
}

// NewWhiteLabelInviteWorker creates a new WhiteLabelInviteWorker.
func NewWhiteLabelInviteWorker(whiteLabel *app.WhiteLabelService, pool *pgxpool.Pool) *WhiteLabelInviteWorker {
	return &WhiteLabelInviteWorker{whiteLabel: whiteLabel, pool: pool}
}

// Work processes a white-label invite email job.
func (w *WhiteLabelInviteWorker) Work(ctx context.Context, job *river.Job[WhiteLabelInviteArgs]) error {
	return w.whiteLabel.SendInviteEmail(ctx, w.pool, job.Args.CustomerID)
}

// WhiteLabelSubmittedWorker delegates to WhiteLabelService.SendSubmissionNotice.
type WhiteLabelSubmittedWorker struct {
	river.WorkerDefaults[WhiteLabelSubmittedArgs]
	whiteLabel *app.WhiteLabelService
	pool       *pgxpool.Pool
}

// NewWhiteLabelSubmittedWorker creates a new WhiteLabelSubmittedWorker.
func NewWhiteLabelSubmittedWorker(whiteLabel *app.WhiteLabelService, pool *pgxpool.Pool) *WhiteLabelSubmittedWorker {
	return &WhiteLabelSubmittedWorker{whiteLabel: whiteLabel, pool: pool}
}

// Work processes a white-label submission staff-notification job.
func (w *WhiteLabelSubmittedWorker) Work(ctx context.Context, job *river.Job[WhiteLabelSubmittedArgs]) error {
	return w.whiteLabel.SendSubmissionNotice(ctx, w.pool, job.Args.ProductID)
}
