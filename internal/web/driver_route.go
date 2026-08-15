package web

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/storefront"
)

// driverActor attributes route actions to the driver holding the link.
//
// Typed as system rather than staff: whoever scanned the QR has no account, so
// there is no staff id to record, and claiming one would put a name on the
// audit trail that nobody verified. The route id in the metadata is what ties
// the action back to a specific run and driver handoff.
func driverActor() app.Actor {
	return app.Actor{
		Type: domain.AuditActorTypeSystem,
		Name: "Delivery driver",
	}
}

// handleDriverRoute renders the driver's stop list. GET /routes/{token}.
//
// Token-authenticated, no login: the driver scans a QR at packout and works
// from their own phone. An unknown, draft, or completed token all render the
// same "this link is finished" page — the token dying when the route completes
// is the intended end state, and distinguishing the cases would only help
// someone guessing tokens.
func (d *Deps) handleDriverRoute(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")

	var saved *app.SavedRoute
	err := store.Tx(r.Context(), d.Pool, func(tx pgx.Tx) error {
		var txErr error
		saved, txErr = d.RouteService.GetRouteByShareToken(r.Context(), tx, token)
		return txErr
	})
	if err != nil {
		storefront.DriverRouteGonePage().Render(r.Context(), w) //nolint:errcheck
		return
	}

	storefront.DriverRoutePage(driverRouteProps(token, saved)).Render(r.Context(), w) //nolint:errcheck
}

// handleDriverStopDelivered marks a stop delivered from the driver's phone.
// POST /routes/{token}/stops/{stopID}/delivered.
//
// POST-only, and never GET: link scanners and prefetchers fetch every URL they
// see, and a GET that completed a delivery would let a mail gateway or a
// browser preloader mark the whole route done. Same reasoning as the
// switch-to-pickup flow.
func (d *Deps) handleDriverStopDelivered(w http.ResponseWriter, r *http.Request) {
	d.updateDriverStop(w, r, func(tx pgx.Tx, routeID, stopID uuid.UUID) (*app.SavedRoute, error) {
		return d.RouteService.MarkStopDelivered(r.Context(), tx, routeID, stopID, driverActor())
	})
}

// handleDriverStopSkipped records a skip with the driver's reason.
// POST /routes/{token}/stops/{stopID}/skip.
func (d *Deps) handleDriverStopSkipped(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		Error(w, r, err)
		return
	}
	reason := r.FormValue("reason")
	d.updateDriverStop(w, r, func(tx pgx.Tx, routeID, stopID uuid.UUID) (*app.SavedRoute, error) {
		return d.RouteService.MarkStopSkipped(r.Context(), tx, routeID, stopID, reason, driverActor())
	})
}

// updateDriverStop is the shared shape of both stop actions: authenticate the
// token, resolve the stop, apply the change, re-render the list.
//
// The whole update runs in one transaction so the stop, the order behind it,
// and any auto-completion of the route commit together — a driver on one bar of
// signal must never end up with a stop marked and its order not.
func (d *Deps) updateDriverStop(
	w http.ResponseWriter,
	r *http.Request,
	apply func(tx pgx.Tx, routeID, stopID uuid.UUID) (*app.SavedRoute, error),
) {
	token := r.PathValue("token")
	stopID, err := uuid.Parse(r.PathValue("stopID"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var saved *app.SavedRoute
	err = store.Tx(r.Context(), d.Pool, func(tx pgx.Tx) error {
		route, txErr := d.RouteService.GetRouteByShareToken(r.Context(), tx, token)
		if txErr != nil {
			return txErr
		}
		saved, txErr = apply(tx, route.Route.ID, stopID)
		return txErr
	})
	if err != nil {
		// htmx swaps a toast in place; a plain form post falls back to the
		// gone page, which is the only other thing a token failure can mean.
		if IsHTMX(r) {
			Error(w, r, err)
			return
		}
		storefront.DriverRouteGonePage().Render(r.Context(), w) //nolint:errcheck
		return
	}

	props := driverRouteProps(token, saved)
	if IsHTMX(r) {
		storefront.DriverRouteBody(props).Render(r.Context(), w) //nolint:errcheck
		return
	}
	storefront.DriverRoutePage(props).Render(r.Context(), w) //nolint:errcheck
}

// driverRouteProps maps a saved route onto what the phone needs to render.
func driverRouteProps(token string, saved *app.SavedRoute) storefront.DriverRouteProps {
	stops := make([]storefront.DriverStop, 0, len(saved.Stops))
	for _, st := range saved.Stops {
		stops = append(stops, storefront.DriverStop{
			ID:           st.ID,
			Position:     st.Position,
			CustomerName: st.CustomerName,
			Address:      st.Address,
			Notes:        st.Notes,
			Status:       st.Status,
			SkipReason:   st.SkipReason,
			Lat:          st.Lat,
			Lng:          st.Lng,
		})
	}
	return storefront.DriverRouteProps{
		Token:     token,
		Stops:     stops,
		Progress:  saved.Progress(),
		Completed: saved.Route.Status == domain.RouteStatusCompleted,
		OriginLat: saved.Route.OriginLat,
		OriginLng: saved.Route.OriginLng,
		Roundtrip: saved.Route.Roundtrip,
	}
}
