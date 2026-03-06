package web

import (
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/storefront"
)

// --- Wholesale application (public) ---

func (d *Deps) handleWholesaleApplyPage(w http.ResponseWriter, r *http.Request) {
	props := storefront.WholesaleApplyProps{
		CartCount: d.cartItemCountFromCookie(r),
	}
	if IsHTMX(r) {
		storefront.WholesaleApplyContent(props).Render(r.Context(), w) //nolint:errcheck
		return
	}
	storefront.WholesaleApplyPage(props).Render(r.Context(), w) //nolint:errcheck
}

func (d *Deps) handleWholesaleApply(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	p := app.ApplyParams{
		Email:       r.FormValue("email"),
		FirstName:   r.FormValue("first_name"),
		LastName:    r.FormValue("last_name"),
		CompanyName: r.FormValue("company_name"),
	}
	if phone := r.FormValue("phone"); phone != "" {
		p.Phone = &phone
	}
	if website := r.FormValue("website"); website != "" {
		p.Website = &website
	}

	if p.Email == "" || p.FirstName == "" || p.LastName == "" || p.CompanyName == "" {
		http.Error(w, "All required fields must be filled", http.StatusBadRequest)
		return
	}

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.WholesaleService.SubmitApplication(ctx, tx, p)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	// TODO: Enqueue WholesaleApplicationNotifyArgs job.
	storefront.WholesaleApplyConfirmation().Render(r.Context(), w) //nolint:errcheck
}

// --- Wholesale portal (authenticated, approved wholesale customers) ---

func (d *Deps) handleWholesaleQuickOrder(w http.ResponseWriter, r *http.Request) {
	// TODO: Implement with RequireApprovedWholesale middleware and quick order template.
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte("<h1>Quick Order</h1><p>Coming soon.</p>")) //nolint:errcheck
}

func (d *Deps) handleWholesaleBulkAdd(w http.ResponseWriter, r *http.Request) {
	// TODO: Parse bulk quantities, validate MOQ, upsert cart items.
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

func (d *Deps) handleWholesaleCheckoutPage(w http.ResponseWriter, r *http.Request) {
	// TODO: Render wholesale checkout review page (PO number, no payment).
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte("<h1>Wholesale Checkout</h1><p>Coming soon.</p>")) //nolint:errcheck
}

func (d *Deps) handleWholesaleCheckoutConfirm(w http.ResponseWriter, r *http.Request) {
	// TODO: Place wholesale order via WholesaleService.PlaceWholesaleOrder.
	http.Error(w, "not implemented", http.StatusNotImplemented)
}
