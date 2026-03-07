package jobs

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/platform/media"
	"github.com/dukerupert/hiri/internal/store"
)

// StoreLabelToR2Worker fetches a shipping label from the provider's URL
// and uploads it to Cloudflare R2 for permanent storage.
type StoreLabelToR2Worker struct {
	river.WorkerDefaults[StoreLabelToR2Args]
	fulfillmentSvc *app.FulfillmentService
	pool           *pgxpool.Pool
	r2             *media.R2Client
	httpClient     *http.Client
}

// NewStoreLabelToR2Worker creates a new StoreLabelToR2Worker.
func NewStoreLabelToR2Worker(
	fulfillmentSvc *app.FulfillmentService,
	pool *pgxpool.Pool,
	r2 *media.R2Client,
) *StoreLabelToR2Worker {
	return &StoreLabelToR2Worker{
		fulfillmentSvc: fulfillmentSvc,
		pool:           pool,
		r2:             r2,
		httpClient:     &http.Client{},
	}
}

// Work fetches the label from the provider URL and uploads it to R2.
func (w *StoreLabelToR2Worker) Work(ctx context.Context, job *river.Job[StoreLabelToR2Args]) error {
	args := job.Args

	// Fetch label bytes from provider URL (external call, outside tx).
	resp, err := w.httpClient.Get(args.LabelURL)
	if err != nil {
		return fmt.Errorf("fetch label from %s: %w", args.LabelURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch label: status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read label body: %w", err)
	}

	// Determine format from URL or content-type.
	format := detectLabelFormat(args.LabelURL, resp.Header.Get("Content-Type"))
	contentType := "application/pdf"
	if format == "png" {
		contentType = "image/png"
	}

	// Build R2 key: labels/<shipment_id>.<format>
	r2Key := fmt.Sprintf("labels/%s.%s", args.ShipmentID, format)

	// Upload to R2 (external call, outside tx).
	if err := w.r2.PutObject(ctx, r2Key, body, contentType); err != nil {
		return fmt.Errorf("upload label to r2: %w", err)
	}

	// Update shipment record with R2 key.
	return store.Tx(ctx, w.pool, func(tx pgx.Tx) error {
		return w.fulfillmentSvc.UpdateShipmentLabel(ctx, tx, args.ShipmentID, r2Key, format)
	})
}

// detectLabelFormat infers label format from URL extension or content-type.
func detectLabelFormat(labelURL, contentType string) string {
	ext := strings.ToLower(path.Ext(labelURL))
	switch ext {
	case ".png":
		return "png"
	case ".pdf":
		return "pdf"
	}
	if strings.Contains(contentType, "image/png") {
		return "png"
	}
	return "pdf"
}
