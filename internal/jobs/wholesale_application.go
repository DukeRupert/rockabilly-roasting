package jobs

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/store"
)

// WholesaleApplicationNotifyWorker sends a notification to staff when a new wholesale application is submitted.
type WholesaleApplicationNotifyWorker struct {
	river.WorkerDefaults[WholesaleApplicationNotifyArgs]
	customers *store.CustomerStore
	pool      *pgxpool.Pool
}

// NewWholesaleApplicationNotifyWorker creates a new WholesaleApplicationNotifyWorker.
func NewWholesaleApplicationNotifyWorker(customers *store.CustomerStore, pool *pgxpool.Pool) *WholesaleApplicationNotifyWorker {
	return &WholesaleApplicationNotifyWorker{
		customers: customers,
		pool:      pool,
	}
}

// Work processes a wholesale application notification job.
func (w *WholesaleApplicationNotifyWorker) Work(ctx context.Context, job *river.Job[WholesaleApplicationNotifyArgs]) error {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	customer, err := w.customers.GetByID(ctx, tx, job.Args.CustomerID)
	if err != nil {
		return fmt.Errorf("get customer %s: %w", job.Args.CustomerID, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}

	// TODO: Send email notification to staff with customer details.
	slog.Info("wholesale application received",
		"customer_id", customer.ID,
		"email", customer.Email,
		"company", customer.CompanyName,
		"river_job_id", job.ID,
	)

	return nil
}
