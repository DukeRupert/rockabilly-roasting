package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

// OrderReminderWindow is how far back an account must have ordered to be
// reminded. Three weeks covers the common wholesale cadences (weekly,
// biweekly, monthly-ish) without nagging accounts that have gone dormant —
// a dormant account needs a sales conversation, not an automated nudge.
const OrderReminderWindow = 21 * 24 * time.Hour

// OrderReminderCutoffLabel is the human deadline printed in the email. The
// reminder goes out Friday morning for a Friday-afternoon cutoff, which is
// what the Orderspace-era reminder service said and what the roasting and
// delivery schedule still runs on.
const OrderReminderCutoffLabel = "Friday afternoon"

// OrderReminderSuppressWindow skips anyone who has already ordered since the
// last reminder went out. Without it a customer who ordered on Wednesday still
// gets Friday's "time to place your order" — and a few weeks of that teaches
// people the email is noise, which costs far more than the missed nudge.
//
// It equals the reminder interval by definition: the job runs weekly, so
// "ordered inside the last 7 days" is exactly "ordered since we last asked".
const OrderReminderSuppressWindow = 7 * 24 * time.Hour

// ErrEmptyNotice is returned when a staff-composed notice has no subject or
// no body — a blank blast to every active wholesale account is never intended.
var ErrEmptyNotice = errors.New("notice subject and body are both required")

// ErrNoPreviousOrder is returned when a reorder is requested for a customer
// who has no completed wholesale order to copy.
var ErrNoPreviousOrder = errors.New("no previous wholesale order")

// ListOrderReminderRecipients returns the accounts that would receive this
// week's reminder. It is the shared audience query: the scheduler enqueues
// from it and the admin preview renders from it, so what staff see in the
// preview is exactly who gets mail.
func (s *WholesaleService) ListOrderReminderRecipients(ctx context.Context, tx pgx.Tx, now time.Time) ([]domain.OrderReminderRecipient, error) {
	return s.customers.ListOrderReminderRecipients(ctx, tx,
		now.Add(-OrderReminderWindow), now.Add(-OrderReminderSuppressWindow))
}

// SetOrderRemindersEnabled turns the weekly reminder on or off for one
// customer — the replacement for the old service's per-customer opt-out flag.
func (s *WholesaleService) SetOrderRemindersEnabled(ctx context.Context, tx pgx.Tx, actor Actor, customerID uuid.UUID, enabled bool) error {
	if err := s.customers.UpdateOrderRemindersEnabled(ctx, tx, customerID, enabled); err != nil {
		return err
	}

	action := audit.AuditCustomerOrderRemindersDisabled
	if enabled {
		action = audit.AuditCustomerOrderRemindersEnabled
	}
	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       action,
		ResourceType: "customer",
		ResourceID:   customerID,
		Metadata:     map[string]any{"enabled": enabled},
	}); err != nil {
		return fmt.Errorf("audit order reminder preference: %w", err)
	}
	return nil
}

// SendOrderReminder emails one wholesale account the weekly order reminder.
//
// Sends live outside a transaction (external call first, then write) per the
// house rule; the audit row is written in its own tx afterwards so a Postmark
// failure never leaves a record claiming the mail went out.
func (s *WholesaleService) SendOrderReminder(ctx context.Context, pool *pgxpool.Pool, customerID uuid.UUID) error {
	var (
		customer  *domain.Customer
		lastItems []emailtemplates.OrderLineItemData
		lastOn    *time.Time
	)
	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		c, err := s.customers.GetByID(ctx, tx, customerID)
		if err != nil {
			return fmt.Errorf("get customer %s: %w", customerID, err)
		}
		customer = c

		// The customer's last order is printed in the email so the decision
		// ("do I need this again?") happens in the inbox instead of after a
		// login. Best-effort: a reminder with no item list is still useful, so
		// a lookup failure must not block the send.
		order, itemsErr := s.lastWholesaleOrder(ctx, tx, customerID)
		if itemsErr != nil || order == nil {
			return nil
		}
		lineItems, itemsErr := s.orders.ListLineItems(ctx, tx, order.ID)
		if itemsErr != nil {
			return nil
		}
		lastItems = emailLineItemsFrom(ctx, tx, s.catalog, lineItems)
		placed := order.PlacedAt
		lastOn = &placed
		return nil
	}); err != nil {
		return err
	}

	// Re-check eligibility at send time. The scheduler fans these out as
	// individual jobs, so a customer can be suspended or opted out in the gap
	// between the scan and the send — and River retries can widen that gap to
	// hours. Dropping the job here is correct: skipping is not an error.
	if !customer.IsApprovedWholesale() || !customer.OrderRemindersEnabled {
		return nil
	}

	html, text, err := s.email.Renderer.Render("order_reminder", emailtemplates.OrderReminderData{
		CompanyName:   reminderGreeting(customer),
		CutoffLabel:   OrderReminderCutoffLabel,
		LastItems:     lastItems,
		LastOrderedOn: lastOn,
		// Deep link straight into a cart prefilled from their last order.
		// Resolved server-side at click time rather than baking an order ID
		// into a URL that will sit in an inbox.
		ReorderURL: s.email.BaseURL + "/wholesale/reorder",
		OrderURL:   s.email.BaseURL + "/wholesale/portal",
		StoreName:  s.email.StoreName,
		StoreURL:   s.email.BaseURL,
	})
	if err != nil {
		s.metrics.EmailsSent.WithLabelValues("order_reminder", "failed").Inc()
		return fmt.Errorf("render order reminder template: %w", err)
	}

	if _, err := s.email.Mailer.Send(ctx, email.Message{
		From:    s.email.FromAddr,
		To:      customer.Email,
		Subject: "Time to place your coffee order",
		HTML:    html,
		Text:    text,
		Tag:     "order-reminder",
	}); err != nil {
		s.metrics.EmailsSent.WithLabelValues("order_reminder", "failed").Inc()
		return fmt.Errorf("send order reminder: %w", err)
	}

	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		return s.audit.Record(ctx, tx, audit.AuditEntry{
			ActorType:    domain.AuditActorTypeSystem,
			ActorName:    "order_reminder_worker",
			Action:       audit.AuditEmailOrderReminderSent,
			ResourceType: "customer",
			ResourceID:   customer.ID,
			Metadata:     map[string]any{"company": reminderGreeting(customer)},
		})
	}); err != nil {
		return fmt.Errorf("audit order reminder: %w", err)
	}

	s.metrics.EmailsSent.WithLabelValues("order_reminder", "sent").Inc()
	return nil
}

// lastWholesaleOrder returns the customer's most recent wholesale order, or
// nil when they have none. Cancelled, refunded and unconfirmed orders are
// excluded — reordering from an order that never completed would hand the
// customer back a basket they already abandoned.
//
// Shared by the reminder email (to print the item list) and the reorder deep
// link (to decide what to load), so the two can never disagree about which
// order "last time" refers to.
func (s *WholesaleService) lastWholesaleOrder(ctx context.Context, tx pgx.Tx, customerID uuid.UUID) (*domain.Order, error) {
	channel := domain.OrderChannelWholesale
	orders, err := s.orders.ListOrders(ctx, tx, store.OrderFilter{
		CustomerID:               &customerID,
		Channel:                  &channel,
		ExcludeCancelledRefunded: true,
		ExcludeUnconfirmed:       true,
		Limit:                    1, // ListOrders sorts placed_at DESC
	})
	if err != nil {
		return nil, fmt.Errorf("list last wholesale order: %w", err)
	}
	if len(orders) == 0 {
		return nil, nil
	}
	return &orders[0], nil
}

// LastWholesaleOrderID exposes the resolved "last order" to the web layer for
// the reorder deep link.
func (s *WholesaleService) LastWholesaleOrderID(ctx context.Context, tx pgx.Tx, customerID uuid.UUID) (uuid.UUID, error) {
	order, err := s.lastWholesaleOrder(ctx, tx, customerID)
	if err != nil {
		return uuid.Nil, err
	}
	if order == nil {
		return uuid.Nil, ErrNoPreviousOrder
	}
	return order.ID, nil
}

// NoticeParams is a staff-composed one-off message to the reminder audience.
type NoticeParams struct {
	Subject string
	Body    string // plain text; blank lines separate paragraphs
}

// SendWholesaleNotice emails one customer a staff-composed notice. This is the
// audited replacement for the old service's unauthenticated /send-adhoc
// endpoint — same purpose (corrections, changed cutoffs, holiday schedules),
// but staff-authenticated, recorded per recipient, and rendered into the
// branded shell rather than accepting raw HTML from the request body.
func (s *WholesaleService) SendWholesaleNotice(ctx context.Context, pool *pgxpool.Pool, customerID uuid.UUID, p NoticeParams) error {
	if strings.TrimSpace(p.Subject) == "" || strings.TrimSpace(p.Body) == "" {
		return ErrEmptyNotice
	}

	var customer *domain.Customer
	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		c, err := s.customers.GetByID(ctx, tx, customerID)
		if err != nil {
			return fmt.Errorf("get customer %s: %w", customerID, err)
		}
		customer = c
		return nil
	}); err != nil {
		return err
	}

	// A notice is operational rather than promotional, so it ignores the
	// weekly-reminder opt-out — but it still never goes to an account that is
	// no longer an approved wholesale customer.
	if !customer.IsApprovedWholesale() {
		return nil
	}

	html, text, err := s.email.Renderer.Render("wholesale_notice", emailtemplates.WholesaleNoticeData{
		Heading:    strings.TrimSpace(p.Subject),
		Paragraphs: splitParagraphs(p.Body),
		OrderURL:   s.email.BaseURL + "/wholesale",
		StoreName:  s.email.StoreName,
		StoreURL:   s.email.BaseURL,
	})
	if err != nil {
		s.metrics.EmailsSent.WithLabelValues("wholesale_notice", "failed").Inc()
		return fmt.Errorf("render wholesale notice template: %w", err)
	}

	if _, err := s.email.Mailer.Send(ctx, email.Message{
		From:    s.email.FromAddr,
		To:      customer.Email,
		Subject: strings.TrimSpace(p.Subject),
		HTML:    html,
		Text:    text,
		Tag:     "wholesale-notice",
	}); err != nil {
		s.metrics.EmailsSent.WithLabelValues("wholesale_notice", "failed").Inc()
		return fmt.Errorf("send wholesale notice: %w", err)
	}

	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		return s.audit.Record(ctx, tx, audit.AuditEntry{
			ActorType:    domain.AuditActorTypeSystem,
			ActorName:    "wholesale_notice_worker",
			Action:       audit.AuditEmailWholesaleNoticeSent,
			ResourceType: "customer",
			ResourceID:   customer.ID,
			Metadata: map[string]any{
				"company": reminderGreeting(customer),
				"subject": strings.TrimSpace(p.Subject),
			},
		})
	}); err != nil {
		return fmt.Errorf("audit wholesale notice: %w", err)
	}

	s.metrics.EmailsSent.WithLabelValues("wholesale_notice", "sent").Inc()
	return nil
}

// reminderGreeting picks the name to address a wholesale account by. Company
// name is what these customers recognize; the personal name is the fallback
// for the rare account that was created without one.
func reminderGreeting(c *domain.Customer) string {
	if c.CompanyName != nil && strings.TrimSpace(*c.CompanyName) != "" {
		return strings.TrimSpace(*c.CompanyName)
	}
	if name := strings.TrimSpace(c.FirstName + " " + c.LastName); name != "" {
		return name
	}
	return "there"
}

// splitParagraphs turns a staff-typed body into paragraphs on blank lines,
// collapsing single newlines inside a paragraph so soft-wrapped input does not
// render as a ragged block.
func splitParagraphs(body string) []string {
	var out []string
	for _, block := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n\n") {
		var lines []string
		for _, line := range strings.Split(block, "\n") {
			if t := strings.TrimSpace(line); t != "" {
				lines = append(lines, t)
			}
		}
		if len(lines) > 0 {
			out = append(out, strings.Join(lines, " "))
		}
	}
	return out
}
