package jobs

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/platform/email"
	"github.com/dukerupert/hiri/internal/store"
)

// InvoiceSendWorker sends an invoice email to the customer.
type InvoiceSendWorker struct {
	river.WorkerDefaults[InvoiceSendArgs]
	invoices  *store.InvoiceStore
	orders    *store.OrderStore
	customers *store.CustomerStore
	pool      *pgxpool.Pool
	mailer    email.Sender
	fromAddr  string
	baseURL   string
}

// NewInvoiceSendWorker creates a new InvoiceSendWorker.
func NewInvoiceSendWorker(invoices *store.InvoiceStore, orders *store.OrderStore, customers *store.CustomerStore, pool *pgxpool.Pool, mailer email.Sender, fromAddr, baseURL string) *InvoiceSendWorker {
	return &InvoiceSendWorker{
		invoices:  invoices,
		orders:    orders,
		customers: customers,
		pool:      pool,
		mailer:    mailer,
		fromAddr:  fromAddr,
		baseURL:   baseURL,
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

	if customerEmail == "" {
		slog.Warn("invoice has no customer email, skipping",
			"invoice_id", invoice.ID,
			"river_job_id", job.ID,
		)
		return nil
	}

	paymentURL := fmt.Sprintf("%s/invoices/%s/pay", w.baseURL, invoice.ID)
	result, err := w.mailer.Send(ctx, email.Message{
		From:    w.fromAddr,
		To:      customerEmail,
		Subject: fmt.Sprintf("Invoice %s", invoice.Number),
		HTML:    fmt.Sprintf(`<p>Invoice <strong>%s</strong> for $%.2f is ready.</p><p><a href="%s">View &amp; Pay Invoice</a></p>`, invoice.Number, float64(invoice.Total)/100, paymentURL),
		Text:    fmt.Sprintf("Invoice %s for $%.2f is ready.\n\nView & pay: %s", invoice.Number, float64(invoice.Total)/100, paymentURL),
		Tag:     "invoice",
	})
	if err != nil {
		return fmt.Errorf("send invoice email: %w", err)
	}

	slog.Info("invoice email sent",
		"invoice_id", invoice.ID,
		"invoice_number", invoice.Number,
		"customer_email", customerEmail,
		"message_id", result.MessageID,
		"river_job_id", job.ID,
	)

	return nil
}
