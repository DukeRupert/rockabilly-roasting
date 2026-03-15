package web

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/admin"
)

func (d *Deps) handleAdminOrderList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	statusFilter := r.URL.Query().Get("status")
	search := r.URL.Query().Get("q")
	pageStr := r.URL.Query().Get("page")

	page := 1
	if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
		page = p
	}

	perPage := 25
	filter := store.OrderFilter{
		Search: search,
		Limit:  perPage + 1,
		Offset: (page - 1) * perPage,
	}

	if statusFilter != "" {
		s := domain.OrderStatus(statusFilter)
		filter.Status = &s
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
	props := admin.OrderListProps{
		Orders:       orders,
		StatusFilter: statusFilter,
		Search:       search,
		TotalCount:   totalCount,
		Page:         page,
		PerPage:      perPage,
		HasMore:      hasMore,
		StaffName:    name,
		StaffRole:    role,
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
	var customer *domain.Customer
	var shippingAddress *domain.Address
	var enrichedItems []admin.EnrichedLineItem

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
		if txErr != nil {
			return txErr
		}

		// Resolve customer.
		if order.CustomerID != nil {
			customer, txErr = d.CustomerService.GetCustomer(ctx, tx, *order.CustomerID)
			if txErr != nil && !errors.Is(txErr, app.ErrCustomerNotFound) {
				return txErr
			}
		}

		// Resolve shipping address.
		shippingAddress, txErr = d.CustomerService.GetAddressByID(ctx, tx, order.ShippingAddressID)
		if txErr != nil && !errors.Is(txErr, app.ErrAddressNotFound) {
			return txErr
		}

		// Resolve variant + product names for each line item.
		enrichedItems = make([]admin.EnrichedLineItem, len(lineItems))
		for i, li := range lineItems {
			enrichedItems[i] = admin.EnrichedLineItem{LineItem: li}

			variant, vErr := d.CatalogService.GetVariant(ctx, tx, li.VariantID)
			if vErr != nil {
				if errors.Is(vErr, app.ErrVariantNotFound) {
					continue
				}
				return vErr
			}
			enrichedItems[i].VariantSKU = variant.SKU

			product, pErr := d.CatalogService.GetProduct(ctx, tx, variant.ProductID)
			if pErr != nil {
				if errors.Is(pErr, app.ErrProductNotFound) {
					continue
				}
				return pErr
			}
			enrichedItems[i].ProductTitle = product.Title
			enrichedItems[i].ProductSlug = product.Slug
		}

		return nil
	})
	if err != nil {
		if errors.Is(err, app.ErrOrderNotFound) {
			http.NotFound(w, r)
			return
		}
		Error(w, r, err)
		return
	}

	name, role := staffNameRole(r)
	props := admin.OrderShowProps{
		Order:           order,
		LineItems:       enrichedItems,
		Adjustments:     adjustments,
		Customer:        customer,
		ShippingAddress: shippingAddress,
		Flash:           r.URL.Query().Get("flash"),
		StaffName:       name,
		StaffRole:       role,
	}

	if IsHTMX(r) {
		admin.OrderShowContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.OrderShow(props).Render(ctx, w) //nolint:errcheck
}

func (d *Deps) handleAdminOrderCancel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.OrderService.CancelOrder(ctx, tx, id, staffActor(r))
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/orders/"+id.String()+"?flash=Order+cancelled", http.StatusSeeOther)
}

func (d *Deps) handleAdminOrderRefund(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.OrderService.RefundOrder(ctx, tx, id, staffActor(r))
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/orders/"+id.String()+"?flash=Order+refunded", http.StatusSeeOther)
}

func (d *Deps) handleAdminOrderFulfill(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.OrderService.FulfillOrder(ctx, tx, id, staffActor(r))
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/orders/"+id.String()+"?flash=Order+fulfilled", http.StatusSeeOther)
}

func (d *Deps) handleAdminOrderShip(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.OrderService.ShipOrder(ctx, tx, id, staffActor(r))
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/orders/"+id.String()+"?flash=Order+marked+as+shipped", http.StatusSeeOther)
}

func (d *Deps) handleAdminOrderPackingSlip(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var order *domain.Order
	var lineItems []domain.LineItem
	var customer *domain.Customer
	var shippingAddress *domain.Address
	var enrichedItems []admin.EnrichedLineItem

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

		if order.CustomerID != nil {
			customer, txErr = d.CustomerService.GetCustomer(ctx, tx, *order.CustomerID)
			if txErr != nil && !errors.Is(txErr, app.ErrCustomerNotFound) {
				return txErr
			}
		}

		shippingAddress, txErr = d.CustomerService.GetAddressByID(ctx, tx, order.ShippingAddressID)
		if txErr != nil && !errors.Is(txErr, app.ErrAddressNotFound) {
			return txErr
		}

		enrichedItems = make([]admin.EnrichedLineItem, len(lineItems))
		for i, li := range lineItems {
			enrichedItems[i] = admin.EnrichedLineItem{LineItem: li}

			variant, vErr := d.CatalogService.GetVariant(ctx, tx, li.VariantID)
			if vErr != nil {
				if errors.Is(vErr, app.ErrVariantNotFound) {
					continue
				}
				return vErr
			}
			enrichedItems[i].VariantSKU = variant.SKU

			product, pErr := d.CatalogService.GetProduct(ctx, tx, variant.ProductID)
			if pErr != nil {
				if errors.Is(pErr, app.ErrProductNotFound) {
					continue
				}
				return pErr
			}
			enrichedItems[i].ProductTitle = product.Title
		}

		return nil
	})
	if err != nil {
		if errors.Is(err, app.ErrOrderNotFound) {
			http.NotFound(w, r)
			return
		}
		Error(w, r, err)
		return
	}

	props := admin.PackingSlipProps{
		Order:           order,
		LineItems:       enrichedItems,
		Customer:        customer,
		ShippingAddress: shippingAddress,
	}

	admin.PackingSlip(props).Render(ctx, w) //nolint:errcheck
}
