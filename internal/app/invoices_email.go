package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/emailtemplates"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/email"
	"github.com/dukerupert/hiri/internal/store"
)

// SendInvoice renders and sends an invoice email to the order's customer.
// If the order has no associated customer, logs a warning and returns nil.
func (s *InvoiceService) SendInvoice(ctx context.Context, pool *pgxpool.Pool, invoiceID uuid.UUID) error {
	var (
		invoice       *domain.Invoice
		order         *domain.Order
		customerEmail string
		customerName  string
	)

	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		var err error
		invoice, err = s.invoices.GetByIDAsStaff(ctx, tx, invoiceID)
		if err != nil {
			return fmt.Errorf("get invoice %s: %w", invoiceID, err)
		}
		order, err = s.orders.GetOrderByIDAsStaff(ctx, tx, invoice.OrderID)
		if err != nil {
			return fmt.Errorf("get order %s: %w", invoice.OrderID, err)
		}
		if order.CustomerID != nil {
			customer, err := s.customers.GetByID(ctx, tx, *order.CustomerID)
			if err != nil {
				return fmt.Errorf("get customer: %w", err)
			}
			customerEmail = customer.Email
			customerName = customer.FirstName
		}
		return nil
	}); err != nil {
		return err
	}

	if customerEmail == "" {
		slog.Warn("invoice has no customer email, skipping", "invoice_id", invoice.ID)
		return nil
	}

	paymentURL := fmt.Sprintf("%s/invoices/%s/pay", s.email.BaseURL, invoice.ID)
	html, text, err := s.email.Renderer.Render("invoice_sent", emailtemplates.InvoiceSentData{
		CustomerName:  customerName,
		InvoiceNumber: invoice.Number,
		OrderNumber:   order.Number,
		Total:         invoice.Total,
		DueDate:       invoice.DueDate,
		PaymentURL:    paymentURL,
		StoreName:     s.email.StoreName,
		StoreURL:      s.email.BaseURL,
	})
	if err != nil {
		s.metrics.EmailsSent.WithLabelValues("invoice", "failed").Inc()
		return fmt.Errorf("render invoice template: %w", err)
	}

	if _, err := s.email.Mailer.Send(ctx, email.Message{
		From:    s.email.FromAddr,
		To:      customerEmail,
		Subject: fmt.Sprintf("Invoice %s", invoice.Number),
		HTML:    html,
		Text:    text,
		Tag:     "invoice",
	}); err != nil {
		s.metrics.EmailsSent.WithLabelValues("invoice", "failed").Inc()
		return fmt.Errorf("send invoice email: %w", err)
	}

	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		return s.audit.Record(ctx, tx, audit.AuditEntry{
			ActorType:    domain.AuditActorTypeSystem,
			ActorName:    "invoice_send_worker",
			Action:       audit.AuditEmailInvoiceSent,
			ResourceType: "invoice",
			ResourceID:   invoice.ID,
			Metadata:     map[string]any{"invoice_number": invoice.Number},
		})
	}); err != nil {
		return fmt.Errorf("audit invoice sent: %w", err)
	}

	s.metrics.EmailsSent.WithLabelValues("invoice", "sent").Inc()
	return nil
}
