package web

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/jobs"
	"github.com/dukerupert/hiri/internal/platform/auth"
	mediapkg "github.com/dukerupert/hiri/internal/platform/media"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/storefront"
)

// --- Account Settings ---

func (d *Deps) handleAccountSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customer, _ := auth.CustomerFromContext(ctx)

	props := storefront.AccountSettingsProps{
		Customer:  customer,
		CartCount: d.cartItemCountFromCookie(r),
	}
	d.fillLocalFulfillmentPrefs(ctx, customer.ID, &props)

	if IsHTMX(r) {
		storefront.AccountSettingsContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	storefront.AccountSettingsPage(props).Render(ctx, w) //nolint:errcheck
}

// fillLocalFulfillmentPrefs decides whether to render the fulfillment-preference
// section on the account settings page. It renders when (a) at least one of the
// customer's saved addresses sits inside a local zip and (b) the merchant has at
// least one local channel enabled. In that case the customer has a real choice:
// the eligible local method(s) — delivery and/or pickup — plus the option to
// have their orders shipped ("mail it to me") instead. When no local channel is
// enabled, or no address is local, everything ships anyway and there's nothing
// to choose, so the section stays hidden.
func (d *Deps) fillLocalFulfillmentPrefs(ctx context.Context, customerID uuid.UUID, props *storefront.AccountSettingsProps) {
	_ = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		cfg, err := d.CheckoutService.GetShippingConfig(ctx, tx)
		if err != nil {
			return err
		}
		// With no local channel enabled, a local zip falls back to shipped
		// anyway — there's no trade-off to offer.
		if !cfg.LocalDeliveryEnabled && !cfg.LocalPickupEnabled {
			return nil
		}
		addrs, err := d.CustomerService.ListAddresses(ctx, tx, customerID)
		if err != nil {
			return err
		}
		hasLocal := false
		for _, a := range addrs {
			if cfg.IsLocal(a.PostalCode) {
				hasLocal = true
				break
			}
		}
		if !hasLocal {
			return nil
		}
		props.ShowLocalFulfillment = true
		props.LocalDeliveryEnabled = cfg.LocalDeliveryEnabled
		props.LocalPickupEnabled = cfg.LocalPickupEnabled
		props.LocalDeliveryDays = cfg.DeliveryDaysLabel()
		props.LocalPickupNote = cfg.LocalPickupInstructions
		return nil
	}) // best-effort; if it fails the section just doesn't render
}

func (d *Deps) handleAccountSettingsUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customer, _ := auth.CustomerFromContext(ctx)

	firstName := strings.TrimSpace(r.FormValue("first_name"))
	lastName := strings.TrimSpace(r.FormValue("last_name"))
	email := strings.TrimSpace(r.FormValue("email"))
	phone := strings.TrimSpace(r.FormValue("phone"))

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

	// Local fulfillment preference: empty string clears the preference.
	// Anything else is validated by the service layer.
	preferredRaw := strings.TrimSpace(r.FormValue("preferred_local_fulfillment"))
	var preferred *domain.ShippingMethod
	if preferredRaw != "" {
		m := domain.ShippingMethod(preferredRaw)
		preferred = &m
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
		// Phone: empty submission clears the field. Skip when unchanged so we
		// don't write no-op audit rows on every settings save.
		var phonePtr *string
		if phone != "" {
			phonePtr = &phone
		}
		if !samePhone(customer.Phone, phonePtr) {
			updated, txErr = d.CustomerService.UpdatePhone(ctx, tx, customer.ID, phonePtr, customerActor(r))
			if txErr != nil {
				return txErr
			}
		}
		// Only update the preference field when the form actually carried it
		// (preferredRaw == "" alone is ambiguous — could mean "clear" or "field
		// absent"). The settings form always submits the field, so if it's
		// missing entirely, leave the preference untouched.
		if _, ok := r.Form["preferred_local_fulfillment"]; ok {
			if perr := d.CustomerService.UpdatePreferredLocalFulfillmentSelf(ctx, tx, customer.ID, preferred); perr != nil {
				return perr
			}
			updated.PreferredLocalFulfillment = preferred
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
	d.fillLocalFulfillmentPrefs(ctx, customer.ID, &props)
	if IsHTMX(r) {
		storefront.AccountSettingsContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	storefront.AccountSettingsPage(props).Render(ctx, w) //nolint:errcheck
}

// samePhone reports whether two optional phone strings represent the same value.
func samePhone(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

// --- Order History ---

func (d *Deps) handleAccountOrders(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customer, _ := auth.CustomerFromContext(ctx)

	var orders []domain.Order

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		orders, txErr = d.OrderService.ListOrders(ctx, tx, store.OrderFilter{
			CustomerID:         &customer.ID,
			Limit:              50,
			ExcludeUnconfirmed: true,
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
	var lineItems []storefront.OrderLineItemView
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
		items, txErr := d.OrderService.ListLineItems(ctx, tx, id)
		if txErr != nil {
			return txErr
		}
		lineItems, txErr = d.enrichOrderLineItems(ctx, tx, items)
		if txErr != nil {
			return txErr
		}
		shipments, txErr = d.FulfillmentService.ListShipmentsByOrder(ctx, tx, id)
		if txErr != nil {
			return txErr
		}
		// scoping: address ID is drawn from the already-ownership-checked order.
		shippingAddr, txErr = d.CustomerService.GetAddressByIDAsStaff(ctx, tx, order.ShippingAddressID)
		if errors.Is(txErr, app.ErrAddressNotFound) {
			// Every order carries a shipping address row, so a miss here is
			// broken data rather than a bad request. ErrAddressNotFound maps to
			// 404, which would quietly hide that; keep it a 500 so it pages.
			return brokenReference("order", id, "address", order.ShippingAddressID)
		}
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
				if errors.Is(pErr, app.ErrSubscriptionPlanNotFound) {
					// The plan ID comes off a live subscription, so a miss is
					// broken data, not a bad URL. Do not let the 404 mapping
					// swallow it.
					return brokenReference("subscription", sub.ID, "plan", sub.PlanID)
				}
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
		// Asked of the service rather than derived here, so the date a paused
		// card promises is the one ResumeSubscription would actually set.
		ResumeOrderOn: d.SubscriptionService.ResumeOrderDate(time.Now()),
		MerchantTZ:    d.MerchantTZ,
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
		if _, txErr := d.SubscriptionService.GetSubscriptionByCustomer(ctx, tx, id, customer.ID); txErr != nil {
			return txErr
		}
		resumed, txErr := d.SubscriptionService.ResumeSubscription(ctx, tx, id, customerActor(r))
		if txErr != nil {
			return txErr
		}
		return d.enqueueResumeEmail(ctx, tx, resumed)
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/account/subscriptions", http.StatusSeeOther)
}

// enqueueResumeEmail queues the customer's resume notification in the same
// transaction as the resume itself, so the mail can never name a charge date a
// rolled-back resume never set. Shared by the account and admin handlers: a
// resume books a charge on the next renewal run, and the customer needs to hear that
// whoever pressed the button.
func (d *Deps) enqueueResumeEmail(ctx context.Context, tx pgx.Tx, sub *domain.Subscription) error {
	_, err := d.RiverClient.InsertTx(ctx, tx, jobs.SubscriptionResumedArgs{
		SubscriptionID: sub.ID,
		CustomerID:     sub.CustomerID,
	}, nil)
	return err
}

// handleAccountSubscriptionRetry lets a customer trigger an immediate renewal
// charge on a past-due subscription — the natural next step after updating
// their card in the billing portal. Enqueues the renewal job rather than
// charging inline so the request returns fast; the worker runs the same path
// the scheduler uses (success clears dunning, failure advances it). No-op
// guidance for non-past-due subs is handled by the button only rendering on
// past-due cards; the status check here is the authoritative guard.
func (d *Deps) handleAccountSubscriptionRetry(w http.ResponseWriter, r *http.Request) {
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
		if sub.Status != domain.SubscriptionStatusPastDue {
			// Nothing to retry — fall through to a clean redirect.
			return nil
		}
		// jobs.RenewalInsertOpts, never a literal — it is what stops a
		// double-click, the staff Retry button, and the scheduler's dunning rung
		// from each queueing their own charge attempt. Options that differ from
		// the other insert sites hash to a different unique_key and deduplicate
		// against none of them.
		res, txErr := d.RiverClient.InsertTx(ctx, tx, jobs.SubscriptionRenewalArgs{
			SubscriptionID: sub.ID,
		}, jobs.RenewalInsertOpts())
		if txErr != nil {
			return txErr
		}
		if res.UniqueSkippedAsDuplicate {
			// Deliberately silent to the customer: an attempt for this
			// subscription already exists today and this click rides on it, so
			// the redirect below tells them the same thing either way. Logged
			// because "I clicked retry and nothing happened" is otherwise
			// unanswerable from support's side.
			d.Logger.Info("customer renewal retry rode on an existing attempt",
				"subscription_id", sub.ID, "customer_id", customer.ID)
		}
		return nil
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/account/subscriptions", http.StatusSeeOther)
}

// handleAccountBillingPortal opens a Stripe-hosted Billing Portal session for
// the authenticated customer and redirects them to it. The portal lets them
// add/replace/remove payment methods on Stripe's side; nothing in our DB
// changes here. Returning to /account/subscriptions when they're done.
func (d *Deps) handleAccountBillingPortal(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customer, _ := auth.CustomerFromContext(ctx)

	if customer.StripeCustomerID == nil {
		// Customers without a Stripe ID never checked out — nothing for the
		// portal to manage. The button shouldn't render for them, so this is
		// purely defensive.
		d.Logger.Warn("billing portal requested but customer has no Stripe customer ID",
			"customer_id", customer.ID)
		http.Redirect(w, r, "/account/subscriptions", http.StatusSeeOther)
		return
	}

	url, err := d.PaymentProvider.CreatePortalSession(ctx, *customer.StripeCustomerID, d.BaseURL+"/account/subscriptions")
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, url, http.StatusSeeOther)
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
		if _, txErr := d.SubscriptionService.GetSubscriptionByCustomer(ctx, tx, id, customer.ID); txErr != nil {
			return txErr
		}
		sub, txErr := d.SubscriptionService.CancelSubscription(ctx, tx, id, customerActor(r))
		if txErr != nil {
			return txErr
		}
		if _, txErr := d.RiverClient.InsertTx(ctx, tx, jobs.SubscriptionCancelledArgs{
			SubscriptionID: sub.ID,
			CustomerID:     sub.CustomerID,
		}, nil); txErr != nil {
			return txErr
		}
		return nil
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
