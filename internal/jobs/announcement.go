package jobs

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/app"
)

// AnnouncementDispatchArgs resolves one announcement's audience at its
// scheduled time and fans out a send job per account.
//
// The two-stage shape (dispatch, then one send job per recipient) is what makes
// a scheduled mailing safe: the audience is decided when the mail actually goes
// out rather than when staff composed it, and a single bad address retries on
// its own instead of aborting the whole send.
type AnnouncementDispatchArgs struct {
	AnnouncementID uuid.UUID `json:"announcement_id"`
}

// Kind returns the job kind identifier.
func (AnnouncementDispatchArgs) Kind() string { return "announcement_dispatch" }

// AnnouncementDispatchWorker delegates to AnnouncementService.
type AnnouncementDispatchWorker struct {
	river.WorkerDefaults[AnnouncementDispatchArgs]
	announcements *app.AnnouncementService
	pool          *pgxpool.Pool
}

// NewAnnouncementDispatchWorker creates a new AnnouncementDispatchWorker.
func NewAnnouncementDispatchWorker(announcements *app.AnnouncementService, pool *pgxpool.Pool) *AnnouncementDispatchWorker {
	return &AnnouncementDispatchWorker{announcements: announcements, pool: pool}
}

// Work fans out one announcement. Idempotent: the service claims the row by
// flipping it out of 'scheduled' in the same statement it reads it, so a second
// run finds nothing to claim and does nothing.
func (w *AnnouncementDispatchWorker) Work(ctx context.Context, job *river.Job[AnnouncementDispatchArgs]) error {
	return w.announcements.DispatchAnnouncement(ctx, w.pool, job.Args.AnnouncementID)
}

// AnnouncementSendArgs sends one announcement to one account.
type AnnouncementSendArgs struct {
	AnnouncementID uuid.UUID `json:"announcement_id"`
	CustomerID     uuid.UUID `json:"customer_id"`
}

// Kind returns the job kind identifier.
func (AnnouncementSendArgs) Kind() string { return "announcement_send" }

// AnnouncementSendWorker delegates to AnnouncementService.
type AnnouncementSendWorker struct {
	river.WorkerDefaults[AnnouncementSendArgs]
	announcements *app.AnnouncementService
	pool          *pgxpool.Pool
}

// NewAnnouncementSendWorker creates a new AnnouncementSendWorker.
func NewAnnouncementSendWorker(announcements *app.AnnouncementService, pool *pgxpool.Pool) *AnnouncementSendWorker {
	return &AnnouncementSendWorker{announcements: announcements, pool: pool}
}

// Work sends one announcement to one account. The service re-checks
// eligibility before mailing, so a job for an account suspended or opted out
// since the fan-out is dropped rather than sent.
func (w *AnnouncementSendWorker) Work(ctx context.Context, job *river.Job[AnnouncementSendArgs]) error {
	return w.announcements.SendAnnouncement(ctx, w.pool, job.Args.AnnouncementID, job.Args.CustomerID)
}
