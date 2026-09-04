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
//
// Gated on the billing mode like every other worker that writes to the
// merchant's books. The gate reads as redundant — this worker only acts on an
// order that already carries a QB invoice id, and only a live run ever sets
// one — but "redundant" here rests on the whole history of a column rather
// than on a check. Go live, bill some orders, switch back to test mode, and
// recording a payment against one of those orders would post a Payment into
// the real company while the admin says nothing is being written. Test mode
// is worth stating outright rather than inferring.
//
// The earlier reasoning for leaving it ungated was that refusing would strand
// money that had actually changed hands. It does not: RecordPayment has
// already committed the payment in Hiri before this job runs, and the skip
// writes an audit entry naming the invoice and amount, so what is owed in
// QuickBooks can be settled by hand or after going live.
type SyncQBPaymentWorker struct {
	river.WorkerDefaults[SyncQBPaymentArgs]
	orders    *store.OrderStore
	customers *store.CustomerStore
	settings  *store.SettingsStore
	qb        quickbooks.Client
	audit     *audit.AuditWriter
	pool      *pgxpool.Pool
	metrics   *metrics.Registry
}

// NewSyncQBPaymentWorker creates a new SyncQBPaymentWorker.
func NewSyncQBPaymentWorker(
	orders *store.OrderStore,
	customers *store.CustomerStore,
	settings *store.SettingsStore,
	qb quickbooks.Client,
	auditWriter *audit.AuditWriter,
	pool *pgxpool.Pool,
	m *metrics.Registry,
) *SyncQBPaymentWorker {
	return &SyncQBPaymentWorker{
		orders:    orders,
		customers: customers,
		settings:  settings,
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
	// Read order to get QB invoice ID and customer ID, and the billing mode
	// alongside it — both are needed before anything can be decided.
	var order *domain.Order
	var mode domain.QBBillingMode
	err := store.Tx(ctx, w.pool, func(tx pgx.Tx) error {
		var txErr error
		order, txErr = w.orders.GetOrderByIDAsStaff(ctx, tx, job.Args.OrderID)
		if txErr != nil {
			return txErr
		}
		mode, txErr = w.settings.GetQBBillingMode(ctx, tx)
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

	// Test mode writes nothing to the merchant's books. Checked after the
	// no-invoice return above, not before it: during a genuine proof period no
	// order carries a QB invoice at all, and announcing a skipped sync for
	// every wholesale payment would bury the case this is here for — an
	// invoice really does exist in QuickBooks and the switch says don't touch
	// it. The payment is already recorded in Hiri; this says what QuickBooks
	// still shows as owed.
	if !mode.IsLive() {
		slog.WarnContext(ctx, "qb sync payment: skipped, billing is in test mode — apply this payment in QuickBooks by hand",
			"order_id", order.ID,
			"invoice_id", job.Args.InvoiceID,
			"qb_invoice_id", *order.QBInvoiceID,
			"amount_cents", job.Args.Amount,
			"job_id", job.ID,
		)
		return store.Tx(ctx, w.pool, func(tx pgx.Tx) error {
			return w.audit.Record(ctx, tx, audit.AuditEntry{
				ActorType:    domain.AuditActorTypeSystem,
				ActorName:    "qb_sync_payment",
				Action:       audit.AuditQBPaymentSyncSkipped,
				ResourceType: "invoice",
				ResourceID:   job.Args.InvoiceID,
				Metadata: map[string]any{
					"reason":        "billing is in test mode",
					"qb_invoice_id": *order.QBInvoiceID,
					"amount_cents":  job.Args.Amount,
					"method":        job.Args.Method,
					"river_job_id":  job.ID,
				},
			})
		})
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
