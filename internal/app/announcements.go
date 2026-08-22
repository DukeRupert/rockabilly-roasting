package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/emailtemplates"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/auth"
	"github.com/dukerupert/hiri/internal/platform/email"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
)

// AnnouncementService composes, schedules and sends staff announcements — the
// one-off notices that go to a whole customer audience ("Labor Day pushes
// Monday's shipment to Tuesday").
//
// It generalizes WholesaleService's one-off notice, which could only ever reach
// the weekly reminder list. The differences that matter: retail is reachable,
// the send can be scheduled and cancelled, and every message carries a working
// per-recipient opt-out.
type AnnouncementService struct {
	announcements *store.AnnouncementStore
	customers     *store.CustomerStore
	customerUsers *store.CustomerUserStore
	audit         *audit.AuditWriter
	metrics       *metrics.Registry
	enqueuer      JobEnqueuer
	email         EmailEnv // populated via WithEmail; required for the Send* methods
	// unsubscribe signs the opt-out link. Never nil — an unconfigured signer
	// reports Enabled() false and the link is omitted rather than printed dead.
	unsubscribe *auth.UnsubscribeSigner
}

// NewAnnouncementService creates a new AnnouncementService.
func NewAnnouncementService(
	announcements *store.AnnouncementStore,
	customers *store.CustomerStore,
	customerUsers *store.CustomerUserStore,
	auditWriter *audit.AuditWriter,
	metricsReg *metrics.Registry,
) *AnnouncementService {
	return &AnnouncementService{
		announcements: announcements,
		customers:     customers,
		customerUsers: customerUsers,
		audit:         auditWriter,
		metrics:       metricsReg,
		unsubscribe:   auth.NewUnsubscribeSigner(""),
	}
}

// WithJobEnqueuer attaches the job enqueuer. Separate from the constructor
// because the river client does not exist until after the services are built.
// Must be called before scheduling anything.
func (s *AnnouncementService) WithJobEnqueuer(enqueuer JobEnqueuer) *AnnouncementService {
	s.enqueuer = enqueuer
	return s
}

// WithEmail attaches the email-send environment. Must be called before any of
// the Send* methods.
func (s *AnnouncementService) WithEmail(env EmailEnv) *AnnouncementService {
	s.email = env
	return s
}

// WithUnsubscribeSigner attaches the signer used for opt-out links. Without it
// announcements still send, minus the link.
func (s *AnnouncementService) WithUnsubscribeSigner(signer *auth.UnsubscribeSigner) *AnnouncementService {
	if signer != nil {
		s.unsubscribe = signer
	}
	return s
}

// ScheduleGrace is how far in the past a requested send time may sit before it
// is rejected. Covers clock skew and the seconds between filling in the form
// and submitting it; anything older is a typo, not an intent to send now.
const ScheduleGrace = 5 * time.Minute

// ScheduleAnnouncementParams is a staff-composed notice awaiting a send time.
type ScheduleAnnouncementParams struct {
	Subject  string
	Body     string // plain text; blank lines separate paragraphs
	Audience domain.AnnouncementAudience
	// SendAt is when the notice should go out. Zero means "now" — the caller
	// does not have to invent a timestamp for the common case.
	SendAt time.Time
}

// ScheduleAnnouncement records a notice and queues its dispatch.
//
// The audience is deliberately NOT resolved here. Only the dispatcher resolves
// it, at send time, so an account suspended or opted out between composing and
// sending is honoured — the same reason the wholesale notice re-reads its list
// instead of trusting the form. For a send scheduled days out, that gap is the
// normal case, not the edge case.
func (s *AnnouncementService) ScheduleAnnouncement(ctx context.Context, tx pgx.Tx, actor Actor, p ScheduleAnnouncementParams) (*domain.Announcement, error) {
	subject := strings.TrimSpace(p.Subject)
	body := strings.TrimSpace(p.Body)
	if subject == "" || body == "" {
		return nil, ErrEmptyAnnouncement
	}
	if !p.Audience.Valid() {
		return nil, ErrInvalidAudience
	}

	sendAt := p.SendAt
	now := time.Now()
	if sendAt.IsZero() {
		sendAt = now
	} else if sendAt.Before(now.Add(-ScheduleGrace)) {
		return nil, ErrScheduleInPast
	}

	announcement, err := s.announcements.Create(ctx, tx, store.CreateAnnouncementParams{
		Subject:       subject,
		Body:          body,
		Audience:      p.Audience,
		ScheduledAt:   sendAt,
		CreatedBy:     actor.ID,
		CreatedByName: actor.Name,
	})
	if err != nil {
		return nil, err
	}

	// Enqueued in the caller's transaction, so a rollback takes the job with
	// it. There is no path where a row exists without its dispatcher, or a
	// dispatcher fires for a row that was never committed.
	if err := s.enqueuer.EnqueueAnnouncementDispatch(ctx, tx, announcement.ID, sendAt); err != nil {
		return nil, fmt.Errorf("enqueue announcement dispatch: %w", err)
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditAnnouncementScheduled,
		ResourceType: "announcement",
		ResourceID:   announcement.ID,
		Metadata: map[string]any{
			"subject":      subject,
			"audience":     string(p.Audience),
			"scheduled_at": sendAt.Format(time.RFC3339),
		},
	}); err != nil {
		return nil, fmt.Errorf("audit announcement scheduled: %w", err)
	}

	return announcement, nil
}

// CancelAnnouncement pulls a notice before it dispatches.
//
// The River job is left alone: it re-reads the row and finds nothing in
// 'scheduled' to claim, so it does nothing. Deleting the job instead would mean
// two systems that can disagree about whether mail is going out, and only one
// of them is transactional.
func (s *AnnouncementService) CancelAnnouncement(ctx context.Context, tx pgx.Tx, actor Actor, id uuid.UUID) error {
	announcement, err := s.announcements.GetByID(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrAnnouncementNotFound
		}
		return err
	}
	if !announcement.Cancellable() {
		return ErrAnnouncementNotCancellable
	}

	if err := s.announcements.Cancel(ctx, tx, id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Lost the race with the dispatcher between the read above and
			// here. Same answer as any other already-sending notice.
			return ErrAnnouncementNotCancellable
		}
		return err
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditAnnouncementCancelled,
		ResourceType: "announcement",
		ResourceID:   id,
		Metadata:     map[string]any{"subject": announcement.Subject},
	}); err != nil {
		return fmt.Errorf("audit announcement cancelled: %w", err)
	}
	return nil
}

// GetAnnouncement returns one notice.
func (s *AnnouncementService) GetAnnouncement(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Announcement, error) {
	announcement, err := s.announcements.GetByID(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrAnnouncementNotFound
		}
		return nil, err
	}
	return announcement, nil
}

// ListAnnouncements returns recent notices, newest scheduled first.
func (s *AnnouncementService) ListAnnouncements(ctx context.Context, tx pgx.Tx, limit int) ([]domain.Announcement, error) {
	return s.announcements.List(ctx, tx, limit)
}

// PreviewRecipients returns who an audience currently resolves to, for the
// compose screen's count and sample. Same query the dispatcher runs, so the
// number staff confirm against is the number that would actually be mailed
// right now.
func (s *AnnouncementService) PreviewRecipients(ctx context.Context, tx pgx.Tx, audience domain.AnnouncementAudience) ([]domain.AnnouncementRecipient, error) {
	if !audience.Valid() {
		return nil, ErrInvalidAudience
	}
	return s.announcements.ListAnnouncementRecipients(ctx, tx, audience)
}

// DispatchAnnouncement resolves the audience and fans out one send job per
// account.
//
// Everything happens in one transaction: claiming the row, enqueueing the
// sends, and marking it sent. River jobs may run more than once, and the claim
// is what makes a second run a no-op — but only if the claim and the fan-out
// commit together. Splitting them would allow a crash to leave a notice marked
// 'sending' with nothing queued behind it.
func (s *AnnouncementService) DispatchAnnouncement(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) error {
	return store.Tx(ctx, pool, func(tx pgx.Tx) error {
		announcement, err := s.announcements.ClaimForDispatch(ctx, tx, id)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// Cancelled, already dispatched, or deleted. Not an error:
				// returning one would make River retry a send staff called off.
				return nil
			}
			return err
		}

		recipients, err := s.announcements.ListAnnouncementRecipients(ctx, tx, announcement.Audience)
		if err != nil {
			return err
		}

		for _, r := range recipients {
			if err := s.enqueuer.EnqueueAnnouncementSend(ctx, tx, announcement.ID, r.CustomerID); err != nil {
				return fmt.Errorf("enqueue announcement send: %w", err)
			}
		}

		if err := s.announcements.MarkSent(ctx, tx, announcement.ID, len(recipients)); err != nil {
			return err
		}

		return s.audit.Record(ctx, tx, audit.AuditEntry{
			ActorType:    domain.AuditActorTypeSystem,
			ActorName:    "announcement_dispatch_worker",
			Action:       audit.AuditAnnouncementDispatched,
			ResourceType: "announcement",
			ResourceID:   announcement.ID,
			Metadata: map[string]any{
				"subject":    announcement.Subject,
				"audience":   string(announcement.Audience),
				"recipients": len(recipients),
			},
		})
	})
}

// SendAnnouncement emails one account a notice.
//
// External call outside a transaction per the house rule: read, send, then
// write the audit row in its own tx, so a Postmark failure never leaves a
// record claiming the mail went out.
func (s *AnnouncementService) SendAnnouncement(ctx context.Context, pool *pgxpool.Pool, announcementID, customerID uuid.UUID) error {
	var (
		announcement *domain.Announcement
		customer     *domain.Customer
		recipients   []MailRecipient
		eligible     bool
	)

	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		a, err := s.announcements.GetByID(ctx, tx, announcementID)
		if err != nil {
			return err
		}
		announcement = a

		c, err := s.customers.GetByID(ctx, tx, customerID)
		if err != nil {
			return fmt.Errorf("get customer %s: %w", customerID, err)
		}
		customer = c

		// Re-checked at send time, not trusted from the fan-out. The dispatcher
		// queues one job per account and River retries can push a send hours
		// past the scan, which is plenty of time for an account to be suspended
		// or for somebody to click unsubscribe.
		eligible, err = s.announcements.IsAnnouncementRecipient(ctx, tx, customerID, a.Audience)
		if err != nil {
			return err
		}
		if !eligible {
			return nil
		}

		recipients, err = s.announcementRecipients(ctx, tx, c)
		return err
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// The announcement or customer was deleted after the fan-out.
			// Nothing to send and nothing to retry.
			return nil
		}
		return err
	}

	// Dropping the job is correct here: skipping is not an error.
	if !eligible {
		return nil
	}

	paragraphs := emailtemplates.ParseAnnouncementBody(announcement.Body)

	// One message per address rather than a shared To: line — the opt-out link
	// and the RFC 8058 header are both per-recipient, so a single shared
	// message could only carry one token and whoever clicked would unsubscribe
	// somebody else.
	//
	// Every address is attempted even if one fails, so a single bad address
	// does not silence the rest of the account. Errors are joined and returned;
	// River retries the job, which re-sends to addresses that already got it.
	// For a one-off notice a rare duplicate beats a silent miss.
	var sendErrs []error
	for _, recipient := range recipients {
		if err := s.sendOne(ctx, announcement, customer, recipient, paragraphs); err != nil {
			s.metrics.EmailsSent.WithLabelValues("announcement", "failed").Inc()
			sendErrs = append(sendErrs, err)
			continue
		}
		s.metrics.EmailsSent.WithLabelValues("announcement", "sent").Inc()
	}
	if len(sendErrs) > 0 {
		return errors.Join(sendErrs...)
	}

	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		return s.audit.Record(ctx, tx, audit.AuditEntry{
			ActorType:    domain.AuditActorTypeSystem,
			ActorName:    "announcement_send_worker",
			Action:       audit.AuditEmailAnnouncementSent,
			ResourceType: "customer",
			ResourceID:   customer.ID,
			Metadata: map[string]any{
				"announcement_id": announcement.ID.String(),
				"subject":         announcement.Subject,
				// One audit row per account, not per message — the notice is an
				// account-level event. The count keeps it obvious afterwards how
				// many people were actually mailed.
				"recipients": len(recipients),
			},
		})
	}); err != nil {
		return fmt.Errorf("audit announcement sent: %w", err)
	}
	return nil
}

// SendAnnouncementTest sends a draft to one address without persisting
// anything. Composing a mailing to every customer and having no way to see it
// first is how typos reach thousands of inboxes; this is the cheap fix.
func (s *AnnouncementService) SendAnnouncementTest(ctx context.Context, pool *pgxpool.Pool, actor Actor, p ScheduleAnnouncementParams, to string) error {
	subject := strings.TrimSpace(p.Subject)
	body := strings.TrimSpace(p.Body)
	if subject == "" || body == "" {
		return ErrEmptyAnnouncement
	}
	to = strings.TrimSpace(to)
	if to == "" {
		return ErrEmptyAnnouncement
	}

	html, text, err := s.email.Renderer.Render("announcement", emailtemplates.AnnouncementData{
		Heading:    subject,
		Greeting:   actor.Name,
		Paragraphs: emailtemplates.ParseAnnouncementBody(body),
		// No opt-out link on a test. The token would be minted for whichever
		// staff address is being tested, and clicking it would silence a real
		// customer record that has nothing to do with the test.
		StoreName: s.email.StoreName,
		StoreURL:  s.email.BaseURL,
	})
	if err != nil {
		return fmt.Errorf("render announcement test: %w", err)
	}

	if _, err := s.email.Mailer.Send(ctx, email.Message{
		From: s.email.FromAddr,
		To:   to,
		// Prefixed so a test landing in a shared inbox is never mistaken for
		// the real mailing going out early.
		Subject: "[TEST] " + subject,
		HTML:    html,
		Text:    text,
		Tag:     "announcement-test",
	}); err != nil {
		return fmt.Errorf("send announcement test: %w", err)
	}

	return store.Tx(ctx, pool, func(tx pgx.Tx) error {
		return s.audit.Record(ctx, tx, audit.AuditEntry{
			ActorType:    actor.Type,
			ActorID:      actor.ID,
			ActorName:    actor.Name,
			Action:       audit.AuditEmailAnnouncementTestSent,
			ResourceType: "announcement",
			ResourceID:   uuid.Nil, // no row exists yet — a test never persists one
			Metadata:     map[string]any{"subject": subject, "to": to},
		})
	})
}

// SetAnnouncementsEnabled turns staff announcements on or off for one account
// contact.
func (s *AnnouncementService) SetAnnouncementsEnabled(ctx context.Context, tx pgx.Tx, actor Actor, customerID uuid.UUID, enabled bool) error {
	if err := s.customers.UpdateAnnouncementsEnabled(ctx, tx, customerID, enabled); err != nil {
		return err
	}

	action := audit.AuditCustomerAnnouncementsDisabled
	if enabled {
		action = audit.AuditCustomerAnnouncementsEnabled
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
		return fmt.Errorf("audit announcement preference: %w", err)
	}
	return nil
}

// SetAnnouncementsFromEmailLink applies a recipient's own opt-out (or undo)
// from the emailed link. The audit row records the customer as the actor so it
// is obvious afterwards who turned it off.
func (s *AnnouncementService) SetAnnouncementsFromEmailLink(ctx context.Context, tx pgx.Tx, target auth.UnsubscribeTarget, enabled bool) error {
	if target.Audience == auth.UnsubscribeAudienceAnnouncementCustomerUser {
		return s.setTeammateAnnouncements(ctx, tx, target.ID, enabled)
	}

	customer, err := s.customers.GetByID(ctx, tx, target.ID)
	if err != nil {
		return fmt.Errorf("get customer %s: %w", target.ID, err)
	}
	return s.SetAnnouncementsEnabled(ctx, tx, Actor{
		Type: domain.AuditActorTypeCustomer,
		ID:   &customer.ID,
		Name: customer.Email,
	}, customer.ID, enabled)
}

// setTeammateAnnouncements flips the flag for one invited login. The audit row
// is recorded against the ACCOUNT with the login in metadata, matching how
// every other customer_user event is filed, so an account's trail stays in one
// place.
func (s *AnnouncementService) setTeammateAnnouncements(ctx context.Context, tx pgx.Tx, userID uuid.UUID, enabled bool) error {
	user, err := s.customerUsers.GetByID(ctx, tx, userID)
	if err != nil {
		return fmt.Errorf("get customer user %s: %w", userID, err)
	}
	if err := s.customerUsers.UpdateAnnouncementsEnabled(ctx, tx, userID, enabled); err != nil {
		return err
	}

	action := audit.AuditCustomerAnnouncementsDisabled
	if enabled {
		action = audit.AuditCustomerAnnouncementsEnabled
	}
	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    domain.AuditActorTypeCustomer,
		ActorName:    user.Email,
		Action:       action,
		ResourceType: "customer",
		ResourceID:   user.CustomerID,
		Metadata: map[string]any{
			"enabled":          enabled,
			"customer_user_id": user.ID.String(),
		},
	}); err != nil {
		return fmt.Errorf("audit announcement preference: %w", err)
	}
	return nil
}

// GetAnnouncementsEnabled reports whether an account contact receives notices.
func (s *AnnouncementService) GetAnnouncementsEnabled(ctx context.Context, tx pgx.Tx, customerID uuid.UUID) (bool, error) {
	return s.customers.GetAnnouncementsEnabled(ctx, tx, customerID)
}

// sendOne renders and sends the notice to a single address.
func (s *AnnouncementService) sendOne(ctx context.Context, a *domain.Announcement, c *domain.Customer, recipient MailRecipient, paragraphs []emailtemplates.AnnouncementParagraph) error {
	// Empty when no signing secret is configured — the template then falls back
	// to "reply and we'll take you off the list" rather than printing a link
	// that could never be verified.
	var unsubscribeURL string
	if token := s.unsubscribe.Sign(recipient.Unsubscribe); token != "" {
		unsubscribeURL = s.email.BaseURL + "/unsubscribe?t=" + url.QueryEscape(token)
	}

	html, text, err := s.email.Renderer.Render("announcement", emailtemplates.AnnouncementData{
		Heading:        a.Subject,
		Greeting:       announcementGreeting(c),
		Paragraphs:     paragraphs,
		UnsubscribeURL: unsubscribeURL,
		StoreName:      s.email.StoreName,
		StoreURL:       s.email.BaseURL,
	})
	if err != nil {
		return fmt.Errorf("render announcement for %s: %w", recipient.Email, err)
	}

	if _, err := s.email.Mailer.Send(ctx, email.Message{
		From:    s.email.FromAddr,
		To:      recipient.Email,
		Subject: a.Subject,
		HTML:    html,
		Text:    text,
		Tag:     "announcement",
		Headers: reminderUnsubscribeHeaders(unsubscribeURL),
	}); err != nil {
		return fmt.Errorf("send announcement to %s: %w", recipient.Email, err)
	}
	return nil
}

// announcementRecipients returns every address on an account that should get
// this notice: the account contact plus any invited teammate who has not opted
// out of announcements.
//
// Deliberately not notificationRecipients: that governs transactional mail
// (order confirmations, the weekly reminder) and reads a different flag. A
// teammate may want shipping updates but not shop notices.
func (s *AnnouncementService) announcementRecipients(ctx context.Context, tx pgx.Tx, customer *domain.Customer) ([]MailRecipient, error) {
	extra, err := s.customerUsers.ListAnnouncementRecipients(ctx, tx, customer.ID)
	if err != nil {
		return nil, err
	}

	out := []MailRecipient{{
		Email: customer.Email,
		Unsubscribe: auth.UnsubscribeTarget{
			Audience: auth.UnsubscribeAudienceAnnouncementCustomer,
			ID:       customer.ID,
		},
	}}
	seen := map[string]struct{}{domain.NormalizeEmail(customer.Email): {}}
	for _, u := range extra {
		key := domain.NormalizeEmail(u.Email)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, MailRecipient{
			Email: u.Email,
			Unsubscribe: auth.UnsubscribeTarget{
				Audience: auth.UnsubscribeAudienceAnnouncementCustomerUser,
				ID:       u.ID,
			},
		})
	}
	return out, nil
}

// announcementGreeting picks the name to address a recipient by: the company
// for a wholesale account (what those customers recognize), the first name for
// retail. Empty when neither is known — the template then omits the greeting
// line entirely, which reads better than "Hi there,".
func announcementGreeting(c *domain.Customer) string {
	if c.AccountType == domain.AccountTypeWholesale && c.CompanyName != nil {
		if name := strings.TrimSpace(*c.CompanyName); name != "" {
			return name
		}
	}
	return strings.TrimSpace(c.FirstName)
}
