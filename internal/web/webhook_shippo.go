package web

import (
	"crypto/subtle"
	"io"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/jobs"
	"github.com/dukerupert/hiri/internal/platform/logging"
	"github.com/dukerupert/hiri/internal/platform/shipping"
	"github.com/dukerupert/hiri/internal/store"
)

const maxShippoWebhookBodyBytes = 1 << 20 // 1MB

// handleShippoWebhook receives Shippo track_updated webhooks and enqueues a
// River job to advance the matching shipment's status.
//
// Shippo does not sign its webhooks, so the endpoint authenticates via an
// unguessable token embedded in the URL path (POST /webhooks/shippo/{token}),
// compared in constant time against SHIPPO_WEBHOOK_SECRET. The path is redacted
// in request logs (see loggingMiddleware) so the secret doesn't leak there.
//
// Returns 200 quickly — Shippo retries on non-2xx. Per Shippo's docs tracking
// webhooks are not idempotent (duplicates and out-of-order deliveries happen);
// the downstream job applies status forward-only so that's safe.
func (d *Deps) handleShippoWebhook(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := logging.FromContext(ctx)

	// No secret configured → the integration is off. 404 rather than reveal
	// that the route exists.
	if d.ShippoWebhookSecret == "" {
		http.NotFound(w, r)
		return
	}
	token := r.PathValue("token")
	if subtle.ConstantTimeCompare([]byte(token), []byte(d.ShippoWebhookSecret)) != 1 {
		logger.Warn("shippo webhook: invalid token")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxShippoWebhookBodyBytes))
	if err != nil {
		logger.Error("shippo webhook: read body", "error", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Acknowledge before doing work — Shippo retries on non-2xx.
	w.WriteHeader(http.StatusOK)

	evt, err := shipping.ParseShippoTrackingWebhook(body)
	if err != nil {
		logger.Error("shippo webhook: parse", "error", err)
		return
	}

	// We only act on tracking updates. Other event types (e.g. transaction_*)
	// are acknowledged and ignored. An empty event with tracking data is also
	// accepted, since Shippo test payloads omit the wrapper.
	if evt.Event != "" && evt.Event != "track_updated" {
		return
	}
	if evt.Data.TrackingNumber == "" || evt.Data.TrackingStatus.Status == "" {
		logger.Warn("shippo webhook: missing tracking number or status", "event", evt.Event)
		return
	}

	if err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.RiverClient.InsertTx(ctx, tx, jobs.ShippoTrackingUpdateArgs{
			TrackingNumber: evt.Data.TrackingNumber,
			Status:         evt.Data.TrackingStatus.Status,
		}, nil)
		return txErr
	}); err != nil {
		logger.Error("shippo webhook: enqueue tracking update",
			"error", err, "tracking_number", evt.Data.TrackingNumber)
	}
}
