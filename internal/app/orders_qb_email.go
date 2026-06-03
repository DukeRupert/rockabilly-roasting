package app

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/emailtemplates"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/email"
	"github.com/dukerupert/hiri/internal/store"
)

// qbNetTermsDays is the net payment term (in days) for wholesale QB invoices.
// The invoice's due date is order.PlacedAt + qbNetTermsDays; the reconciliation
// poll reads the authoritative due date back from QB, but the past-due email
// recomputes it for display.
const qbNetTermsDays = 7

// SendInvoicePaidEmail sends the payment-confirmation email for a wholesale QB
// invoice paid in full. Read → send → audit, matching SendConfirmationEmail.
func (s *OrderService) SendInvoicePaidEmail(ctx context.Context, pool *pgxpool.Pool, orderID, customerID uuid.UUID) error {
	order, customer, err := s.loadOrderAndCustomer(ctx, pool, orderID, customerID)
	if err != nil {
		return err
	}

	html, text, err := s.email.Renderer.Render("invoice_paid", emailtemplates.InvoicePaidData{
		CustomerName:  customer.FirstName,
		InvoiceNumber: qbInvoiceLabel(order),
		OrderNumber:   order.Number,
		AmountPaid:    order.Total,
		StoreName:     s.email.StoreName,
		StoreURL:      s.email.BaseURL,
		AccountURL:    s.email.BaseURL + "/account/orders/" + order.ID.String(),
	})
	if err != nil {
		s.metrics.EmailsSent.WithLabelValues("invoice_paid", "failed").Inc()
		return fmt.Errorf("render invoice paid template: %w", err)
	}

	if _, err := s.email.Mailer.Send(ctx, email.Message{
		From:    s.email.FromAddr,
		To:      customer.Email,
		Subject: fmt.Sprintf("Payment received — invoice %s", qbInvoiceLabel(order)),
		HTML:    html,
		Text:    text,
		Tag:     "invoice-paid",
	}); err != nil {
		s.metrics.EmailsSent.WithLabelValues("invoice_paid", "failed").Inc()
		return fmt.Errorf("send invoice paid email: %w", err)
	}

	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		return s.audit.Record(ctx, tx, audit.AuditEntry{
			ActorType:    domain.AuditActorTypeSystem,
			ActorName:    "invoice_paid_worker",
			Action:       audit.AuditEmailInvoicePaid,
			ResourceType: "order",
			ResourceID:   order.ID,
			Metadata:     map[string]any{"order_number": order.Number},
		})
	}); err != nil {
		return fmt.Errorf("audit invoice paid sent: %w", err)
	}

	s.metrics.EmailsSent.WithLabelValues("invoice_paid", "sent").Inc()
	return nil
}

// SendInvoicePastDueEmail sends a past-due reminder for an overdue wholesale QB
// invoice at the given milestone (days since placed). Read → send → audit.
func (s *OrderService) SendInvoicePastDueEmail(ctx context.Context, pool *pgxpool.Pool, orderID, customerID uuid.UUID, stage int) error {
	order, customer, err := s.loadOrderAndCustomer(ctx, pool, orderID, customerID)
	if err != nil {
		return err
	}

	dueDate := order.PlacedAt.Add(qbNetTermsDays * 24 * time.Hour)
	html, text, err := s.email.Renderer.Render("invoice_past_due", emailtemplates.InvoicePastDueData{
		CustomerName:  customer.FirstName,
		InvoiceNumber: qbInvoiceLabel(order),
		OrderNumber:   order.Number,
		AmountDue:     order.Total,
		DueDate:       &dueDate,
		Stage:         stage,
		PaymentURL:    s.email.BaseURL + "/account/orders/" + order.ID.String(),
		StoreName:     s.email.StoreName,
		StoreURL:      s.email.BaseURL,
	})
	if err != nil {
		s.metrics.EmailsSent.WithLabelValues("invoice_past_due", "failed").Inc()
		return fmt.Errorf("render invoice past due template: %w", err)
	}

	if _, err := s.email.Mailer.Send(ctx, email.Message{
		From:    s.email.FromAddr,
		To:      customer.Email,
		Subject: fmt.Sprintf("Past due — invoice %s", qbInvoiceLabel(order)),
		HTML:    html,
		Text:    text,
		Tag:     "invoice-past-due",
	}); err != nil {
		s.metrics.EmailsSent.WithLabelValues("invoice_past_due", "failed").Inc()
		return fmt.Errorf("send invoice past due email: %w", err)
	}

	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		return s.audit.Record(ctx, tx, audit.AuditEntry{
			ActorType:    domain.AuditActorTypeSystem,
			ActorName:    "invoice_past_due_worker",
			Action:       audit.AuditEmailInvoicePastDue,
			ResourceType: "order",
			ResourceID:   order.ID,
			Metadata:     map[string]any{"order_number": order.Number, "stage": stage},
		})
	}); err != nil {
		return fmt.Errorf("audit invoice past due sent: %w", err)
	}

	s.metrics.EmailsSent.WithLabelValues("invoice_past_due", "sent").Inc()
	return nil
}

// loadOrderAndCustomer reads an order and its customer in a single read tx.
func (s *OrderService) loadOrderAndCustomer(ctx context.Context, pool *pgxpool.Pool, orderID, customerID uuid.UUID) (*domain.Order, *domain.Customer, error) {
	var (
		order    *domain.Order
		customer *domain.Customer
	)
	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		o, err := s.orders.GetOrderByIDAsStaff(ctx, tx, orderID)
		if err != nil {
			return fmt.Errorf("get order %s: %w", orderID, err)
		}
		order = o
		c, err := s.customers.GetByID(ctx, tx, customerID)
		if err != nil {
			return fmt.Errorf("get customer %s: %w", customerID, err)
		}
		customer = c
		return nil
	}); err != nil {
		return nil, nil, err
	}
	return order, customer, nil
}

// qbInvoiceLabel returns the human-readable QB invoice number, falling back to
// the order number if the invoice hasn't been numbered yet.
func qbInvoiceLabel(o *domain.Order) string {
	if o.QBInvoiceNo != nil && *o.QBInvoiceNo != "" {
		return *o.QBInvoiceNo
	}
	return o.Number
}
