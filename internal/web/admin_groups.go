package web

import (
	"math"
	"net/http"
	"strconv"

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
		var variants []domain.Variant
		var basePrices map[uuid.UUID]int
		var groupPrices map[uuid.UUID]map[uuid.UUID]int

		err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
			var txErr error
			variants, txErr = d.CatalogService.ListVariants(ctx, tx, p.ID)
			if txErr != nil {
				return txErr
			}
			basePrices, txErr = d.PricingService.ListBasePricesByProduct(ctx, tx, p.ID, "USD")
			if txErr != nil {
				return txErr
			}
			groupPrices, txErr = d.PricingService.ListGroupPricesByProduct(ctx, tx, p.ID, "USD")
			return txErr
		})
		if err != nil {
			Error(w, r, err)
			return
		}

		if len(variants) == 0 {
			continue
		}

		vps := make([]admin.VariantPricing, len(variants))
		for i, v := range variants {
			vp := admin.VariantPricing{
				Variant:    v,
				GroupPrices: groupPrices[v.ID],
			}
			if cents, ok := basePrices[v.ID]; ok {
				vp.PriceCents = &cents
			}
			if vp.GroupPrices == nil {
				vp.GroupPrices = make(map[uuid.UUID]int)
			}
			vps[i] = vp
		}

		pgs = append(pgs, admin.ProductPricingGroup{
			Product:  p,
			Variants: vps,
		})
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

func (d *Deps) handleAdminGroupPriceBaseUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	variantID, err := uuid.Parse(r.FormValue("variant_id"))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	dollars, err := strconv.ParseFloat(r.FormValue("price"), 64)
	if err != nil || dollars < 0 {
		if IsHTMX(r) {
			d.handleAdminGroupPrices(w, r)
			toast.Toast(toast.VariantError, "Please enter a valid price").Render(ctx, w) //nolint:errcheck
			return
		}
		http.Error(w, "invalid price", http.StatusBadRequest)
		return
	}

	cents := int(math.Round(dollars * 100))

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.PricingService.SetBasePrice(ctx, tx, variantID, cents, "USD")
		return txErr
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

func (d *Deps) handleAdminGroupPriceGroupUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	variantID, err := uuid.Parse(r.FormValue("variant_id"))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	groupID, err := uuid.Parse(r.FormValue("group_id"))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	priceStr := r.FormValue("price")

	// Empty price = delete group price
	if priceStr == "" {
		err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
			return d.PricingService.DeleteGroupPrice(ctx, tx, variantID, groupID, "USD")
		})
		if err != nil {
			Error(w, r, err)
			return
		}
		if IsHTMX(r) {
			d.handleAdminGroupPrices(w, r)
			return
		}
		http.Redirect(w, r, "/admin/groups/prices", http.StatusSeeOther)
		return
	}

	dollars, err := strconv.ParseFloat(priceStr, 64)
	if err != nil || dollars < 0 {
		if IsHTMX(r) {
			d.handleAdminGroupPrices(w, r)
			toast.Toast(toast.VariantError, "Please enter a valid price").Render(ctx, w) //nolint:errcheck
			return
		}
		http.Error(w, "invalid price", http.StatusBadRequest)
		return
	}

	cents := int(math.Round(dollars * 100))

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.PricingService.SetGroupPrice(ctx, tx, variantID, groupID, cents, "USD")
		return txErr
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
