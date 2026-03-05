package web

import (
	"net/http"

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
	var products []domain.Product
	var variantMap = make(map[uuid.UUID][]domain.Variant)

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		plans, txErr = d.SubscriptionService.ListPlans(ctx, tx)
		if txErr != nil {
			return txErr
		}

		// Load all active products with their variants for the create form.
		activeStatus := domain.ProductStatusActive
		products, txErr = d.CatalogService.ListProducts(ctx, tx, store.ProductFilter{
			Status: &activeStatus,
			Limit:  200,
		})
		if txErr != nil {
			return txErr
		}

		for _, p := range products {
			variants, vErr := d.CatalogService.ListVariants(ctx, tx, p.ID)
			if vErr != nil {
				return vErr
			}
			variantMap[p.ID] = variants
		}

		return nil
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	// Build plan info with product/variant names.
	planInfos := make([]admin.PlanVariantInfo, len(plans))
	// Build a quick lookup: variantID → (productTitle, sku).
	type variantInfo struct {
		productTitle string
		sku          string
	}
	variantLookup := make(map[uuid.UUID]variantInfo)
	for _, p := range products {
		for _, v := range variantMap[p.ID] {
			variantLookup[v.ID] = variantInfo{productTitle: p.Title, sku: v.SKU}
		}
	}

	for i, plan := range plans {
		info := variantLookup[plan.VariantID]
		planInfos[i] = admin.PlanVariantInfo{
			Plan:         plan,
			ProductTitle: info.productTitle,
			VariantSKU:   info.sku,
		}
	}

	// Build variant options for the create form.
	var variantOptions []admin.ProductVariantOption
	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		for _, p := range products {
			for _, v := range variantMap[p.ID] {
				ps, psErr := d.PricingService.GetOrCreatePriceSet(ctx, tx, v.ID)
				if psErr != nil {
					return psErr
				}
				variantOptions = append(variantOptions, admin.ProductVariantOption{
					VariantID:    v.ID.String(),
					PriceSetID:   ps.ID.String(),
					ProductTitle: p.Title,
					VariantSKU:   v.SKU,
				})
			}
		}
		return nil
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	props := admin.PlanListProps{
		Plans:    planInfos,
		Variants: variantOptions,
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
	variantIDStr := r.FormValue("variant_id")
	intervalStr := r.FormValue("interval")

	if name == "" || variantIDStr == "" || intervalStr == "" {
		renderPlanError(w, r, "Name, variant, and interval are required.")
		return
	}

	variantID, err := uuid.Parse(variantIDStr)
	if err != nil {
		renderPlanError(w, r, "Invalid variant ID.")
		return
	}

	interval := domain.SubscriptionInterval(intervalStr)

	// Look up the price set for this variant.
	var priceSetID uuid.UUID
	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		ps, txErr := d.PricingService.GetOrCreatePriceSet(ctx, tx, variantID)
		if txErr != nil {
			return txErr
		}
		priceSetID = ps.ID
		return nil
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.SubscriptionService.CreatePlan(ctx, tx, app.CreatePlanParams{
			Name:          name,
			Interval:      interval,
			IntervalCount: 1,
			VariantID:     variantID,
			PriceSetID:    priceSetID,
		}, devActor())
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
		return d.SubscriptionService.UpdatePlanActive(ctx, tx, id, false, devActor())
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
		return d.SubscriptionService.UpdatePlanActive(ctx, tx, id, true, devActor())
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
