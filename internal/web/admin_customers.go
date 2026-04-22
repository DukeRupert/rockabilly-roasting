package web

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
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
		StaffName:    name,
		StaffRole:    role,
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
