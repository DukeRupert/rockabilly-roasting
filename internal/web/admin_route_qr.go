package web

import (
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	qrcode "github.com/skip2/go-qrcode"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/store"
)

// qrSizeDefault is the rendered PNG edge in pixels. Big enough to scan off a
// screen or a printed sheet from arm's length, small enough to stay a ~10KB
// image.
const qrSizeDefault = 320

// qrSizeMax bounds the size parameter so a crafted URL can't ask the server to
// render (and hold in memory) an enormous bitmap.
const qrSizeMax = 1024

// handleAdminRouteQR renders the driver link as a scannable PNG.
// GET /admin/routes/{id}/qr.png
//
// This is the actual handoff: the driver opens their camera, scans, and the
// stop list is on their phone. No typing a token, no app, no account.
//
// Only active routes produce a code. A draft has no share token yet, and a
// completed route's token no longer resolves — encoding either would hand
// someone a QR that opens the "route closed" page, which looks like a bug
// rather than a state.
func (d *Deps) handleAdminRouteQR(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var saved *app.SavedRoute
	err = store.Tx(r.Context(), d.Pool, func(tx pgx.Tx) error {
		var txErr error
		saved, txErr = d.RouteService.GetRoute(r.Context(), tx, id)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	driverURL := saved.DriverURL(d.BaseURL)
	if driverURL == "" {
		http.NotFound(w, r)
		return
	}

	size := qrSizeDefault
	if raw := r.URL.Query().Get("size"); raw != "" {
		if n, convErr := strconv.Atoi(raw); convErr == nil && n > 0 && n <= qrSizeMax {
			size = n
		}
	}

	// Medium recovery: enough redundancy to survive a creased printout without
	// inflating the module count so far that the code stops scanning from a
	// phone held at arm's length.
	png, err := qrcode.Encode(driverURL, qrcode.Medium, size)
	if err != nil {
		Error(w, r, err)
		return
	}

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Length", strconv.Itoa(len(png)))
	// The URL embeds a live credential, so this must not be cached by any
	// shared proxy, and the browser should re-fetch it when the route changes.
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	w.Write(png) //nolint:errcheck
}
