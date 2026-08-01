package jobs

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/app"
)

// WholesaleNoticeArgs sends one staff-composed notice to one customer. Staff
// compose once in the admin and the handler enqueues one job per recipient, so
// a bad address retries on its own instead of aborting the whole send.
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
