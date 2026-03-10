package web

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/jobs"
	"github.com/dukerupert/hiri/internal/platform/auth"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/storefront"
)

const wholesaleCartCookieName = "wholesale_cart_id"

// getWholesaleCartID reads the wholesale cart ID from its dedicated cookie.
func getWholesaleCartID(r *http.Request) *uuid.UUID {
	c, err := r.Cookie(wholesaleCartCookieName)
	if err != nil {
		return nil
	}
	id, err := uuid.Parse(c.Value)
	if err != nil {
		return nil
	}
	return &id
}

// setWholesaleCartCookie writes the wholesale cart ID cookie.
func setWholesaleCartCookie(w http.ResponseWriter, cartID uuid.UUID) {
	http.SetCookie(w, &http.Cookie{
		Name:     wholesaleCartCookieName,
		Value:    cartID.String(),
		Path:     "/",
		MaxAge:   30 * 24 * 60 * 60, // 30 days
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// wholesaleCartItemCount returns the cart item count for the wholesale cart cookie.
func (d *Deps) wholesaleCartItemCount(r *http.Request) int {
	cartID := getWholesaleCartID(r)
	if cartID == nil {
		return 0
	}
	var count int
	_ = store.Tx(r.Context(), d.Pool, func(tx pgx.Tx) error {
		var err error
		count, err = d.CartService.GetItemCount(r.Context(), tx, *cartID)
		return err
	})
	return count
}

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

// --- Wholesale password setup (token from welcome email) ---

func (d *Deps) handleWholesaleSetupPage(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		http.Redirect(w, r, "/wholesale/login", http.StatusSeeOther)
		return
	}
	props := storefront.WholesaleSetupProps{Token: token}
	if IsHTMX(r) {
		storefront.WholesaleSetupContent(props).Render(r.Context(), w) //nolint:errcheck
		return
	}
	storefront.WholesaleSetupPage(props).Render(r.Context(), w) //nolint:errcheck
}

func (d *Deps) handleWholesaleSetup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	token := r.FormValue("token")
	password := r.FormValue("password")
	passwordConfirm := r.FormValue("password_confirm")

	renderErr := func(msg string) {
		props := storefront.WholesaleSetupProps{Token: token, Error: msg}
		if IsHTMX(r) {
			storefront.WholesaleSetupContent(props).Render(ctx, w) //nolint:errcheck
			return
		}
		storefront.WholesaleSetupPage(props).Render(ctx, w) //nolint:errcheck
	}

	if token == "" {
		http.Redirect(w, r, "/wholesale/login", http.StatusSeeOther)
		return
	}

	if password != passwordConfirm {
		renderErr("Passwords do not match.")
		return
	}

	if len(password) < 8 {
		renderErr("Password must be at least 8 characters.")
		return
	}

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.AuthService.SetPasswordWithToken(ctx, tx, token, password)
		return txErr
	})
	if err != nil {
		if err == app.ErrSetupTokenExpired {
			renderErr("This setup link has expired or has already been used.")
			return
		}
		if err == app.ErrPasswordTooShort {
			renderErr("Password must be at least 8 characters.")
			return
		}
		Error(w, r, err)
		return
	}

	props := storefront.WholesaleSetupProps{Success: true}
	if IsHTMX(r) {
		storefront.WholesaleSetupContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	storefront.WholesaleSetupPage(props).Render(ctx, w) //nolint:errcheck
}

// --- Wholesale portal (authenticated, approved wholesale customers) ---

func (d *Deps) handleWholesaleQuickOrder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customer, ok := auth.CustomerFromContext(ctx)
	if !ok {
		http.Redirect(w, r, "/wholesale/login", http.StatusSeeOther)
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
		CartCount:   d.wholesaleCartItemCount(r),
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

	cartID := getWholesaleCartID(r)
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

	setWholesaleCartCookie(w, resultCartID)
	http.Redirect(w, r, "/wholesale/checkout", http.StatusSeeOther)
}

func (d *Deps) handleWholesaleCheckoutPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customer, ok := auth.CustomerFromContext(ctx)
	if !ok {
		http.Redirect(w, r, "/wholesale/login", http.StatusSeeOther)
		return
	}

	companyName := ""
	if customer.CompanyName != nil {
		companyName = *customer.CompanyName
	}

	cartID := getWholesaleCartID(r)
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
					ItemID:      ci.ID,
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
		CartCount:   d.wholesaleCartItemCount(r),
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
		http.Redirect(w, r, "/wholesale/login", http.StatusSeeOther)
		return
	}

	d.Metrics.CheckoutStarted.WithLabelValues("wholesale").Inc()

	poNumber := r.FormValue("po_number")
	notes := r.FormValue("notes")
	cartID := getWholesaleCartID(r)

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
		order, txErr := d.WholesaleService.PlaceWholesaleOrder(ctx, tx, orderParams, actor)
		if txErr != nil {
			return txErr
		}

		// Enqueue QB customer + invoice chain if QB is connected.
		if d.QBClient != nil {
			_, txErr = d.RiverClient.InsertTx(ctx, tx, jobs.EnsureQBCustomerArgs{
				CustomerID: customer.ID,
				OrderID:    order.ID,
			}, nil)
			if txErr != nil {
				return txErr
			}
		}

		// Delete the cart after successful order.
		return d.CartService.DeleteCart(ctx, tx, cart.ID)
	})
	if err != nil {
		reason := classifyCheckoutError(err)
		d.Metrics.CheckoutFailed.WithLabelValues("wholesale", reason).Inc()
		Error(w, r, err)
		return
	}

	d.Metrics.CheckoutCompleted.WithLabelValues("wholesale").Inc()

	// Clear wholesale cart cookie.
	http.SetCookie(w, &http.Cookie{
		Name:   wholesaleCartCookieName,
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	})

	http.Redirect(w, r, "/wholesale/portal", http.StatusSeeOther)
}

// handleWholesaleCartUpdate updates the quantity of a cart item inline.
func (d *Deps) handleWholesaleCartUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	itemID, err := uuid.Parse(r.URL.Query().Get("item_id"))
	if err != nil {
		http.Error(w, "invalid item", http.StatusBadRequest)
		return
	}

	quantity, err := strconv.Atoi(r.FormValue("quantity"))
	if err != nil || quantity < 1 {
		http.Error(w, "invalid quantity", http.StatusBadRequest)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.CartService.UpdateItemQuantity(ctx, tx, itemID, quantity)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	d.handleWholesaleCheckoutPage(w, r)
}

// handleWholesaleCartRemove removes a cart item inline.
func (d *Deps) handleWholesaleCartRemove(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	itemID, err := uuid.Parse(r.URL.Query().Get("item_id"))
	if err != nil {
		http.Error(w, "invalid item", http.StatusBadRequest)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.CartService.RemoveItem(ctx, tx, itemID)
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	d.handleWholesaleCheckoutPage(w, r)
}
