package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/logging"
	"github.com/dukerupert/hiri/internal/platform/payments"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/admin"
)

func (d *Deps) handleAdminOrderList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	view := normalizeOrderView(r.URL.Query().Get("view"))
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
	applyOrderViewFilter(view, &filter)

	var orders []domain.Order
	var rows []admin.OrderRow
	var totalCount int
	var counts store.OrderViewCounts
	var failedLabelIDs map[uuid.UUID]bool

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		orders, txErr = d.OrderService.ListOrders(ctx, tx, filter)
		if txErr != nil {
			return txErr
		}
		totalCount, txErr = d.OrderService.CountOrders(ctx, tx, filter)
		if txErr != nil {
			return txErr
		}
		counts, txErr = d.OrderService.CountOrdersByView(ctx, tx, search)
		if txErr != nil {
			return txErr
		}

		// Enrich each order with customer display info. Bounded by perPage,
		// so per-row lookup is acceptable here.
		rows = make([]admin.OrderRow, 0, len(orders))
		orderIDs := make([]uuid.UUID, 0, len(orders))
		for _, o := range orders {
			row := admin.OrderRow{
				Order:        o,
				CustomerName: "Guest",
				AccountType:  domain.AccountTypeRetail,
			}
			if o.CustomerID != nil {
				c, cErr := d.CustomerService.GetCustomer(ctx, tx, *o.CustomerID)
				if cErr != nil && !errors.Is(cErr, app.ErrCustomerNotFound) {
					return cErr
				}
				if c != nil {
					row.CustomerName = customerDisplayName(c)
					row.CustomerEmail = c.Email
					row.AccountType = c.AccountType
				}
			}
			rows = append(rows, row)
			orderIDs = append(orderIDs, o.ID)
		}

		// Single batched query for label-attempt failures across the page.
		failedLabelIDs, txErr = d.FulfillmentService.ListOrdersWithFailedLabelAttempts(ctx, tx, orderIDs)
		if txErr != nil {
			return txErr
		}
		for i := range rows {
			if failedLabelIDs[rows[i].Order.ID] {
				rows[i].LabelFailed = true
			}
		}
		return nil
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	hasMore := len(rows) > perPage
	if hasMore {
		rows = rows[:perPage]
	}

	name, role := staffNameRole(r)
	props := admin.OrderListProps{
		Rows: rows,
		View: view,
		Counts: admin.OrderViewCounts{
			NeedsAction: counts.NeedsAction,
			OnHold:      counts.OnHold,
			Shipped:     counts.Shipped,
			Archive:     counts.Archive,
			All:         counts.All,
		},
		Search: search,
		TotalCount: totalCount,
		Page:       page,
		PerPage:    perPage,
		HasMore:    hasMore,
		MerchantTZ: d.MerchantTZ,
		Now:        time.Now(),
		StaffName:  name,
		StaffRole:  role,
	}

	if IsHTMX(r) {
		admin.OrderListContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.OrderList(props).Render(ctx, w) //nolint:errcheck
}

// normalizeOrderView returns the canonical view key for the orders list,
// defaulting to "needs_action" when unrecognized or empty.
func normalizeOrderView(v string) string {
	switch v {
	case "needs_action", "on_hold", "shipped", "archive", "all":
		return v
	default:
		return "needs_action"
	}
}

// applyOrderViewFilter mutates the filter to match the chosen view's bucket.
// "all" applies no status restriction.
func applyOrderViewFilter(view string, f *store.OrderFilter) {
	switch view {
	case "needs_action":
		f.Statuses = []domain.OrderStatus{domain.OrderStatusConfirmed, domain.OrderStatusProcessing}
		f.ExcludeUnconfirmed = true
	case "on_hold":
		s := domain.OrderStatusOnHold
		f.Status = &s
	case "shipped":
		s := domain.OrderStatusComplete
		f.Status = &s
	case "archive":
		f.Statuses = []domain.OrderStatus{domain.OrderStatusCancelled, domain.OrderStatusRefunded}
	}
}

// buildVariantLabel joins the variant's option values (e.g., "Whole Bean / 12oz")
// using a per-product label map built from product options.
func buildVariantLabel(ctx context.Context, tx pgx.Tx, d *Deps, variantID uuid.UUID, labels map[uuid.UUID]string) (string, error) {
	vovs, err := d.CatalogService.ListVariantOptionValues(ctx, tx, variantID)
	if err != nil {
		return "", err
	}
	parts := make([]string, 0, len(vovs))
	for _, vov := range vovs {
		if s, ok := labels[vov.ProductOptionValueID]; ok && s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, " / "), nil
}

// canEditOrderLineItemsView mirrors the service-level guard so the swap UI
// only renders when the service would actually accept the change.
func canEditOrderLineItemsView(o *domain.Order) bool {
	if o.Status == domain.OrderStatusCancelled || o.Status == domain.OrderStatusRefunded {
		return false
	}
	switch o.FulfillmentStatus {
	case domain.FulfillmentStatusFulfilled,
		domain.FulfillmentStatusShipped,
		domain.FulfillmentStatusDelivered:
		return false
	}
	return true
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
	var shipments []domain.Shipment
	var latestLabelAttempt *domain.LabelAttempt
	var enrichedItems []admin.EnrichedLineItem

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		order, txErr = d.OrderService.GetOrderAsStaff(ctx, tx, id)
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
		shipments, txErr = d.FulfillmentService.ListShipmentsByOrder(ctx, tx, id)
		if txErr != nil {
			return txErr
		}
		latestLabelAttempt, txErr = d.FulfillmentService.GetLatestLabelAttempt(ctx, tx, id)
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
		shippingAddress, txErr = d.CustomerService.GetAddressByIDAsStaff(ctx, tx, order.ShippingAddressID)
		if txErr != nil && !errors.Is(txErr, app.ErrAddressNotFound) {
			return txErr
		}

		// Resolve variant + product names + sibling variants for each line item.
		enrichedItems = make([]admin.EnrichedLineItem, len(lineItems))
		// Per-product cache so multi-line orders don't re-query options.
		optionLabelByProduct := map[uuid.UUID]map[uuid.UUID]string{}
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

			labels, ok := optionLabelByProduct[product.ID]
			if !ok {
				labels = map[uuid.UUID]string{}
				opts, oErr := d.CatalogService.ListProductOptions(ctx, tx, product.ID)
				if oErr != nil {
					return oErr
				}
				for _, opt := range opts {
					vals, vlErr := d.CatalogService.ListProductOptionValues(ctx, tx, opt.ID)
					if vlErr != nil {
						return vlErr
					}
					for _, val := range vals {
						labels[val.ID] = val.Value
					}
				}
				optionLabelByProduct[product.ID] = labels
			}

			currentLabel, lErr := buildVariantLabel(ctx, tx, d, variant.ID, labels)
			if lErr != nil {
				return lErr
			}
			enrichedItems[i].CurrentLabel = currentLabel

			// Sibling variants (same product, same base price as the current
			// variant). Comparing base-to-base — not against the line item's
			// UnitPrice — keeps subscription-discounted orders swappable, since
			// the discount math depends only on the base price.
			allVariants, avErr := d.CatalogService.ListVariants(ctx, tx, product.ID)
			if avErr != nil {
				return avErr
			}
			currentBase, cbErr := d.PricingService.GetBasePrice(ctx, tx, li.VariantID, order.CurrencyCode)
			if cbErr != nil && !errors.Is(cbErr, app.ErrPriceNotFound) {
				return cbErr
			}
			for _, sv := range allVariants {
				if sv.ID != li.VariantID {
					if currentBase == nil {
						continue
					}
					price, pErr := d.PricingService.GetBasePrice(ctx, tx, sv.ID, order.CurrencyCode)
					if pErr != nil {
						if errors.Is(pErr, app.ErrPriceNotFound) {
							continue
						}
						return pErr
					}
					if price.Amount != currentBase.Amount {
						continue
					}
				}
				label, lblErr := buildVariantLabel(ctx, tx, d, sv.ID, labels)
				if lblErr != nil {
					return lblErr
				}
				if label == "" {
					label = sv.SKU
				}
				enrichedItems[i].SiblingVariants = append(enrichedItems[i].SiblingVariants, admin.SiblingVariant{
					ID:    sv.ID,
					Label: label,
				})
			}
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
		Order:              order,
		LineItems:          enrichedItems,
		Adjustments:        adjustments,
		Customer:           customer,
		ShippingAddress:    shippingAddress,
		Shipments:          shipments,
		LatestLabelAttempt: latestLabelAttempt,
		Flash:              r.URL.Query().Get("flash"),
		MerchantTZ:         d.MerchantTZ,
		StaffName:          name,
		StaffRole:          role,
		CanEditLineItems:   canEditOrderLineItemsView(order),
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
	logger := logging.FromContext(ctx)

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// Phase 1: read the order and validate it's refundable.
	var order *domain.Order
	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		order, txErr = d.OrderService.GetOrderAsStaff(ctx, tx, id)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	if order.StripePaymentIntentID == nil || *order.StripePaymentIntentID == "" {
		Error(w, r, app.ErrOrderNotRefundable)
		return
	}

	// Phase 2: issue the refund against Stripe (outside any transaction).
	if _, err := d.PaymentProvider.Refund(ctx, payments.RefundRequest{
		PaymentIntentID: *order.StripePaymentIntentID,
	}); err != nil {
		logger.Error("admin refund: stripe call failed", "order_id", id, "error", err)
		Error(w, r, err)
		return
	}

	// Phase 3: mark the order refunded in our DB (writes audit + status).
	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.OrderService.RefundOrder(ctx, tx, id, staffActor(r))
		return txErr
	})
	if err != nil {
		// Stripe refund already succeeded; the charge.refunded webhook will
		// reconcile our DB. Log loudly so we notice if that doesn't happen.
		logger.Error("admin refund: stripe succeeded but db update failed", "order_id", id, "error", err)
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

func (d *Deps) handleAdminOrderLineItemVariantUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	orderID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	lineItemID, err := uuid.Parse(r.PathValue("lineItemID"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if err := r.ParseForm(); err != nil {
		Error(w, r, err)
		return
	}
	newVariantID, err := uuid.Parse(r.FormValue("variant_id"))
	if err != nil {
		Error(w, r, app.ErrVariantNotFound)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.OrderService.ChangeLineItemVariant(ctx, tx, orderID, lineItemID, newVariantID, staffActor(r))
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/orders/"+orderID.String()+"?flash=Grind+updated", http.StatusSeeOther)
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

func (d *Deps) handleAdminOrderRevertFulfillment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.OrderService.RevertFulfillment(ctx, tx, id, staffActor(r))
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/orders/"+id.String()+"?flash=Fulfillment+reverted", http.StatusSeeOther)
}

func (d *Deps) handleAdminOrderRevertShipment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.OrderService.RevertShipment(ctx, tx, id, staffActor(r))
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/orders/"+id.String()+"?flash=Shipment+reverted", http.StatusSeeOther)
}

// handleAdminOrderReadyForPickup transitions a pickup order to ready and
// emails the customer. Only valid for orders where shipping_method == pickup.
func (d *Deps) handleAdminOrderReadyForPickup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.OrderService.MarkReadyForPickup(ctx, tx, id, staffActor(r))
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}
	http.Redirect(w, r, "/admin/orders/"+id.String()+"?flash=Customer+notified+pickup+is+ready", http.StatusSeeOther)
}

// handleAdminOrderPickedUp completes a pickup order after the customer
// collects it. No email — the customer is standing at the counter.
func (d *Deps) handleAdminOrderPickedUp(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.OrderService.MarkPickedUp(ctx, tx, id, staffActor(r))
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}
	http.Redirect(w, r, "/admin/orders/"+id.String()+"?flash=Order+marked+as+picked+up", http.StatusSeeOther)
}

// handleAdminOrderOutForDelivery dispatches a local-delivery order on
// delivery day and emails the customer.
func (d *Deps) handleAdminOrderOutForDelivery(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.OrderService.MarkOutForDelivery(ctx, tx, id, staffActor(r))
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}
	http.Redirect(w, r, "/admin/orders/"+id.String()+"?flash=Out+for+local+delivery", http.StatusSeeOther)
}

// handleAdminOrderShippingMethod swaps a local-fulfillment order between
// pickup and local_delivery. Valid only before the order has shipped/been
// picked up; rejects other methods at the service layer.
func (d *Deps) handleAdminOrderShippingMethod(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	target := domain.ShippingMethod(r.FormValue("method"))

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.OrderService.SwapLocalShippingMethod(ctx, tx, id, target, staffActor(r))
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	var flash string
	switch target {
	case domain.ShippingMethodPickup:
		flash = "Shipping+method+changed+to+local+pickup"
	case domain.ShippingMethodLocalDelivery:
		flash = "Shipping+method+changed+to+local+delivery"
	default:
		flash = "Shipping+method+updated"
	}
	http.Redirect(w, r, "/admin/orders/"+id.String()+"?flash="+flash, http.StatusSeeOther)
}

func (d *Deps) handleAdminOrderPackingSlip(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var props admin.PackingSlipProps
	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		props, txErr = d.loadPackingSlipProps(ctx, tx, id)
		return txErr
	})
	if err != nil {
		if errors.Is(err, app.ErrOrderNotFound) {
			http.NotFound(w, r)
			return
		}
		Error(w, r, err)
		return
	}

	admin.PackingSlip(props).Render(ctx, w) //nolint:errcheck
}

// loadPackingSlipProps fetches everything needed to render a single packing
// slip (order, line items + product/variant enrichment, customer, shipping
// address). Pure read; safe to call multiple times in one tx for the batch
// renderer.
func (d *Deps) loadPackingSlipProps(ctx context.Context, tx pgx.Tx, id uuid.UUID) (admin.PackingSlipProps, error) {
	order, err := d.OrderService.GetOrderAsStaff(ctx, tx, id)
	if err != nil {
		return admin.PackingSlipProps{}, err
	}
	lineItems, err := d.OrderService.ListLineItems(ctx, tx, id)
	if err != nil {
		return admin.PackingSlipProps{}, err
	}

	var customer *domain.Customer
	if order.CustomerID != nil {
		customer, err = d.CustomerService.GetCustomer(ctx, tx, *order.CustomerID)
		if err != nil && !errors.Is(err, app.ErrCustomerNotFound) {
			return admin.PackingSlipProps{}, err
		}
	}

	shippingAddress, err := d.CustomerService.GetAddressByIDAsStaff(ctx, tx, order.ShippingAddressID)
	if err != nil && !errors.Is(err, app.ErrAddressNotFound) {
		return admin.PackingSlipProps{}, err
	}

	enrichedItems := make([]admin.EnrichedLineItem, len(lineItems))
	for i, li := range lineItems {
		enrichedItems[i] = admin.EnrichedLineItem{LineItem: li}

		variant, vErr := d.CatalogService.GetVariant(ctx, tx, li.VariantID)
		if vErr != nil {
			if errors.Is(vErr, app.ErrVariantNotFound) {
				continue
			}
			return admin.PackingSlipProps{}, vErr
		}
		enrichedItems[i].VariantSKU = variant.SKU

		product, pErr := d.CatalogService.GetProduct(ctx, tx, variant.ProductID)
		if pErr != nil {
			if errors.Is(pErr, app.ErrProductNotFound) {
				continue
			}
			return admin.PackingSlipProps{}, pErr
		}
		enrichedItems[i].ProductTitle = product.Title
	}

	return admin.PackingSlipProps{
		Order:           order,
		LineItems:       enrichedItems,
		Customer:        customer,
		ShippingAddress: shippingAddress,
		MerchantTZ:      d.MerchantTZ,
	}, nil
}

func (d *Deps) handleAdminOrderInvoice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var props admin.OrderInvoiceProps
	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		props, txErr = d.loadOrderInvoiceProps(ctx, tx, id)
		return txErr
	})
	if err != nil {
		if errors.Is(err, app.ErrOrderNotFound) {
			http.NotFound(w, r)
			return
		}
		Error(w, r, err)
		return
	}

	admin.OrderInvoice(props).Render(ctx, w) //nolint:errcheck
}

// handleAdminOrderInvoiceBatch renders one print-ready HTML document containing
// invoices for every ID in the comma-separated `ids` query param. Used by the
// "print invoices" bulk action on the orders list — opens in a new tab and
// auto-fires window.print() on load.
func (d *Deps) handleAdminOrderInvoiceBatch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	raw := strings.TrimSpace(r.URL.Query().Get("ids"))
	if raw == "" {
		http.Error(w, "no orders selected", http.StatusBadRequest)
		return
	}

	parts := strings.Split(raw, ",")
	const maxBatch = 100
	if len(parts) > maxBatch {
		http.Error(w, fmt.Sprintf("too many orders (max %d per batch)", maxBatch), http.StatusBadRequest)
		return
	}

	ids := make([]uuid.UUID, 0, len(parts))
	seen := make(map[uuid.UUID]struct{}, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		id, err := uuid.Parse(p)
		if err != nil {
			http.Error(w, "invalid order id: "+p, http.StatusBadRequest)
			return
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		http.Error(w, "no orders selected", http.StatusBadRequest)
		return
	}

	items := make([]admin.OrderInvoiceProps, 0, len(ids))
	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		for _, id := range ids {
			props, txErr := d.loadOrderInvoiceProps(ctx, tx, id)
			if txErr != nil {
				return txErr
			}
			items = append(items, props)
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

	admin.OrderInvoiceBatch(admin.OrderInvoiceBatchProps{Items: items}).Render(ctx, w) //nolint:errcheck
}

// maxBatchOrderIDs caps the bulk-fulfillment forms at the same size as the
// invoice batch — 100 orders per action.
const maxBatchOrderIDs = 100

// maxFailuresInRedirect bounds the per-order failure list carried back in the
// /admin/fulfillment redirect URL. Above this count, the rest are summarised
// behind fail_truncated=1 so the URL stays well under typical 8KB limits.
const maxFailuresInRedirect = 25

// parseBatchOrderIDs parses a comma-separated list of order UUIDs from a bulk
// action form field. Trims whitespace, dedupes, enforces maxBatchOrderIDs.
// Returns (ids, 0, "") on success, or (nil, status, message) on failure that
// the caller should write back as an HTTP error response.
func parseBatchOrderIDs(raw string) ([]uuid.UUID, int, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, http.StatusBadRequest, "no orders selected"
	}
	parts := strings.Split(raw, ",")
	if len(parts) > maxBatchOrderIDs {
		return nil, http.StatusBadRequest, fmt.Sprintf("too many orders (max %d per batch)", maxBatchOrderIDs)
	}
	ids := make([]uuid.UUID, 0, len(parts))
	seen := make(map[uuid.UUID]struct{}, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		id, err := uuid.Parse(p)
		if err != nil {
			return nil, http.StatusBadRequest, "invalid order id: " + p
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, http.StatusBadRequest, "no orders selected"
	}
	return ids, 0, ""
}

// buildBatchResultRedirect constructs the /admin/fulfillment redirect URL
// carrying the result of a just-completed bulk verb. Reasons are joined with
// commas; failureReasonFor's output never contains commas today so the
// receiver can split unambiguously.
func buildBatchResultRedirect(verb string, outcome app.BatchOutcome) string {
	v := url.Values{}
	v.Set("batch_result", verb)
	v.Set("ok", strconv.Itoa(len(outcome.Succeeded)))
	v.Set("fail", strconv.Itoa(len(outcome.Failed)))
	if len(outcome.Failed) > 0 {
		n := len(outcome.Failed)
		truncated := false
		if n > maxFailuresInRedirect {
			n = maxFailuresInRedirect
			truncated = true
		}
		ids := make([]string, n)
		reasons := make([]string, n)
		for i := 0; i < n; i++ {
			ids[i] = outcome.Failed[i].OrderID.String()
			reasons[i] = outcome.Failed[i].Reason
		}
		v.Set("fail_ids", strings.Join(ids, ","))
		v.Set("fail_reasons", strings.Join(reasons, ","))
		if truncated {
			v.Set("fail_truncated", "1")
		}
	}
	return "/admin/fulfillment?" + v.Encode()
}

// handleAdminOrderPackingSlipBatch renders N packing slips in one print-ready
// document. GET /admin/orders/batch/packing-slips?ids=uuid1,uuid2,... Mirrors
// handleAdminOrderInvoiceBatch exactly — opens in a new tab from the
// fulfillment bulk action bar and fires window.print() on load.
func (d *Deps) handleAdminOrderPackingSlipBatch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	ids, status, msg := parseBatchOrderIDs(r.URL.Query().Get("ids"))
	if status != 0 {
		http.Error(w, msg, status)
		return
	}

	items := make([]admin.PackingSlipProps, 0, len(ids))
	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		for _, id := range ids {
			props, txErr := d.loadPackingSlipProps(ctx, tx, id)
			if txErr != nil {
				return txErr
			}
			items = append(items, props)
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

	admin.PackingSlipBatch(admin.PackingSlipBatchProps{Items: items}).Render(ctx, w) //nolint:errcheck
}

// handleAdminOrderReadyForPickupBatch applies MarkReadyForPickup to each order
// in the posted order_ids list. POST /admin/orders/batch/ready-for-pickup.
// Same redirect-with-banner shape as handleAdminOrderPickedUpBatch. Each
// successful row enqueues a "your order is ready" email.
func (d *Deps) handleAdminOrderReadyForPickupBatch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	ids, status, msg := parseBatchOrderIDs(r.FormValue("order_ids"))
	if status != 0 {
		http.Error(w, msg, status)
		return
	}
	outcome, err := d.OrderService.MarkReadyForPickupBatch(ctx, d.Pool, ids, staffActor(r))
	if err != nil {
		Error(w, r, err)
		return
	}
	http.Redirect(w, r, buildBatchResultRedirect("ready-for-pickup", outcome), http.StatusSeeOther)
}

// handleAdminOrderPickedUpBatch applies MarkPickedUp to each order in the
// posted order_ids list. POST /admin/orders/batch/picked-up. Always redirects
// to /admin/fulfillment with a structured batch_result query string the
// fulfillment list templ renders as the inline banner — including per-order
// failure reasons so staff can fix the rejected rows.
func (d *Deps) handleAdminOrderPickedUpBatch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	ids, status, msg := parseBatchOrderIDs(r.FormValue("order_ids"))
	if status != 0 {
		http.Error(w, msg, status)
		return
	}
	outcome, err := d.OrderService.MarkPickedUpBatch(ctx, d.Pool, ids, staffActor(r))
	if err != nil {
		Error(w, r, err)
		return
	}
	http.Redirect(w, r, buildBatchResultRedirect("picked-up", outcome), http.StatusSeeOther)
}

// handleAdminOrderOutForDeliveryBatch applies MarkOutForDelivery to each order
// in the posted order_ids list. POST /admin/orders/batch/out-for-delivery.
// Same redirect-with-banner shape as handleAdminOrderPickedUpBatch.
func (d *Deps) handleAdminOrderOutForDeliveryBatch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	ids, status, msg := parseBatchOrderIDs(r.FormValue("order_ids"))
	if status != 0 {
		http.Error(w, msg, status)
		return
	}
	outcome, err := d.OrderService.MarkOutForDeliveryBatch(ctx, d.Pool, ids, staffActor(r))
	if err != nil {
		Error(w, r, err)
		return
	}
	http.Redirect(w, r, buildBatchResultRedirect("out-for-delivery", outcome), http.StatusSeeOther)
}

// loadOrderInvoiceProps fetches everything needed to render a single invoice
// (order, line items + product/variant enrichment, customer, both addresses).
// Pure read; safe to call multiple times in one tx for batch rendering.
func (d *Deps) loadOrderInvoiceProps(ctx context.Context, tx pgx.Tx, id uuid.UUID) (admin.OrderInvoiceProps, error) {
	order, err := d.OrderService.GetOrderAsStaff(ctx, tx, id)
	if err != nil {
		return admin.OrderInvoiceProps{}, err
	}
	lineItems, err := d.OrderService.ListLineItems(ctx, tx, id)
	if err != nil {
		return admin.OrderInvoiceProps{}, err
	}

	var customer *domain.Customer
	if order.CustomerID != nil {
		customer, err = d.CustomerService.GetCustomer(ctx, tx, *order.CustomerID)
		if err != nil && !errors.Is(err, app.ErrCustomerNotFound) {
			return admin.OrderInvoiceProps{}, err
		}
	}

	shippingAddress, err := d.CustomerService.GetAddressByIDAsStaff(ctx, tx, order.ShippingAddressID)
	if err != nil && !errors.Is(err, app.ErrAddressNotFound) {
		return admin.OrderInvoiceProps{}, err
	}

	var billingAddress *domain.Address
	if order.BillingAddressID == order.ShippingAddressID {
		billingAddress = shippingAddress
	} else {
		billingAddress, err = d.CustomerService.GetAddressByIDAsStaff(ctx, tx, order.BillingAddressID)
		if err != nil && !errors.Is(err, app.ErrAddressNotFound) {
			return admin.OrderInvoiceProps{}, err
		}
	}

	enrichedItems := make([]admin.EnrichedLineItem, len(lineItems))
	for i, li := range lineItems {
		enrichedItems[i] = admin.EnrichedLineItem{LineItem: li}

		variant, vErr := d.CatalogService.GetVariant(ctx, tx, li.VariantID)
		if vErr != nil {
			if errors.Is(vErr, app.ErrVariantNotFound) {
				continue
			}
			return admin.OrderInvoiceProps{}, vErr
		}
		enrichedItems[i].VariantSKU = variant.SKU

		product, pErr := d.CatalogService.GetProduct(ctx, tx, variant.ProductID)
		if pErr != nil {
			if errors.Is(pErr, app.ErrProductNotFound) {
				continue
			}
			return admin.OrderInvoiceProps{}, pErr
		}
		enrichedItems[i].ProductTitle = product.Title
	}

	return admin.OrderInvoiceProps{
		Order:           order,
		LineItems:       enrichedItems,
		Customer:        customer,
		ShippingAddress: shippingAddress,
		BillingAddress:  billingAddress,
		MerchantTZ:      d.MerchantTZ,
	}, nil
}

// --- Manual order entry ---

// handleAdminOrderNew renders the manual order entry form.
func (d *Deps) handleAdminOrderNew(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	name, role := staffNameRole(r)
	props := admin.OrderNewProps{
		Form:      admin.OrderNewForm{},
		StaffName: name,
		StaffRole: role,
	}
	if IsHTMX(r) {
		admin.OrderNewContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.OrderNew(props).Render(ctx, w) //nolint:errcheck
}

// handleAdminOrderCreate parses the manual order form, looks up or creates the
// customer, creates a fresh shipping address, validates each line item against
// the catalog, and invokes CheckoutService.CreateManualOrder.
func (d *Deps) handleAdminOrderCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := logging.FromContext(ctx)

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	form := admin.OrderNewForm{
		CustomerEmail:         strings.TrimSpace(r.FormValue("customer_email")),
		CustomerFirstName:     strings.TrimSpace(r.FormValue("customer_first_name")),
		CustomerLastName:      strings.TrimSpace(r.FormValue("customer_last_name")),
		ShippingFirstName:     strings.TrimSpace(r.FormValue("shipping_first_name")),
		ShippingLastName:      strings.TrimSpace(r.FormValue("shipping_last_name")),
		ShippingLine1:         strings.TrimSpace(r.FormValue("shipping_line1")),
		ShippingLine2:         strings.TrimSpace(r.FormValue("shipping_line2")),
		ShippingCity:          strings.TrimSpace(r.FormValue("shipping_city")),
		ShippingState:         strings.TrimSpace(r.FormValue("shipping_state")),
		ShippingPostalCode:    strings.TrimSpace(r.FormValue("shipping_postal_code")),
		ShippingCountry:       strings.TrimSpace(r.FormValue("shipping_country")),
		StripePaymentIntentID: strings.TrimSpace(r.FormValue("stripe_payment_intent_id")),
		PaymentStatus:         strings.TrimSpace(r.FormValue("payment_status")),
		OrderStatus:           strings.TrimSpace(r.FormValue("order_status")),
		Notes:                 strings.TrimSpace(r.FormValue("notes")),
		SubtotalCents:         r.FormValue("subtotal_cents"),
		DiscountCents:         r.FormValue("discount_cents"),
		ShippingCents:         r.FormValue("shipping_cents"),
		TaxCents:              r.FormValue("tax_cents"),
		TotalCents:            r.FormValue("total_cents"),
	}

	skus := r.Form["item_sku"]
	qtys := r.Form["item_quantity"]
	prices := r.Form["item_unit_price_cents"]
	for i := range skus {
		row := admin.OrderNewItemRow{SKU: strings.TrimSpace(skus[i])}
		if i < len(qtys) {
			row.Quantity = strings.TrimSpace(qtys[i])
		}
		if i < len(prices) {
			row.UnitPriceCents = strings.TrimSpace(prices[i])
		}
		form.Items = append(form.Items, row)
	}
	if form.ShippingCountry == "" {
		form.ShippingCountry = "US"
	}

	errs := map[string]string{}
	if form.CustomerEmail == "" {
		errs["customer_email"] = "Email is required"
	}
	if form.ShippingFirstName == "" {
		errs["shipping_first_name"] = "First name is required"
	}
	if form.ShippingLastName == "" {
		errs["shipping_last_name"] = "Last name is required"
	}
	if form.ShippingLine1 == "" {
		errs["shipping_line1"] = "Address is required"
	}
	if form.ShippingCity == "" {
		errs["shipping_city"] = "City is required"
	}
	if form.ShippingState == "" {
		errs["shipping_state"] = "State is required"
	}
	if form.ShippingPostalCode == "" {
		errs["shipping_postal_code"] = "Postal code is required"
	}

	subtotal, ok := parseCents(form.SubtotalCents)
	if !ok {
		errs["subtotal_cents"] = "Must be a whole number of cents"
	}
	discount, ok := parseCents(form.DiscountCents)
	if !ok {
		errs["discount_cents"] = "Must be a whole number of cents"
	}
	shippingCents, ok := parseCents(form.ShippingCents)
	if !ok {
		errs["shipping_cents"] = "Must be a whole number of cents"
	}
	taxCents, ok := parseCents(form.TaxCents)
	if !ok {
		errs["tax_cents"] = "Must be a whole number of cents"
	}
	total, ok := parseCents(form.TotalCents)
	if !ok {
		errs["total_cents"] = "Must be a whole number of cents"
	}

	switch domain.PaymentStatus(form.PaymentStatus) {
	case domain.PaymentStatusAwaiting,
		domain.PaymentStatusAuthorized,
		domain.PaymentStatusCaptured,
		domain.PaymentStatusFailed,
		domain.PaymentStatusRefunded:
	default:
		errs["payment_status"] = "Invalid payment status"
	}
	switch domain.OrderStatus(form.OrderStatus) {
	case domain.OrderStatusPending,
		domain.OrderStatusConfirmed,
		domain.OrderStatusProcessing,
		domain.OrderStatusComplete,
		domain.OrderStatusCancelled:
	default:
		errs["order_status"] = "Invalid order status"
	}

	// Validate line item rows. Empty rows are skipped (admin may leave trailing
	// blanks). Variant SKU lookup happens inside the tx.
	validRows := 0
	for i, row := range form.Items {
		if row.SKU == "" && row.Quantity == "" && row.UnitPriceCents == "" {
			continue
		}
		if row.SKU == "" {
			errs[fmt.Sprintf("item_%d_sku", i)] = "SKU is required"
			continue
		}
		if qty, qOk := parseCents(row.Quantity); !qOk || qty <= 0 {
			errs[fmt.Sprintf("item_%d_quantity", i)] = "Quantity must be > 0"
			continue
		}
		if unit, uOk := parseCents(row.UnitPriceCents); !uOk || unit < 0 {
			errs[fmt.Sprintf("item_%d_unit_price_cents", i)] = "Unit price must be ≥ 0"
			continue
		}
		validRows++
	}
	if validRows == 0 {
		errs["items"] = "Add at least one line item"
	}

	if len(errs) > 0 {
		renderManualOrderForm(ctx, w, r, form, errs, "")
		return
	}

	piPtr := (*string)(nil)
	if form.StripePaymentIntentID != "" {
		v := form.StripePaymentIntentID
		piPtr = &v
	}
	notesPtr := (*string)(nil)
	if form.Notes != "" {
		v := form.Notes
		notesPtr = &v
	}

	var order *domain.Order
	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		// Resolve variant IDs from SKUs (one query per row — fine for admin).
		manualItems := make([]app.ManualOrderItem, 0, validRows)
		for i, row := range form.Items {
			if row.SKU == "" && row.Quantity == "" && row.UnitPriceCents == "" {
				continue
			}
			variant, vErr := d.CatalogService.GetVariantBySKU(ctx, tx, row.SKU)
			if vErr != nil {
				if errors.Is(vErr, app.ErrVariantNotFound) {
					errs[fmt.Sprintf("item_%d_sku", i)] = "SKU not found"
					return errSKUValidation
				}
				return fmt.Errorf("lookup variant %s: %w", row.SKU, vErr)
			}
			qty, _ := parseCents(row.Quantity)
			unit, _ := parseCents(row.UnitPriceCents)
			manualItems = append(manualItems, app.ManualOrderItem{
				VariantID: variant.ID,
				Quantity:  qty,
				UnitPrice: unit,
			})
		}

		// Find or create customer.
		customer, cErr := d.CustomerService.GetCustomerByEmail(ctx, tx, form.CustomerEmail)
		if cErr != nil {
			if !errors.Is(cErr, app.ErrCustomerNotFound) {
				return fmt.Errorf("lookup customer: %w", cErr)
			}
			if form.CustomerFirstName == "" || form.CustomerLastName == "" {
				errs["customer_email"] = "No customer with that email — provide first and last name to create one"
				return errSKUValidation
			}
			customer, cErr = d.CustomerService.CreateRetail(ctx, tx, form.CustomerEmail, form.CustomerFirstName, form.CustomerLastName, nil)
			if cErr != nil {
				return fmt.Errorf("create customer: %w", cErr)
			}
		}

		var line2 *string
		if form.ShippingLine2 != "" {
			v := form.ShippingLine2
			line2 = &v
		}
		actor := staffActor(r)
		address, aErr := d.CustomerService.CreateAddress(ctx, tx, store.CreateAddressParams{
			CustomerID:  &customer.ID,
			FirstName:   form.ShippingFirstName,
			LastName:    form.ShippingLastName,
			Line1:       form.ShippingLine1,
			Line2:       line2,
			City:        form.ShippingCity,
			State:       form.ShippingState,
			PostalCode:  form.ShippingPostalCode,
			CountryCode: form.Country(),
		}, actor)
		if aErr != nil {
			return fmt.Errorf("create address: %w", aErr)
		}

		var oErr error
		order, oErr = d.CheckoutService.CreateManualOrder(ctx, tx, app.CreateManualOrderParams{
			CustomerID:            customer.ID,
			Items:                 manualItems,
			ShippingAddressID:     address.ID,
			BillingAddressID:      address.ID,
			CurrencyCode:          "USD",
			Subtotal:              subtotal,
			DiscountTotal:         discount,
			ShippingTotal:         shippingCents,
			TaxTotal:              taxCents,
			Total:                 total,
			Status:                domain.OrderStatus(form.OrderStatus),
			PaymentStatus:         domain.PaymentStatus(form.PaymentStatus),
			StripePaymentIntentID: piPtr,
			Notes:                 notesPtr,
		}, staffActor(r))
		if oErr != nil {
			return fmt.Errorf("create manual order: %w", oErr)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, errSKUValidation) {
			renderManualOrderForm(ctx, w, r, form, errs, "")
			return
		}
		logger.Error("create manual order", "error", err)
		renderManualOrderForm(ctx, w, r, form, errs, "Could not create order: "+err.Error())
		return
	}

	http.Redirect(w, r, "/admin/orders/"+order.ID.String()+"?flash=Manual+order+created", http.StatusSeeOther)
}

// errSKUValidation is a sentinel used to bail out of the manual-order tx and
// fall through to form re-rendering when input validation fails inside the tx.
var errSKUValidation = errors.New("manual order: validation")

func renderManualOrderForm(ctx context.Context, w http.ResponseWriter, r *http.Request, form admin.OrderNewForm, errs map[string]string, banner string) {
	name, role := staffNameRole(r)
	props := admin.OrderNewProps{
		Form:        form,
		FieldErrors: errs,
		Banner:      banner,
		StaffName:   name,
		StaffRole:   role,
	}
	if IsHTMX(r) {
		admin.OrderNewContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.OrderNew(props).Render(ctx, w) //nolint:errcheck
}

// parseCents accepts a decimal string and returns the integer value. Empty
// string parses as 0 (so optional discount/shipping/tax fields default cleanly).
func parseCents(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, true
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}

