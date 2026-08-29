package jobs

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/app"
)

// ServiceTicketOpenedWorker mails the crew when a wholesale customer reports a
// broken machine from their account.
//
// Registered on every instance for the same reason ServiceStaleSweepWorker is:
// the module is a runtime toggle, and a worker that existed only where it was
// switched on would have to be added and removed by a deploy. The service
// method returns early when the module is off, which also means a job enqueued
// just before somebody switched the module off is dropped rather than failing
// forever in River.
type ServiceTicketOpenedWorker struct {
	river.WorkerDefaults[ServiceTicketOpenedArgs]
	tickets *app.ServiceTicketService
	pool    *pgxpool.Pool
}

// NewServiceTicketOpenedWorker creates a new ServiceTicketOpenedWorker.
func NewServiceTicketOpenedWorker(tickets *app.ServiceTicketService, pool *pgxpool.Pool) *ServiceTicketOpenedWorker {
	return &ServiceTicketOpenedWorker{tickets: tickets, pool: pool}
}

// Work sends the notice. Thin by design — everything it does lives in
// ServiceTicketService.SendTicketOpenedNotice.
func (w *ServiceTicketOpenedWorker) Work(ctx context.Context, job *river.Job[ServiceTicketOpenedArgs]) error {
	return w.tickets.SendTicketOpenedNotice(ctx, w.pool, job.Args.TicketID)
}
