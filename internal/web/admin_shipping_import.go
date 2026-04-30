package web

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/platform/pirateship"
	"github.com/dukerupert/hiri/internal/ui/admin"
)

// maxTrackingUploadBytes caps the size of an uploaded Pirate Ship tracking
// CSV. Pirate Ship's exports are at most ~100 KB even for big batches; the
// 10 MB ceiling here is paranoia, not a real limit on legitimate workloads.
const maxTrackingUploadBytes = 10 * 1024 * 1024

// handleAdminOrdersImportTracking receives a Pirate Ship CSV tracking export
// and applies it row-by-row. Each row runs in its own transaction — a bad
// row reports as an error and the rest still attempt — so partial success
// is the norm. Re-uploading the same file is a no-op because every order
// has already moved past `unfulfilled`.
//
// POST /admin/orders/import-tracking
//
// TODO: when project-wide role gating lands (PermUpdateFulfillment), wire
// it here alongside the matching export endpoint.
func (d *Deps) handleAdminOrdersImportTracking(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	r.Body = http.MaxBytesReader(w, r.Body, maxTrackingUploadBytes)
	if err := r.ParseMultipartForm(maxTrackingUploadBytes); err != nil {
		Error(w, r, err)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		Error(w, r, err)
		return
	}
	defer file.Close()

	rows, err := pirateship.Decode(file)
	if err != nil {
		// A missing Order ID column is the only hard fail — the whole upload
		// is rejected before any writes. Render the summary with a single
		// error row so the admin sees what happened.
		summary := admin.PirateShipImportSummaryProps{
			Errored: []app.ImportResult{{
				LineNumber: 0,
				Reason:     fileErrorReason(header.Filename, err),
			}},
		}
		w.WriteHeader(http.StatusBadRequest)
		admin.PirateShipImportSummary(summary).Render(ctx, w) //nolint:errcheck
		return
	}

	actor := staffActor(r)
	props := admin.PirateShipImportSummaryProps{}
	for _, row := range rows {
		result := d.ShippingImportService.RecordPirateShipTracking(ctx, row, actor)
		switch result.Outcome {
		case app.ImportOutcomeRecorded:
			props.Recorded = append(props.Recorded, result)
		case app.ImportOutcomeSkipped:
			props.Skipped = append(props.Skipped, result)
		case app.ImportOutcomeError:
			slog.Error("admin: pirate ship tracking import row",
				"order_number", result.OrderNumber,
				"line", result.LineNumber,
				"reason", result.Reason,
			)
			props.Errored = append(props.Errored, result)
		}
	}

	admin.PirateShipImportSummary(props).Render(ctx, w) //nolint:errcheck
}

// fileErrorReason builds a friendly explanation for a Decode failure that
// rejects the whole upload. The filename is included so a user uploading
// the wrong artifact can spot the mismatch in the summary.
func fileErrorReason(filename string, err error) string {
	if errors.Is(err, pirateship.ErrMissingOrderIDColumn) {
		if filename != "" {
			return filename + ": missing required \"Order ID\" column"
		}
		return "missing required \"Order ID\" column"
	}
	return err.Error()
}
