package app

import (
	"context"
	"fmt"
	"log/slog"
	"math"
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
// invoice at the given reminder stage. dueDate is QB's authoritative due date,
// threaded through the job args by the reconcile — the email must show the
// date the invoice was actually issued under, not one recomputed from the
// customer's current terms (which may have changed since). Read → send → audit.
func (s *OrderService) SendInvoicePastDueEmail(ctx context.Context, pool *pgxpool.Pool, orderID, customerID uuid.UUID, stage int, dueDate time.Time) error {
	order, customer, err := s.loadOrderAndCustomer(ctx, pool, orderID, customerID)
	if err != nil {
		return err
	}

	// The template hides the "Was Due" row when the date is absent — better
	// than rendering a zero time if an old queued job predates the field.
	var duePtr *time.Time
	if !dueDate.IsZero() {
		duePtr = &dueDate
	}
	html, text, err := s.email.Renderer.Render("invoice_past_due", emailtemplates.InvoicePastDueData{
		CustomerName:  customer.FirstName,
		InvoiceNumber: qbInvoiceLabel(order),
		OrderNumber:   order.Number,
		AmountDue:     order.Total,
		DueDate:       duePtr,
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

// SendQBInvoiceAlertEmail notifies staff that a QB invoicing job failed
// permanently — the order will not be billed until someone intervenes.
// Recipient is EmailEnv.StaffEmail; when unset the alert is logged-only (the
// job cancellation is still in Sentry/logs), not retried forever.
func (s *OrderService) SendQBInvoiceAlertEmail(ctx context.Context, pool *pgxpool.Pool, orderID uuid.UUID, failedKind, cause string) error {
	if s.email.StaffEmail == "" {
		slog.WarnContext(ctx, "qb invoice alert: STAFF_NOTIFICATION_EMAIL unset, alert not emailed",
			"order_id", orderID, "failed_kind", failedKind)
		return nil
	}

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
		if order.CustomerID != nil {
			customer, err = s.customers.GetByID(ctx, tx, *order.CustomerID)
			return err
		}
		return nil
	}); err != nil {
		return err
	}

	companyName := ""
	if customer != nil && customer.CompanyName != nil {
		companyName = *customer.CompanyName
	}

	// Copy is per failed step: a send-only failure means the invoice already
	// exists in QuickBooks — telling staff to issue one by hand would
	// double-bill the customer.
	problem := "The order could not be invoiced in QuickBooks and the job has stopped retrying."
	nextStep := "Fix the underlying problem, then invoice the order by hand in QuickBooks or retry the job. The order will not be billed until then."
	if failedKind == "qb_send_invoice" {
		problem = "The invoice was created in QuickBooks but could not be emailed to the customer."
		nextStep = "Send the existing invoice manually from QuickBooks — do not create a new one."
	}

	html, text, err := s.email.Renderer.Render("qb_invoice_alert", emailtemplates.QBInvoiceAlertData{
		OrderNumber: order.Number,
		CompanyName: companyName,
		Problem:     problem,
		NextStep:    nextStep,
		FailedKind:  failedKind,
		Cause:       cause,
		OrderURL:    s.email.BaseURL + "/admin/orders/" + order.ID.String(),
		StoreName:   s.email.StoreName,
	})
	if err != nil {
		s.metrics.EmailsSent.WithLabelValues("qb_invoice_alert", "failed").Inc()
		return fmt.Errorf("render qb invoice alert template: %w", err)
	}

	if _, err := s.email.Mailer.Send(ctx, email.Message{
		From:    s.email.FromAddr,
		To:      s.email.StaffEmail,
		Subject: fmt.Sprintf("ACTION NEEDED: QuickBooks invoicing failed for order %s", order.Number),
		HTML:    html,
		Text:    text,
		Tag:     "qb-invoice-alert",
	}); err != nil {
		s.metrics.EmailsSent.WithLabelValues("qb_invoice_alert", "failed").Inc()
		return fmt.Errorf("send qb invoice alert: %w", err)
	}

	// Audit is best-effort once the email is out: failing the job here would
	// make River retry and re-send the alert — a duplicate staff email is
	// worse than a missing audit row.
	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		return s.audit.Record(ctx, tx, audit.AuditEntry{
			ActorType:    domain.AuditActorTypeSystem,
			ActorName:    "qb_invoice_alert_worker",
			Action:       audit.AuditEmailQBInvoiceAlert,
			ResourceType: "order",
			ResourceID:   order.ID,
			Metadata:     map[string]any{"failed_kind": failedKind, "order_number": order.Number},
		})
	}); err != nil {
		slog.ErrorContext(ctx, "audit qb invoice alert failed (email already sent)", "error", err.Error())
	}

	s.metrics.EmailsSent.WithLabelValues("qb_invoice_alert", "sent").Inc()
	return nil
}

// SendQBTokenAlertEmail warns staff that the QuickBooks refresh token is
// about to lapse (or already has) — once it does, every QB job stalls until
// the connection is re-authorized in admin settings. Sent daily by the
// qb_token_check periodic job while expiry is inside the warning window.
// Recipient is EmailEnv.StaffEmail; unset means log-only.
func (s *OrderService) SendQBTokenAlertEmail(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, expiresAt time.Time, riverJobID int64) error {
	if s.email.StaffEmail == "" {
		slog.WarnContext(ctx, "qb token alert: STAFF_NOTIFICATION_EMAIL unset, alert not emailed",
			"refresh_expires_at", expiresAt)
		return nil
	}

	remaining := time.Until(expiresAt)
	expired := remaining <= 0
	// Ceil, not truncate: 20 hours of token life is "expires in 1 day", never
	// the nonsensical "0 days" at the most urgent pre-expiry moment.
	daysLeft := int(math.Ceil(remaining.Hours() / 24))

	subject := fmt.Sprintf("QuickBooks connection expires in %d %s — reconnect soon", daysLeft, dayWord(daysLeft))
	if expired {
		subject = "ACTION NEEDED: QuickBooks connection has expired — invoicing is stalled"
	}

	html, text, err := s.email.Renderer.Render("qb_token_alert", emailtemplates.QBTokenAlertData{
		DaysLeft:    daysLeft,
		Expired:     expired,
		ExpiresAt:   expiresAt,
		SettingsURL: s.email.BaseURL + "/admin/settings",
		StoreName:   s.email.StoreName,
	})
	if err != nil {
		s.metrics.EmailsSent.WithLabelValues("qb_token_alert", "failed").Inc()
		return fmt.Errorf("render qb token alert template: %w", err)
	}

	if _, err := s.email.Mailer.Send(ctx, email.Message{
		From:    s.email.FromAddr,
		To:      s.email.StaffEmail,
		Subject: subject,
		HTML:    html,
		Text:    text,
		Tag:     "qb-token-alert",
	}); err != nil {
		s.metrics.EmailsSent.WithLabelValues("qb_token_alert", "failed").Inc()
		return fmt.Errorf("send qb token alert: %w", err)
	}

	// Audit is best-effort once the email is out: failing the job here would
	// make River retry and re-send the alert — a duplicate staff email is
	// worse than a missing audit row.
	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		return s.audit.Record(ctx, tx, audit.AuditEntry{
			ActorType:    domain.AuditActorTypeSystem,
			ActorName:    "qb_token_check_worker",
			Action:       audit.AuditEmailQBTokenAlert,
			ResourceType: "qb_connection",
			ResourceID:   tenantID,
			Metadata:     map[string]any{"refresh_expires_at": expiresAt, "expired": expired, "river_job_id": riverJobID},
		})
	}); err != nil {
		slog.ErrorContext(ctx, "audit qb token alert failed (email already sent)", "error", err.Error())
	}

	s.metrics.EmailsSent.WithLabelValues("qb_token_alert", "sent").Inc()
	return nil
}

// dayWord pluralizes "day" for alert copy.
func dayWord(n int) string {
	if n == 1 {
		return "day"
	}
	return "days"
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

// qbShadowDigestListCap bounds how many would-be invoices the digest lists.
// The count and the total are computed in SQL over the whole window, so a
// capped list never understates what a proof period actually saw.
const qbShadowDigestListCap = 40

// SendQBShadowDigestEmail summarises what QuickBooks billing would have done
// over the given window and mails it to staff.
//
// A proof period nobody looks at proves nothing, which is the whole reason
// this exists. It is sent only while the shop is in shadow mode: once billing
// is live the invoices themselves are the record, and a digest of them would
// be noise.
func (s *OrderService) SendQBShadowDigestEmail(ctx context.Context, pool *pgxpool.Pool, windowDays int) error {
	if s.email.StaffEmail == "" {
		slog.WarnContext(ctx, "qb shadow digest: STAFF_NOTIFICATION_EMAIL unset, digest not emailed")
		return nil
	}
	if s.qbPreviews == nil || s.settings == nil {
		slog.WarnContext(ctx, "qb shadow digest: preview store not wired, nothing to summarise")
		return nil
	}

	since := time.Now().AddDate(0, 0, -windowDays)

	var (
		mode   domain.QBBillingMode
		totals store.QBPreviewTotals
		rows   []store.QBPreviewRow
	)
	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		var txErr error
		if mode, txErr = s.settings.GetQBBillingMode(ctx, tx); txErr != nil {
			return txErr
		}
		if mode.IsLive() {
			return nil
		}
		if totals, txErr = s.qbPreviews.Totals(ctx, tx, since); txErr != nil {
			return txErr
		}
		rows, txErr = s.qbPreviews.ListSince(ctx, tx, since)
		return txErr
	}); err != nil {
		return err
	}

	// Live shops get no digest. Checked here rather than by the scheduler so
	// that flipping to live silences it immediately, without a redeploy.
	if mode.IsLive() {
		return nil
	}

	// Nothing happened this week, so say nothing. Shadow is the default for
	// every install with QuickBooks connected, and a shop that does not run
	// wholesale through it would otherwise receive an empty report forever —
	// which trains staff to ignore the one that eventually matters.
	if totals.Count == 0 {
		return nil
	}

	// Rows needing attention lead: the finding is an account that would fail,
	// not the money. Within each group the original order is preserved.
	sorted := make([]store.QBPreviewRow, 0, len(rows))
	for _, r := range rows {
		if r.NeedsAttention() {
			sorted = append(sorted, r)
		}
	}
	for _, r := range rows {
		if !r.NeedsAttention() {
			sorted = append(sorted, r)
		}
	}
	if len(sorted) > qbShadowDigestListCap {
		sorted = sorted[:qbShadowDigestListCap]
	}

	invoices := make([]emailtemplates.QBShadowDigestInvoice, 0, len(sorted))
	for _, r := range sorted {
		invoices = append(invoices, emailtemplates.QBShadowDigestInvoice{
			OrderNumber: r.OrderNumber,
			Customer:    r.CustomerName,
			TotalCents:  r.TotalCents,
			Terms:       domain.PaymentTermsLabel(r.TermsDays),
			DueDate:     r.DueDate,
			BillEmail:   r.BillEmail,
			URL:         s.email.BaseURL + "/admin/orders/" + r.OrderID.String(),
			Problem:     r.Problem(),
		})
	}

	html, text, err := s.email.Renderer.Render("qb_shadow_digest", emailtemplates.QBShadowDigestData{
		Invoices:      invoices,
		Total:         totals.Count,
		TotalAmtCents: totals.TotalCents,
		Attention:     totals.NeedingAttention,
		Days:          windowDays,
		ReviewURL:     s.email.BaseURL + "/admin/settings/integrations/quickbooks/preview",
		StoreName:     s.email.StoreName,
	})
	if err != nil {
		s.metrics.EmailsSent.WithLabelValues("qb_shadow_digest", "failed").Inc()
		return fmt.Errorf("render qb shadow digest template: %w", err)
	}

	subject := fmt.Sprintf("QuickBooks test mode: %d would-be invoice(s) this week", totals.Count)
	if totals.NeedingAttention > 0 {
		subject = fmt.Sprintf("QuickBooks test mode: %d of %d need attention", totals.NeedingAttention, totals.Count)
	}

	if _, err := s.email.Mailer.Send(ctx, email.Message{
		From:    s.email.FromAddr,
		To:      s.email.StaffEmail,
		Subject: subject,
		HTML:    html,
		Text:    text,
		Tag:     "qb-shadow-digest",
	}); err != nil {
		s.metrics.EmailsSent.WithLabelValues("qb_shadow_digest", "failed").Inc()
		return fmt.Errorf("send qb shadow digest: %w", err)
	}
	s.metrics.EmailsSent.WithLabelValues("qb_shadow_digest", "sent").Inc()
	return nil
}
