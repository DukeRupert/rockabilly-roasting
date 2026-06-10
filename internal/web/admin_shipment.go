package web

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/jobs"
	"github.com/dukerupert/hiri/internal/platform/shipping"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/admin"
)

// handleAdminShipmentRates fetches the purchasable shipping rates for an order
// and renders the rate-chooser panel (cheapest pre-selected). Triggered by the
// "Get shipping rates" button via htmx; the panel is swapped into
// #label-rates-panel on the order page.
//
// GET /admin/orders/{id}/rates
func (d *Deps) handleAdminShipmentRates(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	orderID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	props, err := d.labelRatesProps(ctx, orderID, "")
	if err != nil {
		slog.Error("admin label rates: fetch failed", "error", err, "order_id", orderID)
		props.Error = labelRatesErrorMessage(err)
	}
	admin.LabelRatesPanel(props).Render(ctx, w) //nolint:errcheck
}

// handleAdminShipmentLabelBuy purchases the rate the operator selected and
// persists the shipment. Synchronous (the operator is choosing a specific
// rate and needs immediate confirmation): prepare the request in a read tx,
// buy the rate outside any tx, then persist + enqueue the R2 label sync.
//
// On a buy failure — usually an expired rate — the rates are re-fetched and the
// panel is re-rendered with a "rates refreshed, confirm again" notice rather
// than erroring out. On success it returns an HX-Redirect to the order page.
//
// POST /admin/orders/{id}/label
// Form field: rate (base64-encoded rate snapshot, see admin.DecodeRateOption)
func (d *Deps) handleAdminShipmentLabelBuy(w http.ResponseWriter, r *http.Request) {
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

	view, err := admin.DecodeRateOption(strings.TrimSpace(r.FormValue("rate")))
	if err != nil {
		props, ferr := d.labelRatesProps(ctx, orderID, "")
		if ferr != nil {
			props = admin.LabelRatesProps{OrderID: orderID}
		}
		props.Error = "Select a shipping option, then buy."
		admin.LabelRatesPanel(props).Render(ctx, w) //nolint:errcheck
		return
	}

	actor := staffActor(r)

	// Phase 1: read tx — re-derive box/weight/addresses for the shipment row.
	// The service code is irrelevant here; the chosen rate drives the purchase.
	var req shipping.LabelRequest
	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		req, txErr = d.FulfillmentService.PrepareLabelRequest(ctx, tx, orderID, "")
		return txErr
	})
	if err != nil {
		slog.Error("admin label buy: prep failed", "error", err, "order_id", orderID)
		admin.LabelRatesPanel(admin.LabelRatesProps{
			OrderID: orderID,
			Error:   labelRatesErrorMessage(err),
		}).Render(ctx, w) //nolint:errcheck
		return
	}

	// Phase 2: external buy — no tx held.
	rate := shipping.Rate{
		RateID:          view.RateID,
		ShipmentID:      view.ShipmentID,
		CarrierName:     view.CarrierName,
		ServiceName:     view.ServiceName,
		ServiceToken:    view.ServiceToken,
		AmountCents:     view.AmountCents,
		Currency:        view.Currency,
		EstDeliveryDays: view.EstDeliveryDays,
	}
	result, err := d.FulfillmentService.BuyLabelRate(ctx, rate)
	if err != nil {
		// A buy failure at this point is almost always an expired rate. Re-fetch
		// and let the operator re-confirm against fresh prices.
		slog.Warn("admin label buy: purchase failed, refreshing rates", "error", err, "order_id", orderID)
		props, ferr := d.labelRatesProps(ctx, orderID, "Those rates expired. Refreshed below — pick one and buy again.")
		if ferr != nil {
			props = admin.LabelRatesProps{OrderID: orderID, Error: labelRatesErrorMessage(ferr)}
		}
		admin.LabelRatesPanel(props).Render(ctx, w) //nolint:errcheck
		return
	}

	// Phase 3: write tx — persist shipment + audit + enqueue R2 label sync.
	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		shipment, txErr := d.FulfillmentService.PersistShipmentLabel(ctx, tx, orderID, req, *result, actor)
		if txErr != nil {
			return txErr
		}
		labelURL := ""
		if shipment.LabelURL != nil {
			labelURL = *shipment.LabelURL
		}
		_, txErr = d.RiverClient.InsertTx(ctx, tx, jobs.StoreLabelToR2Args{
			ShipmentID: shipment.ID,
			LabelURL:   labelURL,
		}, nil)
		return txErr
	})
	if err != nil {
		slog.Error("admin label buy: persist failed", "error", err, "order_id", orderID)
		Error(w, r, err)
		return
	}

	redirectTo := "/admin/orders/" + orderID.String() + "?flash=Label+purchased"
	if IsHTMX(r) {
		w.Header().Set("HX-Redirect", redirectTo)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, redirectTo, http.StatusSeeOther)
}

// labelRatesProps prepares a label request and fetches the rates for it,
// mapping the provider rates into the UI view model. Returns the partially
// populated props (with OrderID set) alongside any error so callers can render
// an error panel.
func (d *Deps) labelRatesProps(ctx context.Context, orderID uuid.UUID, notice string) (admin.LabelRatesProps, error) {
	var req shipping.LabelRequest
	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		req, txErr = d.FulfillmentService.PrepareLabelRequest(ctx, tx, orderID, "")
		return txErr
	})
	if err != nil {
		return admin.LabelRatesProps{OrderID: orderID}, err
	}

	rates, err := d.FulfillmentService.FetchRates(ctx, req)
	if err != nil {
		return admin.LabelRatesProps{OrderID: orderID}, err
	}

	views := make([]admin.LabelRateView, 0, len(rates))
	for _, rt := range rates {
		views = append(views, admin.LabelRateView{
			RateID:          rt.RateID,
			ShipmentID:      rt.ShipmentID,
			CarrierName:     rt.CarrierName,
			ServiceName:     rt.ServiceName,
			ServiceToken:    rt.ServiceToken,
			AmountCents:     rt.AmountCents,
			Currency:        rt.Currency,
			EstDeliveryDays: rt.EstDeliveryDays,
		})
	}
	return admin.LabelRatesProps{OrderID: orderID, Rates: views, Notice: notice}, nil
}

// labelRatesErrorMessage maps the common PrepareLabelRequest / rate-fetch
// failures to short operator-facing copy. Mirrors the deterministic cases in
// admin.labelFailureDetail; unknown errors fall back to a generic line so raw
// internals don't leak into the panel.
func labelRatesErrorMessage(err error) string {
	switch {
	case errors.Is(err, app.ErrNoBoxPreset):
		return "No box preset configured. Add one in Settings → Box presets."
	case errors.Is(err, app.ErrShipmentNoPhysicalItems):
		return "Order has no shippable items."
	case errors.Is(err, app.ErrShipmentWeightUnknown):
		return "A variant in this order is missing its weight."
	}
	low := strings.ToLower(err.Error())
	switch {
	case strings.Contains(low, "no rates returned"):
		return "Shippo returned no rates for this address."
	case strings.Contains(low, "address"):
		return "Address rejected by the carrier. Verify the ship-to address and try again."
	case strings.Contains(low, "seller info missing"), strings.Contains(low, "email or phone"):
		return "Origin email/phone missing — fill them in under Settings → Shipping."
	}
	return "Couldn't fetch shipping rates. Try again in a moment."
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
