package jobs

import (
	"context"
	"fmt"
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
	settings    *store.SettingsStore
	qb          quickbooks.Client
	audit       *audit.AuditWriter
	pool        *pgxpool.Pool
	riverClient *river.Client[pgx.Tx]
	metrics     *metrics.Registry
}

// NewEnsureQBCustomerWorker creates a new EnsureQBCustomerWorker.
func NewEnsureQBCustomerWorker(
	customers *store.CustomerStore,
	settings *store.SettingsStore,
	qb quickbooks.Client,
	auditWriter *audit.AuditWriter,
	pool *pgxpool.Pool,
	riverClient *river.Client[pgx.Tx],
	m *metrics.Registry,
) *EnsureQBCustomerWorker {
	return &EnsureQBCustomerWorker{
		customers:   customers,
		settings:    settings,
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
		// Permanent QuickBooks failures cancel, and a cancelled job never
		// reaches jobs.ErrorHandler — so the level has to be decided here.
		terminal := !quickbooks.IsRetryable(err)
		logWorkerFailure(ctx, "qb_ensure_customer", terminal,
			"job_kind", "qb_ensure_customer",
			"job_id", job.ID,
			"attempt", job.Attempt,
			"customer_id", job.Args.CustomerID,
			"error", err.Error(),
		)
		// Bad request errors from QB are permanent — data needs fixing. This
		// job is the head of the invoicing chain, so its failure also means
		// the order never gets billed: alert staff.
		if terminal {
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
	// Read customer and the billing mode together — both are needed before
	// anything can be decided, and neither is worth its own round trip.
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

	// Look, but write nothing. Two situations behave identically here and are
	// deliberately one branch: a shadow-mode run, and an account nothing bills
	// automatically. Both need to know whether this customer matches something
	// in QuickBooks — the review page reports it, and Bill now needs it before
	// a person commits to invoicing — and neither may write to the merchant's
	// books to find out. FindCustomer is read-only, so asking is free.
	//
	// The earlier version skipped the lookup entirely for a manual account,
	// which left the preview unable to say anything about a matching customer:
	// not "no match" but "never asked", presented identically. Bill now on
	// such a row then had nothing to warn with.
	writesAllowed := mode.IsLive() && (customer.BillingMethod.AutoInvoiced() || job.Args.StaffRequested)
	if !writesAllowed {
		qbID := ""
		lookupErr := ""
		if customer.QBCustomerID != nil {
			qbID = *customer.QBCustomerID
		} else {
			found, findErr := w.qb.FindCustomer(ctx, customerDisplayName(customer), customer.Email)
			switch {
			case findErr != nil:
				// Carried forward rather than returned. Failing here would
				// stop the chain, so no preview would be written and the order
				// would simply be absent from the review page and the digest —
				// indistinguishable from an order with nothing to bill. That
				// is the conclusion a proof period must never invite, and this
				// is the likeliest call to fail, being the one the proof period
				// exists to exercise. The row says what went wrong instead.
				lookupErr = findErr.Error()
			case found != nil:
				qbID = found.ID
			}
		}
		return store.Tx(ctx, w.pool, func(tx pgx.Tx) error {
			_, txErr := w.riverClient.InsertTx(ctx, tx, CreateQBInvoiceArgs{
				OrderID:             job.Args.OrderID,
				QBCustomerID:        qbID,
				CustomerLookupError: lookupErr,
				StaffRequested:      job.Args.StaffRequested,
			}, nil)
			return txErr
		})
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
			OrderID:        job.Args.OrderID,
			QBCustomerID:   *customer.QBCustomerID,
			StaffRequested: job.Args.StaffRequested,
		}, nil)
		return txErr
	})
}
