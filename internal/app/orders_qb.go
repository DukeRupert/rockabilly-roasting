package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/store"
)

// QBInvoiceFacts is QuickBooks' current view of a wholesale invoice, expressed
// in the units the order layer compares against. The QB job fetches the invoice
// (external I/O, outside any transaction) and hands these primitives to
// ReconcileWholesalePayment, so the app layer never imports the quickbooks
// package.
type QBInvoiceFacts struct {
	BalanceCents int       // remaining owed, in cents (<= 0 means paid in full)
	TotalCents   int       // invoice total, in cents (<= 0 means voided in QB)
	DueDate      time.Time // net-terms due date from QB; zero if QB omitted it
	NotFound     bool      // the QB invoice was deleted (GetInvoice returned 404)
}

// ReconcileTransition names the payment-status change a reconcile applied, for
// the caller's logging and metrics. ReconcileNone means nothing changed.
type ReconcileTransition string

const (
	ReconcileNone          ReconcileTransition = "none"
	ReconcileInvoiced      ReconcileTransition = "invoiced"
	ReconcilePartiallyPaid ReconcileTransition = "partially_paid"
	ReconcileOverdue       ReconcileTransition = "overdue"
	ReconcileCaptured      ReconcileTransition = "captured"
	ReconcileReverted      ReconcileTransition = "reverted"
)

// overdueReminderMilestones are the days-since-placed thresholds at which a
// past-due reminder is sent for an unpaid wholesale invoice. With net-7 terms,
// day 7 is the due date (first nudge) and the rest are weekly follow-ups. The
// order.overdue_reminder_stage column records the highest milestone already
// sent so each fires exactly once.
var overdueReminderMilestones = []int{7, 14, 21, 30}

// qbReconcileActor is the system actor recorded for QB-driven payment-status
// transitions (webhook or reconciliation poll).
var qbReconcileActor = Actor{Type: domain.AuditActorTypeSystem, Name: "qb_reconcile"}

// ListWholesaleOpenInvoiceOrders returns QB-owned orders whose payment status
// is not yet settled — the candidate set the reconciliation poll iterates.
// Bounded by limit.
func (s *OrderService) ListWholesaleOpenInvoiceOrders(ctx context.Context, pool *pgxpool.Pool, limit int) ([]domain.Order, error) {
	var orders []domain.Order
	err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		var e error
		orders, e = s.orders.ListWholesaleOpenInvoiceOrders(ctx, tx, limit)
		return e
	})
	if err != nil {
		return nil, fmt.Errorf("list wholesale open invoice orders: %w", err)
	}
	return orders, nil
}

// ReconcileQBInvoiceByID fetches the QB-owned order for the given invoice ID
// (FOR UPDATE) and reconciles its payment status against facts QuickBooks
// reported, in one transaction. It returns the transition applied. The caller
// (a QB job) must fetch facts from QuickBooks OUTSIDE any transaction first;
// this method owns the locking transaction so concurrent webhook/poll runs on
// the same invoice serialize. ErrOrderNotFound is returned if no order carries
// the invoice ID (e.g. a webhook for an invoice we didn't create).
func (s *OrderService) ReconcileQBInvoiceByID(ctx context.Context, pool *pgxpool.Pool, qbInvoiceID string, facts QBInvoiceFacts, now time.Time) (ReconcileTransition, error) {
	transition := ReconcileNone
	err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		order, err := s.orders.GetOrderByQBInvoiceIDForUpdate(ctx, tx, qbInvoiceID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrOrderNotFound
			}
			return fmt.Errorf("get order by qb invoice id: %w", err)
		}
		transition, err = s.ReconcileWholesalePayment(ctx, tx, order, facts, now)
		return err
	})
	return transition, err
}

// ReconcileWholesalePayment is the single writer of QuickBooks-driven payment
// status for a wholesale order. Given QB's current view of the invoice, it
// idempotently transitions the order, records an audit entry, and enqueues the
// matching customer email — all in the caller's transaction.
//
// Caller rules: fetch the QB invoice OUTSIDE any transaction, then call this
// INSIDE store.Tx with an order read FOR UPDATE (GetOrderByQBInvoiceIDForUpdate)
// so concurrent webhook/poll reconciles serialize and side effects fire once.
// Callers must not write payment status, audit, or email themselves.
//
// Transition precedence (first match wins):
//  1. invoice voided or deleted in QB  -> revert to pending_invoice
//  2. balance <= 0 (incl. overpayment) -> captured (+ paid email)
//  3. past due (balance owed, now > due)-> overdue (+ milestone reminder email)
//  4. 0 < balance < total              -> partially_paid
//  5. fully unpaid, pending_invoice    -> invoiced
//  6. otherwise                        -> no change
//
// It is a no-op (ReconcileNone) for orders that aren't QB-owned, are in a
// terminal order state, or whose payment status is already settled.
func (s *OrderService) ReconcileWholesalePayment(ctx context.Context, tx pgx.Tx, order *domain.Order, f QBInvoiceFacts, now time.Time) (ReconcileTransition, error) {
	// Only QB-owned orders are reconciled by this path.
	if order.QBInvoiceID == nil {
		return ReconcileNone, nil
	}
	// Never resurrect a cancelled or refunded order.
	if order.Status == domain.OrderStatusCancelled || order.Status == domain.OrderStatusRefunded {
		return ReconcileNone, nil
	}
	// Act only on open wholesale payment states. This also makes the method
	// idempotent: an already-captured (or refunded/voided) order falls through
	// to a no-op.
	switch order.PaymentStatus {
	case domain.PaymentStatusPendingInvoice,
		domain.PaymentStatusInvoiced,
		domain.PaymentStatusPartiallyPaid,
		domain.PaymentStatusOverdue:
	default:
		return ReconcileNone, nil
	}

	switch {
	// 1. Voided or deleted in QB — revert so staff (or a later sync) can issue
	// a fresh invoice. A voided QB invoice zeroes its amounts, so TotalCents<=0.
	case f.NotFound || f.TotalCents <= 0:
		return s.revertVoidedInvoice(ctx, tx, order)

	// 2. Paid in full. balance<=0 also captures overpayment / credit balances.
	case f.BalanceCents <= 0:
		return s.captureInvoicePayment(ctx, tx, order)

	// 3. Past due — balance still owed and QB gave us a due date that has passed.
	case !f.DueDate.IsZero() && now.After(f.DueDate):
		return s.markInvoiceOverdue(ctx, tx, order, now)

	// 4. Partial payment within terms.
	case f.BalanceCents < f.TotalCents:
		return s.transitionPaymentStatus(ctx, tx, order,
			domain.PaymentStatusPartiallyPaid, ReconcilePartiallyPaid, audit.AuditOrderPaymentPartiallyPaid)

	// 5. Fully unpaid within terms — advance pending_invoice -> invoiced once.
	case order.PaymentStatus == domain.PaymentStatusPendingInvoice:
		return s.transitionPaymentStatus(ctx, tx, order,
			domain.PaymentStatusInvoiced, ReconcileInvoiced, audit.AuditOrderPaymentInvoiced)

	default:
		return ReconcileNone, nil
	}
}

// transitionPaymentStatus sets a new payment status with an audit record and no
// email. No-op if the order is already in the target status.
func (s *OrderService) transitionPaymentStatus(
	ctx context.Context, tx pgx.Tx, order *domain.Order,
	status domain.PaymentStatus, transition ReconcileTransition, action string,
) (ReconcileTransition, error) {
	if order.PaymentStatus == status {
		return ReconcileNone, nil
	}
	if _, err := s.orders.UpdateOrderPaymentStatus(ctx, tx, order.ID, status); err != nil {
		return ReconcileNone, fmt.Errorf("update payment status: %w", err)
	}
	if err := s.recordQBAudit(ctx, tx, order, action, nil); err != nil {
		return ReconcileNone, err
	}
	return transition, nil
}

// captureInvoicePayment marks a QB invoice paid in full and enqueues the
// payment-confirmation email in the same transaction.
func (s *OrderService) captureInvoicePayment(ctx context.Context, tx pgx.Tx, order *domain.Order) (ReconcileTransition, error) {
	if _, err := s.orders.UpdateOrderPaymentStatus(ctx, tx, order.ID, domain.PaymentStatusCaptured); err != nil {
		return ReconcileNone, fmt.Errorf("update payment status: %w", err)
	}
	if err := s.recordQBAudit(ctx, tx, order, audit.AuditOrderPaymentCaptured, map[string]any{"payment_method": "ach"}); err != nil {
		return ReconcileNone, err
	}
	if s.enqueuer != nil && order.CustomerID != nil {
		if err := s.enqueuer.EnqueueInvoicePaid(ctx, tx, order.ID, *order.CustomerID); err != nil {
			return ReconcileNone, fmt.Errorf("enqueue invoice paid email: %w", err)
		}
	}
	return ReconcileCaptured, nil
}

// markInvoiceOverdue flips the order to overdue (on first crossing) and sends at
// most one past-due reminder per milestone, advancing overdue_reminder_stage so
// the daily poll never re-sends a milestone it has already notified.
func (s *OrderService) markInvoiceOverdue(ctx context.Context, tx pgx.Tx, order *domain.Order, now time.Time) (ReconcileTransition, error) {
	changed := false

	if order.PaymentStatus != domain.PaymentStatusOverdue {
		if _, err := s.orders.UpdateOrderPaymentStatus(ctx, tx, order.ID, domain.PaymentStatusOverdue); err != nil {
			return ReconcileNone, fmt.Errorf("update payment status: %w", err)
		}
		if err := s.recordQBAudit(ctx, tx, order, audit.AuditOrderPaymentOverdue, nil); err != nil {
			return ReconcileNone, err
		}
		changed = true
	}

	daysOverdue := int(now.Sub(order.PlacedAt).Hours() / 24)
	if milestone := highestOverdueMilestone(daysOverdue); milestone > order.OverdueReminderStage {
		if s.enqueuer != nil && order.CustomerID != nil {
			if err := s.enqueuer.EnqueueInvoicePastDue(ctx, tx, order.ID, *order.CustomerID, milestone); err != nil {
				return ReconcileNone, fmt.Errorf("enqueue past-due email: %w", err)
			}
		}
		if err := s.orders.SetOverdueReminderStage(ctx, tx, order.ID, milestone); err != nil {
			return ReconcileNone, fmt.Errorf("set overdue reminder stage: %w", err)
		}
		changed = true
	}

	if changed {
		return ReconcileOverdue, nil
	}
	return ReconcileNone, nil
}

// revertVoidedInvoice returns an order to pending_invoice after its QB invoice
// was voided or deleted, so a fresh invoice can be issued. Resets the reminder
// stage since the bill is no longer live.
func (s *OrderService) revertVoidedInvoice(ctx context.Context, tx pgx.Tx, order *domain.Order) (ReconcileTransition, error) {
	if order.PaymentStatus == domain.PaymentStatusPendingInvoice {
		return ReconcileNone, nil
	}
	if _, err := s.orders.UpdateOrderPaymentStatus(ctx, tx, order.ID, domain.PaymentStatusPendingInvoice); err != nil {
		return ReconcileNone, fmt.Errorf("update payment status: %w", err)
	}
	if order.OverdueReminderStage != 0 {
		if err := s.orders.SetOverdueReminderStage(ctx, tx, order.ID, 0); err != nil {
			return ReconcileNone, fmt.Errorf("reset overdue reminder stage: %w", err)
		}
	}
	if err := s.recordQBAudit(ctx, tx, order, audit.AuditQBInvoiceVoided, map[string]any{"qb_voided": true}); err != nil {
		return ReconcileNone, err
	}
	return ReconcileReverted, nil
}

// recordQBAudit writes a system audit entry for a QB-driven order transition,
// always tagging the QB invoice ID and merging any extra metadata.
func (s *OrderService) recordQBAudit(ctx context.Context, tx pgx.Tx, order *domain.Order, action string, extra map[string]any) error {
	meta := map[string]any{}
	if order.QBInvoiceID != nil {
		meta["qb_invoice_id"] = *order.QBInvoiceID
	}
	for k, v := range extra {
		meta[k] = v
	}
	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    qbReconcileActor.Type,
		ActorName:    qbReconcileActor.Name,
		Action:       action,
		ResourceType: "order",
		ResourceID:   order.ID,
		Metadata:     meta,
	}); err != nil {
		return fmt.Errorf("audit %s: %w", action, err)
	}
	return nil
}

// highestOverdueMilestone returns the largest reminder milestone that daysOverdue
// has reached, or 0 if none have been reached yet.
func highestOverdueMilestone(daysOverdue int) int {
	highest := 0
	for _, m := range overdueReminderMilestones {
		if daysOverdue >= m {
			highest = m
		}
	}
	return highest
}
