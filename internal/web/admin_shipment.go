package web

import (
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/jobs"
	"github.com/dukerupert/hiri/internal/platform/shipping"
	"github.com/dukerupert/hiri/internal/store"
)

// handleAdminShipmentLabelCreate creates a shipping label via the external
// provider and enqueues a job to store it in R2.
//
// POST /admin/orders/{id}/label
// Form: weight_oz, length_in, width_in, height_in, service_code,
//       from_name, from_street1, from_city, from_state, from_zip, from_country,
//       to_name, to_street1, to_city, to_state, to_zip, to_country
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

	weightOz, _ := strconv.ParseFloat(r.FormValue("weight_oz"), 64)
	lengthIn, _ := strconv.ParseFloat(r.FormValue("length_in"), 64)
	widthIn, _ := strconv.ParseFloat(r.FormValue("width_in"), 64)
	heightIn, _ := strconv.ParseFloat(r.FormValue("height_in"), 64)

	req := shipping.LabelRequest{
		FromName:    r.FormValue("from_name"),
		FromStreet1: r.FormValue("from_street1"),
		FromCity:    r.FormValue("from_city"),
		FromState:   r.FormValue("from_state"),
		FromZip:     r.FormValue("from_zip"),
		FromCountry: r.FormValue("from_country"),
		ToName:      r.FormValue("to_name"),
		ToStreet1:   r.FormValue("to_street1"),
		ToCity:      r.FormValue("to_city"),
		ToState:     r.FormValue("to_state"),
		ToZip:       r.FormValue("to_zip"),
		ToCountry:   r.FormValue("to_country"),
		WeightOz:    weightOz,
		LengthIn:    lengthIn,
		WidthIn:     widthIn,
		HeightIn:    heightIn,
		ServiceCode: r.FormValue("service_code"),
		Reference:   orderID.String(),
	}

	actor := staffActor(r)

	// CreateShipmentLabel calls the external API outside the tx, then
	// persists the shipment record and audit entry inside the tx.
	// We pass nil tx here because the service method needs the external
	// API call to happen first — we open a tx for the DB write + job enqueue.
	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		shipment, txErr := d.FulfillmentService.CreateShipmentLabel(ctx, tx, req, orderID, actor)
		if txErr != nil {
			return txErr
		}

		// Enqueue R2 storage job in the same transaction.
		_, txErr = d.RiverClient.InsertTx(ctx, tx, jobs.StoreLabelToR2Args{
			ShipmentID: shipment.ID,
			LabelURL:   shipment.LabelURL,
		}, nil)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/orders/"+orderID.String(), http.StatusSeeOther)
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
