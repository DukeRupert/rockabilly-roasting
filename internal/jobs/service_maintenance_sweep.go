package jobs

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/app"
)

// ServiceMaintenanceSweepArgs is the daily preventive maintenance sweep.
type ServiceMaintenanceSweepArgs struct{}

// Kind implements river.JobArgs.
func (ServiceMaintenanceSweepArgs) Kind() string { return "service_maintenance_sweep" }

// ServiceMaintenanceSweepWorker backfills the maintenance schedule, books the
// covered work that has come due, and publishes the gauges.
//
// Registered on every instance, not only the ones running the equipment service
// module, for the same reason the stale sweep is: the module is a runtime
// toggle, so a worker that existed only where it was switched on would have to
// be added and removed by a deploy. The service method returns early when the
// module is off.
type ServiceMaintenanceSweepWorker struct {
	river.WorkerDefaults[ServiceMaintenanceSweepArgs]
	plans *app.ServicePlanService
	pool  *pgxpool.Pool
	// loc is the merchant's zone. Which calendar day it is decides what counts
	// as due, and this is the one path that acts on that answer unattended —
	// a sweep reading UTC would, in Los Angeles, spend every afternoon booking
	// tomorrow's work.
	loc *time.Location
}

// NewServiceMaintenanceSweepWorker creates a new ServiceMaintenanceSweepWorker.
func NewServiceMaintenanceSweepWorker(plans *app.ServicePlanService, pool *pgxpool.Pool, loc *time.Location) *ServiceMaintenanceSweepWorker {
	if loc == nil {
		loc = time.UTC
	}
	return &ServiceMaintenanceSweepWorker{plans: plans, pool: pool, loc: loc}
}

// Work runs the sweep. Thin by design — everything it does lives in
// ServicePlanService.SweepMaintenance, which is testable without a River client.
// The day is collapsed to a UTC midnight once resolved in the merchant's zone,
// the same shape the web handlers use, so it compares cleanly against the date
// columns pgx hands back.
func (w *ServiceMaintenanceSweepWorker) Work(ctx context.Context, job *river.Job[ServiceMaintenanceSweepArgs]) error {
	y, m, d := time.Now().In(w.loc).Date()
	// The job id rides along into the audit metadata. This is the one path that
	// opens customer tickets with no human behind it, and "which run did this"
	// is the first question anybody asks about one.
	return w.plans.SweepMaintenance(ctx, w.pool, time.Date(y, m, d, 0, 0, 0, 0, time.UTC), job.ID)
}
