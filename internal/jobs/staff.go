package jobs

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/app"
)

// StaffInviteWorker delegates to StaffService.SendInviteEmail.
type StaffInviteWorker struct {
	river.WorkerDefaults[StaffInviteArgs]
	staff *app.StaffService
	pool  *pgxpool.Pool
}

// NewStaffInviteWorker creates a new StaffInviteWorker.
func NewStaffInviteWorker(staff *app.StaffService, pool *pgxpool.Pool) *StaffInviteWorker {
	return &StaffInviteWorker{staff: staff, pool: pool}
}

// Work processes a staff invite email job.
func (w *StaffInviteWorker) Work(ctx context.Context, job *river.Job[StaffInviteArgs]) error {
	return w.staff.SendInviteEmail(ctx, w.pool, job.Args.StaffID)
}
