package jobs

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/store"
)

// InvoiceSendWorker sends an invoice email to the customer.
type InvoiceSendWorker struct {
	river.WorkerDefaults[InvoiceSendArgs]
	invoices  *store.InvoiceStore
	orders    *store.OrderStore
	customers *store.CustomerStore
	pool      *pgxpool.Pool
}

// NewInvoiceSendWorker creates a new InvoiceSendWorker.
func NewInvoiceSendWorker(invoices *store.InvoiceStore, orders *store.OrderStore, customers *store.CustomerStore, pool *pgxpool.Pool) *InvoiceSendWorker {
	return &InvoiceSendWorker{
		invoices:  invoices,
		orders:    orders,
		customers: customers,
		pool:      pool,
	}
}

// Work processes an invoice send job.
func (w *InvoiceSendWorker) Work(ctx context.Context, job *river.Job[InvoiceSendArgs]) error {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	invoice, err := w.invoices.GetByID(ctx, tx, job.Args.InvoiceID)
	if err != nil {
		return fmt.Errorf("get invoice %s: %w", job.Args.InvoiceID, err)
	}

	order, err := w.orders.GetOrderByID(ctx, tx, invoice.OrderID)
	if err != nil {
		return fmt.Errorf("get order %s: %w", invoice.OrderID, err)
	}

	var customerEmail string
	if order.CustomerID != nil {
		customer, err := w.customers.GetByID(ctx, tx, *order.CustomerID)
		if err != nil {
			return fmt.Errorf("get customer: %w", err)
		}
		customerEmail = customer.Email
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}

	// TODO: Generate invoice PDF and send email with payment link.
	slog.Info("invoice email pending",
		"invoice_id", invoice.ID,
		"invoice_number", invoice.Number,
		"order_id", invoice.OrderID,
		"customer_email", customerEmail,
		"total", invoice.Total,
		"river_job_id", job.ID,
	)

	return nil
}
