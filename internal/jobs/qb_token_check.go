package jobs

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
)

// qbTokenWarnWindow is how far ahead of refresh-token expiry the daily check
// starts warning staff. In healthy operation every QB API call rotates the
// refresh token and pushes expiry ~100 days out, so remaining life inside
// this window means refreshes have stopped (idle or broken) and the
// connection will lapse without a reconnect.
const qbTokenWarnWindow = 7 * 24 * time.Hour

// CheckQBTokenWorker runs the daily refresh-token expiry check and emails
// staff (daily, until reconnected) once expiry is inside the warning window.
type CheckQBTokenWorker struct {
	river.WorkerDefaults[CheckQBTokenArgs]
	creds    *store.QBCredentialStore
	tenantID uuid.UUID
	orders   *app.OrderService
	pool     *pgxpool.Pool
	metrics  *metrics.Registry
}

// NewCheckQBTokenWorker creates a new CheckQBTokenWorker.
func NewCheckQBTokenWorker(
	creds *store.QBCredentialStore,
	tenantID uuid.UUID,
	orders *app.OrderService,
	pool *pgxpool.Pool,
	m *metrics.Registry,
) *CheckQBTokenWorker {
	return &CheckQBTokenWorker{
		creds:    creds,
		tenantID: tenantID,
		orders:   orders,
		pool:     pool,
		metrics:  m,
	}
}

// Work runs one expiry check.
func (w *CheckQBTokenWorker) Work(ctx context.Context, job *river.Job[CheckQBTokenArgs]) error {
	start := time.Now()
	err := w.work(ctx, job.ID)
	metrics.TrackJob(w.metrics, "qb_token_check", start, err)
	if err != nil {
		// Never cancels, so every failure here is one River will retry and
		// jobs.ErrorHandler will page on if it runs out of attempts.
		logWorkerFailure(ctx, "qb_token_check", false,
			"job_kind", "qb_token_check",
			"job_id", job.ID,
			"attempt", job.Attempt,
			"error", err.Error(),
		)
	}
	return err
}

func (w *CheckQBTokenWorker) work(ctx context.Context, riverJobID int64) error {
	var creds *domain.QBCredentials
	err := store.Tx(ctx, w.pool, func(tx pgx.Tx) error {
		var txErr error
		creds, txErr = w.creds.GetByTenantID(ctx, tx, w.tenantID)
		return txErr
	})
	if err != nil {
		// Never connected (or disconnected) — nothing to watch, don't nag.
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}

	if time.Until(creds.RefreshExpiresAt) > qbTokenWarnWindow {
		return nil
	}

	return w.orders.SendQBTokenAlertEmail(ctx, w.pool, w.tenantID, creds.RefreshExpiresAt, riverJobID)
}
