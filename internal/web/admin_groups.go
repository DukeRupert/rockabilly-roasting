package web

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/admin"
)

func (d *Deps) handleAdminGroupList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var groups []domain.CustomerGroup

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		groups, txErr = d.CustomerGroupStore.List(ctx, tx)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	name, role := staffNameRole(r)
	props := admin.GroupListProps{
		Groups:    groups,
		StaffName: name,
		StaffRole: role,
	}

	if IsHTMX(r) {
		admin.GroupListContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.GroupList(props).Render(ctx, w) //nolint:errcheck
}

func (d *Deps) handleAdminGroupCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	groupName := r.FormValue("name")
	if groupName == "" {
		http.Redirect(w, r, "/admin/groups", http.StatusSeeOther)
		return
	}

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.CustomerGroupStore.Create(ctx, tx, groupName, nil)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/groups", http.StatusSeeOther)
}

func (d *Deps) handleAdminGroupDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.CustomerGroupStore.Delete(ctx, tx, id)
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/groups", http.StatusSeeOther)
}

// handleAdminCustomerGroupAdd adds a customer to a group.
func (d *Deps) handleAdminCustomerGroupAdd(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	customerID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	groupID, err := uuid.Parse(r.FormValue("group_id"))
	if err != nil {
		http.Redirect(w, r, "/admin/customers/"+customerID.String(), http.StatusSeeOther)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.CustomerGroupStore.AddMember(ctx, tx, customerID, groupID)
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/customers/"+customerID.String(), http.StatusSeeOther)
}

// handleAdminCustomerGroupRemove removes a customer from a group.
func (d *Deps) handleAdminCustomerGroupRemove(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	customerID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	groupID, err := uuid.Parse(r.PathValue("groupID"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.CustomerGroupStore.RemoveMember(ctx, tx, customerID, groupID)
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/customers/"+customerID.String(), http.StatusSeeOther)
}
