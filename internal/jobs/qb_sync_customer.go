package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/platform/quickbooks"
	"github.com/dukerupert/hiri/internal/store"
)

// SyncQBCustomerWorker syncs customer details to QB, triggered by profile updates.
type SyncQBCustomerWorker struct {
	river.WorkerDefaults[SyncQBCustomerArgs]
	customers *store.CustomerStore
	settings  *store.SettingsStore
	qb        quickbooks.Client
	audit     *audit.AuditWriter
	pool      *pgxpool.Pool
	metrics   *metrics.Registry
}

// NewSyncQBCustomerWorker creates a new SyncQBCustomerWorker.
func NewSyncQBCustomerWorker(
	customers *store.CustomerStore,
	settings *store.SettingsStore,
	qb quickbooks.Client,
	auditWriter *audit.AuditWriter,
	pool *pgxpool.Pool,
	m *metrics.Registry,
) *SyncQBCustomerWorker {
	return &SyncQBCustomerWorker{
		customers: customers,
		settings:  settings,
		qb:        qb,
		audit:     auditWriter,
		pool:      pool,
		metrics:   m,
	}
}

// Work processes a single SyncQBCustomer job.
func (w *SyncQBCustomerWorker) Work(ctx context.Context, job *river.Job[SyncQBCustomerArgs]) error {
	start := time.Now()

	err := w.work(ctx, job)

	metrics.TrackJob(w.metrics, "qb_sync_customer", start, err)
	if err != nil {
		// Permanent QuickBooks failures cancel, and a cancelled job never
		// reaches jobs.ErrorHandler — so the level has to be decided here.
		terminal := !quickbooks.IsRetryable(err)
		logWorkerFailure(ctx, "qb_sync_customer", terminal,
			"job_kind", "qb_sync_customer",
			"job_id", job.ID,
			"attempt", job.Attempt,
			"customer_id", job.Args.CustomerID,
			"error", err.Error(),
		)
		if terminal {
			return river.JobCancel(fmt.Errorf("sync qb customer %s: %w", job.Args.CustomerID, err))
		}
	}
	return err
}

func (w *SyncQBCustomerWorker) work(ctx context.Context, job *river.Job[SyncQBCustomerArgs]) error {
	var customer *domain.Customer
	var mode domain.QBBillingMode
	err := store.Tx(ctx, w.pool, func(tx pgx.Tx) error {
		var txErr error
		customer, txErr = w.customers.GetByID(ctx, tx, job.Args.CustomerID)
		if txErr != nil {
			return txErr
		}
		mode, txErr = w.settings.GetQBBillingMode(ctx, tx)
		return txErr
	})
	if err != nil {
		return fmt.Errorf("get customer: %w", err)
	}

	// Test mode writes nothing to QuickBooks, and this worker is the other way
	// in: approving a wholesale account enqueues it to pre-create the customer
	// before their first order. During a proof period that would create a
	// customer in the merchant's real company and link it locally — and worse,
	// it would corrupt the proof itself, because that account's preview would
	// then report a match against a customer test mode had just made. Nothing
	// is lost by waiting: the invoice chain does find-or-create at billing
	// time, so going live picks this up on the first real order.
	if !mode.IsLive() {
		slog.InfoContext(ctx, "qb sync customer: skipped, billing is in test mode",
			"customer_id", job.Args.CustomerID)
		return nil
	}

	if customer.QBCustomerID == nil {
		// No QB customer yet — find-or-create (e.g., on wholesale approval).
		displayName := customerDisplayName(customer)
		found, findErr := w.qb.FindCustomer(ctx, displayName, customer.Email)
		if findErr != nil {
			return fmt.Errorf("qb find customer: %w", findErr)
		}

		var qbID string
		var auditAction string
		if found != nil {
			qbID = found.ID
			auditAction = audit.AuditQBCustomerLinked
		} else {
			qbID, findErr = w.qb.CreateCustomer(ctx, customer)
			if findErr != nil {
				return fmt.Errorf("qb create customer: %w", findErr)
			}
			auditAction = audit.AuditQBCustomerCreated
		}

		return store.Tx(ctx, w.pool, func(tx pgx.Tx) error {
			if txErr := w.customers.SetQBCustomerID(ctx, tx, customer.ID, qbID); txErr != nil {
				return txErr
			}
			return w.audit.Record(ctx, tx, audit.AuditEntry{
				ActorType:    domain.AuditActorTypeSystem,
				ActorName:    "qb_sync_customer",
				Action:       auditAction,
				ResourceType: "customer",
				ResourceID:   customer.ID,
				Metadata: map[string]any{
					"qb_customer_id": qbID,
					"river_job_id":   job.ID,
					"linked":         found != nil,
				},
			})
		})
	}

	// Skip if already synced after last update
	if customer.QBSyncedAt != nil && !customer.UpdatedAt.After(*customer.QBSyncedAt) {
		return nil
	}

	// External call outside transaction
	if err := w.qb.UpdateCustomer(ctx, *customer.QBCustomerID, customer); err != nil {
		return fmt.Errorf("qb update customer: %w", err)
	}

	// Persist sync timestamp with audit
	return store.Tx(ctx, w.pool, func(tx pgx.Tx) error {
		if txErr := w.customers.SetQBSyncedAt(ctx, tx, customer.ID); txErr != nil {
			return txErr
		}
		return w.audit.Record(ctx, tx, audit.AuditEntry{
			ActorType:    domain.AuditActorTypeSystem,
			ActorName:    "qb_sync_customer",
			Action:       audit.AuditQBCustomerSynced,
			ResourceType: "customer",
			ResourceID:   customer.ID,
			Metadata: map[string]any{
				"qb_customer_id": *customer.QBCustomerID,
				"river_job_id":   job.ID,
			},
		})
	})
}
