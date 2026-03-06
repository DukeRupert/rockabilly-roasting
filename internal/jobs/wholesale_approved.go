package jobs

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/store"
)

// WholesaleApprovedWorker sends a welcome email to an approved wholesale customer.
type WholesaleApprovedWorker struct {
	river.WorkerDefaults[WholesaleApprovedArgs]
	customers *store.CustomerStore
	pool      *pgxpool.Pool
}

// NewWholesaleApprovedWorker creates a new WholesaleApprovedWorker.
func NewWholesaleApprovedWorker(customers *store.CustomerStore, pool *pgxpool.Pool) *WholesaleApprovedWorker {
	return &WholesaleApprovedWorker{
		customers: customers,
		pool:      pool,
	}
}

// Work processes a wholesale approval notification job.
func (w *WholesaleApprovedWorker) Work(ctx context.Context, job *river.Job[WholesaleApprovedArgs]) error {
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

	// TODO: Send welcome email with login credentials and wholesale portal link.
	slog.Info("wholesale application approved — welcome email pending",
		"customer_id", customer.ID,
		"email", customer.Email,
		"company", customer.CompanyName,
		"river_job_id", job.ID,
	)

	return nil
}
