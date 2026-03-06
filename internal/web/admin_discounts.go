package web

import (
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/admin"
)

func (d *Deps) handleAdminDiscountList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	activeFilter := r.URL.Query().Get("active")
	pageStr := r.URL.Query().Get("page")

	page := 1
	if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
		page = p
	}

	perPage := 25
	filter := store.DiscountFilter{
		Limit:  perPage + 1,
		Offset: (page - 1) * perPage,
	}

	if activeFilter != "" {
		active := activeFilter == "true"
		filter.Active = &active
	}

	var discounts []domain.Discount

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		discounts, txErr = d.DiscountService.ListDiscounts(ctx, tx, filter)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	hasMore := len(discounts) > perPage
	if hasMore {
		discounts = discounts[:perPage]
	}

	name, role := staffNameRole(r)
	props := admin.DiscountListProps{
		Discounts:    discounts,
		ActiveFilter: activeFilter,
		Page:         page,
		PerPage:      perPage,
		HasMore:      hasMore,
		StaffName:    name,
		StaffRole:    role,
	}

	if IsHTMX(r) {
		admin.DiscountListContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.DiscountList(props).Render(ctx, w) //nolint:errcheck
}
