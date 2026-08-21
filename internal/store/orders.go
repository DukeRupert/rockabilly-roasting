package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store/sqlcgen"
)

// OrderStore provides database access for orders, carts, line items, and adjustments.
type OrderStore struct {
	metrics QueryRecorder
}

// NewOrderStore creates a new OrderStore. Pass nil for metrics to disable
// query timing instrumentation (e.g. in tests or one-off CLI tools).
func NewOrderStore(metrics QueryRecorder) *OrderStore {
	return &OrderStore{metrics: metrics}
}

// --- Orders ---

// CreateOrderParams holds the fields needed to create an order.
type CreateOrderParams struct {
	Number                string
	CustomerID            *uuid.UUID
	Channel               domain.OrderChannel
	Status                domain.OrderStatus
	PaymentStatus         domain.PaymentStatus
	FulfillmentStatus     domain.FulfillmentStatus
	CurrencyCode          string
	Subtotal              int
	DiscountTotal         int
	ShippingTotal         int
	TaxTotal              int
	Total                 int
	ShippingAddressID     uuid.UUID
	BillingAddressID      uuid.UUID
	SubscriptionID        *uuid.UUID
	DraftByUserID         *uuid.UUID
	TaxExempt             bool
	TaxExemptReason       *string
	StripeTaxID           *string
	ShippingMethod        *domain.ShippingMethod
	RequestedDeliveryDate *time.Time
	// ScheduledDeliveryDate is the local-delivery run this order was promised.
	// Nil for every other method; see domain.Order.ScheduledDeliveryDate.
	ScheduledDeliveryDate *time.Time
	Notes                 *string
	Metadata              map[string]any
	PlacedAt              time.Time
}

// CreateOrder inserts a new order and returns it.
func (s *OrderStore) CreateOrder(ctx context.Context, tx pgx.Tx, p CreateOrderParams) (_ *domain.Order, err error) {
	defer trackQuery(s.metrics, "orders.create", time.Now(), &err)
	var shippingMethod *string
	if p.ShippingMethod != nil {
		s := string(*p.ShippingMethod)
		shippingMethod = &s
	}
	// Default to retail so callers that don't care about channel (and the DB
	// DEFAULT) stay consistent; the CHECK constraint rejects an empty string.
	channel := p.Channel
	if channel == "" {
		channel = domain.OrderChannelRetail
	}
	row, err := sqlcgen.New(tx).CreateOrder(ctx, sqlcgen.CreateOrderParams{
		ID:                    uuid.New(),
		Number:                p.Number,
		CustomerID:            p.CustomerID,
		Channel:               string(channel),
		Status:                string(p.Status),
		PaymentStatus:         string(p.PaymentStatus),
		FulfillmentStatus:     string(p.FulfillmentStatus),
		CurrencyCode:          p.CurrencyCode,
		Subtotal:              int32(p.Subtotal),
		DiscountTotal:         int32(p.DiscountTotal),
		ShippingTotal:         int32(p.ShippingTotal),
		TaxTotal:              int32(p.TaxTotal),
		Total:                 int32(p.Total),
		ShippingAddressID:     p.ShippingAddressID,
		BillingAddressID:      p.BillingAddressID,
		SubscriptionID:        p.SubscriptionID,
		DraftByUserID:         p.DraftByUserID,
		TaxExempt:             p.TaxExempt,
		TaxExemptReason:       p.TaxExemptReason,
		StripeTaxID:           p.StripeTaxID,
		ShippingMethod:        shippingMethod,
		RequestedDeliveryDate: timestampToPG(p.RequestedDeliveryDate),
		ScheduledDeliveryDate: dateTimeToPG(p.ScheduledDeliveryDate),
		Notes:                 p.Notes,
		Metadata:              metadataToJSON(p.Metadata),
		PlacedAt:              p.PlacedAt,
	})
	if err != nil {
		return nil, fmt.Errorf("insert order: %w", err)
	}
	return orderFromRow(row), nil
}

// GetOrderByIDAsStaff returns an order by ID.
func (s *OrderStore) GetOrderByIDAsStaff(ctx context.Context, tx pgx.Tx, id uuid.UUID) (_ *domain.Order, err error) {
	defer trackQuery(s.metrics, "orders.get_by_id", time.Now(), &err)
	row, err := sqlcgen.New(tx).GetOrderByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get order %s: %w", id, err)
	}
	return orderFromRow(row), nil
}

// GetOrderByIDForUpdate returns an order by ID and takes a row-level lock so a
// manual payment override serializes against a concurrent QB reconcile on the
// same order (mirrors GetOrderByQBInvoiceIDForUpdate, but keyed by order id).
func (s *OrderStore) GetOrderByIDForUpdate(ctx context.Context, tx pgx.Tx, id uuid.UUID) (_ *domain.Order, err error) {
	defer trackQuery(s.metrics, "orders.get_by_id_for_update", time.Now(), &err)
	row, err := sqlcgen.New(tx).GetOrderByIDForUpdate(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get order %s (for update): %w", id, err)
	}
	return orderFromRow(row), nil
}

// GetOrderByNumberAsStaff returns an order by its number.
func (s *OrderStore) GetOrderByNumberAsStaff(ctx context.Context, tx pgx.Tx, number string) (_ *domain.Order, err error) {
	defer trackQuery(s.metrics, "orders.get_by_number", time.Now(), &err)
	row, err := sqlcgen.New(tx).GetOrderByNumber(ctx, number)
	if err != nil {
		return nil, fmt.Errorf("get order by number: %w", err)
	}
	return orderFromRow(row), nil
}

// UpdateOrderStatus updates an order's status and returns it.
func (s *OrderStore) UpdateOrderStatus(ctx context.Context, tx pgx.Tx, id uuid.UUID, status domain.OrderStatus) (_ *domain.Order, err error) {
	defer trackQuery(s.metrics, "orders.update_status", time.Now(), &err)
	row, err := sqlcgen.New(tx).UpdateOrderStatus(ctx, sqlcgen.UpdateOrderStatusParams{
		ID:     id,
		Status: string(status),
	})
	if err != nil {
		return nil, fmt.Errorf("update order status: %w", err)
	}
	return orderFromRow(row), nil
}

// UpdateOrderPaymentStatus updates an order's payment status and returns it.
func (s *OrderStore) UpdateOrderPaymentStatus(ctx context.Context, tx pgx.Tx, id uuid.UUID, status domain.PaymentStatus) (_ *domain.Order, err error) {
	defer trackQuery(s.metrics, "orders.update_payment_status", time.Now(), &err)
	row, err := sqlcgen.New(tx).UpdateOrderPaymentStatus(ctx, sqlcgen.UpdateOrderPaymentStatusParams{
		ID:            id,
		PaymentStatus: string(status),
	})
	if err != nil {
		return nil, fmt.Errorf("update order payment status: %w", err)
	}
	return orderFromRow(row), nil
}

// UpdateOrderFulfillmentStatus updates an order's fulfillment status and returns it.
func (s *OrderStore) UpdateOrderFulfillmentStatus(ctx context.Context, tx pgx.Tx, id uuid.UUID, status domain.FulfillmentStatus) (_ *domain.Order, err error) {
	defer trackQuery(s.metrics, "orders.update_fulfillment_status", time.Now(), &err)
	row, err := sqlcgen.New(tx).UpdateOrderFulfillmentStatus(ctx, sqlcgen.UpdateOrderFulfillmentStatusParams{
		ID:                id,
		FulfillmentStatus: string(status),
	})
	if err != nil {
		return nil, fmt.Errorf("update order fulfillment status: %w", err)
	}
	return orderFromRow(row), nil
}

// UpdateOrderShippingMethod sets the shipping method on an order and returns it.
func (s *OrderStore) UpdateOrderShippingMethod(ctx context.Context, tx pgx.Tx, id uuid.UUID, method domain.ShippingMethod) (_ *domain.Order, err error) {
	defer trackQuery(s.metrics, "orders.update_shipping_method", time.Now(), &err)
	m := string(method)
	row, err := sqlcgen.New(tx).UpdateOrderShippingMethod(ctx, sqlcgen.UpdateOrderShippingMethodParams{
		ID:             id,
		ShippingMethod: &m,
	})
	if err != nil {
		return nil, fmt.Errorf("update order shipping method: %w", err)
	}
	return orderFromRow(row), nil
}

// SwitchOrderToPickup moves a local-delivery order to pickup and clears its
// delivery promise. ok is false when the order was not on local delivery — it
// was already switched, or staff changed the method — which lets the caller
// treat a replayed click as a no-op rather than an error.
func (s *OrderStore) SwitchOrderToPickup(ctx context.Context, tx pgx.Tx, id uuid.UUID) (_ *domain.Order, ok bool, err error) {
	defer trackQuery(s.metrics, "orders.switch_to_pickup", time.Now(), &err)
	row, err := sqlcgen.New(tx).SwitchOrderToPickup(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("switch order to pickup: %w", err)
	}
	return orderFromRow(row), true, nil
}

// UpdateOrderStripePaymentIntentID sets the Stripe PaymentIntent ID on an order.
func (s *OrderStore) UpdateOrderStripePaymentIntentID(ctx context.Context, tx pgx.Tx, id uuid.UUID, intentID string) (_ *domain.Order, err error) {
	defer trackQuery(s.metrics, "orders.update_stripe_payment_intent_id", time.Now(), &err)
	row, err := sqlcgen.New(tx).UpdateOrderStripePaymentIntentID(ctx, sqlcgen.UpdateOrderStripePaymentIntentIDParams{
		ID:                    id,
		StripePaymentIntentID: &intentID,
	})
	if err != nil {
		return nil, fmt.Errorf("update order stripe payment intent id: %w", err)
	}
	return orderFromRow(row), nil
}

// UpdateOrderSubscriptionID links an order to a subscription after the fact.
// Used by the subscribe flow, where the order is pre-created at PaymentIntent
// time and the subscription only comes into existence once payment succeeds.
func (s *OrderStore) UpdateOrderSubscriptionID(ctx context.Context, tx pgx.Tx, id, subscriptionID uuid.UUID) (err error) {
	defer trackQuery(s.metrics, "orders.update_subscription_id", time.Now(), &err)
	tag, err := tx.Exec(ctx,
		`UPDATE orders SET subscription_id = $2, updated_at = now() WHERE id = $1`,
		id, subscriptionID,
	)
	if err != nil {
		return fmt.Errorf("update order subscription id: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// GetOrderByStripePaymentIntentIDAsStaff returns an order by its Stripe PaymentIntent ID.
func (s *OrderStore) GetOrderByStripePaymentIntentIDAsStaff(ctx context.Context, tx pgx.Tx, intentID string) (_ *domain.Order, err error) {
	defer trackQuery(s.metrics, "orders.get_by_stripe_payment_intent_id", time.Now(), &err)
	row, err := sqlcgen.New(tx).GetOrderByStripePaymentIntentID(ctx, &intentID)
	if err != nil {
		return nil, fmt.Errorf("get order by stripe payment intent id: %w", err)
	}
	return orderFromRow(row), nil
}

// GetOrderByStripePaymentIntentIDForUpdate returns an order by its Stripe
// PaymentIntent ID and takes a row-level lock. Concurrent transactions on the
// same order serialize: the second tx waits for the first to commit and then
// sees the post-transition state, so callers' conditional state transitions
// (and the side effects gated on them) don't double-fire.
func (s *OrderStore) GetOrderByStripePaymentIntentIDForUpdate(ctx context.Context, tx pgx.Tx, intentID string) (_ *domain.Order, err error) {
	defer trackQuery(s.metrics, "orders.get_by_stripe_payment_intent_id_for_update", time.Now(), &err)
	row, err := sqlcgen.New(tx).GetOrderByStripePaymentIntentIDForUpdate(ctx, &intentID)
	if err != nil {
		return nil, fmt.Errorf("get order by stripe payment intent id (for update): %w", err)
	}
	return orderFromRow(row), nil
}

// SetCustomerPONumber sets the customer PO number on a wholesale order.
func (s *OrderStore) SetCustomerPONumber(ctx context.Context, tx pgx.Tx, id uuid.UUID, poNumber string) (err error) {
	defer trackQuery(s.metrics, "orders.set_customer_po_number", time.Now(), &err)
	_, err = tx.Exec(ctx,
		`UPDATE orders SET customer_po_number = $2 WHERE id = $1`,
		id, poNumber,
	)
	if err != nil {
		return fmt.Errorf("set customer po number: %w", err)
	}
	return nil
}

// DeleteOrder removes an order by ID.
func (s *OrderStore) DeleteOrder(ctx context.Context, tx pgx.Tx, id uuid.UUID) (err error) {
	defer trackQuery(s.metrics, "orders.delete", time.Now(), &err)
	if err := sqlcgen.New(tx).DeleteOrder(ctx, id); err != nil {
		return fmt.Errorf("delete order: %w", err)
	}
	return nil
}

// OrderFilter holds optional filters for listing orders.
type OrderFilter struct {
	Status              *domain.OrderStatus
	Statuses            []domain.OrderStatus // IN filter (takes precedence over singular)
	FulfillmentStatus   *domain.FulfillmentStatus
	FulfillmentStatuses []domain.FulfillmentStatus // IN filter (takes precedence over singular)
	PaymentStatuses     []domain.PaymentStatus     // IN filter; empty = no constraint
	// Channel narrows to a single sales channel (retail / wholesale). Nil leaves
	// the channel unconstrained — dashboards and revenue counts span both.
	Channel *domain.OrderChannel
	// ShippingMethod narrows to a single fulfillment method (shipped / local
	// delivery / pickup). Nil leaves it unconstrained. Orders with no method
	// set never match a non-nil filter.
	ShippingMethod *domain.ShippingMethod
	// OrderIDs restricts the result to an explicit set of orders. Empty leaves
	// the set unconstrained; a non-empty list is ANDed with every other filter
	// (it narrows, it does not widen). Used by the delivery load list when
	// staff hand-pick which orders ride along.
	OrderIDs   []uuid.UUID
	CustomerID *uuid.UUID
	PlacedFrom          *time.Time
	PlacedTo            *time.Time
	Search              string // ILIKE on order number or customer name/email
	// ExcludeUnconfirmed drops orders that are still in the "intent to buy"
	// state — status=pending AND payment_status=awaiting. These exist between
	// PI creation and webhook-driven confirmation (especially for async/BNPL
	// payment methods like Klarna). Set on dashboards, fulfillment queues,
	// and customer-facing order history so unfinalized orders don't pollute
	// counts, revenue, or what the customer sees.
	ExcludeUnconfirmed bool
	// ExcludeCancelledRefunded drops orders in the cancelled/refunded terminal
	// states. Mirrors the exclusion baked into RevenueByDay so callers can
	// keep aggregate sums consistent with the daily-trend chart.
	ExcludeCancelledRefunded bool
	// OnlySubscription narrows to subscription-originated orders (true) or
	// one-time orders (false). Nil leaves the source unconstrained.
	OnlySubscription *bool
	// TotalMin / TotalMax bound the order total, in cents, inclusive. Nil
	// leaves that end unbounded.
	TotalMin *int
	TotalMax *int
	// Sort orders the result. The zero value sorts newest-placed first, which
	// is what every caller got before sorting existed.
	Sort  OrderSort
	Limit int
	Offset int
}

// OrderSort identifies how the list query should order results. Like
// CustomerSort, it's a closed enum rather than a column name so the HTTP layer
// can never reach a raw identifier into the ORDER BY.
type OrderSort string

const (
	OrderSortPlacedDesc OrderSort = "placed_desc"
	OrderSortPlacedAsc  OrderSort = "placed_asc"
	OrderSortTotalDesc  OrderSort = "total_desc"
	OrderSortTotalAsc   OrderSort = "total_asc"
	OrderSortNumberAsc  OrderSort = "number_asc"
	OrderSortNumberDesc OrderSort = "number_desc"
)

// orderOrderBy maps the sort enum to SQL. The default matches the historical
// behaviour every caller depends on: newest placed first.
func orderOrderBy(sort OrderSort) string {
	switch sort {
	case OrderSortPlacedAsc:
		return " ORDER BY placed_at ASC"
	case OrderSortTotalDesc:
		return " ORDER BY total DESC, placed_at DESC"
	case OrderSortTotalAsc:
		return " ORDER BY total ASC, placed_at DESC"
	case OrderSortNumberAsc:
		return " ORDER BY number ASC"
	case OrderSortNumberDesc:
		return " ORDER BY number DESC"
	default:
		return " ORDER BY placed_at DESC"
	}
}

// orderSearchClause is the SQL fragment matching a free-text term against an
// order. It lives here because three separate queries (ListOrders,
// CountOrders, CountOrdersByView) must interpret a search term identically —
// if they drift, the tab counts stop agreeing with the rows on screen.
//
// The customer half is a correlated EXISTS rather than a join so the outer
// query keeps its simple `FROM orders` shape and its unqualified columns.
func orderSearchClause(argN int) string {
	return fmt.Sprintf(
		" AND (number ILIKE $%d OR EXISTS (SELECT 1 FROM customers c WHERE c.id = orders.customer_id"+
			" AND (c.first_name || ' ' || c.last_name ILIKE $%d OR c.email ILIKE $%d"+
			" OR coalesce(c.company_name, '') ILIKE $%d)))",
		argN, argN, argN, argN)
}

// orderWhere grows query with the filter's WHERE clauses and returns the query,
// its args, and the next free placeholder number.
//
// ListOrders and CountOrders must filter identically or the "X–Y of Z"
// pagination total contradicts the rows on screen, so both build their WHERE
// here. SumOrderRevenue and ListDeliveryLoad also take an OrderFilter but
// deliberately keep their own narrower clause sets — they answer different
// questions (revenue totals, load sheets) and honour only the subset of the
// filter that makes sense for them.
func orderWhere(query string, f OrderFilter) (string, []any, int) {
	args := []any{}
	argN := 1

	appendIn := func(column string, values []string) {
		query += " AND " + column + " IN ("
		for i, v := range values {
			if i > 0 {
				query += ", "
			}
			query += fmt.Sprintf("$%d", argN)
			args = append(args, v)
			argN++
		}
		query += ")"
	}

	if f.Channel != nil {
		query += fmt.Sprintf(" AND channel = $%d", argN)
		args = append(args, string(*f.Channel))
		argN++
	}
	if len(f.Statuses) > 0 {
		vals := make([]string, len(f.Statuses))
		for i, s := range f.Statuses {
			vals[i] = string(s)
		}
		appendIn("status", vals)
	} else if f.Status != nil {
		query += fmt.Sprintf(" AND status = $%d", argN)
		args = append(args, string(*f.Status))
		argN++
	}
	if len(f.FulfillmentStatuses) > 0 {
		vals := make([]string, len(f.FulfillmentStatuses))
		for i, s := range f.FulfillmentStatuses {
			vals[i] = string(s)
		}
		appendIn("fulfillment_status", vals)
	} else if f.FulfillmentStatus != nil {
		query += fmt.Sprintf(" AND fulfillment_status = $%d", argN)
		args = append(args, string(*f.FulfillmentStatus))
		argN++
	}
	if len(f.PaymentStatuses) > 0 {
		vals := make([]string, len(f.PaymentStatuses))
		for i, s := range f.PaymentStatuses {
			vals[i] = string(s)
		}
		appendIn("payment_status", vals)
	}
	if f.ShippingMethod != nil {
		query += fmt.Sprintf(" AND shipping_method = $%d", argN)
		args = append(args, string(*f.ShippingMethod))
		argN++
	}
	if len(f.OrderIDs) > 0 {
		query += " AND id IN ("
		for i, id := range f.OrderIDs {
			if i > 0 {
				query += ", "
			}
			query += fmt.Sprintf("$%d", argN)
			args = append(args, id)
			argN++
		}
		query += ")"
	}
	if f.CustomerID != nil {
		query += fmt.Sprintf(" AND customer_id = $%d", argN)
		args = append(args, *f.CustomerID)
		argN++
	}
	if f.PlacedFrom != nil {
		query += fmt.Sprintf(" AND placed_at >= $%d", argN)
		args = append(args, *f.PlacedFrom)
		argN++
	}
	if f.PlacedTo != nil {
		query += fmt.Sprintf(" AND placed_at <= $%d", argN)
		args = append(args, *f.PlacedTo)
		argN++
	}
	if f.TotalMin != nil {
		query += fmt.Sprintf(" AND total >= $%d", argN)
		args = append(args, *f.TotalMin)
		argN++
	}
	if f.TotalMax != nil {
		query += fmt.Sprintf(" AND total <= $%d", argN)
		args = append(args, *f.TotalMax)
		argN++
	}
	if f.Search != "" {
		query += orderSearchClause(argN)
		args = append(args, "%"+f.Search+"%")
		argN++
	}
	if f.ExcludeUnconfirmed {
		query += " AND NOT (status = 'pending' AND payment_status = 'awaiting')"
	}
	if f.ExcludeCancelledRefunded {
		query += " AND status NOT IN ('cancelled', 'refunded')"
	}

	return query, args, argN
}

// ListOrders returns orders matching the given filter (hand-written for dynamic WHERE).
func (s *OrderStore) ListOrders(ctx context.Context, tx pgx.Tx, f OrderFilter) (_ []domain.Order, err error) {
	defer trackQuery(s.metrics, "orders.list", time.Now(), &err)
	query := `SELECT id, number, customer_id, channel, status, payment_status, fulfillment_status,
	                 currency_code, subtotal, discount_total, shipping_total, tax_total, total,
	                 shipping_address_id, billing_address_id, subscription_id, draft_by_user_id,
	                 tax_exempt, tax_exempt_reason, stripe_tax_id, stripe_payment_intent_id,
	                 shipping_method, requested_delivery_date,
	                 customer_po_number, internal_note,
	                 notes, metadata, placed_at, created_at, updated_at
	          FROM orders WHERE true`

	query, args, argN := orderWhere(query, f)

	query += orderOrderBy(f.Sort)

	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	query += fmt.Sprintf(" LIMIT $%d", argN)
	args = append(args, limit)
	argN++

	if f.Offset > 0 {
		query += fmt.Sprintf(" OFFSET $%d", argN)
		args = append(args, f.Offset)
	}

	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list orders: %w", err)
	}
	defer rows.Close()

	var orders []domain.Order
	for rows.Next() {
		var o domain.Order
		var channel, status, paymentStatus, fulfillmentStatus string
		var shippingMethod *string
		var requestedDeliveryDate pgtype.Timestamptz
		var subtotal, discountTotal, shippingTotal, taxTotal, total int32
		var metadata json.RawMessage
		if err := rows.Scan(
			&o.ID, &o.Number, &o.CustomerID, &channel, &status, &paymentStatus, &fulfillmentStatus,
			&o.CurrencyCode, &subtotal, &discountTotal, &shippingTotal, &taxTotal, &total,
			&o.ShippingAddressID, &o.BillingAddressID, &o.SubscriptionID, &o.DraftByUserID,
			&o.TaxExempt, &o.TaxExemptReason, &o.StripeTaxID, &o.StripePaymentIntentID,
			&shippingMethod, &requestedDeliveryDate,
			&o.CustomerPONumber, &o.InternalNote,
			&o.Notes, &metadata, &o.PlacedAt, &o.CreatedAt, &o.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan order: %w", err)
		}
		o.Channel = domain.OrderChannel(channel)
		o.Status = domain.OrderStatus(status)
		o.PaymentStatus = domain.PaymentStatus(paymentStatus)
		o.FulfillmentStatus = domain.FulfillmentStatus(fulfillmentStatus)
		o.Subtotal = int(subtotal)
		o.DiscountTotal = int(discountTotal)
		o.ShippingTotal = int(shippingTotal)
		o.TaxTotal = int(taxTotal)
		o.Total = int(total)
		if shippingMethod != nil {
			sm := domain.ShippingMethod(*shippingMethod)
			o.ShippingMethod = &sm
		}
		o.RequestedDeliveryDate = timestampFromPG(requestedDeliveryDate)
		o.Metadata = metadataFromJSON(metadata)
		orders = append(orders, o)
	}
	return orders, rows.Err()
}

// CountOrders returns the number of orders matching the given filter.
func (s *OrderStore) CountOrders(ctx context.Context, tx pgx.Tx, f OrderFilter) (_ int, err error) {
	defer trackQuery(s.metrics, "orders.count", time.Now(), &err)
	query, args, _ := orderWhere(`SELECT COUNT(*) FROM orders WHERE true`, f)

	var count int
	if err := tx.QueryRow(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count orders: %w", err)
	}
	return count, nil
}

// OrderViewCounts holds per-bucket totals for the admin orders list tabs.
// Buckets are workflow-led, not raw status enums:
//   - NeedsAction = confirmed + processing (excludes unconfirmed pending)
//   - OnHold      = on_hold
//   - Shipped     = complete
//   - Archive     = cancelled + refunded
//   - All         = every order, including unconfirmed
type OrderViewCounts struct {
	NeedsAction int
	OnHold      int
	Shipped     int
	Archive     int
	All         int
}

// CountOrdersByView returns counts for each tab on the admin orders list in
// one query. Search applies to all buckets so tab counts reflect the active
// search term. A non-nil channel scopes every bucket to that sales channel so
// the retail and wholesale order pages each show their own tab counts.
func (s *OrderStore) CountOrdersByView(ctx context.Context, tx pgx.Tx, search string, channel *domain.OrderChannel) (_ OrderViewCounts, err error) {
	defer trackQuery(s.metrics, "orders.count_by_view", time.Now(), &err)
	query := `SELECT
		COUNT(*) FILTER (WHERE status IN ('confirmed', 'processing') AND NOT (status = 'pending' AND payment_status = 'awaiting')) AS needs_action,
		COUNT(*) FILTER (WHERE status = 'on_hold')                                                                                AS on_hold,
		COUNT(*) FILTER (WHERE status = 'complete')                                                                               AS shipped,
		COUNT(*) FILTER (WHERE status IN ('cancelled', 'refunded'))                                                               AS archive,
		COUNT(*)                                                                                                                  AS total
	FROM orders WHERE true`
	args := []any{}
	argN := 1
	if channel != nil {
		query += fmt.Sprintf(" AND channel = $%d", argN)
		args = append(args, string(*channel))
		argN++
	}
	if search != "" {
		query += orderSearchClause(argN)
		args = append(args, "%"+search+"%")
	}

	var c OrderViewCounts
	if err := tx.QueryRow(ctx, query, args...).Scan(&c.NeedsAction, &c.OnHold, &c.Shipped, &c.Archive, &c.All); err != nil {
		return OrderViewCounts{}, fmt.Errorf("count orders by view: %w", err)
	}
	return c, nil
}

// FulfillmentViewCounts holds per-bucket totals for the admin fulfillment queue
// tabs. Buckets are workflow-led, not raw status enums:
//   - NeedsAction = unfulfilled + partially_fulfilled + fulfilled (anything
//     pre-handoff that still wants staff attention), excluding unconfirmed
//   - ReadyToShip = fulfilled only (packed, awaiting label or dispatch)
//   - Shipped    = shipped + partially_shipped
//   - Delivered  = delivered + partially_delivered
//   - All        = every order excluding unconfirmed
//   - LoadList   = the NeedsAction bucket narrowed to local delivery — the
//     orders a driver would be loading for the next run
type FulfillmentViewCounts struct {
	NeedsAction int
	ReadyToShip int
	Shipped     int
	Delivered   int
	All         int
	LoadList    int
}

// CountFulfillmentViews returns counts for every tab on the admin fulfillment
// list in a single query. Unconfirmed orders (status=pending+payment=awaiting)
// are excluded from every bucket — they don't belong in a pack-and-ship queue.
func (s *OrderStore) CountFulfillmentViews(ctx context.Context, tx pgx.Tx, channel *domain.OrderChannel) (_ FulfillmentViewCounts, err error) {
	defer trackQuery(s.metrics, "orders.count_fulfillment_views", time.Now(), &err)
	// NeedsAction and ReadyToShip exclude cancelled/refunded — those orders'
	// fulfillment_status often lingers at 'unfulfilled' from before they were
	// cancelled, so a fulfillment-status-only filter leaks them into the
	// working queue where staff can't act on them anyway. Shipped/Delivered
	// keep the broader set so a cancelled-after-shipping case still surfaces.
	query := `SELECT
		COUNT(*) FILTER (WHERE fulfillment_status IN ('unfulfilled', 'partially_fulfilled', 'fulfilled', 'ready_for_pickup') AND status NOT IN ('cancelled', 'refunded')) AS needs_action,
		COUNT(*) FILTER (WHERE fulfillment_status = 'fulfilled' AND status NOT IN ('cancelled', 'refunded'))                                                              AS ready_to_ship,
		COUNT(*) FILTER (WHERE fulfillment_status IN ('shipped', 'partially_shipped'))                                                                                    AS shipped,
		COUNT(*) FILTER (WHERE fulfillment_status IN ('delivered', 'partially_delivered'))                                                                                AS delivered,
		COUNT(*)                                                                                                                                                          AS total,
		COUNT(*) FILTER (WHERE fulfillment_status IN ('unfulfilled', 'partially_fulfilled', 'fulfilled', 'ready_for_pickup') AND status NOT IN ('cancelled', 'refunded') AND shipping_method = 'local_delivery') AS load_list
	FROM orders
	WHERE NOT (status = 'pending' AND payment_status = 'awaiting')`
	args := []any{}
	// Scope to a single channel when asked, so the retail and wholesale
	// fulfillment queues show counts that match their own rows.
	if channel != nil {
		query += " AND channel = $1"
		args = append(args, string(*channel))
	}
	var c FulfillmentViewCounts
	if err := tx.QueryRow(ctx, query, args...).Scan(&c.NeedsAction, &c.ReadyToShip, &c.Shipped, &c.Delivered, &c.All, &c.LoadList); err != nil {
		return FulfillmentViewCounts{}, fmt.Errorf("count fulfillment views: %w", err)
	}
	return c, nil
}

// ListAbandonedOrderIDs returns the IDs of pre-paid-intent orders (status=pending
// AND payment_status=awaiting) older than olderThan. Used by the periodic
// cleanup worker to cancel orders whose customer never completed payment.
// Returns IDs only — the worker re-fetches each order before cancelling so
// the FOR UPDATE lock is taken inside the cancellation transaction.
func (s *OrderStore) ListAbandonedOrderIDs(ctx context.Context, tx pgx.Tx, olderThan time.Time, limit int) (_ []uuid.UUID, err error) {
	defer trackQuery(s.metrics, "orders.list_abandoned_ids", time.Now(), &err)
	if limit <= 0 {
		limit = 100
	}
	// payment_status 'failed' is included alongside 'awaiting': a failed
	// attempt on a still-live PaymentIntent leaves the order in
	// pending+failed (the customer may retry the same PI), and if they never
	// do, this sweep is what cancels the order and releases its coupon.
	rows, err := tx.Query(ctx,
		`SELECT id FROM orders
		 WHERE status = 'pending'
		   AND payment_status IN ('awaiting', 'failed')
		   AND placed_at < $1
		 ORDER BY placed_at ASC
		 LIMIT $2`,
		olderThan, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list abandoned orders: %w", err)
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan abandoned order id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ListShippedOrderIDsDeliveredBefore returns the IDs of orders still sitting in
// fulfillment_status='shipped' whose ship time is older than olderThan — the
// auto-deliver sweep's working set. Carrier delivery for these is never
// reported (legacy orders predating the current shipping integration have no
// shipments rows at all; live orders may simply have missed the tracking
// webhook), so after a grace window we assume the package arrived.
//
// The ship-time signal is COALESCE(MAX(shipments.shipped_at), updated_at):
// orders with shipment rows use the precise carrier ship time (so a package
// still inside its delivery window is never swept), while legacy orders with no
// shipments fall back to updated_at, which for an imported, long-completed order
// is comfortably in the past. cancelled/refunded orders are excluded — their
// fulfillment_status is left as-is for the record.
func (s *OrderStore) ListShippedOrderIDsDeliveredBefore(ctx context.Context, tx pgx.Tx, olderThan time.Time, limit int) (_ []uuid.UUID, err error) {
	defer trackQuery(s.metrics, "orders.list_shipped_delivered_before", time.Now(), &err)
	if limit <= 0 {
		limit = 100
	}
	rows, err := tx.Query(ctx,
		`SELECT o.id
		   FROM orders o
		  WHERE o.fulfillment_status = 'shipped'
		    AND o.status NOT IN ('cancelled', 'refunded')
		    AND COALESCE(
		          (SELECT MAX(s.shipped_at) FROM shipments s WHERE s.order_id = o.id),
		          o.updated_at
		        ) < $1
		  ORDER BY o.updated_at ASC
		  LIMIT $2`,
		olderThan, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list shipped orders to auto-deliver: %w", err)
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan auto-deliver order id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// SumOrderRevenue returns the total revenue (in cents) for orders matching the filter.
func (s *OrderStore) SumOrderRevenue(ctx context.Context, tx pgx.Tx, f OrderFilter) (_ int, err error) {
	defer trackQuery(s.metrics, "orders.sum_revenue", time.Now(), &err)
	query := `SELECT COALESCE(SUM(total), 0) FROM orders WHERE true`
	args := []any{}
	argN := 1

	// One customer's lifetime spend uses the same sum as the dashboards, so the
	// two can never disagree about what counts as revenue.
	if f.CustomerID != nil {
		query += fmt.Sprintf(" AND customer_id = $%d", argN)
		args = append(args, *f.CustomerID)
		argN++
	}
	if f.Status != nil {
		query += fmt.Sprintf(" AND status = $%d", argN)
		args = append(args, string(*f.Status))
		argN++
	}
	if f.PlacedFrom != nil {
		query += fmt.Sprintf(" AND placed_at >= $%d", argN)
		args = append(args, *f.PlacedFrom)
		argN++
	}
	if f.PlacedTo != nil {
		query += fmt.Sprintf(" AND placed_at <= $%d", argN)
		args = append(args, *f.PlacedTo)
		argN++
	}
	if f.Channel != nil {
		query += fmt.Sprintf(" AND channel = $%d", argN)
		args = append(args, string(*f.Channel))
		argN++ //nolint:ineffassign
	}
	if f.ExcludeUnconfirmed {
		query += " AND NOT (status = 'pending' AND payment_status = 'awaiting')"
	}
	if f.ExcludeCancelledRefunded {
		query += " AND status NOT IN ('cancelled', 'refunded')"
	}
	if f.OnlySubscription != nil {
		if *f.OnlySubscription {
			query += " AND subscription_id IS NOT NULL"
		} else {
			query += " AND subscription_id IS NULL"
		}
	}

	var total int32
	if err := tx.QueryRow(ctx, query, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("sum order revenue: %w", err)
	}
	return int(total), nil
}

// RevenueByDay returns daily revenue and order counts in the merchant's local
// timezone, between [from, to). Cancelled, refunded, and unconfirmed-payment
// (status=pending AND payment_status=awaiting) orders are excluded. Days with
// zero orders are NOT returned — callers fill gaps as needed.
func (s *OrderStore) RevenueByDay(ctx context.Context, tx pgx.Tx, from, to time.Time, tz *time.Location) (_ []domain.DailyRevenue, err error) {
	defer trackQuery(s.metrics, "orders.revenue_by_day", time.Now(), &err)
	tzName := "UTC"
	if tz != nil {
		tzName = tz.String()
	}
	query := `
		SELECT (placed_at AT TIME ZONE $3)::date AS day,
		       COALESCE(SUM(total), 0)::int       AS cents,
		       COUNT(*)::int                      AS orders
		FROM orders
		WHERE placed_at >= $1
		  AND placed_at < $2
		  AND status NOT IN ('cancelled', 'refunded')
		  AND NOT (status = 'pending' AND payment_status = 'awaiting')
		GROUP BY day
		ORDER BY day`
	rows, err := tx.Query(ctx, query, from, to, tzName)
	if err != nil {
		return nil, fmt.Errorf("revenue by day: %w", err)
	}
	defer rows.Close()
	var out []domain.DailyRevenue
	for rows.Next() {
		var d pgtype.Date
		var cents, orders int32
		if err := rows.Scan(&d, &cents, &orders); err != nil {
			return nil, fmt.Errorf("scan revenue by day: %w", err)
		}
		out = append(out, domain.DailyRevenue{
			Date:       d.Time,
			Cents:      int(cents),
			OrderCount: int(orders),
		})
	}
	return out, rows.Err()
}

// ActiveCustomerWindows carries the boundaries the active-customer counts are
// measured over. Each window is [Start, End) with a same-length prior window
// [PriorStart, Start) driving the period-over-period delta. The caller owns the
// definition of "a week" so the store stays free of calendar policy — the
// dashboard builds these off the merchant's local midnight.
type ActiveCustomerWindows struct {
	End               time.Time // exclusive upper bound, shared by all three windows
	WeekStart         time.Time
	WeekPriorStart    time.Time
	MonthStart        time.Time
	MonthPriorStart   time.Time
	QuarterStart      time.Time
	QuarterPriorStart time.Time
}

// ActiveCustomerCounts holds the number of distinct customers who placed an
// order in each window, plus the same count over the equivalent prior window.
type ActiveCustomerCounts struct {
	Week         int
	WeekPrior    int
	Month        int
	MonthPrior   int
	Quarter      int
	QuarterPrior int
}

// CountActiveCustomers counts distinct customers with at least one order in
// each of the three activity windows, in a single pass over the widest range.
// A non-nil channel scopes every count to that sales channel.
//
// Cancelled/refunded and unconfirmed (status=pending AND payment_status=awaiting)
// orders are excluded, mirroring RevenueByDay — a customer whose only order was
// an abandoned payment intent is not active. Orders with no customer_id (guest
// checkouts, customers since deleted) can't be counted as a distinct customer
// and are excluded by COUNT(DISTINCT) anyway; the explicit NULL filter keeps
// that intent visible.
func (s *OrderStore) CountActiveCustomers(ctx context.Context, tx pgx.Tx, w ActiveCustomerWindows, channel *domain.OrderChannel) (_ ActiveCustomerCounts, err error) {
	defer trackQuery(s.metrics, "orders.count_active_customers", time.Now(), &err)
	query := `
		SELECT
			COUNT(DISTINCT customer_id) FILTER (WHERE placed_at >= $2)                        AS week,
			COUNT(DISTINCT customer_id) FILTER (WHERE placed_at >= $3 AND placed_at < $2)     AS week_prior,
			COUNT(DISTINCT customer_id) FILTER (WHERE placed_at >= $4)                        AS month,
			COUNT(DISTINCT customer_id) FILTER (WHERE placed_at >= $5 AND placed_at < $4)     AS month_prior,
			COUNT(DISTINCT customer_id) FILTER (WHERE placed_at >= $6)                        AS quarter,
			COUNT(DISTINCT customer_id) FILTER (WHERE placed_at >= $7 AND placed_at < $6)     AS quarter_prior
		FROM orders
		WHERE placed_at < $1
		  AND placed_at >= $7
		  AND customer_id IS NOT NULL
		  AND status NOT IN ('cancelled', 'refunded')
		  AND NOT (status = 'pending' AND payment_status = 'awaiting')`
	args := []any{
		w.End, w.WeekStart, w.WeekPriorStart,
		w.MonthStart, w.MonthPriorStart,
		w.QuarterStart, w.QuarterPriorStart,
	}
	if channel != nil {
		query += " AND channel = $8"
		args = append(args, string(*channel))
	}

	var c ActiveCustomerCounts
	if err := tx.QueryRow(ctx, query, args...).Scan(
		&c.Week, &c.WeekPrior,
		&c.Month, &c.MonthPrior,
		&c.Quarter, &c.QuarterPrior,
	); err != nil {
		return ActiveCustomerCounts{}, fmt.Errorf("count active customers: %w", err)
	}
	return c, nil
}

// TopProductsSort selects the metric used to rank products in TopProducts.
type TopProductsSort string

const (
	TopProductsSortUnits  TopProductsSort = "units"
	TopProductsSortWeight TopProductsSort = "weight"
)

// TopProducts returns the top-N products over [from, to), ranked by the
// chosen metric. Cancelled and refunded orders are excluded. Each row carries
// units, total shipped weight (grams), and revenue so the caller can present
// any of them without re-querying. Ties are broken by revenue desc.
//
// When sorting by weight, products whose variants have no weight configured
// (weight_grams IS NULL) are excluded — they would otherwise crowd the chart
// with zeroes.
func (s *OrderStore) TopProducts(ctx context.Context, tx pgx.Tx, from, to time.Time, sort TopProductsSort, limit int) (_ []domain.ProductSales, err error) {
	defer trackQuery(s.metrics, "orders.top_products", time.Now(), &err)
	if limit <= 0 {
		limit = 5
	}
	orderBy := "units DESC, revenue DESC"
	weightFilter := ""
	if sort == TopProductsSortWeight {
		orderBy = "weight_grams DESC, revenue DESC"
		weightFilter = " AND v.weight_grams IS NOT NULL"
	}
	query := fmt.Sprintf(`
		SELECT p.id, p.title,
		       COALESCE(SUM(li.quantity), 0)::int                                AS units,
		       COALESCE(SUM(li.quantity * COALESCE(v.weight_grams, 0)), 0)::int  AS weight_grams,
		       COALESCE(SUM(li.total), 0)::int                                   AS revenue
		FROM line_items li
		JOIN orders   o ON o.id = li.order_id
		JOIN variants v ON v.id = li.variant_id
		JOIN products p ON p.id = v.product_id
		WHERE o.placed_at >= $1
		  AND o.placed_at < $2
		  AND o.status NOT IN ('cancelled', 'refunded')
		  AND NOT (o.status = 'pending' AND o.payment_status = 'awaiting')%s
		GROUP BY p.id, p.title
		ORDER BY %s
		LIMIT $3`, weightFilter, orderBy)
	rows, err := tx.Query(ctx, query, from, to, limit)
	if err != nil {
		return nil, fmt.Errorf("top products: %w", err)
	}
	defer rows.Close()
	var out []domain.ProductSales
	for rows.Next() {
		var ps domain.ProductSales
		var units, weight, revenue int32
		if err := rows.Scan(&ps.ProductID, &ps.Title, &units, &weight, &revenue); err != nil {
			return nil, fmt.Errorf("scan top products: %w", err)
		}
		ps.Units = int(units)
		ps.WeightGrams = int(weight)
		ps.Revenue = int(revenue)
		out = append(out, ps)
	}
	return out, rows.Err()
}

// ListDeliveryLoad returns per-product totals across the orders matching the
// filter — the "how many pounds of each coffee go on the van" rollup behind
// the fulfillment queue's load list.
//
// Only the filter fields that make sense for a load-out are honoured
// (Channel, ShippingMethod, FulfillmentStatuses, OrderIDs); the rest are
// ignored because a driver's manifest has no use for search or pagination.
// Cancelled, refunded, and unconfirmed orders are always excluded — the same
// exclusions the fulfillment queue itself applies, so the manifest and the
// rows it summarises never disagree.
//
// Rows are ordered heaviest-first so the coffees that dominate the load sort
// to the top of the sheet, with title as the tiebreak for a stable order.
func (s *OrderStore) ListDeliveryLoad(ctx context.Context, tx pgx.Tx, f OrderFilter) (_ []domain.DeliveryLoadLine, err error) {
	defer trackQuery(s.metrics, "orders.delivery_load", time.Now(), &err)
	query := `
		SELECT p.id, p.title,
		       COALESCE(SUM(li.quantity), 0)::int                               AS units,
		       COALESCE(SUM(li.quantity * COALESCE(v.weight_grams, 0)), 0)::int AS weight_grams,
		       COALESCE(SUM(li.quantity) FILTER (WHERE v.weight_grams IS NULL), 0)::int AS units_missing_weight
		FROM line_items li
		JOIN orders   o ON o.id = li.order_id
		JOIN variants v ON v.id = li.variant_id
		JOIN products p ON p.id = v.product_id
		WHERE o.status NOT IN ('cancelled', 'refunded')
		  AND NOT (o.status = 'pending' AND o.payment_status = 'awaiting')`
	args := []any{}
	argN := 1

	if f.Channel != nil {
		query += fmt.Sprintf(" AND o.channel = $%d", argN)
		args = append(args, string(*f.Channel))
		argN++
	}
	if f.ShippingMethod != nil {
		query += fmt.Sprintf(" AND o.shipping_method = $%d", argN)
		args = append(args, string(*f.ShippingMethod))
		argN++
	}
	if len(f.FulfillmentStatuses) > 0 {
		query += " AND o.fulfillment_status IN ("
		for i, st := range f.FulfillmentStatuses {
			if i > 0 {
				query += ", "
			}
			query += fmt.Sprintf("$%d", argN)
			args = append(args, string(st))
			argN++
		}
		query += ")"
	}
	if len(f.OrderIDs) > 0 {
		query += " AND o.id IN ("
		for i, id := range f.OrderIDs {
			if i > 0 {
				query += ", "
			}
			query += fmt.Sprintf("$%d", argN)
			args = append(args, id)
			argN++
		}
		query += ")"
	}
	query += `
		GROUP BY p.id, p.title
		ORDER BY weight_grams DESC, units DESC, p.title ASC`

	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list delivery load: %w", err)
	}
	defer rows.Close()

	var out []domain.DeliveryLoadLine
	for rows.Next() {
		var l domain.DeliveryLoadLine
		var units, weight, missing int32
		if err := rows.Scan(&l.ProductID, &l.Title, &units, &weight, &missing); err != nil {
			return nil, fmt.Errorf("scan delivery load: %w", err)
		}
		l.Units = int(units)
		l.WeightGrams = int(weight)
		l.UnitsMissingWeight = int(missing)
		out = append(out, l)
	}
	return out, rows.Err()
}

// --- QuickBooks sync methods ---

// SetQBInvoice stores the QB invoice ID and number on an order.
func (s *OrderStore) SetQBInvoice(ctx context.Context, tx pgx.Tx, id uuid.UUID, qbInvoiceID, qbInvoiceNo string) (err error) {
	defer trackQuery(s.metrics, "orders.set_qb_invoice", time.Now(), &err)
	_, err = tx.Exec(ctx,
		`UPDATE orders SET qb_invoice_id = $2, qb_invoice_no = $3, qb_synced_at = now(), updated_at = now() WHERE id = $1`,
		id, qbInvoiceID, qbInvoiceNo,
	)
	if err != nil {
		return fmt.Errorf("set qb invoice: %w", err)
	}
	return nil
}

// qbOrderColumns is the full column list scanned by scanQBOrder. It includes
// overdue_reminder_stage, which the QB reconciliation path reads but the
// sqlc-generated order reads do not. Kept in sync with scanQBOrder's Scan order.
const qbOrderColumns = `id, number, customer_id, status, payment_status, fulfillment_status,
	        currency_code, subtotal, discount_total, shipping_total, tax_total, total,
	        shipping_address_id, billing_address_id, subscription_id, draft_by_user_id,
	        tax_exempt, tax_exempt_reason, stripe_tax_id, stripe_payment_intent_id,
	        shipping_method, requested_delivery_date,
	        qb_invoice_id, qb_invoice_no, qb_synced_at,
	        customer_po_number, internal_note,
	        notes, metadata, overdue_reminder_stage, placed_at, created_at, updated_at`

// scanQBOrder scans a row selected with qbOrderColumns into a domain.Order. It
// accepts pgx.Row, which both single-row QueryRow results and pgx.Rows satisfy
// (both expose Scan(dest ...any) error).
func scanQBOrder(row pgx.Row) (*domain.Order, error) {
	var o domain.Order
	var status, paymentStatus, fulfillmentStatus string
	var shippingMethod *string
	var requestedDeliveryDate pgtype.Timestamptz
	var subtotal, discountTotal, shippingTotal, taxTotal, total int32
	var overdueReminderStage int16
	var metadata json.RawMessage
	if err := row.Scan(
		&o.ID, &o.Number, &o.CustomerID, &status, &paymentStatus, &fulfillmentStatus,
		&o.CurrencyCode, &subtotal, &discountTotal, &shippingTotal, &taxTotal, &total,
		&o.ShippingAddressID, &o.BillingAddressID, &o.SubscriptionID, &o.DraftByUserID,
		&o.TaxExempt, &o.TaxExemptReason, &o.StripeTaxID, &o.StripePaymentIntentID,
		&shippingMethod, &requestedDeliveryDate,
		&o.QBInvoiceID, &o.QBInvoiceNo, &o.QBSyncedAt,
		&o.CustomerPONumber, &o.InternalNote,
		&o.Notes, &metadata, &overdueReminderStage, &o.PlacedAt, &o.CreatedAt, &o.UpdatedAt,
	); err != nil {
		return nil, err
	}
	o.Status = domain.OrderStatus(status)
	o.PaymentStatus = domain.PaymentStatus(paymentStatus)
	o.FulfillmentStatus = domain.FulfillmentStatus(fulfillmentStatus)
	o.Subtotal = int(subtotal)
	o.DiscountTotal = int(discountTotal)
	o.ShippingTotal = int(shippingTotal)
	o.TaxTotal = int(taxTotal)
	o.Total = int(total)
	o.OverdueReminderStage = int(overdueReminderStage)
	if shippingMethod != nil {
		sm := domain.ShippingMethod(*shippingMethod)
		o.ShippingMethod = &sm
	}
	o.RequestedDeliveryDate = timestampFromPG(requestedDeliveryDate)
	o.Metadata = metadataFromJSON(metadata)
	return &o, nil
}

// GetOrderByQBInvoiceIDAsStaff returns an order by its QB invoice ID.
func (s *OrderStore) GetOrderByQBInvoiceIDAsStaff(ctx context.Context, tx pgx.Tx, qbInvoiceID string) (_ *domain.Order, err error) {
	defer trackQuery(s.metrics, "orders.get_by_qb_invoice_id", time.Now(), &err)
	row := tx.QueryRow(ctx,
		`SELECT `+qbOrderColumns+` FROM orders WHERE qb_invoice_id = $1`, qbInvoiceID)
	o, err := scanQBOrder(row)
	if err != nil {
		return nil, fmt.Errorf("get order by qb invoice id: %w", err)
	}
	return o, nil
}

// GetOrderByQBInvoiceIDForUpdate returns an order by its QB invoice ID and takes
// a row-level lock. Concurrent reconciles on the same order serialize: the
// second tx waits for the first to commit and then sees the post-transition
// state, so the conditional payment-status transitions (and the emails gated on
// them) don't double-fire when the webhook and the poll race.
func (s *OrderStore) GetOrderByQBInvoiceIDForUpdate(ctx context.Context, tx pgx.Tx, qbInvoiceID string) (_ *domain.Order, err error) {
	defer trackQuery(s.metrics, "orders.get_by_qb_invoice_id_for_update", time.Now(), &err)
	row := tx.QueryRow(ctx,
		`SELECT `+qbOrderColumns+` FROM orders WHERE qb_invoice_id = $1 FOR UPDATE`, qbInvoiceID)
	o, err := scanQBOrder(row)
	if err != nil {
		return nil, fmt.Errorf("get order by qb invoice id (for update): %w", err)
	}
	return o, nil
}

// ListWholesaleOpenInvoiceOrders returns QB-owned orders whose payment status is
// not yet terminal — the candidate set for the reconciliation poll. Ordered by
// placed_at so the oldest invoices reconcile first; bounded by limit.
func (s *OrderStore) ListWholesaleOpenInvoiceOrders(ctx context.Context, tx pgx.Tx, limit int) (_ []domain.Order, err error) {
	defer trackQuery(s.metrics, "orders.list_wholesale_open_invoice", time.Now(), &err)
	rows, err := tx.Query(ctx,
		`SELECT `+qbOrderColumns+`
		 FROM orders
		 WHERE qb_invoice_id IS NOT NULL
		   AND payment_status IN ('pending_invoice', 'invoiced', 'partially_paid', 'overdue')
		 ORDER BY placed_at
		 LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list wholesale open invoice orders: %w", err)
	}
	defer rows.Close()

	var orders []domain.Order
	for rows.Next() {
		o, scanErr := scanQBOrder(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan wholesale open invoice order: %w", scanErr)
		}
		orders = append(orders, *o)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate wholesale open invoice orders: %w", err)
	}
	return orders, nil
}

// PastDueAccountRow aggregates a customer's overdue wholesale invoices for the
// admin dashboard's past-due accounts panel.
type PastDueAccountRow struct {
	CustomerID     uuid.UUID
	CompanyName    *string
	FirstName      string
	LastName       string
	Email          string
	OverdueOrders  int
	OverdueTotal   int // sum of the overdue orders' invoiced totals, in cents — not net of partial payments (QB holds authoritative balances)
	OldestPlacedAt time.Time
}

// CountPastDueAccounts returns the total number of customers with at least one
// overdue order, so the dashboard count stays truthful when the display list
// is capped.
func (s *OrderStore) CountPastDueAccounts(ctx context.Context, tx pgx.Tx) (_ int, err error) {
	defer trackQuery(s.metrics, "orders.count_past_due_accounts", time.Now(), &err)
	var count int
	err = tx.QueryRow(ctx,
		`SELECT COUNT(DISTINCT customer_id)
		 FROM orders
		 WHERE payment_status = 'overdue'
		   AND status NOT IN ('cancelled', 'refunded')
		   AND customer_id IS NOT NULL`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count past due accounts: %w", err)
	}
	return count, nil
}

// ListPastDueAccounts returns customers with at least one overdue order —
// "account not current" — oldest debt first, with per-account overdue count
// and total. Cancelled/refunded orders never count. Bounded by limit.
func (s *OrderStore) ListPastDueAccounts(ctx context.Context, tx pgx.Tx, limit int) (_ []PastDueAccountRow, err error) {
	defer trackQuery(s.metrics, "orders.list_past_due_accounts", time.Now(), &err)
	rows, err := tx.Query(ctx,
		`SELECT c.id, c.company_name, c.first_name, c.last_name, c.email,
		        COUNT(o.id)::int, COALESCE(SUM(o.total), 0)::int, MIN(o.placed_at)
		 FROM orders o
		 JOIN customers c ON c.id = o.customer_id
		 WHERE o.payment_status = 'overdue'
		   AND o.status NOT IN ('cancelled', 'refunded')
		 GROUP BY c.id, c.company_name, c.first_name, c.last_name, c.email
		 ORDER BY MIN(o.placed_at)
		 LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list past due accounts: %w", err)
	}
	defer rows.Close()

	var accounts []PastDueAccountRow
	for rows.Next() {
		var a PastDueAccountRow
		if scanErr := rows.Scan(&a.CustomerID, &a.CompanyName, &a.FirstName, &a.LastName, &a.Email,
			&a.OverdueOrders, &a.OverdueTotal, &a.OldestPlacedAt); scanErr != nil {
			return nil, fmt.Errorf("scan past due account: %w", scanErr)
		}
		accounts = append(accounts, a)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate past due accounts: %w", err)
	}
	return accounts, nil
}

// ListPastDueCustomerIDs filters the given customers down to those with at
// least one overdue order — the batch flag the fulfillment queue uses to mark
// rows whose account is not current.
func (s *OrderStore) ListPastDueCustomerIDs(ctx context.Context, tx pgx.Tx, customerIDs []uuid.UUID) (_ map[uuid.UUID]bool, err error) {
	defer trackQuery(s.metrics, "orders.list_past_due_customer_ids", time.Now(), &err)
	pastDue := make(map[uuid.UUID]bool)
	if len(customerIDs) == 0 {
		return pastDue, nil
	}
	rows, err := tx.Query(ctx,
		`SELECT DISTINCT customer_id
		 FROM orders
		 WHERE customer_id = ANY($1)
		   AND payment_status = 'overdue'
		   AND status NOT IN ('cancelled', 'refunded')`, customerIDs)
	if err != nil {
		return nil, fmt.Errorf("list past due customer ids: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id uuid.UUID
		if scanErr := rows.Scan(&id); scanErr != nil {
			return nil, fmt.Errorf("scan past due customer id: %w", scanErr)
		}
		pastDue[id] = true
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate past due customer ids: %w", err)
	}
	return pastDue, nil
}

// SetOverdueReminderStage records the highest past-due reminder milestone (days
// since placed) already notified for an order, so the poll never re-sends one.
func (s *OrderStore) SetOverdueReminderStage(ctx context.Context, tx pgx.Tx, id uuid.UUID, stage int) (err error) {
	defer trackQuery(s.metrics, "orders.set_overdue_reminder_stage", time.Now(), &err)
	_, err = tx.Exec(ctx,
		`UPDATE orders SET overdue_reminder_stage = $2, updated_at = now() WHERE id = $1`,
		id, int16(stage),
	)
	if err != nil {
		return fmt.Errorf("set overdue reminder stage: %w", err)
	}
	return nil
}

// --- Carts ---

// CreateCartParams holds the fields needed to create a cart.
type CreateCartParams struct {
	CustomerID        *uuid.UUID
	CurrencyCode      string
	ShippingAddressID uuid.UUID
	BillingAddressID  uuid.UUID
	Metadata          map[string]any
	ExpiresAt         *time.Time
}

// CreateCart inserts a new cart and returns it.
func (s *OrderStore) CreateCart(ctx context.Context, tx pgx.Tx, p CreateCartParams) (_ *domain.Cart, err error) {
	defer trackQuery(s.metrics, "carts.create", time.Now(), &err)
	row, err := sqlcgen.New(tx).CreateCart(ctx, sqlcgen.CreateCartParams{
		ID:                uuid.New(),
		CustomerID:        p.CustomerID,
		CurrencyCode:      p.CurrencyCode,
		ShippingAddressID: &p.ShippingAddressID,
		BillingAddressID:  &p.BillingAddressID,
		Metadata:          metadataToJSON(p.Metadata),
		ExpiresAt:         timestampToPG(p.ExpiresAt),
	})
	if err != nil {
		return nil, fmt.Errorf("insert cart: %w", err)
	}
	return cartFromRow(row), nil
}

// GetCartByIDAsStaff returns a cart by ID.
func (s *OrderStore) GetCartByIDAsStaff(ctx context.Context, tx pgx.Tx, id uuid.UUID) (_ *domain.Cart, err error) {
	defer trackQuery(s.metrics, "carts.get_by_id", time.Now(), &err)
	row, err := sqlcgen.New(tx).GetCartByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get cart %s: %w", id, err)
	}
	return cartFromRow(row), nil
}

// GetCartByCustomerID returns a cart by customer ID.
func (s *OrderStore) GetCartByCustomerID(ctx context.Context, tx pgx.Tx, customerID uuid.UUID) (_ *domain.Cart, err error) {
	defer trackQuery(s.metrics, "carts.get_by_customer", time.Now(), &err)
	row, err := sqlcgen.New(tx).GetCartByCustomerID(ctx, &customerID)
	if err != nil {
		return nil, fmt.Errorf("get cart by customer: %w", err)
	}
	return cartFromRow(row), nil
}

// UpdateCartAddresses updates a cart's shipping and billing addresses.
func (s *OrderStore) UpdateCartAddresses(ctx context.Context, tx pgx.Tx, id, shippingAddressID, billingAddressID uuid.UUID) (_ *domain.Cart, err error) {
	defer trackQuery(s.metrics, "carts.update_addresses", time.Now(), &err)
	row, err := sqlcgen.New(tx).UpdateCartAddresses(ctx, sqlcgen.UpdateCartAddressesParams{
		ID:                id,
		ShippingAddressID: &shippingAddressID,
		BillingAddressID:  &billingAddressID,
	})
	if err != nil {
		return nil, fmt.Errorf("update cart addresses: %w", err)
	}
	return cartFromRow(row), nil
}

// UpdateCartDiscount updates a cart's applied discount and coupon code.
func (s *OrderStore) UpdateCartDiscount(ctx context.Context, tx pgx.Tx, id uuid.UUID, discountID, couponCodeID *uuid.UUID) (_ *domain.Cart, err error) {
	defer trackQuery(s.metrics, "carts.update_discount", time.Now(), &err)
	row, err := sqlcgen.New(tx).UpdateCartDiscount(ctx, sqlcgen.UpdateCartDiscountParams{
		ID:                  id,
		AppliedDiscountID:   discountID,
		AppliedCouponCodeID: couponCodeID,
	})
	if err != nil {
		return nil, fmt.Errorf("update cart discount: %w", err)
	}
	return cartFromRow(row), nil
}

// DeleteCart removes a cart by ID.
func (s *OrderStore) DeleteCart(ctx context.Context, tx pgx.Tx, id uuid.UUID) (err error) {
	defer trackQuery(s.metrics, "carts.delete", time.Now(), &err)
	if err := sqlcgen.New(tx).DeleteCart(ctx, id); err != nil {
		return fmt.Errorf("delete cart: %w", err)
	}
	return nil
}

// --- Line Items ---

// CreateLineItemParams holds the fields needed to create a line item.
type CreateLineItemParams struct {
	OrderID       uuid.UUID
	VariantID     uuid.UUID
	Quantity      int
	UnitPrice     int
	Subtotal      int
	DiscountTotal int
	TaxTotal      int
	Total         int
	Metadata      map[string]any
}

// CreateLineItem inserts a new line item and returns it.
func (s *OrderStore) CreateLineItem(ctx context.Context, tx pgx.Tx, p CreateLineItemParams) (_ *domain.LineItem, err error) {
	defer trackQuery(s.metrics, "line_items.create", time.Now(), &err)
	row, err := sqlcgen.New(tx).CreateLineItem(ctx, sqlcgen.CreateLineItemParams{
		ID:            uuid.New(),
		OrderID:       p.OrderID,
		VariantID:     p.VariantID,
		Quantity:      int32(p.Quantity),
		UnitPrice:     int32(p.UnitPrice),
		Subtotal:      int32(p.Subtotal),
		DiscountTotal: int32(p.DiscountTotal),
		TaxTotal:      int32(p.TaxTotal),
		Total:         int32(p.Total),
		Metadata:      metadataToJSON(p.Metadata),
	})
	if err != nil {
		return nil, fmt.Errorf("insert line item: %w", err)
	}
	return lineItemFromRow(row), nil
}

// GetLineItem returns a single line item by ID.
func (s *OrderStore) GetLineItem(ctx context.Context, tx pgx.Tx, id uuid.UUID) (_ *domain.LineItem, err error) {
	defer trackQuery(s.metrics, "line_items.get", time.Now(), &err)
	row, err := sqlcgen.New(tx).GetLineItem(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get line item: %w", err)
	}
	return lineItemFromRow(row), nil
}

// UpdateLineItemVariant changes the variant_id on a line item and returns
// the updated row. Other line item fields (price, quantity, totals) are
// untouched.
func (s *OrderStore) UpdateLineItemVariant(ctx context.Context, tx pgx.Tx, id, variantID uuid.UUID) (_ *domain.LineItem, err error) {
	defer trackQuery(s.metrics, "line_items.update_variant", time.Now(), &err)
	row, err := sqlcgen.New(tx).UpdateLineItemVariant(ctx, sqlcgen.UpdateLineItemVariantParams{
		ID:        id,
		VariantID: variantID,
	})
	if err != nil {
		return nil, fmt.Errorf("update line item variant: %w", err)
	}
	return lineItemFromRow(row), nil
}

// ListLineItems returns all line items for an order.
func (s *OrderStore) ListLineItems(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) (_ []domain.LineItem, err error) {
	defer trackQuery(s.metrics, "line_items.list_by_order", time.Now(), &err)
	rows, err := sqlcgen.New(tx).ListLineItemsByOrder(ctx, orderID)
	if err != nil {
		return nil, fmt.Errorf("list line items: %w", err)
	}
	items := make([]domain.LineItem, len(rows))
	for i, r := range rows {
		items[i] = *lineItemFromRow(r)
	}
	return items, nil
}

// DeleteLineItem removes a line item by ID.
func (s *OrderStore) DeleteLineItem(ctx context.Context, tx pgx.Tx, id uuid.UUID) (err error) {
	defer trackQuery(s.metrics, "line_items.delete", time.Now(), &err)
	if err := sqlcgen.New(tx).DeleteLineItem(ctx, id); err != nil {
		return fmt.Errorf("delete line item: %w", err)
	}
	return nil
}

// DeleteLineItemsByOrder removes all line items for an order.
func (s *OrderStore) DeleteLineItemsByOrder(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) (err error) {
	defer trackQuery(s.metrics, "line_items.delete_by_order", time.Now(), &err)
	if err := sqlcgen.New(tx).DeleteLineItemsByOrder(ctx, orderID); err != nil {
		return fmt.Errorf("delete line items by order: %w", err)
	}
	return nil
}

// --- Adjustments ---

// CreateAdjustmentParams holds the fields needed to create an adjustment.
type CreateAdjustmentParams struct {
	OrderID    uuid.UUID
	LineItemID *uuid.UUID
	Label      string
	Amount     int
	SourceType string
	SourceID   uuid.UUID
}

// CreateAdjustment inserts a new adjustment and returns it.
func (s *OrderStore) CreateAdjustment(ctx context.Context, tx pgx.Tx, p CreateAdjustmentParams) (_ *domain.Adjustment, err error) {
	defer trackQuery(s.metrics, "adjustments.create", time.Now(), &err)
	row, err := sqlcgen.New(tx).CreateAdjustment(ctx, sqlcgen.CreateAdjustmentParams{
		ID:         uuid.New(),
		OrderID:    p.OrderID,
		LineItemID: p.LineItemID,
		Label:      p.Label,
		Amount:     int32(p.Amount),
		SourceType: p.SourceType,
		SourceID:   p.SourceID,
	})
	if err != nil {
		return nil, fmt.Errorf("insert adjustment: %w", err)
	}
	return adjustmentFromRow(row), nil
}

// ListAdjustmentsByOrder returns all adjustments for an order.
func (s *OrderStore) ListAdjustmentsByOrder(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) (_ []domain.Adjustment, err error) {
	defer trackQuery(s.metrics, "adjustments.list_by_order", time.Now(), &err)
	rows, err := sqlcgen.New(tx).ListAdjustmentsByOrder(ctx, orderID)
	if err != nil {
		return nil, fmt.Errorf("list adjustments: %w", err)
	}
	adjs := make([]domain.Adjustment, len(rows))
	for i, r := range rows {
		adjs[i] = *adjustmentFromRow(r)
	}
	return adjs, nil
}

// ListAdjustmentsByLineItem returns all adjustments for a line item.
func (s *OrderStore) ListAdjustmentsByLineItem(ctx context.Context, tx pgx.Tx, lineItemID uuid.UUID) (_ []domain.Adjustment, err error) {
	defer trackQuery(s.metrics, "adjustments.list_by_line_item", time.Now(), &err)
	rows, err := sqlcgen.New(tx).ListAdjustmentsByLineItem(ctx, &lineItemID)
	if err != nil {
		return nil, fmt.Errorf("list adjustments by line item: %w", err)
	}
	adjs := make([]domain.Adjustment, len(rows))
	for i, r := range rows {
		adjs[i] = *adjustmentFromRow(r)
	}
	return adjs, nil
}

// DeleteAdjustment removes an adjustment by ID.
func (s *OrderStore) DeleteAdjustment(ctx context.Context, tx pgx.Tx, id uuid.UUID) (err error) {
	defer trackQuery(s.metrics, "adjustments.delete", time.Now(), &err)
	if err := sqlcgen.New(tx).DeleteAdjustment(ctx, id); err != nil {
		return fmt.Errorf("delete adjustment: %w", err)
	}
	return nil
}

// --- Row converters ---

func orderFromRow(r sqlcgen.Order) *domain.Order {
	o := &domain.Order{
		ID:                r.ID,
		Number:            r.Number,
		CustomerID:        r.CustomerID,
		Channel:           domain.OrderChannel(r.Channel),
		Status:            domain.OrderStatus(r.Status),
		PaymentStatus:     domain.PaymentStatus(r.PaymentStatus),
		FulfillmentStatus: domain.FulfillmentStatus(r.FulfillmentStatus),
		CurrencyCode:      r.CurrencyCode,
		Subtotal:          int(r.Subtotal),
		DiscountTotal:     int(r.DiscountTotal),
		ShippingTotal:     int(r.ShippingTotal),
		TaxTotal:          int(r.TaxTotal),
		Total:             int(r.Total),
		ShippingAddressID: r.ShippingAddressID,
		BillingAddressID:  r.BillingAddressID,
		SubscriptionID:    r.SubscriptionID,
		DraftByUserID:     r.DraftByUserID,
		TaxExempt:         r.TaxExempt,
		TaxExemptReason:   r.TaxExemptReason,
		StripeTaxID:              r.StripeTaxID,
		StripePaymentIntentID:    r.StripePaymentIntentID,
		QBInvoiceID:              r.QbInvoiceID,
		QBInvoiceNo:              r.QbInvoiceNo,
		QBSyncedAt:               timestampFromPG(r.QbSyncedAt),
		RequestedDeliveryDate:    timestampFromPG(r.RequestedDeliveryDate),
		ScheduledDeliveryDate:    dateFromPG(r.ScheduledDeliveryDate),
		CustomerPONumber:         r.CustomerPoNumber,
		InternalNote:             r.InternalNote,
		Notes:                    r.Notes,
		Metadata:                 metadataFromJSON(r.Metadata),
		PlacedAt:          r.PlacedAt,
		CreatedAt:         r.CreatedAt,
		UpdatedAt:         r.UpdatedAt,
	}
	if r.ShippingMethod != nil {
		sm := domain.ShippingMethod(*r.ShippingMethod)
		o.ShippingMethod = &sm
	}
	return o
}

func cartFromRow(r sqlcgen.Cart) *domain.Cart {
	var shippingAddrID, billingAddrID uuid.UUID
	if r.ShippingAddressID != nil {
		shippingAddrID = *r.ShippingAddressID
	}
	if r.BillingAddressID != nil {
		billingAddrID = *r.BillingAddressID
	}
	return &domain.Cart{
		ID:                  r.ID,
		CustomerID:          r.CustomerID,
		CurrencyCode:        r.CurrencyCode,
		ShippingAddressID:   shippingAddrID,
		BillingAddressID:    billingAddrID,
		AppliedDiscountID:   r.AppliedDiscountID,
		AppliedCouponCodeID: r.AppliedCouponCodeID,
		Metadata:            metadataFromJSON(r.Metadata),
		ExpiresAt:           timestampFromPG(r.ExpiresAt),
		CreatedAt:           r.CreatedAt,
	}
}

func lineItemFromRow(r sqlcgen.LineItem) *domain.LineItem {
	return &domain.LineItem{
		ID:            r.ID,
		OrderID:       r.OrderID,
		VariantID:     r.VariantID,
		Quantity:      int(r.Quantity),
		UnitPrice:     int(r.UnitPrice),
		Subtotal:      int(r.Subtotal),
		DiscountTotal: int(r.DiscountTotal),
		TaxTotal:      int(r.TaxTotal),
		Total:         int(r.Total),
		Metadata:      metadataFromJSON(r.Metadata),
	}
}

func adjustmentFromRow(r sqlcgen.Adjustment) *domain.Adjustment {
	return &domain.Adjustment{
		ID:         r.ID,
		OrderID:    r.OrderID,
		LineItemID: r.LineItemID,
		Label:      r.Label,
		Amount:     int(r.Amount),
		SourceType: r.SourceType,
		SourceID:   r.SourceID,
	}
}
