package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/jobs"
	"github.com/dukerupert/hiri/internal/platform/auth"
	mediapkg "github.com/dukerupert/hiri/internal/platform/media"
	"github.com/dukerupert/hiri/internal/platform/ratelimit"
	"github.com/dukerupert/hiri/internal/platform/turnstile"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/layouts"
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
		CartCount:        d.cartItemCountFromCookie(r),
		TurnstileSiteKey: d.TurnstileSiteKey,
	}
	if IsHTMX(r) {
		storefront.WholesaleApplyContent(props).Render(r.Context(), w) //nolint:errcheck
		return
	}
	storefront.WholesaleApplyPage(props).Render(r.Context(), w) //nolint:errcheck
}

func (d *Deps) handleWholesaleApply(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	// Honeypot — hidden field never filled by real users. Silently accept and
	// render the same confirmation so bots can't distinguish a hit from a miss.
	if strings.TrimSpace(r.FormValue("fax")) != "" {
		storefront.WholesaleApplyConfirmation().Render(r.Context(), w) //nolint:errcheck
		return
	}

	// Cloudflare Turnstile — only enforced when a secret is configured. Real
	// users get a visible failure they can retry; bots that can't solve the
	// challenge are stopped before any DB work happens.
	if d.TurnstileVerifier.Enabled() {
		token := r.FormValue("cf-turnstile-response")
		if err := d.TurnstileVerifier.Verify(ctx, token, ratelimit.ClientIP(r)); err != nil {
			if errors.Is(err, turnstile.ErrInvalidToken) {
				http.Error(w, "Bot check failed. Please refresh the page and try again.", http.StatusBadRequest)
				return
			}
			d.Logger.Warn("turnstile verify failed", "error", err)
			http.Error(w, "Bot check unavailable. Please try again in a moment.", http.StatusServiceUnavailable)
			return
		}
	}

	p := app.ApplyParams{
		Email:       strings.TrimSpace(r.FormValue("email")),
		FirstName:   strings.TrimSpace(r.FormValue("first_name")),
		LastName:    strings.TrimSpace(r.FormValue("last_name")),
		CompanyName: strings.TrimSpace(r.FormValue("company_name")),
	}
	if phone := strings.TrimSpace(r.FormValue("phone")); phone != "" {
		p.Phone = &phone
	}
	if website := strings.TrimSpace(r.FormValue("website")); website != "" {
		p.Website = &website
	}

	if p.Email == "" || p.FirstName == "" || p.LastName == "" || p.CompanyName == "" {
		http.Error(w, "All required fields must be filled", http.StatusBadRequest)
		return
	}

	// Length caps reject oversized payloads from automated probes.
	if len(p.Email) > 254 ||
		len(p.FirstName) > 100 || len(p.LastName) > 100 ||
		len(p.CompanyName) > 200 ||
		(p.Phone != nil && len(*p.Phone) > 30) ||
		(p.Website != nil && len(*p.Website) > 255) {
		http.Error(w, "Input too long", http.StatusBadRequest)
		return
	}

	if _, err := mail.ParseAddress(p.Email); err != nil {
		http.Error(w, "Invalid email address", http.StatusBadRequest)
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

	if len(password) < 10 {
		renderErr("Password must be at least 10 characters.")
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
			renderErr("Password must be at least 10 characters.")
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
	// The sheet pre-fills each row with what's already in the cart, so the
	// portal always shows the buyer's current order (and bulk-add can use set
	// semantics — resubmitting the sheet never doubles quantities).
	cartQty := map[uuid.UUID]int{}
	var lastOrder *domain.Order
	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		// Access identity comes from the membership join (the source of truth), not the
		// deprecated customer.customer_group_id column.
		viewer, txErr := d.CatalogService.ResolveViewer(ctx, tx, customer.ID)
		if txErr != nil {
			return txErr
		}
		products, txErr = d.WholesaleService.QuickOrderCatalog(ctx, tx, viewer.GroupIDs, customer.ID, d.PricingService, "USD")
		if txErr != nil {
			return txErr
		}
		if cartID := getWholesaleCartID(r); cartID != nil {
			cart, txErr := d.CartService.GetCart(ctx, tx, *cartID)
			if txErr != nil && !errors.Is(txErr, app.ErrCartNotFound) {
				return txErr
			}
			if txErr == nil {
				items, txErr := d.CartService.ListItems(ctx, tx, cart.ID)
				if txErr != nil {
					return txErr
				}
				for _, ci := range items {
					cartQty[ci.VariantID] = ci.Quantity
				}
			}
		}

		// The buyer's most recent order powers the one-click "reorder last
		// order" shortcut — restocking the same lineup is the core job here.
		channel := domain.OrderChannelWholesale
		recent, txErr := d.OrderService.ListOrders(ctx, tx, store.OrderFilter{
			CustomerID:         &customer.ID,
			Channel:            &channel,
			Limit:              1,
			ExcludeUnconfirmed: true,
		})
		if txErr != nil {
			return txErr
		}
		if len(recent) > 0 {
			lastOrder = &recent[0]
		}
		return nil
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
				CartQty:      cartQty[v.ID],
			}
		}
		imageURL := ""
		if p.ImageURL != "" {
			imageURL = d.MediaConfig.ProductImageURL(p.ImageURL, mediapkg.VariantCard)
		}
		templateProducts[i] = storefront.QuickOrderProduct{
			ID:       p.ID,
			Title:    p.Title,
			ImageURL: imageURL,
			Options:  p.Options,
			Variants: variants,
		}
	}

	props := storefront.WholesalePortalProps{
		CompanyName: companyName,
		Products:    templateProducts,
		CartCount:   d.wholesaleCartItemCount(r),
	}
	if lastOrder != nil {
		props.LastOrder = &storefront.PortalLastOrder{
			ID:       lastOrder.ID,
			Number:   lastOrder.Number,
			PlacedAt: lastOrder.PlacedAt,
			Total:    lastOrder.Total,
		}
	}

	if IsHTMX(r) {
		storefront.WholesalePortalContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	storefront.WholesalePortalPage(props).Render(ctx, w) //nolint:errcheck
}

func (d *Deps) handleWholesaleBulkAdd(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customer, ok := auth.CustomerFromContext(ctx)
	if !ok {
		http.Redirect(w, r, "/wholesale/login", http.StatusSeeOther)
		return
	}

	if err := r.ParseForm(); err != nil {
		Error(w, r, fmt.Errorf("parse form: %w", err))
		return
	}

	// Parse qty[variant_id] = quantity from form. The sheet's inputs pre-fill
	// with the current cart, so the submitted quantities are the whole order:
	// zero/blank means "this variant should not be in the order". Every field
	// present is kept — positive quantities are applied with set semantics
	// (never incremented), and zeroes clear the line if one exists.
	type bulkItem struct {
		VariantID uuid.UUID
		Quantity  int
	}
	var items []bulkItem
	anyPositive := false
	for key, values := range r.Form {
		if len(key) <= 4 || key[:4] != "qty[" || key[len(key)-1] != ']' {
			continue
		}
		variantIDStr := key[4 : len(key)-1]
		variantID, err := uuid.Parse(variantIDStr)
		if err != nil {
			continue
		}
		qty := 0
		if len(values) > 0 && values[0] != "" {
			qty, err = strconv.Atoi(values[0])
			if err != nil || qty < 0 {
				continue
			}
		}
		if qty > 0 {
			anyPositive = true
		}
		items = append(items, bulkItem{VariantID: variantID, Quantity: qty})
	}

	cartID := getWholesaleCartID(r)

	// Nothing ordered and no cart to clear — stay on the sheet.
	if !anyPositive && cartID == nil {
		http.Redirect(w, r, "/wholesale/portal", http.StatusSeeOther)
		return
	}

	var resultCartID uuid.UUID
	var skipped int
	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		cart, txErr := d.CartService.GetOrCreateCart(ctx, tx, cartID)
		if txErr != nil {
			return txErr
		}
		resultCartID = cart.ID

		// Only issue removals for variants actually in the cart — most rows on
		// a fresh sheet are blank and need no write at all.
		inCart := map[uuid.UUID]bool{}
		existing, txErr := d.CartService.ListItems(ctx, tx, cart.ID)
		if txErr != nil {
			return txErr
		}
		for _, ci := range existing {
			inCart[ci.VariantID] = true
		}

		for _, item := range items {
			if item.Quantity == 0 && !inCart[item.VariantID] {
				continue
			}
			_, txErr := d.CartService.SetItemForCustomer(ctx, tx, cart.ID, item.VariantID, item.Quantity, customer.ID, "USD")
			switch {
			case txErr == nil:
			case errors.Is(txErr, app.ErrVariantArchived),
				errors.Is(txErr, app.ErrVariantNotFound),
				errors.Is(txErr, app.ErrVariantNotInChannel),
				errors.Is(txErr, app.ErrProductNotAccessible),
				errors.Is(txErr, app.ErrPriceNotFound):
				// A variant retired between the sheet render and the submit —
				// leave that line off and keep the rest of the order rather
				// than failing the whole sheet (and losing every quantity the
				// buyer typed).
				skipped++
			default:
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
	dest := "/wholesale/checkout"
	if skipped > 0 {
		dest += fmt.Sprintf("?sheet_skipped=%d", skipped)
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

// handleWholesaleReorder re-adds every line item from a past order to the
// wholesale cart at current pricing, then sends the buyer to checkout to review.
// Reordering is the core wholesale job — restocking the same lineup on a regular
// cadence — so this is one click from order history.
//
// Items whose variant has since been archived, hidden from wholesale, or lost
// its price are skipped and counted rather than failing the whole reorder, so a
// café restocking last month's order is told what changed instead of silently
// shorted. Current prices apply, so the checkout's existing price-change review
// still governs the final amount.
func (d *Deps) handleWholesaleReorder(w http.ResponseWriter, r *http.Request) {
	customer, ok := auth.CustomerFromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/wholesale/login", http.StatusSeeOther)
		return
	}

	orderID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	d.reorderIntoCart(w, r, customer, orderID)
}

// handleWholesaleReorderLatest is the GET entry point used by the weekly
// reminder email's "Reorder This" button. Email clients can only issue GET and
// the email carries no order ID, so the customer's last order is resolved
// server-side at click time — which also keeps order IDs out of URLs that sit
// in inboxes indefinitely.
//
// A mutating GET is acceptable here precisely because the route is behind the
// wholesale auth guard: link scanners and inbox prefetchers arrive without a
// session cookie, so they get the login redirect and never touch a cart.
func (d *Deps) handleWholesaleReorderLatest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customer, ok := auth.CustomerFromContext(ctx)
	if !ok {
		http.Redirect(w, r, "/wholesale/login", http.StatusSeeOther)
		return
	}

	var orderID uuid.UUID
	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		orderID, txErr = d.WholesaleService.LastWholesaleOrderID(ctx, tx, customer.ID)
		return txErr
	})
	if errors.Is(err, app.ErrNoPreviousOrder) {
		// Nothing to copy — send them to the quick-order sheet rather than an
		// error; they came here to place an order either way. The portal's
		// "Same as last time?" card hides itself when there is no last order,
		// so the page explains the situation without a banner.
		http.Redirect(w, r, "/wholesale/portal", http.StatusSeeOther)
		return
	}
	if err != nil {
		Error(w, r, err)
		return
	}

	d.reorderIntoCart(w, r, customer, orderID)
}

// reorderIntoCart loads every line of orderID into the caller's wholesale cart
// and redirects to checkout. Shared by the order-history POST and the email
// GET so both behave identically.
func (d *Deps) reorderIntoCart(w http.ResponseWriter, r *http.Request, customer *domain.Customer, orderID uuid.UUID) {
	ctx := r.Context()
	cartID := getWholesaleCartID(r)
	var resultCartID uuid.UUID
	var added, skipped int

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		// scoping: AsStaff is safe — ownership is enforced immediately below.
		order, txErr := d.OrderService.GetOrderAsStaff(ctx, tx, orderID)
		if txErr != nil {
			return txErr
		}
		if order.CustomerID == nil || *order.CustomerID != customer.ID {
			return app.ErrOrderNotFound
		}
		items, txErr := d.OrderService.ListLineItems(ctx, tx, orderID)
		if txErr != nil {
			return txErr
		}

		cart, txErr := d.CartService.GetOrCreateCart(ctx, tx, cartID)
		if txErr != nil {
			return txErr
		}
		resultCartID = cart.ID

		for _, li := range items {
			if li.Quantity <= 0 {
				continue
			}
			// Set semantics keep reorder idempotent: clicking it twice (or on
			// top of a half-built cart) pins each line to the past order's
			// quantity instead of stacking on whatever was there.
			_, addErr := d.CartService.SetItemForCustomer(ctx, tx, cart.ID, li.VariantID, li.Quantity, customer.ID, order.CurrencyCode)
			switch {
			case addErr == nil:
				added++
			case errors.Is(addErr, app.ErrVariantArchived),
				errors.Is(addErr, app.ErrVariantNotFound),
				errors.Is(addErr, app.ErrVariantNotInChannel),
				errors.Is(addErr, app.ErrProductNotAccessible),
				errors.Is(addErr, app.ErrPriceNotFound):
				// Variant retired or no longer sold to this customer — leave it
				// off and tell them, rather than failing the whole reorder.
				skipped++
			default:
				return addErr
			}
		}
		return nil
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	setWholesaleCartCookie(w, resultCartID)

	dest := fmt.Sprintf("/wholesale/checkout?reordered=%d", added)
	if skipped > 0 {
		dest += fmt.Sprintf("&skipped=%d", skipped)
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

// reorderNotice builds the informational banner shown on checkout after a
// reorder (?reordered / ?skipped, set by handleWholesaleReorder) or after a
// quick-order submit that had to leave lines off (?sheet_skipped, set by
// handleWholesaleBulkAdd). Empty when neither applies.
func reorderNotice(r *http.Request) string {
	if n, _ := strconv.Atoi(r.URL.Query().Get("sheet_skipped")); n > 0 {
		return fmt.Sprintf("Left off %s that %s not available anymore — everything else came through.", itemCount(n), isAre(n))
	}
	reordered, _ := strconv.Atoi(r.URL.Query().Get("reordered"))
	skipped, _ := strconv.Atoi(r.URL.Query().Get("skipped"))
	if reordered <= 0 && skipped <= 0 {
		return ""
	}
	switch {
	case reordered == 0:
		return "None of the items from that order are available anymore, so nothing was added. Give us a shout and we'll sort it out."
	case skipped == 0:
		return fmt.Sprintf("Loaded %s from your past order — look it over and send it our way.", itemCount(reordered))
	default:
		return fmt.Sprintf("Loaded %s from your past order. %s aren't available anymore and were left off.", itemCount(reordered), itemCount(skipped))
	}
}

// itemCount renders "1 item" / "N items".
func itemCount(n int) string {
	if n == 1 {
		return "1 item"
	}
	return fmt.Sprintf("%d items", n)
}

func isAre(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

func (d *Deps) handleWholesaleCheckoutPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customer, ok := auth.CustomerFromContext(ctx)
	if !ok {
		http.Redirect(w, r, "/wholesale/login", http.StatusSeeOther)
		return
	}
	d.renderWholesaleCheckout(w, r, customer, false, "", 0)
}

// renderWholesaleCheckout renders the wholesale checkout page. If banner is true,
// the price-change banner is shown. errMsg, when non-empty, is surfaced as an
// inline error. status=0 means 200.
func (d *Deps) renderWholesaleCheckout(w http.ResponseWriter, r *http.Request, customer *domain.Customer, banner bool, errMsg string, status int) {
	ctx := r.Context()

	companyName := ""
	if customer.CompanyName != nil {
		companyName = *customer.CompanyName
	}

	cartID := getWholesaleCartID(r)
	var checkoutItems []storefront.WholesaleCheckoutItem
	var addresses []domain.Address
	var shipCfg *domain.ShippingConfig
	subtotal := 0
	// Collected alongside the line items so the page can warn about MOQ
	// violations before the buyer hits Place Order (which hard-rejects them).
	var cartItemsForMOQ []domain.CartItem
	var variantsForMOQ []domain.Variant
	productNameByVariant := map[uuid.UUID]string{}

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		addrs, txErr := d.CustomerService.ListAddresses(ctx, tx, customer.ID)
		if txErr != nil {
			return txErr
		}
		addresses = addrs

		// Shipping config drives the local-delivery toggle and the local ZIP
		// list the checkout uses to gate the delivery option.
		shipCfg, txErr = d.CheckoutService.GetShippingConfig(ctx, tx)
		if txErr != nil {
			return txErr
		}

		if cartID == nil {
			return nil
		}
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
				MinQty:      variant.WholesaleMinQty,
				Multiple:    variant.WholesaleMultiple,
			})

			cartItemsForMOQ = append(cartItemsForMOQ, domain.CartItem{VariantID: ci.VariantID, Quantity: ci.Quantity})
			variantsForMOQ = append(variantsForMOQ, *variant)
			productNameByVariant[ci.VariantID] = product.Title
		}
		return nil
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	defaultAddressID := ""
	for _, a := range addresses {
		if a.IsDefault {
			defaultAddressID = a.ID.String()
			break
		}
	}

	// Name every line that would fail MOQ validation at Place Order, so the
	// buyer can fix quantities here instead of being rejected at the last step.
	var moqProblems []string
	for _, v := range domain.ValidateWholesaleCart(cartItemsForMOQ, variantsForMOQ) {
		name := productNameByVariant[v.VariantID]
		if name == "" {
			name = v.VariantName
		}
		if v.Minimum > 0 {
			moqProblems = append(moqProblems, fmt.Sprintf("%s (%s) — you have %d, the minimum is %d.", name, v.VariantName, v.Ordered, v.Minimum))
		} else {
			moqProblems = append(moqProblems, fmt.Sprintf("%s (%s) — comes in multiples of %d, you have %d.", name, v.VariantName, v.Multiple, v.Ordered))
		}
	}

	props := storefront.WholesaleCheckoutProps{
		CompanyName:       companyName,
		Items:             checkoutItems,
		Subtotal:          subtotal,
		Error:             errMsg,
		Notice:            reorderNotice(r),
		CartCount:         d.wholesaleCartItemCount(r),
		PriceChangeBanner: banner,
		MOQProblems:       moqProblems,
		Addresses:         addresses,
		DefaultAddressID:  defaultAddressID,
	}
	if shipCfg != nil {
		props.LocalDeliveryEnabled = shipCfg.LocalDeliveryEnabled
		props.LocalZipCodes = shipCfg.LocalZipCodes
		props.LocalDeliveryDays = shipCfg.DeliveryDaysLabel()
		props.LocalPickupInstructions = shipCfg.LocalPickupInstructions
		if date, ok := shipCfg.NextDeliveryDate(time.Now(), d.MerchantTZ); ok {
			props.LocalDeliveryDate = domain.DeliveryDateLabel(date)
			props.LocalDeliveryCutoff = shipCfg.CutoffLabel()
		}
	}

	if status != 0 && !IsHTMX(r) {
		w.WriteHeader(status)
	}
	if IsHTMX(r) {
		storefront.WholesaleCheckoutContent(props).Render(ctx, w) //nolint:errcheck
		// Keep the header cart badge in sync — the main swap only replaces
		// #wholesale-checkout, so the count chip is updated out-of-band.
		layouts.CartBadgeOOB(props.CartCount).Render(ctx, w) //nolint:errcheck
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

	// Address selection: a saved address ID, or "new"/empty for the inline form.
	shipSel := r.FormValue("shipping_address_id")
	billSame := r.FormValue("billing_same") != ""
	billSel := r.FormValue("billing_address_id")

	// Validate new-address completeness up front (no DB) so we can re-render the
	// form with a friendly message instead of failing the order transaction.
	if isNewWholesaleAddr(shipSel) && !wholesaleNewAddrComplete(r, "ship") {
		d.Metrics.CheckoutFailed.WithLabelValues("wholesale", "address_incomplete").Inc()
		d.renderWholesaleCheckout(w, r, customer, false, "Please enter a complete shipping address — street, city, state, and ZIP.", http.StatusBadRequest)
		return
	}
	if !billSame && isNewWholesaleAddr(billSel) && !wholesaleNewAddrComplete(r, "bill") {
		d.Metrics.CheckoutFailed.WithLabelValues("wholesale", "address_incomplete").Inc()
		d.renderWholesaleCheckout(w, r, customer, false, "Please enter a complete billing address — street, city, state, and ZIP.", http.StatusBadRequest)
		return
	}

	actor := customerActor(r)

	var stale bool
	var orderNumber string
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

		// Last line of defence before money moves: if anything repriced while
		// this cart sat, rewrite it and send the buyer back to look rather than
		// charging a total they were never shown. CartService owns the
		// repricing rule — this handler only reacts to the outcome.
		moved, txErr := d.CartService.RepriceCart(ctx, tx, cart.ID, customer.ID, "USD")
		if txErr != nil {
			return txErr
		}
		if len(moved) > 0 {
			stale = true
			return nil
		}

		items := make([]app.CartItem, 0, len(cartItems))
		for _, ci := range cartItems {
			items = append(items, app.CartItem{
				VariantID: ci.VariantID,
				Quantity:  ci.Quantity,
				UnitPrice: ci.UnitPrice,
			})
		}

		// Resolve the chosen fulfillment method against the ship-to ZIP before
		// creating anything, so an invalid choice rolls the transaction back
		// cleanly. Pickup and free shipping are always allowed; local delivery
		// only inside the local zone. The ZIP comes from the new-address form
		// or, for a saved address, a read-only lookup.
		shipZip := strings.TrimSpace(r.FormValue("ship_postal_code"))
		if !isNewWholesaleAddr(shipSel) {
			if id, perr := uuid.Parse(shipSel); perr == nil {
				if a, gerr := d.CustomerService.GetAddress(ctx, tx, id, customer.ID); gerr == nil {
					shipZip = a.PostalCode
				}
			}
		}
		shipCfg, txErr := d.CheckoutService.GetShippingConfig(ctx, tx)
		if txErr != nil {
			return txErr
		}
		method := domain.ShippingMethod(r.FormValue("fulfillment_method"))
		if method == "" {
			method = domain.ShippingMethodShipped
		}
		if !shipCfg.WholesaleMethodAllowed(shipZip, method) {
			return app.ErrFulfillmentUnavailable
		}

		// Resolve the shipping address (creating it if the customer entered a
		// new one), then billing — defaulting to the shipping address.
		shipID, txErr := d.resolveWholesaleAddress(ctx, tx, customer, r, shipSel, "ship", actor)
		if txErr != nil {
			return txErr
		}
		billID := shipID
		if !billSame {
			billID, txErr = d.resolveWholesaleAddress(ctx, tx, customer, r, billSel, "bill", actor)
			if txErr != nil {
				return txErr
			}
		}

		orderParams := app.PlaceWholesaleOrderParams{
			CustomerID:   customer.ID,
			Items:        items,
			CurrencyCode: "USD",
			// Wholesale is invoiced; shipping is negotiated offline and billed
			// on the invoice, not calculated at checkout.
			ShippingCents:     0,
			ShippingAddressID: shipID,
			BillingAddressID:  billID,
			ShippingMethod:    &method,
		}
		if poNumber != "" {
			orderParams.CustomerPONumber = &poNumber
		}
		if notes != "" {
			orderParams.Notes = &notes
		}

		order, txErr := d.WholesaleService.PlaceWholesaleOrder(ctx, tx, orderParams, actor)
		if txErr != nil {
			return txErr
		}
		orderNumber = order.Number

		// Send the buyer an order confirmation with line items, mirroring retail
		// checkout. The order_confirm job/template is channel-agnostic — wholesale
		// reuses it as-is. Rides on this tx so no email goes if the order rolls back.
		_, txErr = d.RiverClient.InsertTx(ctx, tx, jobs.OrderConfirmEmailArgs{
			OrderID:    order.ID,
			CustomerID: customer.ID,
		}, nil)
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
		// Address problems are fixable on this page — re-render the checkout
		// with a plain-language message and the form intact rather than bouncing
		// the buyer to a generic error toast.
		switch {
		case errors.Is(err, app.ErrAddressIncomplete):
			d.Metrics.CheckoutFailed.WithLabelValues("wholesale", "address_incomplete").Inc()
			d.renderWholesaleCheckout(w, r, customer, false, "That address is missing its street, city, state, or ZIP. Pick another saved address or enter a new one below.", http.StatusUnprocessableEntity)
			return
		case errors.Is(err, app.ErrAddressNotFound):
			d.Metrics.CheckoutFailed.WithLabelValues("wholesale", "address_incomplete").Inc()
			d.renderWholesaleCheckout(w, r, customer, false, "That saved address is no longer available. Pick another or enter a new one below.", http.StatusUnprocessableEntity)
			return
		case errors.Is(err, app.ErrFulfillmentUnavailable):
			d.Metrics.CheckoutFailed.WithLabelValues("wholesale", "fulfillment_unavailable").Inc()
			d.renderWholesaleCheckout(w, r, customer, false, "Local delivery isn't available for that ZIP. Choose pickup or free shipping.", http.StatusUnprocessableEntity)
			return
		case errors.Is(err, app.ErrMOQViolation):
			// The re-render recomputes the per-line violations from the cart, so
			// the banner below names exactly which lines are short.
			d.Metrics.CheckoutFailed.WithLabelValues("wholesale", "moq_violation").Inc()
			d.renderWholesaleCheckout(w, r, customer, false, "Some lines don't meet wholesale minimums yet — adjust the quantities flagged below and place the order again.", http.StatusUnprocessableEntity)
			return
		}
		reason := classifyCheckoutError(err)
		d.Metrics.CheckoutFailed.WithLabelValues("wholesale", reason).Inc()
		Error(w, r, err)
		return
	}

	if stale {
		d.Metrics.CheckoutFailed.WithLabelValues("wholesale", "prices_stale").Inc()
		d.renderWholesaleCheckout(w, r, customer, true, "", http.StatusConflict)
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

	// Send the buyer to a confirmation page. For htmx (the form is hx-boosted),
	// HX-Redirect triggers a full browser navigation rather than a content swap —
	// that re-renders the layout header so the cart badge clears (a boosted swap
	// only replaces #main-content and would leave the stale count behind).
	confirmURL := "/wholesale/order-confirmed"
	if orderNumber != "" {
		confirmURL += "?number=" + url.QueryEscape(orderNumber)
	}
	if IsHTMX(r) {
		w.Header().Set("HX-Redirect", confirmURL)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, confirmURL, http.StatusSeeOther)
}

// handleWholesaleOrderConfirmed renders the post-checkout success page. The cart
// has already been cleared by the confirm handler, so CartCount is always 0.
func (d *Deps) handleWholesaleOrderConfirmed(w http.ResponseWriter, r *http.Request) {
	customer, ok := auth.CustomerFromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/wholesale/login", http.StatusSeeOther)
		return
	}

	companyName := ""
	if customer.CompanyName != nil {
		companyName = *customer.CompanyName
	}

	props := storefront.WholesaleOrderConfirmedProps{
		CompanyName: companyName,
		OrderNumber: r.URL.Query().Get("number"),
		CartCount:   0,
	}

	if IsHTMX(r) {
		storefront.WholesaleOrderConfirmedContent(props).Render(r.Context(), w) //nolint:errcheck
		return
	}
	storefront.WholesaleOrderConfirmedPage(props).Render(r.Context(), w) //nolint:errcheck
}

// isNewWholesaleAddr reports whether an address selection means "enter a new
// address" rather than referencing a saved one.
func isNewWholesaleAddr(sel string) bool {
	return sel == "" || sel == "new"
}

// wholesaleNewAddrComplete checks the minimum required fields for a new address
// submitted under the given form prefix ("ship"/"bill").
func wholesaleNewAddrComplete(r *http.Request, prefix string) bool {
	return strings.TrimSpace(r.FormValue(prefix+"_line1")) != "" &&
		strings.TrimSpace(r.FormValue(prefix+"_city")) != "" &&
		strings.TrimSpace(r.FormValue(prefix+"_state")) != "" &&
		strings.TrimSpace(r.FormValue(prefix+"_postal_code")) != ""
}

// resolveWholesaleAddress turns an address selection into an address ID. A saved
// selection is validated against the customer's own addresses; "new"/empty
// creates an address from the prefixed form fields within the same transaction
// so it commits atomically with the order. Completeness is validated by the
// caller before the transaction starts.
func (d *Deps) resolveWholesaleAddress(ctx context.Context, tx pgx.Tx, customer *domain.Customer, r *http.Request, sel, prefix string, actor app.Actor) (uuid.UUID, error) {
	if !isNewWholesaleAddr(sel) {
		id, err := uuid.Parse(sel)
		if err != nil {
			return uuid.Nil, app.ErrAddressNotFound
		}
		addr, err := d.CustomerService.GetAddress(ctx, tx, id, customer.ID)
		if err != nil {
			return uuid.Nil, err
		}
		// A saved address (e.g. imported from a prior system) can be missing the
		// fields we need to ship. Catch it here so the buyer gets a fix-it
		// message rather than a downstream failure.
		if !addressShippable(addr) {
			return uuid.Nil, app.ErrAddressIncomplete
		}
		return addr.ID, nil
	}

	p := store.CreateAddressParams{
		CustomerID:  &customer.ID,
		FirstName:   strings.TrimSpace(r.FormValue(prefix + "_first_name")),
		LastName:    strings.TrimSpace(r.FormValue(prefix + "_last_name")),
		Line1:       strings.TrimSpace(r.FormValue(prefix + "_line1")),
		City:        strings.TrimSpace(r.FormValue(prefix + "_city")),
		State:       strings.TrimSpace(r.FormValue(prefix + "_state")),
		PostalCode:  strings.TrimSpace(r.FormValue(prefix + "_postal_code")),
		CountryCode: strings.TrimSpace(r.FormValue(prefix + "_country_code")),
	}
	if p.CountryCode == "" {
		p.CountryCode = "US"
	}
	if v := strings.TrimSpace(r.FormValue(prefix + "_line2")); v != "" {
		p.Line2 = &v
	}
	if v := strings.TrimSpace(r.FormValue(prefix + "_company")); v != "" {
		p.Company = &v
	}

	addr, err := d.CustomerService.CreateAddress(ctx, tx, p, actor)
	if err != nil {
		return uuid.Nil, err
	}
	return addr.ID, nil
}

// handleWholesaleCartUpdate updates the quantity of a cart item inline, at the
// customer's price for the new quantity.
func (d *Deps) handleWholesaleCartUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	customer, ok := auth.CustomerFromContext(ctx)
	if !ok {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}

	cartID := getWholesaleCartID(r)
	if cartID == nil {
		http.Error(w, "no cart", http.StatusBadRequest)
		return
	}

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
		_, txErr := d.CartService.UpdateItemQuantityForCustomer(ctx, tx, *cartID, itemID, quantity, customer.ID, "USD")
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

	cartID := getWholesaleCartID(r)
	if cartID == nil {
		http.Error(w, "no cart", http.StatusBadRequest)
		return
	}

	itemID, err := uuid.Parse(r.URL.Query().Get("item_id"))
	if err != nil {
		http.Error(w, "invalid item", http.StatusBadRequest)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.CartService.RemoveItem(ctx, tx, *cartID, itemID)
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	d.handleWholesaleCheckoutPage(w, r)
}
