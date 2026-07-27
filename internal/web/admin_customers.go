package web

import (
	"fmt"
	"net/http"
	"net/mail"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/auth"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/admin"
)

func (d *Deps) handleAdminCustomerList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	search := r.URL.Query().Get("q")
	pageStr := r.URL.Query().Get("page")

	page := 1
	if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
		page = p
	}

	perPage := 25
	filter := store.CustomerFilter{
		Search: search,
		Limit:  perPage + 1,
		Offset: (page - 1) * perPage,
	}

	var customers []domain.Customer
	var totalCount int

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		customers, txErr = d.CustomerService.ListCustomers(ctx, tx, filter)
		if txErr != nil {
			return txErr
		}
		totalCount, txErr = d.CustomerService.CountCustomers(ctx, tx, filter)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	hasMore := len(customers) > perPage
	if hasMore {
		customers = customers[:perPage]
	}

	name, role := staffNameRole(r)
	props := admin.CustomerListProps{
		Customers:  customers,
		Search:     search,
		TotalCount: totalCount,
		Page:       page,
		PerPage:    perPage,
		HasMore:    hasMore,
		MerchantTZ: d.MerchantTZ,
		StaffName:  name,
		StaffRole:  role,
	}

	if IsHTMX(r) {
		admin.CustomerListContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.CustomerList(props).Render(ctx, w) //nolint:errcheck
}

func (d *Deps) handleAdminCustomerShow(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var customer *domain.Customer
	var addresses []domain.Address
	var memberGroups []domain.CustomerGroup
	var allGroups []domain.CustomerGroup
	var priceLists []domain.PriceList
	var recentOrders []domain.Order
	var activity []domain.AuditEntry

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		customer, txErr = d.CustomerService.GetCustomer(ctx, tx, id)
		if txErr != nil {
			return txErr
		}
		addresses, txErr = d.CustomerService.ListAddresses(ctx, tx, id)
		if txErr != nil {
			return txErr
		}
		memberGroups, txErr = d.CustomerGroupService.ListByCustomer(ctx, tx, id)
		if txErr != nil {
			return txErr
		}
		allGroups, txErr = d.CustomerGroupService.List(ctx, tx)
		if txErr != nil {
			return txErr
		}
		priceLists, txErr = d.PriceListService.List(ctx, tx)
		if txErr != nil {
			return txErr
		}
		recentOrders, txErr = d.OrderService.ListOrders(ctx, tx, store.OrderFilter{
			CustomerID: &id,
			Limit:      5,
		})
		if txErr != nil {
			return txErr
		}
		activity, txErr = d.AuditQueryService.ListForCustomer(ctx, tx, id, 25)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	name, role := staffNameRole(r)
	props := admin.CustomerShowProps{
		Customer:     customer,
		Addresses:    addresses,
		MemberGroups: memberGroups,
		AllGroups:    allGroups,
		PriceLists:   priceLists,
		RecentOrders: recentOrders,
		Activity:     activity,
		MerchantTZ:   d.MerchantTZ,
		StaffName:    name,
		StaffRole:    role,
		CanEditEmail: staffCan(r, auth.PermEditCustomers),
	}

	if IsHTMX(r) {
		admin.CustomerShowContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.CustomerShow(props).Render(ctx, w) //nolint:errcheck
}

func (d *Deps) handleAdminCustomerPaymentTerms(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var days *int
	if v := r.FormValue("payment_terms_days"); v != "" {
		d, err := strconv.Atoi(v)
		if err != nil || (d != 7 && d != 15 && d != 21 && d != 30) {
			http.Error(w, "Invalid payment terms", http.StatusBadRequest)
			return
		}
		days = &d
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.CustomerService.UpdatePaymentTerms(ctx, tx, id, days, staffActor(r))
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/admin/customers/%s", id), http.StatusSeeOther)
}

// handleAdminCustomerEmail changes the address a customer signs in and receives
// mail at. Used when a customer loses access to the address on file — a shared
// shop inbox moving to a new provider, a staffer leaving a wholesale account.
// Mounted behind customers:write: this is the front half of an account
// takeover, so it stays with the roles trusted to edit customer records.
func (d *Deps) handleAdminCustomerEmail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	email := strings.TrimSpace(r.FormValue("email"))
	if len(email) > 255 {
		http.Error(w, "Email address too long", http.StatusBadRequest)
		return
	}
	if _, err := mail.ParseAddress(email); err != nil {
		http.Error(w, "Invalid email address", http.StatusBadRequest)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.CustomerService.UpdateEmailAsStaff(ctx, tx, id, email, staffActor(r))
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/admin/customers/%s", id), http.StatusSeeOther)
}

func (d *Deps) handleAdminCustomerPriceList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var priceListID *uuid.UUID
	if v := r.FormValue("price_list_id"); v != "" {
		parsed, err := uuid.Parse(v)
		if err != nil {
			http.Error(w, "Invalid price list", http.StatusBadRequest)
			return
		}
		priceListID = &parsed
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.CustomerService.UpdatePriceList(ctx, tx, id, priceListID, staffActor(r))
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/admin/customers/%s", id), http.StatusSeeOther)
}

func (d *Deps) handleAdminCustomerBillingMethod(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	method := domain.BillingMethod(r.FormValue("billing_method"))
	if method != domain.BillingMethodManual && method != domain.BillingMethodACH && method != domain.BillingMethodCreditCard {
		http.Error(w, "Invalid billing method", http.StatusBadRequest)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.CustomerService.UpdateBillingMethod(ctx, tx, id, method, staffActor(r))
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/admin/customers/%s", id), http.StatusSeeOther)
}

// handleAdminCustomerSendPasswordSetup triggers a password setup/reset email
// to a customer. Works whether the customer has a password set or not — the
// email wording adapts. Used by staff when a customer reports trouble signing in.
func (d *Deps) handleAdminCustomerSendPasswordSetup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if err := d.AuthService.SendPasswordSetupEmail(ctx, d.Pool, id, staffActor(r)); err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/admin/customers/%s", id), http.StatusSeeOther)
}

// handleAdminCustomerSendVerification emails the customer a link that proves
// the address on file works. Redeeming it also signs them in, which is the
// point after a staff-corrected address: the customer confirms the new address
// and lands in their account without a password reset they didn't ask for.
//
// Ungated like send-password-setup beside it — both mail a sign-in link to the
// address already on record, so neither widens what the recipient can reach.
// Changing that address is the privileged move, and that route is gated.
func (d *Deps) handleAdminCustomerSendVerification(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if err := d.AuthService.SendVerificationEmailAsStaff(ctx, d.Pool, id, staffActor(r)); err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/admin/customers/%s", id), http.StatusSeeOther)
}

func (d *Deps) handleAdminCustomerLocalFulfillment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var preferred *domain.ShippingMethod
	switch v := r.FormValue("local_fulfillment"); v {
	case "":
		preferred = nil
	case string(domain.ShippingMethodLocalDelivery):
		m := domain.ShippingMethodLocalDelivery
		preferred = &m
	case string(domain.ShippingMethodPickup):
		m := domain.ShippingMethodPickup
		preferred = &m
	default:
		http.Error(w, "Invalid local fulfillment preference", http.StatusBadRequest)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.CustomerService.UpdatePreferredLocalFulfillment(ctx, tx, id, preferred, staffActor(r))
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/admin/customers/%s", id), http.StatusSeeOther)
}
