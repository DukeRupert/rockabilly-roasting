package web

import (
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/admin"
)

func (d *Deps) handleAdminFulfillmentList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	statusFilter := r.URL.Query().Get("status")
	pageStr := r.URL.Query().Get("page")

	page := 1
	if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
		page = p
	}

	perPage := 25
	filter := store.OrderFilter{
		Limit:  perPage + 1,
		Offset: (page - 1) * perPage,
	}

	if statusFilter != "" {
		s := domain.FulfillmentStatus(statusFilter)
		filter.FulfillmentStatus = &s
	}

	var orders []domain.Order

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		orders, txErr = d.OrderService.ListOrders(ctx, tx, filter)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	hasMore := len(orders) > perPage
	if hasMore {
		orders = orders[:perPage]
	}

	name, role := staffNameRole(r)
	props := admin.FulfillmentListProps{
		Orders:       orders,
		StatusFilter: statusFilter,
		Page:         page,
		PerPage:      perPage,
		HasMore:      hasMore,
		StaffName:    name,
		StaffRole:    role,
	}

	if IsHTMX(r) {
		admin.FulfillmentListContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.FulfillmentList(props).Render(ctx, w) //nolint:errcheck
}
