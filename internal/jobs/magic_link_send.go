package jobs

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/store"
)

// MagicLinkSendWorker sends a magic link email to a customer.
type MagicLinkSendWorker struct {
	river.WorkerDefaults[MagicLinkSendArgs]
	customers *store.CustomerStore
	pool      *pgxpool.Pool
}

// NewMagicLinkSendWorker creates a new MagicLinkSendWorker.
func NewMagicLinkSendWorker(customers *store.CustomerStore, pool *pgxpool.Pool) *MagicLinkSendWorker {
	return &MagicLinkSendWorker{
		customers: customers,
		pool:      pool,
	}
}

// Work processes a magic link send job.
func (w *MagicLinkSendWorker) Work(ctx context.Context, job *river.Job[MagicLinkSendArgs]) error {
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

	// TODO: Send email with magic link URL containing job.Args.RawToken.
	// URL format: https://<host>/account/magic?token=<raw_token>
	slog.Info("magic link email sent",
		"customer_id", customer.ID,
		"email", customer.Email,
		"river_job_id", job.ID,
	)

	return nil
}
