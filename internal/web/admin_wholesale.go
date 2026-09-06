package web

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/jobs"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/admin"
)

// handleAdminWholesalePriceList assigns (or clears) a wholesale applicant's price
// list from the wholesale review screen, so staff can set pricing before approving.
// It reuses the customer-scoped UpdatePriceList service method and redirects back
// to the wholesale channel of the customer list, preserving the active status filter.
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

	http.Redirect(w, r, wholesaleListURL(r.FormValue("status")), http.StatusSeeOther)
}

// wholesaleListURL is where every wholesale review action returns to: the
// wholesale channel of the customer list, keeping the status filter the staffer
// was working in. An unrecognized status drops the filter rather than erroring —
// a stale form value should land them on the roster, not a 400.
func wholesaleListURL(status string) string {
	switch status {
	case "pending", "approved", "suspended", "declined":
		return admin.CustomersWholesalePath + "?status=" + status
	default:
		return admin.CustomersWholesalePath
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
		qbConfigured, txErr := d.QB.ConfiguredTx(ctx, tx)
		if txErr != nil {
			return txErr
		}
		if qbConfigured {
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

	http.Redirect(w, r, wholesaleListURL(r.FormValue("status")), http.StatusSeeOther)
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

	http.Redirect(w, r, wholesaleListURL(r.FormValue("status")), http.StatusSeeOther)
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

	http.Redirect(w, r, wholesaleListURL(r.FormValue("status")), http.StatusSeeOther)
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

	http.Redirect(w, r, wholesaleListURL(r.FormValue("status")), http.StatusSeeOther)
}

// ptrTo is a helper to create a pointer to a value (avoids import from app package).
func ptrTo[T any](v T) *T {
	return &v
}
