package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/platform/quickbooks"
	"github.com/dukerupert/hiri/internal/store"

	"github.com/dukerupert/hiri/internal/domain"
)

// customerDisplayName returns the display name for a QB customer.
// Uses CompanyName if available (wholesale), otherwise "FirstName LastName".
func customerDisplayName(c *domain.Customer) string {
	if c.CompanyName != nil && *c.CompanyName != "" {
		return *c.CompanyName
	}
	return c.FirstName + " " + c.LastName
}

// EnsureQBCustomerWorker creates or updates a QB customer, then chains to CreateQBInvoice.
type EnsureQBCustomerWorker struct {
	river.WorkerDefaults[EnsureQBCustomerArgs]
	customers   *store.CustomerStore
	qb          quickbooks.Client
	audit       *audit.AuditWriter
	pool        *pgxpool.Pool
	riverClient *river.Client[pgx.Tx]
	metrics     *metrics.Registry
}

// NewEnsureQBCustomerWorker creates a new EnsureQBCustomerWorker.
func NewEnsureQBCustomerWorker(
	customers *store.CustomerStore,
	qb quickbooks.Client,
	auditWriter *audit.AuditWriter,
	pool *pgxpool.Pool,
	riverClient *river.Client[pgx.Tx],
	m *metrics.Registry,
) *EnsureQBCustomerWorker {
	return &EnsureQBCustomerWorker{
		customers:   customers,
		qb:          qb,
		audit:       auditWriter,
		pool:        pool,
		riverClient: riverClient,
		metrics:     m,
	}
}

// Work processes a single EnsureQBCustomer job.
func (w *EnsureQBCustomerWorker) Work(ctx context.Context, job *river.Job[EnsureQBCustomerArgs]) error {
	start := time.Now()

	err := w.work(ctx, job)

	metrics.TrackJob(w.metrics, "qb_ensure_customer", start, err)
	if err != nil {
		slog.ErrorContext(ctx, "background job qb_ensure_customer failed",
			"job_kind", "qb_ensure_customer",
			"job_id", job.ID,
			"customer_id", job.Args.CustomerID,
			"error", err.Error(),
		)
		// Bad request errors from QB are permanent — data needs fixing. This
		// job is the head of the invoicing chain, so its failure also means
		// the order never gets billed: alert staff.
		if !quickbooks.IsRetryable(err) {
			enqueueQBInvoiceAlert(ctx, w.pool, w.riverClient, job.Args.OrderID, "qb_ensure_customer", err)
			return river.JobCancel(fmt.Errorf("ensure qb customer %s: %w", job.Args.CustomerID, err))
		}
		if job.Attempt >= job.MaxAttempts {
			// Final retry burned — River discards the job after this return,
			// so alert now or the failure is silent.
			enqueueQBInvoiceAlert(ctx, w.pool, w.riverClient, job.Args.OrderID, "qb_ensure_customer", err)
		}
	}
	return err
}

func (w *EnsureQBCustomerWorker) work(ctx context.Context, job *river.Job[EnsureQBCustomerArgs]) error {
	// Read customer
	var customer *domain.Customer
	err := store.Tx(ctx, w.pool, func(tx pgx.Tx) error {
		var txErr error
		customer, txErr = w.customers.GetByID(ctx, tx, job.Args.CustomerID)
		return txErr
	})
	if err != nil {
		return fmt.Errorf("get customer: %w", err)
	}

	if customer.QBCustomerID == nil {
		// Try to find an existing QB customer before creating a new one.
		// Many wholesale clients already exist in QuickBooks.
		displayName := customerDisplayName(customer)
		found, err := w.qb.FindCustomer(ctx, displayName, customer.Email)
		if err != nil {
			return fmt.Errorf("qb find customer: %w", err)
		}

		var qbID string
		var auditAction string
		if found != nil {
			// Link to existing QB customer
			qbID = found.ID
			auditAction = audit.AuditQBCustomerLinked
		} else {
			// No match — create a new QB customer
			qbID, err = w.qb.CreateCustomer(ctx, customer)
			if err != nil {
				return fmt.Errorf("qb create customer: %w", err)
			}
			auditAction = audit.AuditQBCustomerCreated
		}

		// Persist QB customer ID in a transaction with audit
		err = store.Tx(ctx, w.pool, func(tx pgx.Tx) error {
			if txErr := w.customers.SetQBCustomerID(ctx, tx, customer.ID, qbID); txErr != nil {
				return txErr
			}
			return w.audit.Record(ctx, tx, audit.AuditEntry{
				ActorType:    domain.AuditActorTypeSystem,
				ActorName:    "qb_ensure_customer",
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
		if err != nil {
			return err
		}
		customer.QBCustomerID = &qbID
	} else {
		// Existing customer — sync if details changed since last sync
		if customer.QBSyncedAt == nil || customer.UpdatedAt.After(*customer.QBSyncedAt) {
			if err := w.qb.UpdateCustomer(ctx, *customer.QBCustomerID, customer); err != nil {
				return fmt.Errorf("qb update customer: %w", err)
			}
			err = store.Tx(ctx, w.pool, func(tx pgx.Tx) error {
				if txErr := w.customers.SetQBSyncedAt(ctx, tx, customer.ID); txErr != nil {
					return txErr
				}
				return w.audit.Record(ctx, tx, audit.AuditEntry{
					ActorType:    domain.AuditActorTypeSystem,
					ActorName:    "qb_ensure_customer",
					Action:       audit.AuditQBCustomerSynced,
					ResourceType: "customer",
					ResourceID:   customer.ID,
					Metadata: map[string]any{
						"qb_customer_id": *customer.QBCustomerID,
						"river_job_id":   job.ID,
					},
				})
			})
			if err != nil {
				return err
			}
		}
	}

	// Chain to invoice creation
	return store.Tx(ctx, w.pool, func(tx pgx.Tx) error {
		_, txErr := w.riverClient.InsertTx(ctx, tx, CreateQBInvoiceArgs{
			OrderID:      job.Args.OrderID,
			QBCustomerID: *customer.QBCustomerID,
		}, nil)
		return txErr
	})
}
