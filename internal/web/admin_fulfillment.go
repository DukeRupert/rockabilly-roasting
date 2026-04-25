package web

import (
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/admin"
)

// FulfillmentActionStatuses are the fulfillment statuses that require action.
var FulfillmentActionStatuses = []domain.FulfillmentStatus{
	domain.FulfillmentStatusUnfulfilled,
	domain.FulfillmentStatusPartiallyFulfilled,
	domain.FulfillmentStatusFulfilled,
}

func (d *Deps) handleAdminFulfillmentList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	filterParam := r.URL.Query().Get("filter")
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

	switch filterParam {
	case "", "needs_action":
		filterParam = "needs_action"
		filter.FulfillmentStatuses = FulfillmentActionStatuses
	case "ready_to_ship":
		s := domain.FulfillmentStatusFulfilled
		filter.FulfillmentStatus = &s
	case "all":
		// no filter
	default:
		s := domain.FulfillmentStatus(filterParam)
		filter.FulfillmentStatus = &s
	}

	var orders []domain.Order
	var totalCount int

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		orders, txErr = d.OrderService.ListOrders(ctx, tx, filter)
		if txErr != nil {
			return txErr
		}
		totalCount, txErr = d.OrderService.CountOrders(ctx, tx, filter)
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
		Orders:     orders,
		Filter:     filterParam,
		TotalCount: totalCount,
		Page:       page,
		PerPage:    perPage,
		HasMore:    hasMore,
		MerchantTZ: d.MerchantTZ,
		StaffName:  name,
		StaffRole:  role,
	}

	if IsHTMX(r) {
		admin.FulfillmentListContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.FulfillmentList(props).Render(ctx, w) //nolint:errcheck
}
