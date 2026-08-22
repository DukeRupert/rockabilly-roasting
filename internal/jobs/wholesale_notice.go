package jobs

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/app"
)

// WholesaleNoticeArgs sends one staff-composed notice to one customer.
//
// Superseded by AnnouncementSendArgs: the composer behind this moved to
// Announcements, which reaches retail too and can schedule the send. Nothing
// enqueues this any more. The worker stays registered so any job queued before
// that switch still drains on deploy rather than dying on an unknown kind —
// safe to delete once no wholesale_notice rows remain in river_job.
type WholesaleNoticeArgs struct {
	CustomerID uuid.UUID `json:"customer_id"`
	Subject    string    `json:"subject"`
	Body       string    `json:"body"`
}

// Kind returns the job kind identifier.
func (WholesaleNoticeArgs) Kind() string { return "wholesale_notice" }

// WholesaleNoticeWorker delegates to WholesaleService.SendWholesaleNotice.
type WholesaleNoticeWorker struct {
	river.WorkerDefaults[WholesaleNoticeArgs]
	wholesale *app.WholesaleService
	pool      *pgxpool.Pool
}

// NewWholesaleNoticeWorker creates a new WholesaleNoticeWorker.
func NewWholesaleNoticeWorker(wholesale *app.WholesaleService, pool *pgxpool.Pool) *WholesaleNoticeWorker {
	return &WholesaleNoticeWorker{wholesale: wholesale, pool: pool}
}

// Work sends one wholesale notice.
func (w *WholesaleNoticeWorker) Work(ctx context.Context, job *river.Job[WholesaleNoticeArgs]) error {
	return w.wholesale.SendWholesaleNotice(ctx, w.pool, job.Args.CustomerID, app.NoticeParams{
		Subject: job.Args.Subject,
		Body:    job.Args.Body,
	})
}
