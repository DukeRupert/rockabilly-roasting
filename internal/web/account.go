package web

import (
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/auth"
	mediapkg "github.com/dukerupert/hiri/internal/platform/media"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/storefront"
)

// --- Account Settings ---

func (d *Deps) handleAccountSettings(w http.ResponseWriter, r *http.Request) {
	customer, _ := auth.CustomerFromContext(r.Context())

	props := storefront.AccountSettingsProps{
		Customer:  customer,
		CartCount: d.cartItemCountFromCookie(r),
	}

	if IsHTMX(r) {
		storefront.AccountSettingsContent(props).Render(r.Context(), w) //nolint:errcheck
		return
	}
	storefront.AccountSettingsPage(props).Render(r.Context(), w) //nolint:errcheck
}

func (d *Deps) handleAccountSettingsUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customer, _ := auth.CustomerFromContext(ctx)

	firstName := strings.TrimSpace(r.FormValue("first_name"))
	lastName := strings.TrimSpace(r.FormValue("last_name"))
	email := strings.TrimSpace(r.FormValue("email"))

	if firstName == "" || lastName == "" || email == "" {
		props := storefront.AccountSettingsProps{
			Customer:  customer,
			CartCount: d.cartItemCountFromCookie(r),
			Error:     "All fields are required.",
		}
		if IsHTMX(r) {
			storefront.AccountSettingsContent(props).Render(ctx, w) //nolint:errcheck
			return
		}
		storefront.AccountSettingsPage(props).Render(ctx, w) //nolint:errcheck
		return
	}

	var updated *domain.Customer
	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		updated, txErr = d.CustomerService.UpdateName(ctx, tx, customer.ID, firstName, lastName)
		if txErr != nil {
			return txErr
		}
		if email != customer.Email {
			updated, txErr = d.CustomerService.UpdateEmail(ctx, tx, customer.ID, email)
			if txErr != nil {
				return txErr
			}
		}
		return nil
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	props := storefront.AccountSettingsProps{
		Customer:  updated,
		CartCount: d.cartItemCountFromCookie(r),
		Success:   "Your settings have been updated.",
	}
	if IsHTMX(r) {
		storefront.AccountSettingsContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	storefront.AccountSettingsPage(props).Render(ctx, w) //nolint:errcheck
}

// --- Order History ---

func (d *Deps) handleAccountOrders(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customer, _ := auth.CustomerFromContext(ctx)

	var orders []domain.Order

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		orders, txErr = d.OrderService.ListOrders(ctx, tx, store.OrderFilter{
			CustomerID: &customer.ID,
			Limit:      50,
		})
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	props := storefront.AccountOrdersProps{
		Customer:  customer,
		Orders:    orders,
		CartCount: d.cartItemCountFromCookie(r),
	}

	if IsHTMX(r) {
		storefront.AccountOrdersContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	storefront.AccountOrdersPage(props).Render(ctx, w) //nolint:errcheck
}

func (d *Deps) handleAccountOrderShow(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customer, _ := auth.CustomerFromContext(ctx)

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var order *domain.Order
	var lineItems []domain.LineItem
	var shipments []domain.Shipment
	var shippingAddr *domain.Address

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		// scoping: AsStaff is safe — ownership is enforced immediately below.
		order, txErr = d.OrderService.GetOrderAsStaff(ctx, tx, id)
		if txErr != nil {
			return txErr
		}
		// Enforce ownership — return 404 if not this customer's order
		if order.CustomerID == nil || *order.CustomerID != customer.ID {
			return app.ErrOrderNotFound
		}
		lineItems, txErr = d.OrderService.ListLineItems(ctx, tx, id)
		if txErr != nil {
			return txErr
		}
		shipments, txErr = d.FulfillmentService.ListShipmentsByOrder(ctx, tx, id)
		if txErr != nil {
			return txErr
		}
		// scoping: address ID is drawn from the already-ownership-checked order.
		shippingAddr, txErr = d.CustomerService.GetAddressByIDAsStaff(ctx, tx, order.ShippingAddressID)
		if txErr != nil {
			return txErr
		}
		return nil
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	props := storefront.AccountOrderShowProps{
		Customer:  customer,
		Order:     order,
		LineItems: lineItems,
		Shipments: shipments,
		Address:   shippingAddr,
		CartCount: d.cartItemCountFromCookie(r),
	}

	if IsHTMX(r) {
		storefront.AccountOrderShowContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	storefront.AccountOrderShowPage(props).Render(ctx, w) //nolint:errcheck
}

// --- Subscriptions ---

func (d *Deps) handleAccountSubscriptions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customer, _ := auth.CustomerFromContext(ctx)

	var rows []storefront.AccountSubscriptionRow

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		subs, txErr := d.SubscriptionService.ListSubscriptionsByCustomer(ctx, tx, customer.ID)
		if txErr != nil {
			return txErr
		}

		plans := map[uuid.UUID]*domain.SubscriptionPlan{}
		products := map[uuid.UUID]*domain.Product{}
		thumbs := map[uuid.UUID]string{}

		rows = make([]storefront.AccountSubscriptionRow, 0, len(subs))
		for i := range subs {
			sub := subs[i]
			row := storefront.AccountSubscriptionRow{Subscription: sub}

			if plan, ok := plans[sub.PlanID]; ok {
				row.Plan = plan
			} else {
				plan, pErr := d.SubscriptionService.GetPlan(ctx, tx, sub.PlanID)
				if pErr != nil {
					return pErr
				}
				plans[sub.PlanID] = plan
				row.Plan = plan
			}

			variant, vErr := d.CatalogService.GetVariant(ctx, tx, sub.VariantID)
			if vErr != nil {
				return vErr
			}
			row.Variant = variant

			if product, ok := products[variant.ProductID]; ok {
				row.Product = product
			} else {
				product, prErr := d.CatalogService.GetProduct(ctx, tx, variant.ProductID)
				if prErr != nil {
					return prErr
				}
				products[variant.ProductID] = product
				row.Product = product
			}

			if url, ok := thumbs[variant.ProductID]; ok {
				row.ThumbnailURL = url
			} else {
				media, mErr := d.CatalogService.ListProductMedia(ctx, tx, variant.ProductID)
				if mErr != nil {
					return mErr
				}
				if len(media) > 0 {
					url := d.MediaConfig.ProductImageURL(media[0].R2Key, mediapkg.VariantCard)
					thumbs[variant.ProductID] = url
					row.ThumbnailURL = url
				}
			}

			price, prErr := d.PricingService.GetBasePrice(ctx, tx, variant.ID, "USD")
			if prErr == nil && row.Plan != nil {
				unit := price.Amount
				if row.Plan.DiscountPct > 0 {
					unit = unit - (unit*row.Plan.DiscountPct)/100
				}
				row.UnitPrice = &unit
			}

			rows = append(rows, row)
		}
		return nil
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	props := storefront.AccountSubscriptionsProps{
		Customer:  customer,
		Rows:      rows,
		CartCount: d.cartItemCountFromCookie(r),
	}

	if IsHTMX(r) {
		storefront.AccountSubscriptionsContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	storefront.AccountSubscriptionsPage(props).Render(ctx, w) //nolint:errcheck
}

func (d *Deps) handleAccountSubscriptionPause(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customer, _ := auth.CustomerFromContext(ctx)

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		// Verify ownership
		sub, txErr := d.SubscriptionService.GetSubscriptionByCustomer(ctx, tx, id, customer.ID)
		if txErr != nil {
			return txErr
		}
		_ = sub
		_, txErr = d.SubscriptionService.PauseSubscription(ctx, tx, id, nil, customerActor(r))
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/account/subscriptions", http.StatusSeeOther)
}

func (d *Deps) handleAccountSubscriptionResume(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customer, _ := auth.CustomerFromContext(ctx)

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		sub, txErr := d.SubscriptionService.GetSubscriptionByCustomer(ctx, tx, id, customer.ID)
		if txErr != nil {
			return txErr
		}
		_ = sub
		_, txErr = d.SubscriptionService.ResumeSubscription(ctx, tx, id, customerActor(r))
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/account/subscriptions", http.StatusSeeOther)
}

func (d *Deps) handleAccountSubscriptionCancel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customer, _ := auth.CustomerFromContext(ctx)

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		sub, txErr := d.SubscriptionService.GetSubscriptionByCustomer(ctx, tx, id, customer.ID)
		if txErr != nil {
			return txErr
		}
		_ = sub
		_, txErr = d.SubscriptionService.CancelSubscription(ctx, tx, id, customerActor(r))
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/account/subscriptions", http.StatusSeeOther)
}

// --- Addresses ---

func (d *Deps) handleAccountAddresses(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customer, _ := auth.CustomerFromContext(ctx)

	var addresses []domain.Address

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		addresses, txErr = d.CustomerService.ListAddresses(ctx, tx, customer.ID)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	props := storefront.AccountAddressesProps{
		Customer:  customer,
		Addresses: addresses,
		CartCount: d.cartItemCountFromCookie(r),
	}

	if IsHTMX(r) {
		storefront.AccountAddressesContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	storefront.AccountAddressesPage(props).Render(ctx, w) //nolint:errcheck
}

func (d *Deps) handleAccountAddressCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customer, _ := auth.CustomerFromContext(ctx)

	p := store.CreateAddressParams{
		CustomerID:  &customer.ID,
		FirstName:   strings.TrimSpace(r.FormValue("first_name")),
		LastName:    strings.TrimSpace(r.FormValue("last_name")),
		Line1:       strings.TrimSpace(r.FormValue("line1")),
		City:        strings.TrimSpace(r.FormValue("city")),
		State:       strings.TrimSpace(r.FormValue("state")),
		PostalCode:  strings.TrimSpace(r.FormValue("postal_code")),
		CountryCode: strings.TrimSpace(r.FormValue("country_code")),
	}
	if p.CountryCode == "" {
		p.CountryCode = "US"
	}
	if line2 := strings.TrimSpace(r.FormValue("line2")); line2 != "" {
		p.Line2 = &line2
	}
	if company := strings.TrimSpace(r.FormValue("company")); company != "" {
		p.Company = &company
	}

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.CustomerService.CreateAddress(ctx, tx, p, customerActor(r))
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/account/addresses", http.StatusSeeOther)
}

func (d *Deps) handleAccountAddressUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customer, _ := auth.CustomerFromContext(ctx)

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	p := store.CreateAddressParams{
		FirstName:   strings.TrimSpace(r.FormValue("first_name")),
		LastName:    strings.TrimSpace(r.FormValue("last_name")),
		Line1:       strings.TrimSpace(r.FormValue("line1")),
		City:        strings.TrimSpace(r.FormValue("city")),
		State:       strings.TrimSpace(r.FormValue("state")),
		PostalCode:  strings.TrimSpace(r.FormValue("postal_code")),
		CountryCode: strings.TrimSpace(r.FormValue("country_code")),
	}
	if p.CountryCode == "" {
		p.CountryCode = "US"
	}
	if line2 := strings.TrimSpace(r.FormValue("line2")); line2 != "" {
		p.Line2 = &line2
	}
	if company := strings.TrimSpace(r.FormValue("company")); company != "" {
		p.Company = &company
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.CustomerService.UpdateAddress(ctx, tx, id, customer.ID, p, customerActor(r))
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/account/addresses", http.StatusSeeOther)
}

func (d *Deps) handleAccountAddressDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customer, _ := auth.CustomerFromContext(ctx)

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		// Don't allow deleting the last address
		count, txErr := d.CustomerService.CountAddresses(ctx, tx, customer.ID)
		if txErr != nil {
			return txErr
		}
		if count <= 1 {
			return app.ErrLastAddress
		}
		return d.CustomerService.DeleteAddress(ctx, tx, id, customer.ID, customerActor(r))
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/account/addresses", http.StatusSeeOther)
}

func (d *Deps) handleAccountAddressSetDefault(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customer, _ := auth.CustomerFromContext(ctx)

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.CustomerService.SetDefaultAddress(ctx, tx, id, customer.ID)
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/account/addresses", http.StatusSeeOther)
}
