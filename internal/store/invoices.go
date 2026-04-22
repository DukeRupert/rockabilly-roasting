package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store/sqlcgen"
)

// InvoiceStore provides database access for invoices, lines, and payments.
type InvoiceStore struct{}

// NewInvoiceStore creates a new InvoiceStore.
func NewInvoiceStore() *InvoiceStore {
	return &InvoiceStore{}
}

// --- Invoices ---

// CreateInvoiceParams holds the fields needed to create an invoice.
type CreateInvoiceParams struct {
	OrderID      uuid.UUID
	Number       string
	Subtotal     int
	Shipping     int
	TaxTotal     int
	Total        int
	DueDate      *string // date string "2024-01-15"
	Notes        *string
	InternalNote *string
	CreatedBy    *uuid.UUID
}

// Create inserts a new invoice and returns it.
func (s *InvoiceStore) Create(ctx context.Context, tx pgx.Tx, p CreateInvoiceParams) (*domain.Invoice, error) {
	row, err := sqlcgen.New(tx).CreateInvoice(ctx, sqlcgen.CreateInvoiceParams{
		ID:           uuid.New(),
		OrderID:      p.OrderID,
		Number:       p.Number,
		Status:       string(domain.InvoiceStatusDraft),
		Subtotal:     int32(p.Subtotal),
		Shipping:     int32(p.Shipping),
		TaxTotal:     int32(p.TaxTotal),
		Total:        int32(p.Total),
		DueDate:      dateToPG(p.DueDate),
		Notes:        p.Notes,
		InternalNote: p.InternalNote,
		CreatedBy:    p.CreatedBy,
	})
	if err != nil {
		return nil, fmt.Errorf("insert invoice: %w", err)
	}
	return invoiceFromRow(row), nil
}

// GetByIDAsStaff returns an invoice by ID (staff-only — no customer scoping).
func (s *InvoiceStore) GetByIDAsStaff(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Invoice, error) {
	row, err := sqlcgen.New(tx).GetInvoiceByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get invoice %s: %w", id, err)
	}
	return invoiceFromRow(row), nil
}

// ListByOrder returns all invoices for an order.
func (s *InvoiceStore) ListByOrder(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) ([]domain.Invoice, error) {
	rows, err := sqlcgen.New(tx).ListInvoicesByOrder(ctx, orderID)
	if err != nil {
		return nil, fmt.Errorf("list invoices by order: %w", err)
	}
	invoices := make([]domain.Invoice, len(rows))
	for i, r := range rows {
		invoices[i] = *invoiceFromRow(r)
	}
	return invoices, nil
}

// MarkSent marks an invoice as sent.
func (s *InvoiceStore) MarkSent(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Invoice, error) {
	row, err := sqlcgen.New(tx).UpdateInvoiceSent(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("mark invoice sent: %w", err)
	}
	return invoiceFromRow(row), nil
}

// MarkPaid marks an invoice as fully paid.
func (s *InvoiceStore) MarkPaid(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Invoice, error) {
	row, err := sqlcgen.New(tx).UpdateInvoicePaid(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("mark invoice paid: %w", err)
	}
	return invoiceFromRow(row), nil
}

// MarkVoided marks an invoice as void.
func (s *InvoiceStore) MarkVoided(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Invoice, error) {
	row, err := sqlcgen.New(tx).UpdateInvoiceVoided(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("mark invoice voided: %w", err)
	}
	return invoiceFromRow(row), nil
}

// UpdateStatus updates an invoice's status.
func (s *InvoiceStore) UpdateStatus(ctx context.Context, tx pgx.Tx, id uuid.UUID, status domain.InvoiceStatus) (*domain.Invoice, error) {
	row, err := sqlcgen.New(tx).UpdateInvoiceStatus(ctx, sqlcgen.UpdateInvoiceStatusParams{
		ID:     id,
		Status: string(status),
	})
	if err != nil {
		return nil, fmt.Errorf("update invoice status: %w", err)
	}
	return invoiceFromRow(row), nil
}

// UpdateAmountPaid updates the amount_paid field on an invoice.
func (s *InvoiceStore) UpdateAmountPaid(ctx context.Context, tx pgx.Tx, id uuid.UUID, amountPaid int) (*domain.Invoice, error) {
	row, err := sqlcgen.New(tx).UpdateInvoiceAmountPaid(ctx, sqlcgen.UpdateInvoiceAmountPaidParams{
		ID:         id,
		AmountPaid: int32(amountPaid),
	})
	if err != nil {
		return nil, fmt.Errorf("update invoice amount paid: %w", err)
	}
	return invoiceFromRow(row), nil
}

// NextNumber returns the next sequential invoice number (e.g., "INV-1042").
func (s *InvoiceStore) NextNumber(ctx context.Context, tx pgx.Tx) (string, error) {
	nextNum, err := sqlcgen.New(tx).NextInvoiceNumber(ctx)
	if err != nil {
		return "", fmt.Errorf("next invoice number: %w", err)
	}
	return fmt.Sprintf("INV-%04d", nextNum), nil
}

// --- Invoice Lines ---

// CreateInvoiceLineParams holds the fields needed to create an invoice line.
type CreateInvoiceLineParams struct {
	InvoiceID   uuid.UUID
	VariantID   *uuid.UUID
	Description string
	Quantity    int
	UnitPrice   int
	Total       int
}

// CreateLine inserts a new invoice line.
func (s *InvoiceStore) CreateLine(ctx context.Context, tx pgx.Tx, p CreateInvoiceLineParams) (*domain.InvoiceLine, error) {
	row, err := sqlcgen.New(tx).CreateInvoiceLine(ctx, sqlcgen.CreateInvoiceLineParams{
		ID:          uuid.New(),
		InvoiceID:   p.InvoiceID,
		VariantID:   p.VariantID,
		Description: p.Description,
		Quantity:    int32(p.Quantity),
		UnitPrice:   int32(p.UnitPrice),
		Total:       int32(p.Total),
	})
	if err != nil {
		return nil, fmt.Errorf("insert invoice line: %w", err)
	}
	return invoiceLineFromRow(row), nil
}

// ListLines returns all lines for an invoice.
func (s *InvoiceStore) ListLines(ctx context.Context, tx pgx.Tx, invoiceID uuid.UUID) ([]domain.InvoiceLine, error) {
	rows, err := sqlcgen.New(tx).ListInvoiceLinesByInvoice(ctx, invoiceID)
	if err != nil {
		return nil, fmt.Errorf("list invoice lines: %w", err)
	}
	lines := make([]domain.InvoiceLine, len(rows))
	for i, r := range rows {
		lines[i] = *invoiceLineFromRow(r)
	}
	return lines, nil
}

// --- Invoice Payments ---

// CreatePaymentParams holds the fields needed to record a payment.
type CreatePaymentParams struct {
	InvoiceID  uuid.UUID
	Amount     int
	Method     string
	Reference  *string
	Note       *string
	RecordedBy *uuid.UUID
}

// CreatePayment records a payment against an invoice.
func (s *InvoiceStore) CreatePayment(ctx context.Context, tx pgx.Tx, p CreatePaymentParams) (*domain.InvoicePayment, error) {
	row, err := sqlcgen.New(tx).CreateInvoicePayment(ctx, sqlcgen.CreateInvoicePaymentParams{
		ID:         uuid.New(),
		InvoiceID:  p.InvoiceID,
		Amount:     int32(p.Amount),
		Method:     p.Method,
		Reference:  p.Reference,
		Note:       p.Note,
		RecordedBy: p.RecordedBy,
	})
	if err != nil {
		return nil, fmt.Errorf("insert invoice payment: %w", err)
	}
	return invoicePaymentFromRow(row), nil
}

// ListPayments returns all payments for an invoice.
func (s *InvoiceStore) ListPayments(ctx context.Context, tx pgx.Tx, invoiceID uuid.UUID) ([]domain.InvoicePayment, error) {
	rows, err := sqlcgen.New(tx).ListInvoicePaymentsByInvoice(ctx, invoiceID)
	if err != nil {
		return nil, fmt.Errorf("list invoice payments: %w", err)
	}
	payments := make([]domain.InvoicePayment, len(rows))
	for i, r := range rows {
		payments[i] = *invoicePaymentFromRow(r)
	}
	return payments, nil
}

// SumPayments returns the total amount paid against an invoice.
func (s *InvoiceStore) SumPayments(ctx context.Context, tx pgx.Tx, invoiceID uuid.UUID) (int, error) {
	total, err := sqlcgen.New(tx).SumInvoicePayments(ctx, invoiceID)
	if err != nil {
		return 0, fmt.Errorf("sum invoice payments: %w", err)
	}
	return int(total), nil
}

// RecalculatePaymentStatus recalculates amount_paid and status for an invoice.
func (s *InvoiceStore) RecalculatePaymentStatus(ctx context.Context, tx pgx.Tx, invoiceID uuid.UUID) (*domain.Invoice, error) {
	totalPaid, err := s.SumPayments(ctx, tx, invoiceID)
	if err != nil {
		return nil, err
	}

	invoice, err := s.UpdateAmountPaid(ctx, tx, invoiceID, totalPaid)
	if err != nil {
		return nil, err
	}

	var newStatus domain.InvoiceStatus
	switch {
	case invoice.AmountDue <= 0:
		newStatus = domain.InvoiceStatusPaid
	case totalPaid > 0:
		newStatus = domain.InvoiceStatusPartiallyPaid
	default:
		return invoice, nil // no status change
	}

	if invoice.Status != newStatus {
		invoice, err = s.UpdateStatus(ctx, tx, invoiceID, newStatus)
		if err != nil {
			return nil, err
		}
		if newStatus == domain.InvoiceStatusPaid {
			invoice, err = s.MarkPaid(ctx, tx, invoiceID)
			if err != nil {
				return nil, err
			}
		}
	}

	return invoice, nil
}

// --- Row converters ---

func invoiceFromRow(r sqlcgen.Invoice) *domain.Invoice {
	var amountDue int
	if r.AmountDue != nil {
		amountDue = int(*r.AmountDue)
	}
	return &domain.Invoice{
		ID:           r.ID,
		OrderID:      r.OrderID,
		Number:       r.Number,
		Status:       domain.InvoiceStatus(r.Status),
		Subtotal:     int(r.Subtotal),
		Shipping:     int(r.Shipping),
		TaxTotal:     int(r.TaxTotal),
		Total:        int(r.Total),
		AmountPaid:   int(r.AmountPaid),
		AmountDue:    amountDue,
		DueDate:      dateFromPG(r.DueDate),
		Notes:        r.Notes,
		InternalNote: r.InternalNote,
		SentAt:       timestampFromPG(r.SentAt),
		PaidAt:       timestampFromPG(r.PaidAt),
		VoidedAt:     timestampFromPG(r.VoidedAt),
		CreatedBy:    r.CreatedBy,
		CreatedAt:    r.CreatedAt,
		UpdatedAt:    r.UpdatedAt,
	}
}

func invoiceLineFromRow(r sqlcgen.InvoiceLine) *domain.InvoiceLine {
	return &domain.InvoiceLine{
		ID:          r.ID,
		InvoiceID:   r.InvoiceID,
		VariantID:   r.VariantID,
		Description: r.Description,
		Quantity:    int(r.Quantity),
		UnitPrice:   int(r.UnitPrice),
		Total:       int(r.Total),
	}
}

func invoicePaymentFromRow(r sqlcgen.InvoicePayment) *domain.InvoicePayment {
	return &domain.InvoicePayment{
		ID:         r.ID,
		InvoiceID:  r.InvoiceID,
		Amount:     int(r.Amount),
		Method:     domain.PaymentMethod(r.Method),
		Reference:  r.Reference,
		Note:       r.Note,
		RecordedBy: r.RecordedBy,
		PaidAt:     r.PaidAt,
	}
}
