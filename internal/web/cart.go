package web

import (
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/storefront"
)

const cartCookieName = "cart_id"

// getCartID reads the cart ID from the cookie. Returns nil if not set.
func getCartID(r *http.Request) *uuid.UUID {
	c, err := r.Cookie(cartCookieName)
	if err != nil {
		return nil
	}
	id, err := uuid.Parse(c.Value)
	if err != nil {
		return nil
	}
	return &id
}

// setCartCookie writes the cart ID cookie.
func setCartCookie(w http.ResponseWriter, cartID uuid.UUID) {
	http.SetCookie(w, &http.Cookie{
		Name:     cartCookieName,
		Value:    cartID.String(),
		Path:     "/",
		MaxAge:   30 * 24 * 60 * 60, // 30 days
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// handleCartAdd adds a variant to the cart.
func (d *Deps) handleCartAdd(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	variantID, err := uuid.Parse(r.FormValue("variant_id"))
	if err != nil {
		http.Error(w, "invalid variant", http.StatusBadRequest)
		return
	}

	quantity := 1
	if q, err := strconv.Atoi(r.FormValue("quantity")); err == nil && q > 0 {
		quantity = q
	}

	cartID := getCartID(r)

	var resultCartID uuid.UUID
	var itemCount int

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		cart, txErr := d.CartService.GetOrCreateCart(ctx, tx, cartID)
		if txErr != nil {
			return txErr
		}
		resultCartID = cart.ID

		_, txErr = d.CartService.AddItem(ctx, tx, cart.ID, variantID, quantity)
		if txErr != nil {
			return txErr
		}

		itemCount, txErr = d.CartService.GetItemCount(ctx, tx, cart.ID)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	setCartCookie(w, resultCartID)

	if IsHTMX(r) {
		// Return the updated cart count badge for the header
		storefront.CartBadge(itemCount).Render(ctx, w) //nolint:errcheck
		return
	}
	http.Redirect(w, r, "/cart", http.StatusSeeOther)
}

// handleCartView renders the cart page.
func (d *Deps) handleCartView(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	cartID := getCartID(r)
	if cartID == nil {
		props := storefront.CartPageProps{}
		if IsHTMX(r) {
			storefront.CartContent(props).Render(ctx, w) //nolint:errcheck
			return
		}
		storefront.CartPage(props).Render(ctx, w) //nolint:errcheck
		return
	}

	var items []storefront.CartItemDisplay
	var subtotal int

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		cartItems, txErr := d.CartService.ListItems(ctx, tx, *cartID)
		if txErr != nil {
			return txErr
		}

		for _, ci := range cartItems {
			// Look up variant info for display
			variant, vErr := d.CatalogService.GetVariant(ctx, tx, ci.VariantID)
			if vErr != nil {
				return vErr
			}

			// Get product for title
			product, pErr := d.CatalogService.GetProduct(ctx, tx, variant.ProductID)
			if pErr != nil {
				return pErr
			}

			lineTotal := ci.UnitPrice * ci.Quantity
			subtotal += lineTotal

			items = append(items, storefront.CartItemDisplay{
				ItemID:       ci.ID,
				VariantID:    ci.VariantID,
				ProductTitle: product.Title,
				SKU:          variant.SKU,
				Quantity:     ci.Quantity,
				UnitPrice:    ci.UnitPrice,
				LineTotal:    lineTotal,
			})
		}
		return nil
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	props := storefront.CartPageProps{
		Items:    items,
		Subtotal: subtotal,
	}
	if IsHTMX(r) {
		storefront.CartContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	storefront.CartPage(props).Render(ctx, w) //nolint:errcheck
}

// handleCartUpdateQuantity updates the quantity of a cart item.
func (d *Deps) handleCartUpdateQuantity(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	itemID, err := uuid.Parse(r.FormValue("item_id"))
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

	// Re-render the cart page
	if IsHTMX(r) {
		d.handleCartView(w, r)
		return
	}
	http.Redirect(w, r, "/cart", http.StatusSeeOther)
}

// handleCartRemoveItem removes an item from the cart.
func (d *Deps) handleCartRemoveItem(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	itemID, err := uuid.Parse(r.FormValue("item_id"))
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

	if IsHTMX(r) {
		// For remove, we need to update both the cart content and the badge
		d.handleCartView(w, r)
		return
	}
	http.Redirect(w, r, "/cart", http.StatusSeeOther)
}

// cartItemCountFromCookie is a helper to get the cart item count for header rendering.
func (d *Deps) cartItemCountFromCookie(r *http.Request) int {
	cartID := getCartID(r)
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

// renderCartBadge writes the cart badge partial with current count.
func (d *Deps) renderCartBadge(w http.ResponseWriter, r *http.Request) {
	count := d.cartItemCountFromCookie(r)
	storefront.CartBadge(count).Render(r.Context(), w) //nolint:errcheck
}

// handleCartCount returns just the cart badge (for htmx polling or OOB swaps).
func (d *Deps) handleCartCount(w http.ResponseWriter, r *http.Request) {
	d.renderCartBadge(w, r)
}
