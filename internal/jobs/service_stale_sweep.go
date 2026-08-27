package jobs

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/app"
)

// ServiceStaleSweepArgs is the daily equipment-service staleness sweep.
type ServiceStaleSweepArgs struct{}

// Kind implements river.JobArgs.
func (ServiceStaleSweepArgs) Kind() string { return "service_stale_sweep" }

// ServiceStaleSweepWorker digests the open service tickets nobody has spoken to
// the customer about and mails staff about them.
//
// Registered on every instance, not only the ones running the equipment service
// module: the module is a runtime toggle, so a worker that existed only where it
// was switched on would have to be added and removed by a deploy. The service
// method returns early when the module is off, which costs one map lookup a day
// on shops that do not service machines.
type ServiceStaleSweepWorker struct {
	river.WorkerDefaults[ServiceStaleSweepArgs]
	tickets *app.ServiceTicketService
	pool    *pgxpool.Pool
}

// NewServiceStaleSweepWorker creates a new ServiceStaleSweepWorker.
func NewServiceStaleSweepWorker(tickets *app.ServiceTicketService, pool *pgxpool.Pool) *ServiceStaleSweepWorker {
	return &ServiceStaleSweepWorker{tickets: tickets, pool: pool}
}

// Work runs the sweep. Thin by design — everything it does lives in
// ServiceTicketService.SweepStaleTickets, which is where it can be tested
// without a River client.
func (w *ServiceStaleSweepWorker) Work(ctx context.Context, _ *river.Job[ServiceStaleSweepArgs]) error {
	return w.tickets.SweepStaleTickets(ctx, w.pool, time.Now())
}
