package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

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
		MerchantTZ:   d.MerchantTZ,
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
		MerchantTZ:      d.MerchantTZ,
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
		order, txErr = d.OrderService.GetOrderAsStaff(ctx, tx, id)
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

		shippingAddress, txErr = d.CustomerService.GetAddressByIDAsStaff(ctx, tx, order.ShippingAddressID)
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
		MerchantTZ:      d.MerchantTZ,
	}

	admin.PackingSlip(props).Render(ctx, w) //nolint:errcheck
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
			customer, cErr = d.CustomerService.CreateRetail(ctx, tx, form.CustomerEmail, form.CustomerFirstName, form.CustomerLastName)
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

