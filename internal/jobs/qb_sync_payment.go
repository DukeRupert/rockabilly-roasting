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

// SyncQBPaymentWorker records a manual payment in QBO against the linked invoice.
type SyncQBPaymentWorker struct {
	river.WorkerDefaults[SyncQBPaymentArgs]
	orders    *store.OrderStore
	customers *store.CustomerStore
	qb        quickbooks.Client
	audit     *audit.AuditWriter
	pool      *pgxpool.Pool
	metrics   *metrics.Registry
}

// NewSyncQBPaymentWorker creates a new SyncQBPaymentWorker.
func NewSyncQBPaymentWorker(
	orders *store.OrderStore,
	customers *store.CustomerStore,
	qb quickbooks.Client,
	auditWriter *audit.AuditWriter,
	pool *pgxpool.Pool,
	m *metrics.Registry,
) *SyncQBPaymentWorker {
	return &SyncQBPaymentWorker{
		orders:    orders,
		customers: customers,
		qb:        qb,
		audit:     auditWriter,
		pool:      pool,
		metrics:   m,
	}
}

// Work processes a single SyncQBPayment job.
func (w *SyncQBPaymentWorker) Work(ctx context.Context, job *river.Job[SyncQBPaymentArgs]) error {
	start := time.Now()

	err := w.work(ctx, job)

	metrics.TrackJob(w.metrics, "qb_sync_payment", start, err)
	if err != nil {
		// Permanent QuickBooks failures cancel, and a cancelled job never
		// reaches jobs.ErrorHandler — so the level has to be decided here.
		terminal := !quickbooks.IsRetryable(err)
		logWorkerFailure(ctx, "qb_sync_payment", terminal,
			"job_kind", "qb_sync_payment",
			"job_id", job.ID,
			"attempt", job.Attempt,
			"order_id", job.Args.OrderID,
			"error", err.Error(),
		)
		if terminal {
			return river.JobCancel(fmt.Errorf("sync qb payment for order %s: %w", job.Args.OrderID, err))
		}
	}
	return err
}

func (w *SyncQBPaymentWorker) work(ctx context.Context, job *river.Job[SyncQBPaymentArgs]) error {
	// Read order to get QB invoice ID and customer ID
	var order *domain.Order
	err := store.Tx(ctx, w.pool, func(tx pgx.Tx) error {
		var txErr error
		order, txErr = w.orders.GetOrderByIDAsStaff(ctx, tx, job.Args.OrderID)
		return txErr
	})
	if err != nil {
		return fmt.Errorf("get order: %w", err)
	}

	// No QB invoice linked — nothing to sync payment against
	if order.QBInvoiceID == nil {
		slog.WarnContext(ctx, "order has no QB invoice, skipping payment sync",
			"order_id", order.ID,
			"job_id", job.ID,
		)
		return nil
	}

	if order.CustomerID == nil {
		return river.JobCancel(fmt.Errorf("order %s has no customer", order.ID))
	}

	// Get QB customer ID from the customer record
	var customer *domain.Customer
	err = store.Tx(ctx, w.pool, func(tx pgx.Tx) error {
		var txErr error
		customer, txErr = w.customers.GetByID(ctx, tx, *order.CustomerID)
		return txErr
	})
	if err != nil {
		return fmt.Errorf("get customer: %w", err)
	}

	if customer.QBCustomerID == nil {
		slog.WarnContext(ctx, "customer has no QB customer ID, skipping payment sync",
			"customer_id", customer.ID,
			"order_id", order.ID,
			"job_id", job.ID,
		)
		return nil
	}

	// Create payment in QB (external call outside transaction)
	payment, err := w.qb.CreatePayment(ctx, quickbooks.PaymentParams{
		CustomerID: *customer.QBCustomerID,
		InvoiceID:  *order.QBInvoiceID,
		Amount:     job.Args.Amount,
		Method:     job.Args.Method,
		Reference:  job.Args.Reference,
	})
	if err != nil {
		return fmt.Errorf("qb create payment: %w", err)
	}

	// Audit the sync
	return store.Tx(ctx, w.pool, func(tx pgx.Tx) error {
		return w.audit.Record(ctx, tx, audit.AuditEntry{
			ActorType:    domain.AuditActorTypeSystem,
			ActorName:    "qb_sync_payment",
			Action:       audit.AuditQBPaymentSynced,
			ResourceType: "invoice",
			ResourceID:   job.Args.InvoiceID,
			Metadata: map[string]any{
				"qb_payment_id": payment.ID,
				"qb_invoice_id": *order.QBInvoiceID,
				"amount_cents":  job.Args.Amount,
				"method":        job.Args.Method,
				"river_job_id":  job.ID,
			},
		})
	})
}
