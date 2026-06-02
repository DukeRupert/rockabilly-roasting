package web

import (
	"context"
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
	"github.com/dukerupert/hiri/internal/ui/components/toast"
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

// --- Group Pricing Page ---

func (d *Deps) handleAdminGroupPrices(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var groups []domain.CustomerGroup
	var products []domain.Product

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		groups, txErr = d.CustomerGroupService.List(ctx, tx)
		if txErr != nil {
			return txErr
		}
		products, txErr = d.CatalogService.ListProducts(ctx, tx, store.ProductFilter{})
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	// Build product pricing groups
	pgs := make([]admin.ProductPricingGroup, 0, len(products))
	for _, p := range products {
		var pg admin.ProductPricingGroup
		err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
			var txErr error
			pg, txErr = d.buildGroupPricingProduct(ctx, tx, p)
			return txErr
		})
		if err != nil {
			Error(w, r, err)
			return
		}
		if len(pg.Variants) == 0 {
			continue
		}
		pgs = append(pgs, pg)
	}

	name, role := staffNameRole(r)
	props := admin.GroupPricingProps{
		Products:  pgs,
		Groups:    groups,
		StaffName: name,
		StaffRole: role,
	}

	if IsHTMX(r) {
		admin.GroupPricingContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.GroupPricing(props).Render(ctx, w) //nolint:errcheck
}

// priceOpKind enumerates the mutations a bulk price save can apply to one cell.
type priceOpKind int

const (
	opBaseSet priceOpKind = iota
	opGroupSet
	opGroupDelete
)

type priceOp struct {
	kind      priceOpKind
	variantID uuid.UUID
	groupID   uuid.UUID
	cents     int
}

// handleAdminGroupPriceBulkUpdate applies every changed price in a single product's
// form (base prices plus all group prices) in one transaction. Each cell submits a
// current value plus a hidden "_prev" value; only cells whose value actually changed
// are written, and a cleared group cell deletes that group's override.
func (d *Deps) handleAdminGroupPriceBulkUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	ops, err := parseGroupPriceForm(r)
	if err != nil {
		if IsHTMX(r) {
			d.handleAdminGroupPrices(w, r)
			toast.Toast(toast.VariantError, "Please enter a valid price").Render(ctx, w) //nolint:errcheck
			return
		}
		http.Error(w, "invalid price", http.StatusBadRequest)
		return
	}

	if len(ops) == 0 {
		// Nothing changed — just re-render the current state.
		if IsHTMX(r) {
			d.handleAdminGroupPrices(w, r)
			return
		}
		http.Redirect(w, r, "/admin/groups/prices", http.StatusSeeOther)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		for _, op := range ops {
			var txErr error
			switch op.kind {
			case opBaseSet:
				_, txErr = d.PricingService.SetBasePrice(ctx, tx, op.variantID, op.cents, "USD")
			case opGroupSet:
				_, txErr = d.PricingService.SetGroupPrice(ctx, tx, op.variantID, op.groupID, op.cents, "USD")
			case opGroupDelete:
				txErr = d.PricingService.DeleteGroupPrice(ctx, tx, op.variantID, op.groupID, "USD")
			}
			if txErr != nil {
				return txErr
			}
		}
		return nil
	})
	if err != nil {
		if IsHTMX(r) {
			d.handleAdminGroupPrices(w, r)
			_, msg := mapError(err)
			toast.Toast(toast.VariantError, msg).Render(ctx, w) //nolint:errcheck
			return
		}
		Error(w, r, err)
		return
	}

	if IsHTMX(r) {
		d.handleAdminGroupPrices(w, r)
		return
	}
	http.Redirect(w, r, "/admin/groups/prices", http.StatusSeeOther)
}

// buildGroupPricingProduct assembles one product's variants and their base + group
// prices for rendering. The caller supplies the transaction.
func (d *Deps) buildGroupPricingProduct(ctx context.Context, tx pgx.Tx, p domain.Product) (admin.ProductPricingGroup, error) {
	variants, err := d.CatalogService.ListVariants(ctx, tx, p.ID)
	if err != nil {
		return admin.ProductPricingGroup{}, err
	}
	basePrices, err := d.PricingService.ListBasePricesByProduct(ctx, tx, p.ID, "USD")
	if err != nil {
		return admin.ProductPricingGroup{}, err
	}
	groupPrices, err := d.PricingService.ListGroupPricesByProduct(ctx, tx, p.ID, "USD")
	if err != nil {
		return admin.ProductPricingGroup{}, err
	}

	vps := make([]admin.VariantPricing, len(variants))
	for i, v := range variants {
		vp := admin.VariantPricing{
			Variant:     v,
			GroupPrices: groupPrices[v.ID],
		}
		if cents, ok := basePrices[v.ID]; ok {
			c := cents
			vp.PriceCents = &c
		}
		if vp.GroupPrices == nil {
			vp.GroupPrices = make(map[uuid.UUID]int)
		}
		vps[i] = vp
	}
	return admin.ProductPricingGroup{Product: p, Variants: vps}, nil
}

// parseGroupPriceForm turns a submitted group-pricing form into the list of changed
// cells. Returns an error if any submitted price is malformed or negative.
func parseGroupPriceForm(r *http.Request) ([]priceOp, error) {
	var ops []priceOp

	for key := range r.PostForm {
		switch {
		case strings.HasPrefix(key, "base:"):
			variantID, err := uuid.Parse(strings.TrimPrefix(key, "base:"))
			if err != nil {
				continue
			}
			cur, err := parseDollarCents(r.PostForm.Get(key))
			if err != nil {
				return nil, err
			}
			prev, _ := parseDollarCents(r.PostForm.Get("base_prev:" + variantID.String()))
			if centsEqual(cur, prev) || cur == nil {
				// Unchanged, or empty (there is no clear-base operation).
				continue
			}
			ops = append(ops, priceOp{kind: opBaseSet, variantID: variantID, cents: *cur})

		case strings.HasPrefix(key, "group:"):
			gid, vid, ok := splitGroupKey(strings.TrimPrefix(key, "group:"))
			if !ok {
				continue
			}
			cur, err := parseDollarCents(r.PostForm.Get(key))
			if err != nil {
				return nil, err
			}
			prev, _ := parseDollarCents(r.PostForm.Get("group_prev:" + gid.String() + ":" + vid.String()))
			if centsEqual(cur, prev) {
				continue
			}
			if cur == nil {
				ops = append(ops, priceOp{kind: opGroupDelete, variantID: vid, groupID: gid})
			} else {
				ops = append(ops, priceOp{kind: opGroupSet, variantID: vid, groupID: gid, cents: *cur})
			}
		}
	}

	return ops, nil
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
