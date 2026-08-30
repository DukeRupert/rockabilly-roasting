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
}

// NewServiceMaintenanceSweepWorker creates a new ServiceMaintenanceSweepWorker.
func NewServiceMaintenanceSweepWorker(plans *app.ServicePlanService, pool *pgxpool.Pool) *ServiceMaintenanceSweepWorker {
	return &ServiceMaintenanceSweepWorker{plans: plans, pool: pool}
}

// Work runs the sweep. Thin by design — everything it does lives in
// ServicePlanService.SweepMaintenance, which is testable without a River client.
func (w *ServiceMaintenanceSweepWorker) Work(ctx context.Context, _ *river.Job[ServiceMaintenanceSweepArgs]) error {
	return w.plans.SweepMaintenance(ctx, w.pool, time.Now())
}
