package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
)

// QBPreviewStore persists what the QuickBooks chain would have billed while
// the shop is in shadow mode. See db/migrations/078_qb_billing_mode.sql.
type QBPreviewStore struct{}

// NewQBPreviewStore creates a new QBPreviewStore.
func NewQBPreviewStore() *QBPreviewStore { return &QBPreviewStore{} }

// Upsert records the invoice an order would have produced, replacing any
// earlier preview for the same order.
//
// Replacing rather than appending is deliberate: the invoice chain is
// idempotent and may run again for an order, and a proof period is read as
// "what would be billed now", not as a history of attempts. The audit log
// holds the history.
func (s *QBPreviewStore) Upsert(ctx context.Context, tx pgx.Tx, p *domain.QBInvoicePreview) error {
	lines, err := json.Marshal(p.Lines)
	if err != nil {
		return fmt.Errorf("marshal preview lines: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO qb_invoice_previews (
			order_id, customer_id, qb_customer_id, would_create_customer,
			doc_number, bill_email, terms_days, due_date,
			subtotal_cents, shipping_cents, total_cents, term_id, lines,
			existing_qb_invoice_id, lookup_error
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		ON CONFLICT (order_id) DO UPDATE SET
			customer_id            = EXCLUDED.customer_id,
			qb_customer_id         = EXCLUDED.qb_customer_id,
			would_create_customer  = EXCLUDED.would_create_customer,
			doc_number             = EXCLUDED.doc_number,
			bill_email             = EXCLUDED.bill_email,
			terms_days             = EXCLUDED.terms_days,
			due_date               = EXCLUDED.due_date,
			subtotal_cents         = EXCLUDED.subtotal_cents,
			shipping_cents         = EXCLUDED.shipping_cents,
			total_cents            = EXCLUDED.total_cents,
			term_id                = EXCLUDED.term_id,
			lines                  = EXCLUDED.lines,
			existing_qb_invoice_id = EXCLUDED.existing_qb_invoice_id,
			lookup_error           = EXCLUDED.lookup_error,
			updated_at             = now()`,
		p.OrderID, p.CustomerID, p.QBCustomerID, p.WouldCreateCustomer,
		p.DocNumber, p.BillEmail, p.TermsDays, p.DueDate,
		p.SubtotalCents, p.ShippingCents, p.TotalCents, p.TermID, lines,
		p.ExistingQBInvoiceID, p.LookupError,
	)
	if err != nil {
		return fmt.Errorf("upsert qb invoice preview: %w", err)
	}
	return nil
}

// QBPreviewRow is one preview joined to the order and customer it belongs to,
// for the admin list and the digest. The names are carried alongside so
// neither has to re-read the customer per row.
type QBPreviewRow struct {
	domain.QBInvoicePreview
	OrderNumber  string
	CustomerName string
}

const qbPreviewSelect = `
	SELECT p.id, p.order_id, p.customer_id, p.qb_customer_id, p.would_create_customer,
	       p.doc_number, p.bill_email, p.terms_days, p.due_date,
	       p.subtotal_cents, p.shipping_cents, p.total_cents, p.term_id, p.lines,
	       p.existing_qb_invoice_id, p.lookup_error, p.created_at, p.updated_at,
	       o.number,
	       COALESCE(NULLIF(c.company_name, ''), TRIM(c.first_name || ' ' || c.last_name), '')
	  FROM qb_invoice_previews p
	  JOIN orders o ON o.id = p.order_id
	  LEFT JOIN customers c ON c.id = p.customer_id`

func scanQBPreviewRows(rows pgx.Rows) ([]QBPreviewRow, error) {
	defer rows.Close()

	var out []QBPreviewRow
	for rows.Next() {
		var r QBPreviewRow
		var lines []byte
		var companyOrName *string
		if err := rows.Scan(
			&r.ID, &r.OrderID, &r.CustomerID, &r.QBCustomerID, &r.WouldCreateCustomer,
			&r.DocNumber, &r.BillEmail, &r.TermsDays, &r.DueDate,
			&r.SubtotalCents, &r.ShippingCents, &r.TotalCents, &r.TermID, &lines,
			&r.ExistingQBInvoiceID, &r.LookupError, &r.CreatedAt, &r.UpdatedAt,
			&r.OrderNumber, &companyOrName,
		); err != nil {
			return nil, fmt.Errorf("scan qb invoice preview: %w", err)
		}
		if len(lines) > 0 {
			if err := json.Unmarshal(lines, &r.Lines); err != nil {
				return nil, fmt.Errorf("unmarshal preview lines: %w", err)
			}
		}
		r.CustomerName = ptrToStr(companyOrName)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list qb invoice previews: %w", err)
	}
	return out, nil
}

// List returns the most recent previews, newest first.
func (s *QBPreviewStore) List(ctx context.Context, tx pgx.Tx, limit int) ([]QBPreviewRow, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := tx.Query(ctx, qbPreviewSelect+` ORDER BY p.created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list qb invoice previews: %w", err)
	}
	return scanQBPreviewRows(rows)
}

// ListSince returns previews touched at or after the given time, oldest first,
// which is the order the weekly digest reads them in.
//
// The window is on updated_at rather than created_at because Upsert refreshes a
// preview in place. An order first previewed weeks ago and re-previewed this
// week — a retry, an edited order, a QBO lookup that has only just started
// failing — is news now, and windowing on creation would hide exactly the rows
// the digest exists to raise.
func (s *QBPreviewStore) ListSince(ctx context.Context, tx pgx.Tx, since time.Time) ([]QBPreviewRow, error) {
	rows, err := tx.Query(ctx, qbPreviewSelect+` WHERE p.updated_at >= $1 ORDER BY p.updated_at`, since)
	if err != nil {
		return nil, fmt.Errorf("list qb invoice previews since: %w", err)
	}
	return scanQBPreviewRows(rows)
}

// GetByOrder returns the preview for one order, or nil when there is none.
func (s *QBPreviewStore) GetByOrder(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) (*QBPreviewRow, error) {
	rows, err := tx.Query(ctx, qbPreviewSelect+` WHERE p.order_id = $1`, orderID)
	if err != nil {
		return nil, fmt.Errorf("get qb invoice preview: %w", err)
	}
	found, err := scanQBPreviewRows(rows)
	if err != nil {
		return nil, err
	}
	if len(found) == 0 {
		return nil, nil
	}
	return &found[0], nil
}

// Count returns how many previews exist, for the review badge.
func (s *QBPreviewStore) Count(ctx context.Context, tx pgx.Tx) (int, error) {
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM qb_invoice_previews`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count qb invoice previews: %w", err)
	}
	return n, nil
}

// QBPreviewTotals summarises a proof period.
type QBPreviewTotals struct {
	Count            int
	TotalCents       int
	NeedingAttention int
}

// Totals summarises previews touched at or after the given time — updated_at,
// for the reason ListSince gives. Pass the zero time for every row. Counting in
// SQL rather than over a fetched slice keeps the figures honest when a proof
// period has run long enough to outgrow a page.
func (s *QBPreviewStore) Totals(ctx context.Context, tx pgx.Tx, since time.Time) (QBPreviewTotals, error) {
	var t QBPreviewTotals
	err := tx.QueryRow(ctx, `
		SELECT count(*),
		       COALESCE(sum(total_cents), 0),
		       count(*) FILTER (
		           WHERE would_create_customer
		              OR lookup_error IS NOT NULL
		              OR existing_qb_invoice_id IS NOT NULL
		              OR bill_email = ''
		       )
		  FROM qb_invoice_previews
		 WHERE updated_at >= $1`, since,
	).Scan(&t.Count, &t.TotalCents, &t.NeedingAttention)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return QBPreviewTotals{}, fmt.Errorf("qb invoice preview totals: %w", err)
	}
	return t, nil
}
