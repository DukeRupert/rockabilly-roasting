package web

import (
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/auth"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/storefront"
)

// The wholesale account area (/wholesale/account/*) mirrors the retail account
// area but is scoped to wholesale customers. It reuses the same service layer
// throughout — only the rendered templates and redirect targets differ. All
// routes sit behind requireApprovedWholesale (see router.go), so every handler
// can assume an approved wholesale customer is in context.

func wholesaleCompanyName(customer *domain.Customer) string {
	if customer.CompanyName != nil {
		return *customer.CompanyName
	}
	return ""
}

// --- Orders ---

func (d *Deps) handleWholesaleAccountOrders(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customer, _ := auth.CustomerFromContext(ctx)

	var orders []domain.Order
	channel := domain.OrderChannelWholesale

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		orders, txErr = d.OrderService.ListOrders(ctx, tx, store.OrderFilter{
			CustomerID:         &customer.ID,
			Channel:            &channel,
			Limit:              50,
			ExcludeUnconfirmed: true,
		})
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	props := storefront.WholesaleAccountOrdersProps{
		Customer:    customer,
		CompanyName: wholesaleCompanyName(customer),
		Orders:      orders,
		CartCount:   d.wholesaleCartItemCount(r),
	}

	if IsHTMX(r) {
		storefront.WholesaleAccountOrdersContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	storefront.WholesaleAccountOrdersPage(props).Render(ctx, w) //nolint:errcheck
}

func (d *Deps) handleWholesaleAccountOrderShow(w http.ResponseWriter, r *http.Request) {
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
		// Enforce ownership — return 404 if not this customer's order.
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
		if txErr != nil {
			return txErr
		}
		return nil
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	props := storefront.WholesaleAccountOrderShowProps{
		Customer:    customer,
		CompanyName: wholesaleCompanyName(customer),
		Order:       order,
		LineItems:   lineItems,
		Shipments:   shipments,
		Address:     shippingAddr,
		CartCount:   d.wholesaleCartItemCount(r),
	}

	if IsHTMX(r) {
		storefront.WholesaleAccountOrderShowContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	storefront.WholesaleAccountOrderShowPage(props).Render(ctx, w) //nolint:errcheck
}

// --- Settings ---

func (d *Deps) handleWholesaleAccountSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customer, _ := auth.CustomerFromContext(ctx)

	props := storefront.WholesaleAccountSettingsProps{
		Customer:    customer,
		CompanyName: wholesaleCompanyName(customer),
		CartCount:   d.wholesaleCartItemCount(r),
	}
	if IsHTMX(r) {
		storefront.WholesaleAccountSettingsContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	storefront.WholesaleAccountSettingsPage(props).Render(ctx, w) //nolint:errcheck
}

func (d *Deps) handleWholesaleAccountSettingsUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customer, _ := auth.CustomerFromContext(ctx)

	firstName := strings.TrimSpace(r.FormValue("first_name"))
	lastName := strings.TrimSpace(r.FormValue("last_name"))
	phone := strings.TrimSpace(r.FormValue("phone"))

	if firstName == "" || lastName == "" {
		props := storefront.WholesaleAccountSettingsProps{
			Customer:    customer,
			CompanyName: wholesaleCompanyName(customer),
			CartCount:   d.wholesaleCartItemCount(r),
			Error:       "First and last name are required.",
		}
		if IsHTMX(r) {
			storefront.WholesaleAccountSettingsContent(props).Render(ctx, w) //nolint:errcheck
			return
		}
		storefront.WholesaleAccountSettingsPage(props).Render(ctx, w) //nolint:errcheck
		return
	}

	var updated *domain.Customer
	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		updated, txErr = d.CustomerService.UpdateName(ctx, tx, customer.ID, firstName, lastName)
		if txErr != nil {
			return txErr
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
		return nil
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	props := storefront.WholesaleAccountSettingsProps{
		Customer:    updated,
		CompanyName: wholesaleCompanyName(updated),
		CartCount:   d.wholesaleCartItemCount(r),
		Success:     "Your details have been updated.",
	}
	if IsHTMX(r) {
		storefront.WholesaleAccountSettingsContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	storefront.WholesaleAccountSettingsPage(props).Render(ctx, w) //nolint:errcheck
}

// --- Addresses ---

func (d *Deps) handleWholesaleAccountAddresses(w http.ResponseWriter, r *http.Request) {
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

	props := storefront.WholesaleAccountAddressesProps{
		Customer:    customer,
		CompanyName: wholesaleCompanyName(customer),
		Addresses:   addresses,
		CartCount:   d.wholesaleCartItemCount(r),
	}
	if IsHTMX(r) {
		storefront.WholesaleAccountAddressesContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	storefront.WholesaleAccountAddressesPage(props).Render(ctx, w) //nolint:errcheck
}

func (d *Deps) handleWholesaleAccountAddressCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customer, _ := auth.CustomerFromContext(ctx)

	p := wholesaleAddressParams(r)
	p.CustomerID = &customer.ID

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.CustomerService.CreateAddress(ctx, tx, p, customerActor(r))
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}
	http.Redirect(w, r, "/wholesale/account/addresses", http.StatusSeeOther)
}

func (d *Deps) handleWholesaleAccountAddressUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customer, _ := auth.CustomerFromContext(ctx)

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	p := wholesaleAddressParams(r)
	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.CustomerService.UpdateAddress(ctx, tx, id, customer.ID, p, customerActor(r))
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}
	http.Redirect(w, r, "/wholesale/account/addresses", http.StatusSeeOther)
}

func (d *Deps) handleWholesaleAccountAddressDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customer, _ := auth.CustomerFromContext(ctx)

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		// Don't allow deleting the last address.
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
	http.Redirect(w, r, "/wholesale/account/addresses", http.StatusSeeOther)
}

func (d *Deps) handleWholesaleAccountAddressSetDefault(w http.ResponseWriter, r *http.Request) {
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
	http.Redirect(w, r, "/wholesale/account/addresses", http.StatusSeeOther)
}

// wholesaleAddressParams parses the shared address form fields into store params.
func wholesaleAddressParams(r *http.Request) store.CreateAddressParams {
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
	return p
}

// --- Security ---

func (d *Deps) handleWholesaleAccountSecurity(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customer, _ := auth.CustomerFromContext(ctx)

	props := storefront.WholesaleAccountSecurityProps{
		Customer:    customer,
		CompanyName: wholesaleCompanyName(customer),
		HasPassword: customer.PasswordHash != nil,
		CartCount:   d.wholesaleCartItemCount(r),
	}
	if IsHTMX(r) {
		storefront.WholesaleAccountSecurityContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	storefront.WholesaleAccountSecurityPage(props).Render(ctx, w) //nolint:errcheck
}

func (d *Deps) handleWholesaleAccountPasswordSet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customer, _ := auth.CustomerFromContext(ctx)
	newPassword := r.FormValue("new_password")

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.AuthService.SetPassword(ctx, tx, customer.ID, newPassword, customerActor(r))
	})
	if err != nil {
		props := storefront.WholesaleAccountSecurityProps{
			Customer:    customer,
			CompanyName: wholesaleCompanyName(customer),
			HasPassword: customer.PasswordHash != nil,
			CartCount:   d.wholesaleCartItemCount(r),
		}
		if errors.Is(err, app.ErrPasswordTooShort) {
			props.Error = "Password must be at least 10 characters."
		} else {
			Error(w, r, err)
			return
		}
		d.renderWholesaleSecurity(w, r, props)
		return
	}

	props := storefront.WholesaleAccountSecurityProps{
		Customer:    customer,
		CompanyName: wholesaleCompanyName(customer),
		HasPassword: true,
		CartCount:   d.wholesaleCartItemCount(r),
		Success:     "Password set. You can now sign in with your password.",
	}
	d.renderWholesaleSecurity(w, r, props)
}

func (d *Deps) handleWholesaleAccountPasswordChange(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customer, _ := auth.CustomerFromContext(ctx)
	currentPassword := r.FormValue("current_password")
	newPassword := r.FormValue("new_password")

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.AuthService.ChangePassword(ctx, tx, customer.ID, currentPassword, newPassword, customerActor(r))
	})
	if err != nil {
		props := storefront.WholesaleAccountSecurityProps{
			Customer:    customer,
			CompanyName: wholesaleCompanyName(customer),
			HasPassword: customer.PasswordHash != nil,
			CartCount:   d.wholesaleCartItemCount(r),
		}
		switch {
		case errors.Is(err, app.ErrInvalidCredentials):
			props.Error = "Current password is incorrect."
		case errors.Is(err, app.ErrPasswordTooShort):
			props.Error = "Password must be at least 10 characters."
		default:
			Error(w, r, err)
			return
		}
		d.renderWholesaleSecurity(w, r, props)
		return
	}

	props := storefront.WholesaleAccountSecurityProps{
		Customer:    customer,
		CompanyName: wholesaleCompanyName(customer),
		HasPassword: true,
		CartCount:   d.wholesaleCartItemCount(r),
		Success:     "Password updated.",
	}
	d.renderWholesaleSecurity(w, r, props)
}

func (d *Deps) renderWholesaleSecurity(w http.ResponseWriter, r *http.Request, props storefront.WholesaleAccountSecurityProps) {
	if IsHTMX(r) {
		storefront.WholesaleAccountSecurityContent(props).Render(r.Context(), w) //nolint:errcheck
		return
	}
	storefront.WholesaleAccountSecurityPage(props).Render(r.Context(), w) //nolint:errcheck
}
