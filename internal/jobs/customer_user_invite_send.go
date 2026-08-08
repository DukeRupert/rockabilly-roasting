package jobs

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/app"
)

// CustomerUserInviteSendWorker delegates to CustomerUserService.SendInviteEmail.
// All loading, rendering, sending and metrics live in the app layer.
type CustomerUserInviteSendWorker struct {
	river.WorkerDefaults[CustomerUserInviteSendArgs]
	customerUsers *app.CustomerUserService
	pool          *pgxpool.Pool
}

// NewCustomerUserInviteSendWorker creates a new CustomerUserInviteSendWorker.
func NewCustomerUserInviteSendWorker(customerUsers *app.CustomerUserService, pool *pgxpool.Pool) *CustomerUserInviteSendWorker {
	return &CustomerUserInviteSendWorker{customerUsers: customerUsers, pool: pool}
}

// Work processes a customer user invite send job.
func (w *CustomerUserInviteSendWorker) Work(ctx context.Context, job *river.Job[CustomerUserInviteSendArgs]) error {
	return w.customerUsers.SendInviteEmail(ctx, w.pool, job.Args.CustomerUserID, job.Args.RawToken)
}
