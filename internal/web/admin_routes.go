package web

import (
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/admin"
)

// planRouteDate resolves which delivery run a newly planned route covers.
//
// Uses the same NextDeliveryDate the checkout promise uses, so the route lands
// on the run customers were told about rather than on whatever "today" happens
// to be when staff hit the button. Falls back to today only when no delivery
// schedule is configured, which keeps the feature usable before the Mon/Thu
// cutoff is set up.
func (d *Deps) planRouteDate(r *http.Request) time.Time {
	loc := d.MerchantTZ
	if loc == nil {
		loc = time.UTC
	}
	now := time.Now().In(loc)

	var cfg *domain.ShippingConfig
	_ = store.Tx(r.Context(), d.Pool, func(tx pgx.Tx) error {
		c, err := d.CheckoutService.GetShippingConfig(r.Context(), tx)
		if err != nil {
			return err
		}
		cfg = c
		return nil
	})
	if cfg != nil {
		if date, ok := cfg.NextDeliveryDate(now, loc); ok {
			return time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)
		}
	}
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
}

// handleAdminRoutePlan plans a route over the checked delivery orders and
// saves it as a draft. POST /admin/fulfillment/route/plan.
//
// Submitted by the Load list tab's form, so the selection arrives the same way
// the print sheet gets it: repeated ids params. Planning is slow-ish (a
// geocode pass plus an OSRM call), which is why it is a POST that redirects
// rather than an htmx swap — a double-click should not plan twice.
func (d *Deps) handleAdminRoutePlan(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		Error(w, r, err)
		return
	}
	selected, _ := parseLoadListSelection(r.Form)

	saved, plan, err := d.RouteService.PlanAndSaveRoute(
		r.Context(), d.Pool, d.planRouteDate(r),
		app.PlanRouteOptions{OrderIDs: selected, Roundtrip: true},
		staffActor(r),
	)
	if err != nil {
		Error(w, r, err)
		return
	}

	// Unroutable stops don't survive persistence — they aren't stops. Carry
	// the count in the query string so the review page can say so out loud
	// rather than silently showing a shorter route than staff expected.
	dest := fmt.Sprintf("/admin/routes/%s", saved.Route.ID)
	if n := len(plan.Unroutable); n > 0 {
		dest = fmt.Sprintf("%s?unroutable=%d", dest, n)
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

// handleAdminRouteShow renders one route: the ordered stop list, low-confidence
// flags, and the activate / complete controls. GET /admin/routes/{id}.
func (d *Deps) handleAdminRouteShow(w http.ResponseWriter, r *http.Request) {
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

	name, role := staffNameRole(r)
	props := admin.RouteShowProps{
		Route:      saved.Route,
		Stops:      saved.Stops,
		Progress:   saved.Progress(),
		DriverURL:  saved.DriverURL(d.BaseURL),
		Unroutable: parseUnroutableCount(r),
		MerchantTZ: d.MerchantTZ,
		StaffName:  name,
		StaffRole:  role,
	}

	if IsHTMX(r) {
		admin.RouteShowContent(props).Render(r.Context(), w) //nolint:errcheck
		return
	}
	admin.RouteShow(props).Render(r.Context(), w) //nolint:errcheck
}

// parseUnroutableCount reads the ?unroutable= hint the plan redirect leaves.
// A mangled value simply means "no warning" — it is a display nicety, not a
// reason to fail the page.
func parseUnroutableCount(r *http.Request) int {
	raw := r.URL.Query().Get("unroutable")
	if raw == "" {
		return 0
	}
	var n int
	if _, err := fmt.Sscanf(raw, "%d", &n); err != nil || n < 0 {
		return 0
	}
	return n
}

// handleAdminRouteActivate mints the share token and opens the route to the
// driver. POST /admin/routes/{id}/activate.
func (d *Deps) handleAdminRouteActivate(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	err = store.Tx(r.Context(), d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.RouteService.ActivateRoute(r.Context(), tx, id, staffActor(r))
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/routes/%s", id), http.StatusSeeOther)
}

// handleAdminRouteComplete ends the run, which also retires the driver's link.
// POST /admin/routes/{id}/complete.
func (d *Deps) handleAdminRouteComplete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	err = store.Tx(r.Context(), d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.RouteService.CompleteRoute(r.Context(), tx, id, staffActor(r))
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/routes/%s", id), http.StatusSeeOther)
}

// handleAdminRouteStopRemove drops one stop and re-plans the rest.
// POST /admin/routes/{id}/stops/{stopID}/remove.
//
// A genuine re-plan, not a delete: removing the middle of a route changes what
// the optimal order of the remaining stops is, and leaving the old sequence in
// place would hand the driver a route that is no longer the short one. The
// order itself is untouched — it stays in the delivery queue for the next run.
func (d *Deps) handleAdminRouteStopRemove(w http.ResponseWriter, r *http.Request) {
	routeID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	stopID, err := uuid.Parse(r.PathValue("stopID"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var keep []uuid.UUID
	var routeDate time.Time
	err = store.Tx(r.Context(), d.Pool, func(tx pgx.Tx) error {
		saved, txErr := d.RouteService.GetRoute(r.Context(), tx, routeID)
		if txErr != nil {
			return txErr
		}
		routeDate = saved.Route.RouteDate
		for _, st := range saved.Stops {
			if st.ID != stopID {
				keep = append(keep, st.OrderID)
			}
		}
		return nil
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	if len(keep) == 0 {
		// Removing the last stop would leave an empty draft that cannot be
		// activated. Say so rather than re-planning into a dead end.
		Error(w, r, app.ErrRouteEmpty)
		return
	}

	saved, _, err := d.RouteService.PlanAndSaveRoute(
		r.Context(), d.Pool, routeDate,
		app.PlanRouteOptions{OrderIDs: keep, Roundtrip: true},
		staffActor(r),
	)
	if err != nil {
		Error(w, r, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/routes/%s", saved.Route.ID), http.StatusSeeOther)
}

// handleAdminRouteList shows recent routes. GET /admin/routes.
func (d *Deps) handleAdminRouteList(w http.ResponseWriter, r *http.Request) {
	var routes []domain.DeliveryRoute
	err := store.Tx(r.Context(), d.Pool, func(tx pgx.Tx) error {
		var txErr error
		routes, txErr = d.RouteService.ListRoutes(r.Context(), tx, 50)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	name, role := staffNameRole(r)
	props := admin.RouteListProps{
		Routes:     routes,
		MerchantTZ: d.MerchantTZ,
		StaffName:  name,
		StaffRole:  role,
	}
	if IsHTMX(r) {
		admin.RouteListContent(props).Render(r.Context(), w) //nolint:errcheck
		return
	}
	admin.RouteList(props).Render(r.Context(), w) //nolint:errcheck
}
