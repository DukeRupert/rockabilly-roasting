package jobs

import (
	"context"
	"fmt"

	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/platform/media"
)

// R2ImageDeleteWorker deletes images from Cloudflare R2.
// Runs as a background job so DB deletion and R2 deletion are decoupled.
type R2ImageDeleteWorker struct {
	river.WorkerDefaults[R2ImageDeleteArgs]
	r2 *media.R2Client
}

// NewR2ImageDeleteWorker creates a new R2ImageDeleteWorker.
func NewR2ImageDeleteWorker(r2 *media.R2Client) *R2ImageDeleteWorker {
	return &R2ImageDeleteWorker{r2: r2}
}

// Work deletes the image from R2.
func (w *R2ImageDeleteWorker) Work(ctx context.Context, job *river.Job[R2ImageDeleteArgs]) error {
	if err := w.r2.DeleteObject(ctx, job.Args.R2Key); err != nil {
		return fmt.Errorf("r2 image delete %s: %w", job.Args.R2Key, err)
	}
	return nil
}
