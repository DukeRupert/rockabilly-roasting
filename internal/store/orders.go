package store

import (
	"context"
	"encoding/json"
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
	row, err := sqlcgen.New(tx).CreateOrder(ctx, sqlcgen.CreateOrderParams{
		ID:                    uuid.New(),
		Number:                p.Number,
		CustomerID:            p.CustomerID,
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
	FulfillmentStatus   *domain.FulfillmentStatus
	FulfillmentStatuses []domain.FulfillmentStatus // IN filter (takes precedence over singular)
	CustomerID          *uuid.UUID
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
	Limit              int
	Offset             int
}

// ListOrders returns orders matching the given filter (hand-written for dynamic WHERE).
func (s *OrderStore) ListOrders(ctx context.Context, tx pgx.Tx, f OrderFilter) (_ []domain.Order, err error) {
	defer trackQuery(s.metrics, "orders.list", time.Now(), &err)
	query := `SELECT id, number, customer_id, status, payment_status, fulfillment_status,
	                 currency_code, subtotal, discount_total, shipping_total, tax_total, total,
	                 shipping_address_id, billing_address_id, subscription_id, draft_by_user_id,
	                 tax_exempt, tax_exempt_reason, stripe_tax_id, stripe_payment_intent_id,
	                 shipping_method, requested_delivery_date,
	                 customer_po_number, internal_note,
	                 notes, metadata, placed_at, created_at, updated_at
	          FROM orders WHERE true`
	args := []any{}
	argN := 1

	if f.Status != nil {
		query += fmt.Sprintf(" AND status = $%d", argN)
		args = append(args, string(*f.Status))
		argN++
	}
	if len(f.FulfillmentStatuses) > 0 {
		query += " AND fulfillment_status IN ("
		for i, s := range f.FulfillmentStatuses {
			if i > 0 {
				query += ", "
			}
			query += fmt.Sprintf("$%d", argN)
			args = append(args, string(s))
			argN++
		}
		query += ")"
	} else if f.FulfillmentStatus != nil {
		query += fmt.Sprintf(" AND fulfillment_status = $%d", argN)
		args = append(args, string(*f.FulfillmentStatus))
		argN++
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
	if f.Search != "" {
		query += fmt.Sprintf(" AND (number ILIKE $%d OR EXISTS (SELECT 1 FROM customers c WHERE c.id = orders.customer_id AND (c.first_name || ' ' || c.last_name ILIKE $%d OR c.email ILIKE $%d)))", argN, argN, argN)
		args = append(args, "%"+f.Search+"%")
		argN++
	}
	if f.ExcludeUnconfirmed {
		query += " AND NOT (status = 'pending' AND payment_status = 'awaiting')"
	}

	query += " ORDER BY placed_at DESC"

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
		var status, paymentStatus, fulfillmentStatus string
		var shippingMethod *string
		var requestedDeliveryDate pgtype.Timestamptz
		var subtotal, discountTotal, shippingTotal, taxTotal, total int32
		var metadata json.RawMessage
		if err := rows.Scan(
			&o.ID, &o.Number, &o.CustomerID, &status, &paymentStatus, &fulfillmentStatus,
			&o.CurrencyCode, &subtotal, &discountTotal, &shippingTotal, &taxTotal, &total,
			&o.ShippingAddressID, &o.BillingAddressID, &o.SubscriptionID, &o.DraftByUserID,
			&o.TaxExempt, &o.TaxExemptReason, &o.StripeTaxID, &o.StripePaymentIntentID,
			&shippingMethod, &requestedDeliveryDate,
			&o.CustomerPONumber, &o.InternalNote,
			&o.Notes, &metadata, &o.PlacedAt, &o.CreatedAt, &o.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan order: %w", err)
		}
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
	query := `SELECT COUNT(*) FROM orders WHERE true`
	args := []any{}
	argN := 1

	if f.Status != nil {
		query += fmt.Sprintf(" AND status = $%d", argN)
		args = append(args, string(*f.Status))
		argN++
	}
	if len(f.FulfillmentStatuses) > 0 {
		query += " AND fulfillment_status IN ("
		for i, s := range f.FulfillmentStatuses {
			if i > 0 {
				query += ", "
			}
			query += fmt.Sprintf("$%d", argN)
			args = append(args, string(s))
			argN++
		}
		query += ")"
	} else if f.FulfillmentStatus != nil {
		query += fmt.Sprintf(" AND fulfillment_status = $%d", argN)
		args = append(args, string(*f.FulfillmentStatus))
		argN++
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
	if f.Search != "" {
		query += fmt.Sprintf(" AND (number ILIKE $%d OR EXISTS (SELECT 1 FROM customers c WHERE c.id = orders.customer_id AND (c.first_name || ' ' || c.last_name ILIKE $%d OR c.email ILIKE $%d)))", argN, argN, argN)
		args = append(args, "%"+f.Search+"%")
		argN++ //nolint:ineffassign
	}
	if f.ExcludeUnconfirmed {
		query += " AND NOT (status = 'pending' AND payment_status = 'awaiting')"
	}

	var count int
	if err := tx.QueryRow(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count orders: %w", err)
	}
	return count, nil
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
	rows, err := tx.Query(ctx,
		`SELECT id FROM orders
		 WHERE status = 'pending'
		   AND payment_status = 'awaiting'
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

// SumOrderRevenue returns the total revenue (in cents) for orders matching the filter.
func (s *OrderStore) SumOrderRevenue(ctx context.Context, tx pgx.Tx, f OrderFilter) (_ int, err error) {
	defer trackQuery(s.metrics, "orders.sum_revenue", time.Now(), &err)
	query := `SELECT COALESCE(SUM(total), 0) FROM orders WHERE true`
	args := []any{}
	argN := 1

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
		argN++ //nolint:ineffassign
	}
	if f.ExcludeUnconfirmed {
		query += " AND NOT (status = 'pending' AND payment_status = 'awaiting')"
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

// GetOrderByQBInvoiceIDAsStaff returns an order by its QB invoice ID.
func (s *OrderStore) GetOrderByQBInvoiceIDAsStaff(ctx context.Context, tx pgx.Tx, qbInvoiceID string) (_ *domain.Order, err error) {
	defer trackQuery(s.metrics, "orders.get_by_qb_invoice_id", time.Now(), &err)
	var o domain.Order
	var status, paymentStatus, fulfillmentStatus string
	var shippingMethod *string
	var requestedDeliveryDate pgtype.Timestamptz
	var subtotal, discountTotal, shippingTotal, taxTotal, total int32
	var metadata json.RawMessage
	err = tx.QueryRow(ctx,
		`SELECT id, number, customer_id, status, payment_status, fulfillment_status,
		        currency_code, subtotal, discount_total, shipping_total, tax_total, total,
		        shipping_address_id, billing_address_id, subscription_id, draft_by_user_id,
		        tax_exempt, tax_exempt_reason, stripe_tax_id, stripe_payment_intent_id,
		        shipping_method, requested_delivery_date,
		        qb_invoice_id, qb_invoice_no, qb_synced_at,
		        customer_po_number, internal_note,
		        notes, metadata, placed_at, created_at, updated_at
		 FROM orders WHERE qb_invoice_id = $1`, qbInvoiceID,
	).Scan(
		&o.ID, &o.Number, &o.CustomerID, &status, &paymentStatus, &fulfillmentStatus,
		&o.CurrencyCode, &subtotal, &discountTotal, &shippingTotal, &taxTotal, &total,
		&o.ShippingAddressID, &o.BillingAddressID, &o.SubscriptionID, &o.DraftByUserID,
		&o.TaxExempt, &o.TaxExemptReason, &o.StripeTaxID, &o.StripePaymentIntentID,
		&shippingMethod, &requestedDeliveryDate,
		&o.QBInvoiceID, &o.QBInvoiceNo, &o.QBSyncedAt,
		&o.CustomerPONumber, &o.InternalNote,
		&o.Notes, &metadata, &o.PlacedAt, &o.CreatedAt, &o.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("get order by qb invoice id: %w", err)
	}
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
	return &o, nil
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
		RequestedDeliveryDate:    timestampFromPG(r.RequestedDeliveryDate),
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
