package web

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/jobs"
	"github.com/dukerupert/hiri/internal/store"
)

// handleAdminShipmentLabelCreate enqueues a BuyLabel job for one order. The
// job orchestrates the two-phase label purchase (read → provider → write).
// Staff are redirected back to the order page immediately — the new shipment
// row appears on the next render once the worker finishes (typically 1–3s).
//
// POST /admin/orders/{id}/label
// Optional form field: service_code (defaults to Shippo's usps_ground_advantage)
func (d *Deps) handleAdminShipmentLabelCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	orderID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if err := r.ParseForm(); err != nil {
		Error(w, r, err)
		return
	}
	serviceCode := strings.TrimSpace(r.FormValue("service_code"))

	actor := staffActor(r)
	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.RiverClient.InsertTx(ctx, tx, jobs.BuyLabelArgs{
			OrderID:     orderID,
			ServiceCode: serviceCode,
			ActorType:   string(actor.Type),
			ActorID:     actor.ID,
			ActorName:   actor.Name,
		}, nil)
		return txErr
	})
	if err != nil {
		slog.Error("admin label create: enqueue failed", "error", err, "order_id", orderID)
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/orders/"+orderID.String()+"?flash=Label+queued", http.StatusSeeOther)
}

// handleAdminShipmentBulkLabelCreate enqueues a BuyLabel job for each
// selected order. Used by the order-list multi-select toolbar.
//
// POST /admin/orders/labels
// Form: repeated order_id values, optional service_code
func (d *Deps) handleAdminShipmentBulkLabelCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := r.ParseForm(); err != nil {
		Error(w, r, err)
		return
	}

	// Accepts either repeated `order_id` (HTML-friendly) or a single
	// comma-separated `order_ids` field (matches the Alpine multi-select state).
	rawIDs := r.Form["order_id"]
	if csv := strings.TrimSpace(r.FormValue("order_ids")); csv != "" {
		rawIDs = append(rawIDs, strings.Split(csv, ",")...)
	}
	if len(rawIDs) == 0 {
		http.Redirect(w, r, "/admin/orders?flash=No+orders+selected", http.StatusSeeOther)
		return
	}

	orderIDs := make([]uuid.UUID, 0, len(rawIDs))
	seen := map[uuid.UUID]bool{}
	for _, raw := range rawIDs {
		id, parseErr := uuid.Parse(strings.TrimSpace(raw))
		if parseErr != nil {
			continue
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		orderIDs = append(orderIDs, id)
	}
	if len(orderIDs) == 0 {
		http.Redirect(w, r, "/admin/orders?flash=No+valid+order+IDs", http.StatusSeeOther)
		return
	}

	serviceCode := strings.TrimSpace(r.FormValue("service_code"))
	actor := staffActor(r)

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		for _, id := range orderIDs {
			if _, txErr := d.RiverClient.InsertTx(ctx, tx, jobs.BuyLabelArgs{
				OrderID:     id,
				ServiceCode: serviceCode,
				ActorType:   string(actor.Type),
				ActorID:     actor.ID,
				ActorName:   actor.Name,
			}, nil); txErr != nil {
				return txErr
			}
		}
		return nil
	})
	if err != nil {
		slog.Error("admin bulk label create: enqueue failed", "error", err, "count", len(orderIDs))
		Error(w, r, err)
		return
	}

	flash := "Queued+labels+for+" + strconv.Itoa(len(orderIDs)) + "+order(s)"
	http.Redirect(w, r, "/admin/orders?flash="+flash, http.StatusSeeOther)
}

// handleAdminShipmentLabelDownload generates a presigned R2 URL for the
// shipment's label and redirects the browser to it.
//
// GET /admin/shipments/{id}/label
func (d *Deps) handleAdminShipmentLabelDownload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	shipmentID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var r2Key *string
	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		r2Key, txErr = d.FulfillmentService.GetShipmentLabelKey(ctx, tx, shipmentID)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	if r2Key == nil || *r2Key == "" {
		http.NotFound(w, r)
		return
	}

	url, err := d.R2Client.PresignGetURL(ctx, *r2Key, 5*time.Minute)
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, url, http.StatusTemporaryRedirect)
}
