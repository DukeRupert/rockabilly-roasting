package web

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/platform/auth"
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
	ctx := r.Context()
	customer, ok := auth.CustomerFromContext(ctx)
	if !ok {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	companyName := ""
	if customer.CompanyName != nil {
		companyName = *customer.CompanyName
	}

	var products []app.QuickOrderProduct
	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		products, txErr = d.WholesaleService.QuickOrderCatalog(ctx, tx, d.PricingService, "USD")
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	// Map app types to template types.
	templateProducts := make([]storefront.QuickOrderProduct, len(products))
	for i, p := range products {
		variants := make([]storefront.QuickOrderVariant, len(p.Variants))
		for j, v := range p.Variants {
			variants[j] = storefront.QuickOrderVariant{
				ID:           v.ID,
				SKU:          v.SKU,
				OptionValues: v.OptionValues,
				UnitPrice:    v.UnitPrice,
				MinQty:       v.MinQty,
				Multiple:     v.Multiple,
				InStock:      v.InStock,
			}
		}
		templateProducts[i] = storefront.QuickOrderProduct{
			ID:       p.ID,
			Title:    p.Title,
			ImageURL: p.ImageURL,
			Options:  p.Options,
			Variants: variants,
		}
	}

	props := storefront.WholesalePortalProps{
		CompanyName: companyName,
		Products:    templateProducts,
	}

	if IsHTMX(r) {
		storefront.WholesalePortalContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	storefront.WholesalePortalPage(props).Render(ctx, w) //nolint:errcheck
}

func (d *Deps) handleWholesaleBulkAdd(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := r.ParseForm(); err != nil {
		Error(w, r, fmt.Errorf("parse form: %w", err))
		return
	}

	// Parse qty[variant_id] = quantity from form.
	type bulkItem struct {
		VariantID uuid.UUID
		Quantity  int
	}
	var items []bulkItem
	for key, values := range r.Form {
		if len(key) <= 4 || key[:4] != "qty[" || key[len(key)-1] != ']' {
			continue
		}
		variantIDStr := key[4 : len(key)-1]
		variantID, err := uuid.Parse(variantIDStr)
		if err != nil {
			continue
		}
		if len(values) == 0 || values[0] == "" || values[0] == "0" {
			continue
		}
		qty, err := strconv.Atoi(values[0])
		if err != nil || qty <= 0 {
			continue
		}
		items = append(items, bulkItem{VariantID: variantID, Quantity: qty})
	}

	if len(items) == 0 {
		http.Redirect(w, r, "/wholesale/portal", http.StatusSeeOther)
		return
	}

	// Use the cookie-based cart (same as retail).
	cartID := getCartID(r)
	var resultCartID uuid.UUID

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		cart, txErr := d.CartService.GetOrCreateCart(ctx, tx, cartID)
		if txErr != nil {
			return txErr
		}
		resultCartID = cart.ID
		for _, item := range items {
			if _, txErr := d.CartService.AddItem(ctx, tx, cart.ID, item.VariantID, item.Quantity); txErr != nil {
				return txErr
			}
		}
		return nil
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	setCartCookie(w, resultCartID)
	http.Redirect(w, r, "/wholesale/checkout", http.StatusSeeOther)
}

func (d *Deps) handleWholesaleCheckoutPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customer, ok := auth.CustomerFromContext(ctx)
	if !ok {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	companyName := ""
	if customer.CompanyName != nil {
		companyName = *customer.CompanyName
	}

	cartID := getCartID(r)
	var checkoutItems []storefront.WholesaleCheckoutItem
	subtotal := 0

	if cartID != nil {
		err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
			cart, txErr := d.CartService.GetOrCreateCart(ctx, tx, cartID)
			if txErr != nil {
				return txErr
			}
			cartItems, txErr := d.CartService.ListItems(ctx, tx, cart.ID)
			if txErr != nil {
				return txErr
			}

			for _, ci := range cartItems {
				variant, txErr := d.CatalogService.GetVariant(ctx, tx, ci.VariantID)
				if txErr != nil {
					return txErr
				}
				product, txErr := d.CatalogService.GetProduct(ctx, tx, variant.ProductID)
				if txErr != nil {
					return txErr
				}

				lineTotal := ci.UnitPrice * ci.Quantity
				subtotal += lineTotal

				checkoutItems = append(checkoutItems, storefront.WholesaleCheckoutItem{
					VariantID:   ci.VariantID,
					ProductName: product.Title,
					SKU:         variant.SKU,
					Quantity:    ci.Quantity,
					UnitPrice:   ci.UnitPrice,
					LineTotal:   lineTotal,
				})
			}
			return nil
		})
		if err != nil {
			Error(w, r, err)
			return
		}
	}

	props := storefront.WholesaleCheckoutProps{
		CompanyName: companyName,
		Items:       checkoutItems,
		Subtotal:    subtotal,
	}

	if IsHTMX(r) {
		storefront.WholesaleCheckoutContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	storefront.WholesaleCheckoutPage(props).Render(ctx, w) //nolint:errcheck
}

func (d *Deps) handleWholesaleCheckoutConfirm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customer, ok := auth.CustomerFromContext(ctx)
	if !ok {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	poNumber := r.FormValue("po_number")
	notes := r.FormValue("notes")
	cartID := getCartID(r)

	if cartID == nil {
		http.Redirect(w, r, "/wholesale/portal", http.StatusSeeOther)
		return
	}

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		cart, txErr := d.CartService.GetOrCreateCart(ctx, tx, cartID)
		if txErr != nil {
			return txErr
		}
		cartItems, txErr := d.CartService.ListItems(ctx, tx, cart.ID)
		if txErr != nil {
			return txErr
		}
		if len(cartItems) == 0 {
			return app.ErrCartEmpty
		}

		items := make([]app.CartItem, 0, len(cartItems))
		for _, ci := range cartItems {
			items = append(items, app.CartItem{
				VariantID: ci.VariantID,
				Quantity:  ci.Quantity,
				UnitPrice: ci.UnitPrice,
			})
		}

		orderParams := app.PlaceWholesaleOrderParams{
			CustomerID:   customer.ID,
			Items:        items,
			CurrencyCode: "USD",
		}
		if poNumber != "" {
			orderParams.CustomerPONumber = &poNumber
		}
		if notes != "" {
			orderParams.Notes = &notes
		}

		actor := customerActor(r)
		_, txErr = d.WholesaleService.PlaceWholesaleOrder(ctx, tx, orderParams, actor)
		if txErr != nil {
			return txErr
		}

		// Delete the cart after successful order.
		return d.CartService.DeleteCart(ctx, tx, cart.ID)
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	// Clear cart cookie.
	http.SetCookie(w, &http.Cookie{
		Name:   cartCookieName,
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	})

	http.Redirect(w, r, "/wholesale/portal", http.StatusSeeOther)
}
