package app

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/emailtemplates"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/email"
	"github.com/dukerupert/hiri/internal/store"
)

// staleDigestLimit caps how many tickets the digest lists. A mail with two
// hundred rows is a mail nobody reads; the queue is the place to work through
// a backlog, and the digest says how many it left out.
const staleDigestLimit = 20

// SweepStaleTickets is the daily pass over open service tickets that nobody has
// spoken to the customer about inside domain.DefaultStaleContactWindow.
//
// Until now staleness was purely pull-based: the queue has a Stale scope, but
// it only surfaces work for a staffer who thinks to click it. A ticket going
// quiet is exactly the failure that nobody thinks to go looking for, so this
// pushes it — one digest to staff, no customer mail ever.
//
// Two things always happen and one sometimes does. The gauges are always
// published, because a monitoring series that goes blank on a quiet day is
// indistinguishable from a broken exporter. The digest is sent only when there
// is something in it — a daily "nothing is wrong" mail trains people to filter
// the address, which costs exactly the alert this exists to deliver.
//
// Returns nil (not an error) when the module is off or STAFF_NOTIFICATION_EMAIL
// is unset: neither is a fault, and returning an error would make River retry a
// job that will never succeed on this instance.
//
// Idempotency: the job is keyed on the day by its InsertOpts, so a re-enqueue
// inside the same period is dropped by River rather than sending twice. A
// genuine retry after a send failure can re-send the digest — a duplicate
// digest is a far better outcome than a silently missing one.
func (s *ServiceTicketService) SweepStaleTickets(ctx context.Context, pool *pgxpool.Pool, now time.Time) error {
	if s.modules == nil || !s.modules.Enabled(domain.ModuleEquipmentService) {
		return nil
	}

	cutoff := now.Add(-domain.DefaultStaleContactWindow)

	var (
		stale        []domain.ServiceTicket
		openByStatus map[domain.ServiceTicketStatus]int
	)
	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		var txErr error
		// Unlimited: the count drives the gauge and the "showing N of M" line,
		// so it has to be the real total, not the display slice.
		stale, txErr = s.ListStale(ctx, tx, cutoff, 0)
		if txErr != nil {
			return txErr
		}
		openByStatus, txErr = s.tickets.CountOpenByStatus(ctx, tx)
		return txErr
	}); err != nil {
		return fmt.Errorf("sweep stale service tickets: %w", err)
	}

	s.publishTicketGauges(openByStatus, len(stale))

	if len(stale) == 0 {
		return nil
	}
	if s.email.Mailer == nil || s.email.StaffEmail == "" {
		slog.WarnContext(ctx, "service stale sweep: STAFF_NOTIFICATION_EMAIL unset, digest not emailed",
			"stale", len(stale))
		return nil
	}

	return s.sendStaleDigest(ctx, pool, stale, now)
}

// publishTicketGauges sets the open-by-status and stale gauges.
//
// Every known status is written, including the ones the grouped query did not
// return, so a status that empties out drops to zero instead of holding its
// last value forever — a stuck series is worse than no series.
func (s *ServiceTicketService) publishTicketGauges(openByStatus map[domain.ServiceTicketStatus]int, stale int) {
	if s.metrics == nil {
		return
	}
	for _, status := range domain.OpenServiceTicketStatuses() {
		s.metrics.ServiceTicketsOpen.WithLabelValues(string(status)).Set(float64(openByStatus[status]))
	}
	s.metrics.ServiceTicketsStale.Set(float64(stale))
}

// sendStaleDigest composes and sends the staff digest, then records the send.
//
// The mail goes out before the audit row is written and outside any
// transaction: the send is the external call, and holding a transaction open
// across it is the thing the RenewalService pattern exists to prevent.
func (s *ServiceTicketService) sendStaleDigest(ctx context.Context, pool *pgxpool.Pool, stale []domain.ServiceTicket, now time.Time) error {
	total := len(stale)

	ordered := orderStaleForDigest(stale, staleDigestLimit)

	rows, err := s.digestRows(ctx, pool, ordered, now)
	if err != nil {
		return err
	}

	html, text, err := s.email.Renderer.Render("service_stale_digest", emailtemplates.ServiceStaleDigestData{
		Tickets:    rows,
		Total:      total,
		WindowDays: int(domain.DefaultStaleContactWindow.Hours() / 24),
		QueueURL:   s.email.BaseURL + "/admin/service?scope=stale",
		StoreName:  s.email.StoreName,
	})
	if err != nil {
		s.recordEmailFailure()
		return fmt.Errorf("render service stale digest: %w", err)
	}

	subject := fmt.Sprintf("%d service ticket%s going quiet", total, pluralSuffix(total))
	if _, err := s.email.Mailer.Send(ctx, email.Message{
		From:    s.email.FromAddr,
		To:      s.email.StaffEmail,
		Subject: subject,
		HTML:    html,
		Text:    text,
		Tag:     "service-stale-digest",
	}); err != nil {
		s.recordEmailFailure()
		return fmt.Errorf("send service stale digest: %w", err)
	}
	if s.metrics != nil {
		s.metrics.EmailsSent.WithLabelValues("service_stale_digest", "sent").Inc()
	}

	// Best effort once the mail is out, for the same reason the QB alert does
	// it this way: failing here makes River retry and re-send a digest that
	// already landed, and a duplicate staff mail costs more than a missing
	// audit row.
	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		return s.audit.Record(ctx, tx, audit.AuditEntry{
			ActorType:    domain.AuditActorTypeSystem,
			ActorName:    "service_stale_sweep",
			Action:       audit.AuditServiceStaleSwept,
			ResourceType: "service_ticket",
			Metadata: map[string]any{
				"stale":  total,
				"listed": len(rows),
			},
		})
	}); err != nil {
		slog.ErrorContext(ctx, "service stale sweep: digest sent but audit failed",
			"stale", total, "error", err.Error())
	}
	return nil
}

// orderStaleForDigest picks and orders the tickets the digest will list.
//
// The input arrives quietest first (the store orders by last_contact_at), which
// is the right tiebreak but the wrong headline: a machine reported down
// outranks one merely limping, however long each has been quiet. A stable sort
// promotes the down tickets and leaves the quietest-first order intact inside
// each group.
//
// The caller's slice is left alone — it is still needed at full length for the
// total, and quietly reordering a caller's data is how a "showing 20 of 31"
// line ends up disagreeing with itself.
func orderStaleForDigest(stale []domain.ServiceTicket, limit int) []domain.ServiceTicket {
	ordered := make([]domain.ServiceTicket, len(stale))
	copy(ordered, stale)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].Severity == domain.ServiceSeverityDown &&
			ordered[j].Severity != domain.ServiceSeverityDown
	})
	if limit > 0 && len(ordered) > limit {
		ordered = ordered[:limit]
	}
	return ordered
}

// digestRows turns tickets into mail lines, resolving each customer's name.
//
// Bounded by staleDigestLimit, so the per-ticket customer lookup is fine. A
// customer that cannot be loaded leaves the name blank rather than failing the
// digest — the ticket number is what staff act on, and losing the whole mail
// over one deleted row would be a poor trade.
func (s *ServiceTicketService) digestRows(ctx context.Context, pool *pgxpool.Pool, tickets []domain.ServiceTicket, now time.Time) ([]emailtemplates.ServiceStaleDigestTicket, error) {
	rows := make([]emailtemplates.ServiceStaleDigestTicket, 0, len(tickets))

	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		for _, t := range tickets {
			name := ""
			if s.customers != nil {
				if c, err := s.customers.GetByID(ctx, tx, t.CustomerID); err == nil && c != nil {
					name = digestCustomerName(c)
				}
			}
			rows = append(rows, emailtemplates.ServiceStaleDigestTicket{
				Number:    t.Number,
				Title:     t.Title,
				Customer:  name,
				Severity:  string(t.Severity),
				Status:    t.Status.Label(),
				QuietDays: int(now.Sub(t.LastContactAt).Hours() / 24),
				URL:       s.email.BaseURL + "/admin/service/tickets/" + t.ID.String(),
				Down:      t.Severity == domain.ServiceSeverityDown,
			})
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("build service stale digest rows: %w", err)
	}
	return rows, nil
}

func (s *ServiceTicketService) recordEmailFailure() {
	if s.metrics != nil {
		s.metrics.EmailsSent.WithLabelValues("service_stale_digest", "failed").Inc()
	}
}

// digestCustomerName is who the ticket belongs to, in the order staff would say
// it: the shop's name first, then a person, then the address they were signed
// up with. Deliberately not reminderGreeting, whose "there" fallback is right
// for a salutation and useless in a list of tickets.
func digestCustomerName(c *domain.Customer) string {
	if c.CompanyName != nil && strings.TrimSpace(*c.CompanyName) != "" {
		return strings.TrimSpace(*c.CompanyName)
	}
	if name := strings.TrimSpace(c.FirstName + " " + c.LastName); name != "" {
		return name
	}
	return c.Email
}

// pluralSuffix is the "s" on a counted noun.
func pluralSuffix(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
