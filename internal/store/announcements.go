package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
)

// AnnouncementStore persists staff-composed customer notices and resolves the
// audience each one sends to.
type AnnouncementStore struct{}

// NewAnnouncementStore creates a new AnnouncementStore.
func NewAnnouncementStore() *AnnouncementStore { return &AnnouncementStore{} }

const announcementColumns = `id, subject, body, audience, status, scheduled_at, sent_at,
	                     recipient_count, created_by, created_by_name, created_at, updated_at`

// CreateAnnouncementParams is the input for a newly composed notice.
type CreateAnnouncementParams struct {
	Subject       string
	Body          string
	Audience      domain.AnnouncementAudience
	ScheduledAt   time.Time
	CreatedBy     *uuid.UUID
	CreatedByName string
}

// Create inserts a scheduled announcement.
func (s *AnnouncementStore) Create(ctx context.Context, tx pgx.Tx, p CreateAnnouncementParams) (*domain.Announcement, error) {
	row := tx.QueryRow(ctx,
		`INSERT INTO announcements (subject, body, audience, scheduled_at, created_by, created_by_name)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING `+announcementColumns,
		p.Subject, p.Body, string(p.Audience), p.ScheduledAt, p.CreatedBy, p.CreatedByName)

	a, err := scanAnnouncement(row)
	if err != nil {
		return nil, fmt.Errorf("create announcement: %w", err)
	}
	return a, nil
}

// GetByID returns one announcement. Staff-only — announcements have no
// customer-scoped view. Returns pgx.ErrNoRows when it does not exist.
func (s *AnnouncementStore) GetByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Announcement, error) {
	row := tx.QueryRow(ctx, `SELECT `+announcementColumns+` FROM announcements WHERE id = $1`, id)
	a, err := scanAnnouncement(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, err // mapped to app.ErrAnnouncementNotFound by the caller
		}
		return nil, fmt.Errorf("get announcement %s: %w", id, err)
	}
	return a, nil
}

// List returns announcements newest-scheduled first.
func (s *AnnouncementStore) List(ctx context.Context, tx pgx.Tx, limit int) ([]domain.Announcement, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := tx.Query(ctx,
		`SELECT `+announcementColumns+` FROM announcements ORDER BY scheduled_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list announcements: %w", err)
	}
	defer rows.Close()

	out := []domain.Announcement{}
	for rows.Next() {
		a, scanErr := scanAnnouncement(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan announcement: %w", scanErr)
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// ClaimForDispatch moves a scheduled announcement to 'sending' and returns it.
//
// The status check is inside the UPDATE, so the read and the transition happen
// in one statement: a cancel that lands between them loses the race rather than
// being silently overwritten, and a duplicate dispatch job (River retries, and
// jobs may run more than once) finds no scheduled row and does nothing. Returns
// pgx.ErrNoRows when the row is missing, already dispatched, or cancelled — all
// of which mean "do not send", which is the only thing the caller acts on.
func (s *AnnouncementStore) ClaimForDispatch(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Announcement, error) {
	row := tx.QueryRow(ctx,
		`UPDATE announcements
		 SET status = 'sending', updated_at = now()
		 WHERE id = $1 AND status = 'scheduled'
		 RETURNING `+announcementColumns, id)

	a, err := scanAnnouncement(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		return nil, fmt.Errorf("claim announcement %s: %w", id, err)
	}
	return a, nil
}

// MarkSent records the dispatch result: how many recipients were resolved and
// when the fan-out completed.
func (s *AnnouncementStore) MarkSent(ctx context.Context, tx pgx.Tx, id uuid.UUID, recipients int) error {
	_, err := tx.Exec(ctx,
		`UPDATE announcements
		 SET status = 'sent', sent_at = now(), recipient_count = $2, updated_at = now()
		 WHERE id = $1`, id, recipients)
	if err != nil {
		return fmt.Errorf("mark announcement %s sent: %w", id, err)
	}
	return nil
}

// Cancel pulls a scheduled announcement. Returns pgx.ErrNoRows when the row is
// gone or has already left the scheduled state — by then mail is going out and
// there is nothing to cancel.
func (s *AnnouncementStore) Cancel(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	tag, err := tx.Exec(ctx,
		`UPDATE announcements SET status = 'cancelled', updated_at = now()
		 WHERE id = $1 AND status = 'scheduled'`, id)
	if err != nil {
		return fmt.Errorf("cancel announcement %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// ListAnnouncementRecipients resolves an audience to accounts.
//
// Shared by the compose screen's estimate and the dispatcher's fan-out, so what
// staff are told they are about to mail is produced by the same query that
// actually mails it.
//
// Retail means "has actually bought": at least one non-cancelled, non-refunded
// retail order, or a live subscription. Accounts that registered and never
// ordered are excluded — an operational notice about a delayed shipment means
// nothing to them, and mailing addresses that have never transacted is how a
// sending domain's reputation gets spent for no return.
//
// Wholesale means approved accounts only. A pending applicant is not yet a
// customer and a suspended one has been deliberately cut off; neither should
// receive an account-wide mailing.
//
// Both sides require announcements_enabled — the opt-out flag the unsubscribe
// link in every announcement writes to.
func (s *AnnouncementStore) ListAnnouncementRecipients(ctx context.Context, tx pgx.Tx, audience domain.AnnouncementAudience) ([]domain.AnnouncementRecipient, error) {
	var where string
	switch audience {
	case domain.AnnouncementAudienceRetail:
		where = retailAudienceClause
	case domain.AnnouncementAudienceWholesale:
		where = wholesaleAudienceClause
	case domain.AnnouncementAudienceAll:
		where = "((" + retailAudienceClause + ") OR (" + wholesaleAudienceClause + "))"
	default:
		// Unreachable from the web layer, which validates first. Failing loudly
		// beats defaulting to "everyone" if a new audience is ever added and
		// this switch is not.
		return nil, fmt.Errorf("unknown announcement audience %q", audience)
	}

	rows, err := tx.Query(ctx,
		`SELECT c.id, c.email, c.company_name, c.first_name, c.last_name, c.account_type
		 FROM customers c
		 WHERE c.announcements_enabled AND `+where+`
		 ORDER BY c.account_type, c.company_name NULLS LAST, c.email`)
	if err != nil {
		return nil, fmt.Errorf("list announcement recipients: %w", err)
	}
	defer rows.Close()

	out := []domain.AnnouncementRecipient{}
	for rows.Next() {
		var r domain.AnnouncementRecipient
		if err := rows.Scan(&r.CustomerID, &r.Email, &r.CompanyName,
			&r.FirstName, &r.LastName, &r.AccountType); err != nil {
			return nil, fmt.Errorf("scan announcement recipient: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// The two halves of the audience predicate, kept as constants so the "all"
// case is literally the union of the other two and cannot drift from them.
const retailAudienceClause = `c.account_type = 'retail' AND (
	EXISTS (SELECT 1 FROM orders o
	        WHERE o.customer_id = c.id
	          AND o.status NOT IN ('cancelled', 'refunded'))
	OR EXISTS (SELECT 1 FROM subscriptions s
	           WHERE s.customer_id = c.id
	             AND s.status IN ('active', 'paused', 'past_due')))`

const wholesaleAudienceClause = `c.account_type = 'wholesale' AND c.wholesale_status = 'approved'`

// IsAnnouncementRecipient re-checks one account's eligibility at send time.
//
// The dispatcher fans out one job per recipient, so an account can be
// suspended, or opt out, in the gap between the fan-out and the send — and
// River retries can widen that gap to hours. Every other send path in this
// codebase re-checks before mailing; this is that check for announcements.
func (s *AnnouncementStore) IsAnnouncementRecipient(ctx context.Context, tx pgx.Tx, customerID uuid.UUID, audience domain.AnnouncementAudience) (bool, error) {
	var where string
	switch audience {
	case domain.AnnouncementAudienceRetail:
		where = retailAudienceClause
	case domain.AnnouncementAudienceWholesale:
		where = wholesaleAudienceClause
	case domain.AnnouncementAudienceAll:
		where = "((" + retailAudienceClause + ") OR (" + wholesaleAudienceClause + "))"
	default:
		return false, fmt.Errorf("unknown announcement audience %q", audience)
	}

	var ok bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM customers c
		                WHERE c.id = $1 AND c.announcements_enabled AND `+where+`)`,
		customerID).Scan(&ok); err != nil {
		return false, fmt.Errorf("check announcement recipient %s: %w", customerID, err)
	}
	return ok, nil
}

// rowScanner is satisfied by both pgx.Row and pgx.Rows, so one scan helper
// serves the single-row and list queries.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanAnnouncement(row rowScanner) (*domain.Announcement, error) {
	var (
		a        domain.Announcement
		audience string
		status   string
	)
	if err := row.Scan(&a.ID, &a.Subject, &a.Body, &audience, &status,
		&a.ScheduledAt, &a.SentAt, &a.RecipientCount,
		&a.CreatedBy, &a.CreatedByName, &a.CreatedAt, &a.UpdatedAt); err != nil {
		return nil, err
	}
	a.Audience = domain.AnnouncementAudience(audience)
	a.Status = domain.AnnouncementStatus(status)
	return &a, nil
}
