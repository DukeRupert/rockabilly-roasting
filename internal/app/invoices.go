package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
)

// InvoiceService contains business logic for invoice management.
type InvoiceService struct {
	invoices  *store.InvoiceStore
	orders    *store.OrderStore
	audit     *audit.AuditWriter
	metrics   *metrics.Registry
	customers *store.CustomerStore // populated via WithEmail; required for SendInvoice
	email     EmailEnv             // populated via WithEmail; required for SendInvoice
}

// NewInvoiceService creates a new InvoiceService.
func NewInvoiceService(
	invoices *store.InvoiceStore,
	orders *store.OrderStore,
	audit *audit.AuditWriter,
	metrics *metrics.Registry,
) *InvoiceService {
	return &InvoiceService{
		invoices: invoices,
		orders:   orders,
		audit:    audit,
		metrics:  metrics,
	}
}

// WithEmail attaches email-send environment. Must be called before SendInvoice.
func (s *InvoiceService) WithEmail(env EmailEnv, customers *store.CustomerStore) *InvoiceService {
	s.email = env
	s.customers = customers
	return s
}

// CreateFromOrder creates an invoice from an order's line items.
func (s *InvoiceService) CreateFromOrder(
	ctx context.Context,
	tx pgx.Tx,
	orderID uuid.UUID,
	dueDate *string,
	notes *string,
	actor Actor,
) (*domain.Invoice, error) {
	order, err := s.orders.GetOrderByIDAsStaff(ctx, tx, orderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrOrderNotFound
		}
		return nil, fmt.Errorf("get order: %w", err)
	}

	if order.QBInvoiceID != nil {
		return nil, ErrOrderQBManaged
	}

	if order.PaymentStatus != domain.PaymentStatusPendingInvoice {
		return nil, ErrOrderNotInvoiceable
	}

	number, err := s.invoices.NextNumber(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("next invoice number: %w", err)
	}

	invoice, err := s.invoices.Create(ctx, tx, store.CreateInvoiceParams{
		OrderID:   orderID,
		Number:    number,
		Subtotal:  order.Subtotal,
		Shipping:  order.ShippingTotal,
		TaxTotal:  order.TaxTotal,
		Total:     order.Total,
		DueDate:   dueDate,
		Notes:     notes,
		CreatedBy: actor.ID,
	})
	if err != nil {
		return nil, fmt.Errorf("create invoice: %w", err)
	}

	// Copy order line items to invoice lines.
	lineItems, err := s.orders.ListLineItems(ctx, tx, orderID)
	if err != nil {
		return nil, fmt.Errorf("list line items: %w", err)
	}

	for _, li := range lineItems {
		_, err := s.invoices.CreateLine(ctx, tx, store.CreateInvoiceLineParams{
			InvoiceID:   invoice.ID,
			VariantID:   &li.VariantID,
			Description: fmt.Sprintf("Variant %s", li.VariantID),
			Quantity:    li.Quantity,
			UnitPrice:   li.UnitPrice,
			Total:       li.Total,
		})
		if err != nil {
			return nil, fmt.Errorf("create invoice line: %w", err)
		}
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditInvoiceCreated,
		ResourceType: "invoice",
		ResourceID:   invoice.ID,
		After:        invoice,
		Metadata:     map[string]any{"order_id": orderID},
	}); err != nil {
		return nil, fmt.Errorf("audit invoice created: %w", err)
	}

	return invoice, nil
}

// MarkSent transitions an invoice from draft to sent and updates the order's
// payment status to invoiced. Actual email delivery is handled separately by
// SendInvoice (called from the InvoiceSend worker).
func (s *InvoiceService) MarkSent(ctx context.Context, tx pgx.Tx, invoiceID uuid.UUID, actor Actor) (*domain.Invoice, error) {
	invoice, err := s.invoices.GetByIDAsStaff(ctx, tx, invoiceID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrInvoiceNotFound
		}
		return nil, fmt.Errorf("get invoice: %w", err)
	}

	if err := s.ensureOrderNotQBManaged(ctx, tx, invoice.OrderID); err != nil {
		return nil, err
	}

	if invoice.Status != domain.InvoiceStatusDraft {
		return nil, ErrInvoiceNotSendable
	}

	updated, err := s.invoices.MarkSent(ctx, tx, invoiceID)
	if err != nil {
		return nil, fmt.Errorf("mark invoice sent: %w", err)
	}

	// Update order payment status to invoiced.
	_, err = s.orders.UpdateOrderPaymentStatus(ctx, tx, invoice.OrderID, domain.PaymentStatusInvoiced)
	if err != nil {
		return nil, fmt.Errorf("update order payment status: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditInvoiceSent,
		ResourceType: "invoice",
		ResourceID:   invoiceID,
		After:        updated,
	}); err != nil {
		return nil, fmt.Errorf("audit invoice sent: %w", err)
	}

	return updated, nil
}

// RecordPayment records a payment against an invoice and updates statuses.
func (s *InvoiceService) RecordPayment(
	ctx context.Context,
	tx pgx.Tx,
	invoiceID uuid.UUID,
	amount int,
	method string,
	reference *string,
	note *string,
	actor Actor,
) (*domain.Invoice, error) {
	invoice, err := s.invoices.GetByIDAsStaff(ctx, tx, invoiceID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrInvoiceNotFound
		}
		return nil, fmt.Errorf("get invoice: %w", err)
	}

	if err := s.ensureOrderNotQBManaged(ctx, tx, invoice.OrderID); err != nil {
		return nil, err
	}

	if invoice.Status == domain.InvoiceStatusPaid || invoice.Status == domain.InvoiceStatusVoid {
		return nil, ErrInvoiceNotPayable
	}

	_, err = s.invoices.CreatePayment(ctx, tx, store.CreatePaymentParams{
		InvoiceID:  invoiceID,
		Amount:     amount,
		Method:     method,
		Reference:  reference,
		Note:       note,
		RecordedBy: actor.ID,
	})
	if err != nil {
		return nil, fmt.Errorf("create payment: %w", err)
	}

	// Recalculate invoice payment status.
	updated, err := s.invoices.RecalculatePaymentStatus(ctx, tx, invoiceID)
	if err != nil {
		return nil, fmt.Errorf("recalculate payment status: %w", err)
	}

	// Sync order payment status.
	orderStatus := domain.PaymentStatusPartiallyPaid
	if updated.Status == domain.InvoiceStatusPaid {
		orderStatus = domain.PaymentStatusCaptured
	}
	_, err = s.orders.UpdateOrderPaymentStatus(ctx, tx, invoice.OrderID, orderStatus)
	if err != nil {
		return nil, fmt.Errorf("update order payment status: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditInvoicePaymentRecorded,
		ResourceType: "invoice",
		ResourceID:   invoiceID,
		After:        updated,
		Metadata: map[string]any{
			"amount": amount,
			"method": method,
		},
	}); err != nil {
		return nil, fmt.Errorf("audit payment recorded: %w", err)
	}

	return updated, nil
}

// VoidInvoice marks an invoice as void.
func (s *InvoiceService) VoidInvoice(ctx context.Context, tx pgx.Tx, invoiceID uuid.UUID, actor Actor) (*domain.Invoice, error) {
	invoice, err := s.invoices.GetByIDAsStaff(ctx, tx, invoiceID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrInvoiceNotFound
		}
		return nil, fmt.Errorf("get invoice: %w", err)
	}

	if err := s.ensureOrderNotQBManaged(ctx, tx, invoice.OrderID); err != nil {
		return nil, err
	}

	if invoice.Status == domain.InvoiceStatusPaid || invoice.Status == domain.InvoiceStatusVoid {
		return nil, ErrInvoiceNotVoidable
	}

	updated, err := s.invoices.MarkVoided(ctx, tx, invoiceID)
	if err != nil {
		return nil, fmt.Errorf("mark invoice voided: %w", err)
	}

	// Revert order payment status to pending_invoice so a new invoice can be created.
	_, err = s.orders.UpdateOrderPaymentStatus(ctx, tx, invoice.OrderID, domain.PaymentStatusPendingInvoice)
	if err != nil {
		return nil, fmt.Errorf("revert order payment status: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditInvoiceVoided,
		ResourceType: "invoice",
		ResourceID:   invoiceID,
		After:        updated,
	}); err != nil {
		return nil, fmt.Errorf("audit invoice voided: %w", err)
	}

	return updated, nil
}

// GetInvoiceAsStaff returns an invoice by ID.
func (s *InvoiceService) GetInvoiceAsStaff(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Invoice, error) {
	invoice, err := s.invoices.GetByIDAsStaff(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrInvoiceNotFound
		}
		return nil, fmt.Errorf("get invoice: %w", err)
	}
	return invoice, nil
}

// ListInvoicesByOrder returns all invoices for an order.
func (s *InvoiceService) ListInvoicesByOrder(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) ([]domain.Invoice, error) {
	invoices, err := s.invoices.ListByOrder(ctx, tx, orderID)
	if err != nil {
		return nil, fmt.Errorf("list invoices: %w", err)
	}
	return invoices, nil
}

// ListInvoiceLines returns all lines for an invoice.
func (s *InvoiceService) ListInvoiceLines(ctx context.Context, tx pgx.Tx, invoiceID uuid.UUID) ([]domain.InvoiceLine, error) {
	lines, err := s.invoices.ListLines(ctx, tx, invoiceID)
	if err != nil {
		return nil, fmt.Errorf("list invoice lines: %w", err)
	}
	return lines, nil
}

// ListInvoicePayments returns all payments for an invoice.
func (s *InvoiceService) ListInvoicePayments(ctx context.Context, tx pgx.Tx, invoiceID uuid.UUID) ([]domain.InvoicePayment, error) {
	payments, err := s.invoices.ListPayments(ctx, tx, invoiceID)
	if err != nil {
		return nil, fmt.Errorf("list invoice payments: %w", err)
	}
	return payments, nil
}

// ensureOrderNotQBManaged blocks manual invoice operations on a QB-owned order.
// The presence of qb_invoice_id is the ownership discriminator: such orders are
// reconciled solely by the QuickBooks path, so the manual invoice flow must not
// touch their payment status.
func (s *InvoiceService) ensureOrderNotQBManaged(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) error {
	order, err := s.orders.GetOrderByIDAsStaff(ctx, tx, orderID)
	if err != nil {
		return fmt.Errorf("get order: %w", err)
	}
	if order.QBInvoiceID != nil {
		return ErrOrderQBManaged
	}
	return nil
}
