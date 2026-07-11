package web

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/jobs"
	"github.com/dukerupert/hiri/internal/platform/logging"
	"github.com/dukerupert/hiri/internal/platform/quickbooks"
	"github.com/dukerupert/hiri/internal/store"
)

const maxQBWebhookBodyBytes = 1 << 20 // 1MB

// qbWebhookOps are the Invoice webhook operations that trigger a reconcile.
// Create is deliberately absent: for invoices we cut ourselves it races the
// transaction that persists qb_invoice_id (the reconcile job would find no
// order and permanently skip), and the send-status Update that follows moments
// later covers the fast pending_invoice → invoiced flip anyway. Emailed and
// Merge are ignored — neither changes payment state.
var qbWebhookOps = map[string]bool{
	"Update": true,
	"Void":   true,
	"Delete": true,
}

// handleQBWebhook receives QuickBooks Online webhook events, verifies the
// HMAC signature, and enqueues River jobs for invoice updates.
// Always returns 200 quickly — QB retries on non-2xx.
func (d *Deps) handleQBWebhook(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := logging.FromContext(ctx)

	body, err := io.ReadAll(io.LimitReader(r.Body, maxQBWebhookBodyBytes))
	if err != nil {
		logger.Error("qb webhook: read body", "error", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Verify HMAC signature
	sig := r.Header.Get("intuit-signature")
	if !quickbooks.VerifySignature(sig, body, d.QBWebhookVerifierToken) {
		logger.Warn("qb webhook: invalid signature")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	// Always return 200 quickly — QB retries on non-2xx
	w.WriteHeader(http.StatusOK)

	// Parse payload
	var payload quickbooks.WebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		logger.Error("qb webhook: unmarshal failed", "error", err)
		return
	}

	// Enqueue a River job for each invoice change. Beyond Update (payments
	// applied), Void/Delete matter — the reconcile reverts the order to
	// pending_invoice so a fresh invoice can be cut — and without them a void
	// waits up to a day for the reconcile poll.
	for _, notification := range payload.EventNotifications {
		for _, entity := range notification.DataChangeEvent.Entities {
			if entity.Name == "Invoice" && qbWebhookOps[entity.Operation] {
				logger.Info("qb webhook: invoice event",
					"qb_invoice_id", entity.ID,
					"operation", entity.Operation,
					"realm_id", notification.RealmID,
				)
				_ = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
					_, txErr := d.RiverClient.InsertTx(ctx, tx, jobs.ProcessQBInvoiceUpdateArgs{
						QBInvoiceID: entity.ID,
						RealmID:     notification.RealmID,
					}, nil)
					return txErr
				})
			}
		}
	}
}
