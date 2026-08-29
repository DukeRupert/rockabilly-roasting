package app

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
)

// QBPreviewRow is one would-be invoice with the order and customer it belongs
// to, as the web layer needs it. It exists so handlers can render the review
// page without importing store.
type QBPreviewRow struct {
	domain.QBInvoicePreview
	OrderNumber  string
	CustomerName string
}

// QBShadowSummary is the review page's header: what a proof period has seen so
// far. Counts come from SQL over every row, not from the capped list, so the
// figures do not quietly shrink when a proof period outgrows a page.
type QBShadowSummary struct {
	Rows       []QBPreviewRow
	Count      int
	TotalCents int
	Attention  int
}

// QBBillingMode reports whether QuickBooks billing is allowed to move money.
func (s *OrderService) QBBillingMode(ctx context.Context, tx pgx.Tx) (domain.QBBillingMode, error) {
	if s.settings == nil {
		return domain.DefaultQBBillingMode, nil
	}
	return s.settings.GetQBBillingMode(ctx, tx)
}

// ListQBPreviews returns what shadow billing has recorded, newest first, with
// the totals for the whole set.
func (s *OrderService) ListQBPreviews(ctx context.Context, tx pgx.Tx, limit int) (QBShadowSummary, error) {
	var out QBShadowSummary
	if s.qbPreviews == nil {
		return out, nil
	}
	rows, err := s.qbPreviews.List(ctx, tx, limit)
	if err != nil {
		return out, err
	}
	for _, r := range rows {
		out.Rows = append(out.Rows, QBPreviewRow{
			QBInvoicePreview: r.QBInvoicePreview,
			OrderNumber:      r.OrderNumber,
			CustomerName:     r.CustomerName,
		})
		out.TotalCents += r.TotalCents
		if r.NeedsAttention() {
			out.Attention++
		}
	}
	out.Count = len(out.Rows)
	return out, nil
}

// CountQBPreviews returns how many would-be invoices are waiting to be
// reviewed, for the badge on the integrations page.
func (s *OrderService) CountQBPreviews(ctx context.Context, tx pgx.Tx) (int, error) {
	if s.qbPreviews == nil {
		return 0, nil
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM qb_invoice_previews`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count qb invoice previews: %w", err)
	}
	return n, nil
}

// SetQBBillingMode switches QuickBooks billing between shadow and live and
// records who did it.
//
// The audit entry is the point of this method existing rather than the handler
// writing the column: "when did we start billing customers, and who decided
// that" is the first question anyone asks afterwards.
func (s *OrderService) SetQBBillingMode(ctx context.Context, tx pgx.Tx, mode domain.QBBillingMode, actor Actor) error {
	if !mode.Valid() {
		return fmt.Errorf("%w: %q", ErrInvalidQBBillingMode, mode)
	}
	if s.settings == nil {
		return fmt.Errorf("qb billing mode: settings store not wired")
	}
	previous, err := s.settings.GetQBBillingMode(ctx, tx)
	if err != nil {
		return err
	}
	if previous == mode {
		return nil
	}
	if err := s.settings.UpdateQBBillingMode(ctx, tx, mode); err != nil {
		return err
	}
	action := audit.AuditQBBillingModeShadowed
	if mode.IsLive() {
		action = audit.AuditQBBillingModeLive
	}
	return s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       action,
		ResourceType: "store_settings",
		ResourceID:   uuid.Nil, // store_settings is a singleton row
		After:        map[string]any{"qb_billing_mode": string(mode)},
		Metadata:     map[string]any{"previous": string(previous)},
	})
}
