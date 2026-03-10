package jobs

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/emailtemplates"
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
	renderer  *emailtemplates.Renderer
	fromAddr  string
	baseURL   string
	storeName string
}

// NewInvoiceSendWorker creates a new InvoiceSendWorker.
func NewInvoiceSendWorker(invoices *store.InvoiceStore, orders *store.OrderStore, customers *store.CustomerStore, pool *pgxpool.Pool, mailer email.Sender, renderer *emailtemplates.Renderer, fromAddr, baseURL, storeName string) *InvoiceSendWorker {
	return &InvoiceSendWorker{
		invoices:  invoices,
		orders:    orders,
		customers: customers,
		pool:      pool,
		mailer:    mailer,
		renderer:  renderer,
		fromAddr:  fromAddr,
		baseURL:   baseURL,
		storeName: storeName,
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

	var customerEmail, customerName string
	if order.CustomerID != nil {
		customer, err := w.customers.GetByID(ctx, tx, *order.CustomerID)
		if err != nil {
			return fmt.Errorf("get customer: %w", err)
		}
		customerEmail = customer.Email
		customerName = customer.FirstName
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
	html, text, err := w.renderer.Render("invoice_sent", emailtemplates.InvoiceSentData{
		CustomerName:  customerName,
		InvoiceNumber: invoice.Number,
		OrderNumber:   order.Number,
		Total:         invoice.Total,
		DueDate:       invoice.DueDate,
		PaymentURL:    paymentURL,
		StoreName:     w.storeName,
		StoreURL:      w.baseURL,
	})
	if err != nil {
		return fmt.Errorf("render invoice template: %w", err)
	}

	result, err := w.mailer.Send(ctx, email.Message{
		From:    w.fromAddr,
		To:      customerEmail,
		Subject: fmt.Sprintf("Invoice %s", invoice.Number),
		HTML:    html,
		Text:    text,
		Tag:     "invoice",
	})
	if err != nil {
		return fmt.Errorf("send invoice email: %w", err)
	}

	slog.Info("invoice email sent",
		"invoice_id", invoice.ID,
		"invoice_number", invoice.Number,
		"customer_id", order.CustomerID,
		"message_id", result.MessageID,
		"river_job_id", job.ID,
	)

	return nil
}
