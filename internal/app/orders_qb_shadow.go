package app

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/quickbooks"
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
	out.Truncated = out.Count > len(out.Rows)
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

	if err := enqueue(ctx, tx, *order.CustomerID, order.ID); err != nil {
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
type QBChainEnqueuer func(ctx context.Context, tx pgx.Tx, customerID, orderID uuid.UUID) error

// QBItemChoice is one selectable QuickBooks item, for the settings picker.
type QBItemChoice struct {
	ID            string
	Name          string
	Type          string
	IncomeAccount string
}

// QBItemSettings is the current invoice-item mapping plus what it could be
// changed to. Choices is nil when the company's items could not be listed —
// the page then says so rather than rendering an empty picker, which would
// read as "this company has no items".
type QBItemSettings struct {
	Current    store.QBItemConfig
	Choices    []QBItemChoice
	ListErr    error
	EnvSalesID string // fallback still supplied by the environment, if any
}

// QBItemSettingsFor reads the configured invoice items and lists what the
// connected company offers.
//
// The listing is a live read-only call, so it is safe during a proof period —
// deciding which item to bill against is exactly the sort of thing a shop
// should settle before going live.
func (s *OrderService) QBItemSettingsFor(ctx context.Context, tx pgx.Tx, qb quickbooks.Client, envSalesID string) (QBItemSettings, error) {
	out := QBItemSettings{EnvSalesID: envSalesID}
	if s.settings == nil {
		return out, nil
	}
	cfg, err := s.settings.GetQBItemConfig(ctx, tx)
	if err != nil {
		return out, err
	}
	out.Current = cfg

	if qb == nil {
		return out, nil
	}
	items, listErr := qb.ListItems(ctx)
	if listErr != nil {
		// Not fatal: the page must still show what is currently chosen, and a
		// failed listing is a different fact from "there is nothing to choose".
		out.ListErr = listErr
		return out, nil
	}
	for _, item := range items {
		out.Choices = append(out.Choices, QBItemChoice{
			ID: item.ID, Name: item.Name, Type: item.Type, IncomeAccount: item.IncomeAccount,
		})
	}
	return out, nil
}

// SetQBItems records which QuickBooks items wholesale invoices bill against.
//
// The names are stored beside the IDs so the settings page can name the
// current choice without calling QuickBooks, and so an audit entry read months
// later says "Wholesale Coffee" rather than "19".
func (s *OrderService) SetQBItems(ctx context.Context, tx pgx.Tx, qb quickbooks.Client, salesID, shippingID string, actor Actor) error {
	if s.settings == nil {
		return fmt.Errorf("qb items: settings store not wired")
	}
	if salesID == "" {
		return ErrQBSalesItemRequired
	}

	// Resolve names from the company rather than trusting the form: the IDs
	// have to exist in QuickBooks or every invoice fails, and this is the last
	// point where saying so is cheap.
	byID := map[string]quickbooks.Item{}
	if qb != nil {
		items, err := qb.ListItems(ctx)
		if err != nil {
			return fmt.Errorf("qb list items: %w", err)
		}
		for _, item := range items {
			byID[item.ID] = item
		}
		if _, ok := byID[salesID]; !ok {
			return ErrQBItemNotFound
		}
		if shippingID != "" {
			if _, ok := byID[shippingID]; !ok {
				return ErrQBItemNotFound
			}
		}
	}

	cfg := store.QBItemConfig{
		SalesItemID:      salesID,
		SalesItemName:    byID[salesID].Name,
		ShippingItemID:   shippingID,
		ShippingItemName: byID[shippingID].Name,
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
			"qb_sales_item_id":    cfg.SalesItemID,
			"qb_sales_item_name":  cfg.SalesItemName,
			"qb_shipping_item_id": cfg.ShippingItemID,
		},
	})
}
