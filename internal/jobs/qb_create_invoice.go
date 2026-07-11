package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/platform/quickbooks"
	"github.com/dukerupert/hiri/internal/store"
)

// CreateQBInvoiceWorker creates a QB invoice for a wholesale order.
type CreateQBInvoiceWorker struct {
	river.WorkerDefaults[CreateQBInvoiceArgs]
	orders    *store.OrderStore
	customers *store.CustomerStore
	catalog   *store.CatalogStore
	qb        quickbooks.Client
	audit     *audit.AuditWriter
	pool      *pgxpool.Pool
	metrics   *metrics.Registry
}

// NewCreateQBInvoiceWorker creates a new CreateQBInvoiceWorker.
func NewCreateQBInvoiceWorker(
	orders *store.OrderStore,
	customers *store.CustomerStore,
	catalog *store.CatalogStore,
	qb quickbooks.Client,
	auditWriter *audit.AuditWriter,
	pool *pgxpool.Pool,
	m *metrics.Registry,
) *CreateQBInvoiceWorker {
	return &CreateQBInvoiceWorker{
		orders:    orders,
		customers: customers,
		catalog:   catalog,
		qb:        qb,
		audit:     auditWriter,
		pool:      pool,
		metrics:   m,
	}
}

// Work processes a single CreateQBInvoice job.
func (w *CreateQBInvoiceWorker) Work(ctx context.Context, job *river.Job[CreateQBInvoiceArgs]) error {
	start := time.Now()

	err := w.work(ctx, job)

	metrics.TrackJob(w.metrics, "qb_create_invoice", start, err)
	if err != nil {
		slog.ErrorContext(ctx, "job failed",
			"job_kind", "qb_create_invoice",
			"job_id", job.ID,
			"order_id", job.Args.OrderID,
			"error", err.Error(),
		)
		if !quickbooks.IsRetryable(err) {
			return river.JobCancel(fmt.Errorf("create qb invoice for order %s: %w", job.Args.OrderID, err))
		}
	}
	return err
}

func (w *CreateQBInvoiceWorker) work(ctx context.Context, job *river.Job[CreateQBInvoiceArgs]) error {
	// Read order, line items, and the customer's NET terms
	var order *domain.Order
	var items []domain.LineItem
	var customer *domain.Customer
	err := store.Tx(ctx, w.pool, func(tx pgx.Tx) error {
		var txErr error
		order, txErr = w.orders.GetOrderByIDAsStaff(ctx, tx, job.Args.OrderID)
		if txErr != nil {
			return txErr
		}
		// Idempotency: if the invoice already exists (job retry), skip the
		// remaining reads — the early return below never uses them.
		if order.QBInvoiceID != nil {
			return nil
		}
		items, txErr = w.orders.ListLineItems(ctx, tx, order.ID)
		if txErr != nil {
			return txErr
		}
		if order.CustomerID != nil {
			customer, txErr = w.customers.GetByID(ctx, tx, *order.CustomerID)
			return txErr
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("get order: %w", err)
	}
	if order.QBInvoiceID != nil {
		return nil
	}
	termsDays := app.EffectivePaymentTermsDays(customer)

	// Build QB invoice lines
	lines := make([]quickbooks.InvoiceLine, 0, len(items))
	for _, item := range items {
		// Build a description from variant info
		desc := fmt.Sprintf("Order item (qty %d)", item.Quantity)

		// Try to get product/variant description from catalog
		_ = store.Tx(ctx, w.pool, func(tx pgx.Tx) error {
			variant, txErr := w.catalog.GetVariantByID(ctx, tx, item.VariantID)
			if txErr == nil && variant != nil {
				product, pErr := w.catalog.GetProductByID(ctx, tx, variant.ProductID)
				if pErr == nil && product != nil {
					desc = product.Title
					if variant.SKU != "" {
						desc += " (" + variant.SKU + ")"
					}
				}
			}
			return nil // non-fatal if we can't get descriptions
		})

		lines = append(lines, quickbooks.InvoiceLine{
			Description: desc,
			Quantity:    item.Quantity,
			UnitAmount:  item.UnitPrice,
			Amount:      item.Total,
		})
	}

	// Create QB invoice (external call outside transaction). Due date follows
	// the customer's NET terms; net-7 when none are set.
	invoice, err := w.qb.CreateInvoice(ctx, quickbooks.InvoiceParams{
		CustomerID: job.Args.QBCustomerID,
		DocNumber:  formatOrderRef(order.Number),
		DueDate:    order.PlacedAt.Add(time.Duration(termsDays) * 24 * time.Hour),
		Lines:      lines,
		Shipping:   order.ShippingTotal,
	})
	if err != nil {
		return fmt.Errorf("qb create invoice: %w", err)
	}

	// Persist QB invoice ID in a transaction with audit
	return store.Tx(ctx, w.pool, func(tx pgx.Tx) error {
		if txErr := w.orders.SetQBInvoice(ctx, tx, order.ID, invoice.ID, invoice.DocNumber); txErr != nil {
			return txErr
		}
		return w.audit.Record(ctx, tx, audit.AuditEntry{
			ActorType:    domain.AuditActorTypeSystem,
			ActorName:    "qb_create_invoice",
			Action:       audit.AuditQBInvoiceCreated,
			ResourceType: "order",
			ResourceID:   order.ID,
			Metadata: map[string]any{
				"qb_invoice_id":  invoice.ID,
				"qb_doc_number":  invoice.DocNumber,
				"net_terms_days": termsDays,
				"river_job_id":   job.ID,
			},
		})
	})
}

// formatOrderRef formats an order number for use as the QB DocNumber.
func formatOrderRef(number string) string {
	return number // the order already has a formatted number
}
