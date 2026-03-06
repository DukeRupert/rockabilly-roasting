package web

import (
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
)

func (d *Deps) handleAdminWholesaleList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	statusFilter := r.URL.Query().Get("status")
	if statusFilter == "" {
		statusFilter = "pending"
	}
	pageStr := r.URL.Query().Get("page")
	page := 1
	if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
		page = p
	}

	perPage := 25
	ws := domain.WholesaleStatus(statusFilter)
	filter := store.CustomerFilter{
		AccountType:     ptrTo(domain.AccountTypeWholesale),
		WholesaleStatus: &ws,
		Limit:           perPage + 1,
		Offset:          (page - 1) * perPage,
	}

	var customers []domain.Customer

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		customers, txErr = d.CustomerService.ListCustomers(ctx, tx, filter)
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

	// TODO: Render admin.WholesaleList / admin.WholesaleListContent template.
	_ = hasMore
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte("<h1>Wholesale Applications</h1><p>Template pending.</p>")) //nolint:errcheck
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
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	// TODO: Enqueue WholesaleApprovedArgs job for welcome email.
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
