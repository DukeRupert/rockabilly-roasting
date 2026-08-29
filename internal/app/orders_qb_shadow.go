package app

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/store"
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
// far.
//
// Count, TotalCents and Attention come from SQL over every row, while Rows is
// capped — so the figures describe the whole proof period even when the list
// below them does not. Truncated says so, because a total that disagrees with
// a visibly shorter list reads as a bug rather than as a page limit.
type QBShadowSummary struct {
	Rows       []QBPreviewRow
	Count      int
	TotalCents int
	Attention  int
	Truncated  bool
	// AwaitingManual is how many listed orders will not be billed by anything
	// unless a person invoices them — accounts on manual billing.
	AwaitingManual int
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
	// The zero time means "every preview", not a window: the page shows the
	// whole proof period, unlike the digest which shows one week of it.
	totals, err := s.qbPreviews.Totals(ctx, tx, time.Time{})
	if err != nil {
		return out, err
	}
	out.Count = totals.Count
	out.TotalCents = totals.TotalCents
	out.Attention = totals.NeedingAttention
	out.AwaitingManual = totals.AwaitingManual

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
	}
	// Count covers only the automatically billed orders, while the list holds
	// those plus the manual ones — so compare against the whole set.
	out.Truncated = out.Count+out.AwaitingManual > len(out.Rows)
	return out, nil
}

// CountQBPreviews returns how many would-be invoices are waiting to be
// reviewed, for the badge on the integrations page.
func (s *OrderService) CountQBPreviews(ctx context.Context, tx pgx.Tx) (int, error) {
	if s.qbPreviews == nil {
		return 0, nil
	}
	return s.qbPreviews.Count(ctx, tx)
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

// BillOrderNow enqueues the QuickBooks invoice chain for an order that has not
// been invoiced yet.
//
// This exists because the chain is otherwise only ever started by wholesale
// checkout. Orders placed while the shop is in test mode are recorded rather
// than billed, and without this there would be no way to bill them afterwards
// short of invoicing by hand in QuickBooks — the proof period would quietly
// cost the shop every order it covered.
//
// Refuses while in test mode: the point of test mode is that nothing bills, and
// a button that silently made an exception would undermine the only guarantee
// it offers.
func (s *OrderService) BillOrderNow(ctx context.Context, tx pgx.Tx, orderID uuid.UUID, enqueue QBChainEnqueuer, actor Actor) error {
	mode, err := s.QBBillingMode(ctx, tx)
	if err != nil {
		return err
	}
	if !mode.IsLive() {
		return ErrQBBillingNotLive
	}

	order, err := s.orders.GetOrderByIDAsStaff(ctx, tx, orderID)
	if err != nil {
		return fmt.Errorf("get order %s: %w", orderID, err)
	}
	if order.QBInvoiceID != nil {
		// Already billed. Returning an error rather than re-enqueueing keeps
		// the double-billing guard at the top of the chain rather than relying
		// on the adopt-by-DocNumber probe further down.
		return ErrQBOrderAlreadyInvoiced
	}
	if order.CustomerID == nil {
		return ErrQBOrderNotBillable
	}
	if order.Channel != domain.OrderChannelWholesale {
		return ErrQBOrderNotBillable
	}

	// staffRequested: a person is asking, which is the only thing that bills
	// an account nothing bills automatically.
	if err := enqueue(ctx, tx, *order.CustomerID, order.ID, true); err != nil {
		return fmt.Errorf("enqueue qb chain: %w", err)
	}
	return s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditQBOrderBilledManually,
		ResourceType: "order",
		ResourceID:   order.ID,
		Metadata:     map[string]any{"order_number": order.Number},
	})
}

// QBChainEnqueuer starts the QuickBooks invoice chain for one order. It is a
// function rather than a method on JobEnqueuer so the app layer does not have
// to learn the job args, which live in jobs/.
type QBChainEnqueuer func(ctx context.Context, tx pgx.Tx, customerID, orderID uuid.UUID, staffRequested bool) error

// QBItemConfigFor returns which QuickBooks items invoices bill against.
//
// Only the stored mapping. Listing what the company *offers* is an Intuit
// round trip and is made at the boundary, outside any transaction — see the
// external-call rule in CLAUDE.md.
func (s *OrderService) QBItemConfigFor(ctx context.Context, tx pgx.Tx) (store.QBItemConfig, error) {
	if s.settings == nil {
		return store.QBItemConfig{}, nil
	}
	return s.settings.GetQBItemConfig(ctx, tx)
}

// SetQBItems records which QuickBooks items wholesale invoices bill against.
//
// The caller has already resolved the choice against the connected company;
// this writes it and records who changed it. Names are stored beside the IDs
// so the settings page can name the current choice without calling
// QuickBooks, and so an audit entry read months later says "Wholesale Coffee"
// rather than "19".
func (s *OrderService) SetQBItems(ctx context.Context, tx pgx.Tx, cfg store.QBItemConfig, actor Actor) error {
	if s.settings == nil {
		return fmt.Errorf("qb items: settings store not wired")
	}
	if cfg.SalesItemID == "" {
		return ErrQBSalesItemRequired
	}
	// Read the outgoing mapping first: "which account did this used to post
	// to" is the question asked when a month of revenue turns up in the wrong
	// place, and it cannot be answered from the new value alone.
	previous, err := s.settings.GetQBItemConfig(ctx, tx)
	if err != nil {
		return err
	}
	if err := s.settings.UpdateQBItemConfig(ctx, tx, cfg); err != nil {
		return err
	}
	return s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditQBItemsUpdated,
		ResourceType: "store_settings",
		ResourceID:   uuid.Nil,
		After: map[string]any{
			"qb_sales_item_id":      cfg.SalesItemID,
			"qb_sales_item_name":    cfg.SalesItemName,
			"qb_shipping_item_id":   cfg.ShippingItemID,
			"qb_shipping_item_name": cfg.ShippingItemName,
		},
		Metadata: map[string]any{
			"previous_sales_item_id":      previous.SalesItemID,
			"previous_sales_item_name":    previous.SalesItemName,
			"previous_shipping_item_id":   previous.ShippingItemID,
			"previous_shipping_item_name": previous.ShippingItemName,
		},
	})
}
