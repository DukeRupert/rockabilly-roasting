package web

import (
	"math"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/admin"
	"github.com/dukerupert/hiri/internal/ui/components/toast"
)

// --- Price list CRUD ---

func (d *Deps) handleAdminPriceListList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var lists []domain.PriceList
	customerCounts := map[uuid.UUID]int{}

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		lists, txErr = d.PriceListService.List(ctx, tx)
		if txErr != nil {
			return txErr
		}
		for _, pl := range lists {
			n, cErr := d.PriceListService.CountCustomers(ctx, tx, pl.ID)
			if cErr != nil {
				return cErr
			}
			customerCounts[pl.ID] = n
		}
		return nil
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	name, role := staffNameRole(r)
	props := admin.PriceListListProps{
		Lists:          lists,
		CustomerCounts: customerCounts,
		StaffName:      name,
		StaffRole:      role,
	}

	if IsHTMX(r) {
		admin.PriceListListContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.PriceListList(props).Render(ctx, w) //nolint:errcheck
}

func (d *Deps) handleAdminPriceListCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	listName := r.FormValue("name")
	if listName == "" {
		http.Redirect(w, r, "/admin/price-lists", http.StatusSeeOther)
		return
	}

	status := domain.PriceListStatus(r.FormValue("status"))
	if status == "" {
		status = domain.PriceListStatusActive
	}

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.PriceListService.Create(ctx, tx, listName, domain.PriceListTypeOverride, status, staffActor(r))
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/price-lists", http.StatusSeeOther)
}

func (d *Deps) handleAdminPriceListUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	listName := r.FormValue("name")
	status := domain.PriceListStatus(r.FormValue("status"))
	if listName == "" || status == "" {
		http.Redirect(w, r, "/admin/price-lists", http.StatusSeeOther)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.PriceListService.Update(ctx, tx, id, listName, status, staffActor(r))
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/price-lists", http.StatusSeeOther)
}

func (d *Deps) handleAdminPriceListDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.PriceListService.Delete(ctx, tx, id, staffActor(r))
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/price-lists", http.StatusSeeOther)
}

// --- Price list pricing matrix ---

func (d *Deps) handleAdminPriceListPrices(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var lists []domain.PriceList
	var products []domain.Product

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		lists, txErr = d.PriceListService.List(ctx, tx)
		if txErr != nil {
			return txErr
		}
		products, txErr = d.CatalogService.ListProducts(ctx, tx, store.ProductFilter{})
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	pgs := make([]admin.ProductPriceListPricing, 0, len(products))
	for _, p := range products {
		var variants []domain.Variant
		var basePrices map[uuid.UUID]int
		var listPrices map[uuid.UUID]map[uuid.UUID]int

		err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
			var txErr error
			variants, txErr = d.CatalogService.ListVariants(ctx, tx, p.ID)
			if txErr != nil {
				return txErr
			}
			basePrices, txErr = d.PricingService.ListBasePricesByProduct(ctx, tx, p.ID, "USD")
			if txErr != nil {
				return txErr
			}
			listPrices, txErr = d.PricingService.ListPriceListPricesByProduct(ctx, tx, p.ID, "USD")
			return txErr
		})
		if err != nil {
			Error(w, r, err)
			return
		}

		if len(variants) == 0 {
			continue
		}

		vps := make([]admin.VariantPriceListPricing, len(variants))
		for i, v := range variants {
			vp := admin.VariantPriceListPricing{
				Variant:    v,
				ListPrices: listPrices[v.ID],
			}
			if cents, ok := basePrices[v.ID]; ok {
				vp.PriceCents = &cents
			}
			if vp.ListPrices == nil {
				vp.ListPrices = make(map[uuid.UUID]int)
			}
			vps[i] = vp
		}

		pgs = append(pgs, admin.ProductPriceListPricing{
			Product:  p,
			Variants: vps,
		})
	}

	name, role := staffNameRole(r)
	props := admin.PriceListPricingProps{
		Products:  pgs,
		Lists:     lists,
		StaffName: name,
		StaffRole: role,
	}

	if IsHTMX(r) {
		admin.PriceListPricingContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.PriceListPricing(props).Render(ctx, w) //nolint:errcheck
}

func (d *Deps) handleAdminPriceListPriceUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	variantID, err := uuid.Parse(r.FormValue("variant_id"))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	listID, err := uuid.Parse(r.FormValue("price_list_id"))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	priceStr := r.FormValue("price")

	if priceStr == "" {
		err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
			return d.PricingService.DeletePriceListPrice(ctx, tx, variantID, listID, "USD")
		})
		if err != nil {
			Error(w, r, err)
			return
		}
		if IsHTMX(r) {
			d.handleAdminPriceListPrices(w, r)
			return
		}
		http.Redirect(w, r, "/admin/price-lists/prices", http.StatusSeeOther)
		return
	}

	dollars, err := strconv.ParseFloat(priceStr, 64)
	if err != nil || dollars < 0 {
		if IsHTMX(r) {
			d.handleAdminPriceListPrices(w, r)
			toast.Toast(toast.VariantError, "Please enter a valid price").Render(ctx, w) //nolint:errcheck
			return
		}
		http.Error(w, "invalid price", http.StatusBadRequest)
		return
	}

	cents := int(math.Round(dollars * 100))

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.PricingService.SetPriceListPrice(ctx, tx, variantID, listID, cents, "USD")
		return txErr
	})
	if err != nil {
		if IsHTMX(r) {
			d.handleAdminPriceListPrices(w, r)
			_, msg := mapError(err)
			toast.Toast(toast.VariantError, msg).Render(ctx, w) //nolint:errcheck
			return
		}
		Error(w, r, err)
		return
	}

	if IsHTMX(r) {
		d.handleAdminPriceListPrices(w, r)
		return
	}
	http.Redirect(w, r, "/admin/price-lists/prices", http.StatusSeeOther)
}
