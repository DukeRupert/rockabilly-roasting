package jobs

import (
	"context"
	"fmt"

	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/platform/media"
)

// CFImageDeleteWorker deletes images from Cloudflare Images.
// Runs as a background job so DB deletion and CF deletion are decoupled.
type CFImageDeleteWorker struct {
	river.WorkerDefaults[CFImageDeleteArgs]
	cfImages *media.CFImagesClient
}

// NewCFImageDeleteWorker creates a new CFImageDeleteWorker.
func NewCFImageDeleteWorker(cfImages *media.CFImagesClient) *CFImageDeleteWorker {
	return &CFImageDeleteWorker{cfImages: cfImages}
}

// Work deletes the image from Cloudflare Images.
func (w *CFImageDeleteWorker) Work(ctx context.Context, job *river.Job[CFImageDeleteArgs]) error {
	if err := w.cfImages.Delete(ctx, job.Args.CFImageID); err != nil {
		return fmt.Errorf("cf image delete %s: %w", job.Args.CFImageID, err)
	}
	return nil
}
