package web

import (
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/jobs"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/admin"
)

func (d *Deps) handleAdminWholesaleList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Default to the "all" overview rather than a single status: it's the front
	// door to the page (roster + applications), and the per-pill counts below
	// give staff the scent to route themselves to wherever the work is.
	statusFilter := wholesaleStatusFilter(r.URL.Query().Get("status"))
	pageStr := r.URL.Query().Get("page")
	page := 1
	if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
		page = p
	}

	perPage := 25
	filter := store.CustomerFilter{
		AccountType: ptrTo(domain.AccountTypeWholesale),
		Limit:       perPage + 1,
		Offset:      (page - 1) * perPage,
	}
	// "all" leaves WholesaleStatus unset so every wholesale account is listed.
	if statusFilter != "all" {
		ws := domain.WholesaleStatus(statusFilter)
		filter.WholesaleStatus = &ws
	}

	var customers []domain.Customer
	var priceLists []domain.PriceList
	counts := map[string]int{}

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		customers, txErr = d.CustomerService.ListCustomers(ctx, tx, filter)
		if txErr != nil {
			return txErr
		}
		priceLists, txErr = d.PriceListService.List(ctx, tx)
		if txErr != nil {
			return txErr
		}
		// Per-status counts power the filter-bar badges (and the "all" total),
		// turning the filter row into a lightweight dashboard.
		base := store.CustomerFilter{AccountType: ptrTo(domain.AccountTypeWholesale)}
		counts["all"], txErr = d.CustomerService.CountCustomers(ctx, tx, base)
		if txErr != nil {
			return txErr
		}
		for _, st := range []domain.WholesaleStatus{
			domain.WholesaleStatusPending,
			domain.WholesaleStatusApproved,
			domain.WholesaleStatusSuspended,
			domain.WholesaleStatusDeclined,
		} {
			f := base
			ws := st
			f.WholesaleStatus = &ws
			counts[string(st)], txErr = d.CustomerService.CountCustomers(ctx, tx, f)
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

	hasMore := len(customers) > perPage
	if hasMore {
		customers = customers[:perPage]
	}

	name, role := staffNameRole(r)
	props := admin.WholesaleListProps{
		Customers:    customers,
		PriceLists:   priceLists,
		StatusFilter: statusFilter,
		Counts:       counts,
		Page:         page,
		HasMore:      hasMore,
		MerchantTZ:   d.MerchantTZ,
		StaffName:    name,
		StaffRole:    role,
	}

	if IsHTMX(r) {
		admin.WholesaleListContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.WholesaleList(props).Render(ctx, w) //nolint:errcheck
}

// handleAdminWholesalePriceList assigns (or clears) a wholesale applicant's price
// list from the wholesale review screen, so staff can set pricing before approving.
// It reuses the customer-scoped UpdatePriceList service method and redirects back
// to the wholesale list, preserving the active status filter.
func (d *Deps) handleAdminWholesalePriceList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var priceListID *uuid.UUID
	if v := r.FormValue("price_list_id"); v != "" {
		parsed, parseErr := uuid.Parse(v)
		if parseErr != nil {
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

	http.Redirect(w, r, "/admin/wholesale?status="+wholesaleStatusFilter(r.FormValue("status")), http.StatusSeeOther)
}

// wholesaleStatusFilter sanitizes a status filter value (from the query string
// or a form), falling back to the "all" overview for anything unrecognized so
// the view/redirect target is always a known one.
func wholesaleStatusFilter(s string) string {
	switch s {
	case "all", "pending", "approved", "suspended", "declined":
		return s
	default:
		return "all"
	}
}

func (d *Deps) handleAdminWholesaleApprove(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	actor := staffActor(r)

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.WholesaleService.ApproveApplication(ctx, tx, id, actor)
		if txErr != nil {
			return txErr
		}
		_, txErr = d.RiverClient.InsertTx(ctx, tx, jobs.WholesaleApprovedArgs{CustomerID: id}, nil)
		if txErr != nil {
			return txErr
		}
		// Pre-create QB customer so it's ready before first order.
		if d.QBClient != nil {
			_, txErr = d.RiverClient.InsertTx(ctx, tx, jobs.SyncQBCustomerArgs{CustomerID: id}, nil)
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

	http.Redirect(w, r, "/admin/wholesale", http.StatusSeeOther)
}

func (d *Deps) handleAdminWholesaleDecline(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	notes := r.FormValue("notes")
	actor := staffActor(r)

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.WholesaleService.DeclineApplication(ctx, tx, id, notes, actor)
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/wholesale", http.StatusSeeOther)
}

func (d *Deps) handleAdminWholesaleSuspend(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	notes := r.FormValue("notes")
	actor := staffActor(r)

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.WholesaleService.SuspendAccount(ctx, tx, id, notes, actor)
		if txErr != nil {
			return txErr
		}
		_, txErr = d.RiverClient.InsertTx(ctx, tx, jobs.WholesaleSuspendedArgs{CustomerID: id}, nil)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/wholesale", http.StatusSeeOther)
}

func (d *Deps) handleAdminWholesaleReactivate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	actor := staffActor(r)

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.WholesaleService.ReactivateAccount(ctx, tx, id, actor)
		if txErr != nil {
			return txErr
		}
		// Send the welcome/setup email so they can set a new password.
		_, txErr = d.RiverClient.InsertTx(ctx, tx, jobs.WholesaleApprovedArgs{CustomerID: id}, nil)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/wholesale", http.StatusSeeOther)
}

// ptrTo is a helper to create a pointer to a value (avoids import from app package).
func ptrTo[T any](v T) *T {
	return &v
}
