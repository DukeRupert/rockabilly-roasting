package app

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/emailtemplates"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/email"
	"github.com/dukerupert/hiri/internal/store"
)

// SendTicketOpenedNotice tells staff a wholesale customer has reported a
// problem from the portal.
//
// This is the mail the module exists for. A cafe whose machine dies at 6am
// rings somebody, or it doesn't and simply stops buying coffee; the portal
// turns that into a structured report, and this turns the report into something
// that lands in front of the crew without anybody refreshing a queue.
//
// Two phases, the RenewalService pattern: everything is read in one transaction,
// the send happens outside any transaction, and the audit row is written in a
// second one. Rendering an email while holding a transaction open is how a slow
// SMTP round trip becomes a database problem.
//
// Returns nil (not an error) when the module is off or STAFF_NOTIFICATION_EMAIL
// is unset. Neither is a fault worth retrying: the worker is registered on every
// instance, so a shop that does not service machines would otherwise accumulate
// jobs that can never succeed.
func (s *ServiceTicketService) SendTicketOpenedNotice(ctx context.Context, pool *pgxpool.Pool, ticketID uuid.UUID) error {
	if s.modules == nil || !s.modules.Enabled(domain.ModuleEquipmentService) {
		return nil
	}

	var (
		ticket   *domain.ServiceTicket
		customer *domain.Customer
		machine  *domain.Equipment
	)
	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		var txErr error
		ticket, txErr = s.GetByID(ctx, tx, ticketID)
		if txErr != nil {
			return txErr
		}
		if s.customers != nil {
			customer, txErr = s.customers.GetByID(ctx, tx, ticket.CustomerID)
			if txErr != nil {
				return txErr
			}
		}
		// A machine that has since been deleted must not lose the crew the
		// report — the ticket text is what they act on.
		if ticket.EquipmentID != nil {
			machine, _ = s.equipment.GetByID(ctx, tx, *ticket.EquipmentID)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("load service ticket %s for opened notice: %w", ticketID, err)
	}

	if s.email.Mailer == nil || s.email.StaffEmail == "" {
		slog.WarnContext(ctx, "service ticket opened: STAFF_NOTIFICATION_EMAIL unset, notice not emailed",
			"ticket", ticket.Number)
		return nil
	}

	data := emailtemplates.ServiceTicketOpenedData{
		Number:        ticket.Number,
		Title:         ticket.Title,
		Description:   ticket.Description,
		Severity:      string(ticket.Severity),
		SeverityLabel: ticket.Severity.Label(),
		Down:          ticket.Severity == domain.ServiceSeverityDown,
		ReportedAt:    ticket.CreatedAt,
		TicketURL:     s.email.BaseURL + "/admin/service/tickets/" + ticket.ID.String(),
		StoreName:     s.email.StoreName,
	}
	if customer != nil {
		data.Customer = digestCustomerName(customer)
		data.CustomerEmail = customer.Email
		data.Phone = customerPhone(customer)
	}
	if machine != nil {
		data.Machine = machine.Description()
		data.SerialNumber = machine.SerialNumber
	}

	html, text, err := s.email.Renderer.Render("service_ticket_opened", data)
	if err != nil {
		s.recordOpenedEmailFailure()
		return fmt.Errorf("render service ticket opened notice: %w", err)
	}

	// The subject is the triage: a crew scanning a phone at 6am has to be able
	// to tell "machine down" from "grinder sounds odd" without opening anything.
	subject := "Service report: " + ticket.Title
	if data.Down {
		subject = "MACHINE DOWN: " + ticket.Title
	}
	if data.Customer != "" {
		subject += " — " + data.Customer
	}

	if _, err := s.email.Mailer.Send(ctx, email.Message{
		From:    s.email.FromAddr,
		To:      s.email.StaffEmail,
		Subject: subject,
		HTML:    html,
		Text:    text,
		Tag:     "service-ticket-opened",
	}); err != nil {
		s.recordOpenedEmailFailure()
		return fmt.Errorf("send service ticket opened notice: %w", err)
	}
	if s.metrics != nil {
		s.metrics.EmailsSent.WithLabelValues("service_ticket_opened", "sent").Inc()
	}

	// Best effort once the mail is out, for the same reason the stale digest
	// does it this way: failing here makes River retry and re-send a notice that
	// already landed, and a duplicate staff mail costs less than a missing one.
	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		return s.audit.Record(ctx, tx, audit.AuditEntry{
			ActorType:    domain.AuditActorTypeSystem,
			ActorName:    "service_ticket_opened",
			Action:       audit.AuditServiceTicketNotified,
			ResourceType: "service_ticket",
			ResourceID:   ticket.ID,
			Metadata: map[string]any{
				"number":   ticket.Number,
				"severity": string(ticket.Severity),
				"to":       s.email.StaffEmail,
			},
		})
	}); err != nil {
		slog.ErrorContext(ctx, "service ticket opened: notice sent but audit failed",
			"ticket", ticket.Number, "error", err.Error())
	}
	return nil
}

func (s *ServiceTicketService) recordOpenedEmailFailure() {
	if s.metrics != nil {
		s.metrics.EmailsSent.WithLabelValues("service_ticket_opened", "failed").Inc()
	}
}

// customerPhone is the number to ring back on, blank when we never took one.
// The report form asks for the best time to reach them, which is worth nothing
// without this beside it.
func customerPhone(c *domain.Customer) string {
	if c.Phone == nil {
		return ""
	}
	return strings.TrimSpace(*c.Phone)
}
