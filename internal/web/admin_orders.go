package web

import (
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/admin"
)

func (d *Deps) handleAdminOrderList(w http.ResponseWriter, r *http.Request) {
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
		s := domain.OrderStatus(statusFilter)
		filter.Status = &s
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

	props := admin.OrderListProps{
		Orders:       orders,
		StatusFilter: statusFilter,
		Page:         page,
		PerPage:      perPage,
		HasMore:      hasMore,
	}

	if IsHTMX(r) {
		admin.OrderListContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.OrderList(props).Render(ctx, w) //nolint:errcheck
}

func (d *Deps) handleAdminOrderShow(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var order *domain.Order
	var lineItems []domain.LineItem
	var adjustments []domain.Adjustment

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		order, txErr = d.OrderService.GetOrder(ctx, tx, id)
		if txErr != nil {
			return txErr
		}
		lineItems, txErr = d.OrderService.ListLineItems(ctx, tx, id)
		if txErr != nil {
			return txErr
		}
		adjustments, txErr = d.OrderService.ListAdjustments(ctx, tx, id)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	props := admin.OrderShowProps{
		Order:       order,
		LineItems:   lineItems,
		Adjustments: adjustments,
	}

	if IsHTMX(r) {
		admin.OrderShowContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.OrderShow(props).Render(ctx, w) //nolint:errcheck
}
