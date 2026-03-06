package web

import (
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/admin"
	"github.com/dukerupert/hiri/internal/ui/components/toast"
)

func (d *Deps) handleAdminPlanList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var plans []domain.SubscriptionPlan

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		plans, txErr = d.SubscriptionService.ListPlans(ctx, tx)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	name, role := staffNameRole(r)
	props := admin.PlanListProps{
		Plans:     plans,
		StaffName: name,
		StaffRole: role,
	}

	if IsHTMX(r) {
		admin.PlanListContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.PlanList(props).Render(ctx, w) //nolint:errcheck
}

func (d *Deps) handleAdminPlanCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := r.ParseForm(); err != nil {
		Error(w, r, err)
		return
	}

	name := r.FormValue("name")
	intervalStr := r.FormValue("interval")
	discountStr := r.FormValue("discount_pct")

	if name == "" || intervalStr == "" {
		renderPlanError(w, r, "Name and interval are required.")
		return
	}

	interval := domain.SubscriptionInterval(intervalStr)

	discountPct := 0
	if discountStr != "" {
		var err error
		discountPct, err = strconv.Atoi(discountStr)
		if err != nil || discountPct < 0 || discountPct > 100 {
			renderPlanError(w, r, "Discount must be 0-100.")
			return
		}
	}

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.SubscriptionService.CreatePlan(ctx, tx, app.CreatePlanParams{
			Name:          name,
			Interval:      interval,
			IntervalCount: 1,
			DiscountPct:   discountPct,
		}, staffActor(r))
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/plans", http.StatusSeeOther)
}

func (d *Deps) handleAdminPlanDeactivate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.SubscriptionService.UpdatePlanActive(ctx, tx, id, false, staffActor(r))
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/plans", http.StatusSeeOther)
}

func (d *Deps) handleAdminPlanActivate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.SubscriptionService.UpdatePlanActive(ctx, tx, id, true, staffActor(r))
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/plans", http.StatusSeeOther)
}

func (d *Deps) handleAdminPlanUpdateDiscount(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if err := r.ParseForm(); err != nil {
		Error(w, r, err)
		return
	}

	discountPct, err := strconv.Atoi(r.FormValue("discount_pct"))
	if err != nil || discountPct < 0 || discountPct > 100 {
		renderPlanError(w, r, "Discount must be 0-100.")
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.SubscriptionService.UpdatePlanDiscount(ctx, tx, id, discountPct, staffActor(r))
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/plans", http.StatusSeeOther)
}

func renderPlanError(w http.ResponseWriter, r *http.Request, msg string) {
	if IsHTMX(r) {
		w.WriteHeader(http.StatusOK)
		toast.Toast(toast.VariantError, msg).Render(r.Context(), w) //nolint:errcheck
		return
	}
	http.Error(w, msg, http.StatusBadRequest)
}
