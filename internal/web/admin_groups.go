package web

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"

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
		groups, txErr = d.CustomerGroupService.List(ctx, tx)
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
		_, txErr := d.CustomerGroupService.Create(ctx, tx, groupName, staffActor(r))
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
		return d.CustomerGroupService.Delete(ctx, tx, id, staffActor(r))
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/groups", http.StatusSeeOther)
}

// priceOpKind enumerates the mutations a bulk price save can apply to one cell.
// Shared with the price-list bulk editor (see admin_price_lists.go), where
// opGroupSet/opGroupDelete mean set/clear a price-list price.
type priceOpKind int

const (
	opGroupSet priceOpKind = iota
	opGroupDelete
)

type priceOp struct {
	kind      priceOpKind
	variantID uuid.UUID
	groupID   uuid.UUID
	cents     int
}

// splitGroupKey splits a "<groupID>:<variantID>" form key into its two UUIDs.
func splitGroupKey(s string) (groupID, variantID uuid.UUID, ok bool) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return uuid.Nil, uuid.Nil, false
	}
	gid, err := uuid.Parse(parts[0])
	if err != nil {
		return uuid.Nil, uuid.Nil, false
	}
	vid, err := uuid.Parse(parts[1])
	if err != nil {
		return uuid.Nil, uuid.Nil, false
	}
	return gid, vid, true
}

// parseDollarCents parses a dollar string into cents. An empty string yields a nil
// pointer (meaning "unset"); a malformed or negative value yields an error.
func parseDollarCents(s string) (*int, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	dollars, err := strconv.ParseFloat(s, 64)
	if err != nil || dollars < 0 {
		return nil, errors.New("invalid price")
	}
	cents := int(math.Round(dollars * 100))
	return &cents, nil
}

func centsEqual(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
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
		return d.CustomerGroupService.AddMember(ctx, tx, customerID, groupID, staffActor(r))
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
		return d.CustomerGroupService.RemoveMember(ctx, tx, customerID, groupID, staffActor(r))
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/customers/"+customerID.String(), http.StatusSeeOther)
}
