package web

import (
	"context"
	"fmt"
	"net/http"
	"strings"

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
		var pp admin.ProductPriceListPricing
		err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
			var txErr error
			pp, txErr = d.buildPriceListProduct(ctx, tx, p)
			return txErr
		})
		if err != nil {
			Error(w, r, err)
			return
		}
		if len(pp.Variants) == 0 {
			continue
		}
		pgs = append(pgs, pp)
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

// handleAdminPriceListPriceBulkUpdate applies every changed list override in a single
// product's form in one transaction. Each cell submits a current value plus a hidden
// "list_prev" value; only changed cells are written, and a cleared cell deletes that
// list's override (falling back to the base price).
func (d *Deps) handleAdminPriceListPriceBulkUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	ops, err := parsePriceListForm(r)
	if err != nil {
		// Don't re-render the table — that would discard the value the user is
		// in the middle of fixing. Just surface the error.
		if IsHTMX(r) {
			w.Header().Set("HX-Reswap", "none")
			toast.Toast(toast.VariantError, "Please enter a valid price").Render(ctx, w) //nolint:errcheck
			return
		}
		http.Error(w, "invalid price", http.StatusBadRequest)
		return
	}

	if len(ops) == 0 {
		if IsHTMX(r) {
			w.Header().Set("HX-Reswap", "none")
			toast.Toast(toast.VariantWarning, "No changes to save").Render(ctx, w) //nolint:errcheck
			return
		}
		http.Redirect(w, r, "/admin/price-lists/prices", http.StatusSeeOther)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		for _, op := range ops {
			var txErr error
			if op.kind == opGroupDelete {
				txErr = d.PricingService.DeletePriceListPrice(ctx, tx, op.variantID, op.groupID, "USD")
			} else {
				_, txErr = d.PricingService.SetPriceListPrice(ctx, tx, op.variantID, op.groupID, op.cents, "USD")
			}
			if txErr != nil {
				return txErr
			}
		}
		return nil
	})
	if err != nil {
		if IsHTMX(r) {
			w.Header().Set("HX-Reswap", "none")
			_, msg := mapError(err)
			toast.Toast(toast.VariantError, msg).Render(ctx, w) //nolint:errcheck
			return
		}
		Error(w, r, err)
		return
	}

	if IsHTMX(r) {
		// Success: leave the user's table exactly as it is (no full re-render, so
		// scroll position and focus are preserved). Confirm with a toast, and
		// OOB-update each saved cell's hidden "previous value" so the next save's
		// change-detection compares against what we just persisted.
		w.Header().Set("HX-Reswap", "none")
		toast.Toast(toast.VariantSuccess, savedPricesMessage(len(ops))).Render(ctx, w) //nolint:errcheck
		for _, op := range ops {
			newPrev := ""
			if op.kind != opGroupDelete {
				newPrev = fmt.Sprintf("%.2f", float64(op.cents)/100)
			}
			admin.PriceListPrevOOB(op.groupID, op.variantID, newPrev).Render(ctx, w) //nolint:errcheck
		}
		return
	}
	http.Redirect(w, r, "/admin/price-lists/prices", http.StatusSeeOther)
}

// savedPricesMessage builds the success-toast text for a bulk price save.
func savedPricesMessage(n int) string {
	if n == 1 {
		return "Saved 1 price"
	}
	return fmt.Sprintf("Saved %d prices", n)
}

// buildPriceListProduct assembles one product's variants and their base + price-list
// prices for rendering. The caller supplies the transaction.
func (d *Deps) buildPriceListProduct(ctx context.Context, tx pgx.Tx, p domain.Product) (admin.ProductPriceListPricing, error) {
	variants, err := d.CatalogService.ListVariants(ctx, tx, p.ID)
	if err != nil {
		return admin.ProductPriceListPricing{}, err
	}
	basePrices, err := d.PricingService.ListBasePricesByProduct(ctx, tx, p.ID, "USD")
	if err != nil {
		return admin.ProductPriceListPricing{}, err
	}
	listPrices, err := d.PricingService.ListPriceListPricesByProduct(ctx, tx, p.ID, "USD")
	if err != nil {
		return admin.ProductPriceListPricing{}, err
	}

	vps := make([]admin.VariantPriceListPricing, len(variants))
	for i, v := range variants {
		vp := admin.VariantPriceListPricing{
			Variant:    v,
			ListPrices: listPrices[v.ID],
		}
		if cents, ok := basePrices[v.ID]; ok {
			c := cents
			vp.PriceCents = &c
		}
		if vp.ListPrices == nil {
			vp.ListPrices = make(map[uuid.UUID]int)
		}
		vps[i] = vp
	}
	return admin.ProductPriceListPricing{Product: p, Variants: vps}, nil
}

// parsePriceListForm turns a submitted price-list form into the list of changed cells.
// The groupID field on each priceOp carries the price list ID. Returns an error if any
// submitted price is malformed or negative.
func parsePriceListForm(r *http.Request) ([]priceOp, error) {
	var ops []priceOp

	for key := range r.PostForm {
		if !strings.HasPrefix(key, "list:") {
			continue
		}
		listID, variantID, ok := splitGroupKey(strings.TrimPrefix(key, "list:"))
		if !ok {
			continue
		}
		cur, err := parseDollarCents(r.PostForm.Get(key))
		if err != nil {
			return nil, err
		}
		prev, _ := parseDollarCents(r.PostForm.Get("list_prev:" + listID.String() + ":" + variantID.String()))
		if centsEqual(cur, prev) {
			continue
		}
		if cur == nil {
			ops = append(ops, priceOp{kind: opGroupDelete, variantID: variantID, groupID: listID})
		} else {
			ops = append(ops, priceOp{kind: opGroupSet, variantID: variantID, groupID: listID, cents: *cur})
		}
	}

	return ops, nil
}
