package web

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/store"
)

// maxSkippedNumbersInHeader caps how many skipped order numbers we surface
// inline. Above the cap we set X-Hiri-Export-Skipped-Truncated so the UI
// knows to send the user to a separate detail view (which we'll build later
// if real volume warrants it).
const maxSkippedNumbersInHeader = 50

// handleAdminOrdersExportCSV streams a Pirate-Ship-compatible CSV of orders
// that are ready to ship: paid (captured or authorized) + unfulfilled.
//
// Optional query params:
//   - ids — comma-separated order UUIDs (overrides the status filter)
//
// Skipped orders (e.g. missing variant weight) are surfaced via response
// headers so the admin button can toast a warning without a second request.
//
// TODO: when project-wide role gating lands (PermUpdateFulfillment), wire it
// here alongside the other fulfillment endpoints.
func (d *Deps) handleAdminOrdersExportCSV(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	ids, err := parseOrderIDList(r.URL.Query().Get("ids"))
	if err != nil {
		http.Error(w, "invalid ids parameter", http.StatusBadRequest)
		return
	}

	var (
		csvBytes []byte
		skipped  []app.SkippedOrder
	)
	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		csvBytes, skipped, txErr = d.ShippingExportService.BuildPirateShipCSV(ctx, tx, ids)
		return txErr
	})
	if err != nil {
		slog.Error("admin: pirate ship export", "error", err)
		Error(w, r, err)
		return
	}

	filename := "hiri-orders-" + time.Now().In(d.MerchantTZ).Format("2006-01-02-1504") + ".csv"
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	writeSkippedHeaders(w, skipped)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(csvBytes)
}

// parseOrderIDList parses an optional `ids=a,b,c` query parameter into a
// slice of UUIDs. Empty input returns (nil, nil) — the caller falls back to
// the status filter.
func parseOrderIDList(raw string) ([]uuid.UUID, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	out := make([]uuid.UUID, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		id, err := uuid.Parse(p)
		if err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, nil
}

// writeSkippedHeaders emits the export-summary headers the admin UI reads.
// Header values are ASCII-safe (order numbers are digits + letters + dashes
// in our schema), so no encoding gymnastics needed.
func writeSkippedHeaders(w http.ResponseWriter, skipped []app.SkippedOrder) {
	if len(skipped) == 0 {
		w.Header().Set("X-Hiri-Export-Skipped-Count", "0")
		return
	}
	w.Header().Set("X-Hiri-Export-Skipped-Count", strconv.Itoa(len(skipped)))

	numbers := skipped
	truncated := false
	if len(numbers) > maxSkippedNumbersInHeader {
		numbers = numbers[:maxSkippedNumbersInHeader]
		truncated = true
	}
	parts := make([]string, len(numbers))
	for i, s := range numbers {
		parts[i] = s.Number
	}
	w.Header().Set("X-Hiri-Export-Skipped-Order-Numbers", strings.Join(parts, ","))
	if truncated {
		w.Header().Set("X-Hiri-Export-Skipped-Truncated", "true")
	}
}

