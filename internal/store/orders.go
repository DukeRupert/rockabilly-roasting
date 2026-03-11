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
type OrderStore struct{}

// NewOrderStore creates a new OrderStore.
func NewOrderStore() *OrderStore {
	return &OrderStore{}
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
func (s *OrderStore) CreateOrder(ctx context.Context, tx pgx.Tx, p CreateOrderParams) (*domain.Order, error) {
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

// GetOrderByID returns an order by ID.
func (s *OrderStore) GetOrderByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Order, error) {
	row, err := sqlcgen.New(tx).GetOrderByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get order %s: %w", id, err)
	}
	return orderFromRow(row), nil
}

// GetOrderByNumber returns an order by its number.
func (s *OrderStore) GetOrderByNumber(ctx context.Context, tx pgx.Tx, number string) (*domain.Order, error) {
	row, err := sqlcgen.New(tx).GetOrderByNumber(ctx, number)
	if err != nil {
		return nil, fmt.Errorf("get order by number: %w", err)
	}
	return orderFromRow(row), nil
}

// UpdateOrderStatus updates an order's status and returns it.
func (s *OrderStore) UpdateOrderStatus(ctx context.Context, tx pgx.Tx, id uuid.UUID, status domain.OrderStatus) (*domain.Order, error) {
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
func (s *OrderStore) UpdateOrderPaymentStatus(ctx context.Context, tx pgx.Tx, id uuid.UUID, status domain.PaymentStatus) (*domain.Order, error) {
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
func (s *OrderStore) UpdateOrderFulfillmentStatus(ctx context.Context, tx pgx.Tx, id uuid.UUID, status domain.FulfillmentStatus) (*domain.Order, error) {
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
func (s *OrderStore) UpdateOrderStripePaymentIntentID(ctx context.Context, tx pgx.Tx, id uuid.UUID, intentID string) (*domain.Order, error) {
	row, err := sqlcgen.New(tx).UpdateOrderStripePaymentIntentID(ctx, sqlcgen.UpdateOrderStripePaymentIntentIDParams{
		ID:                    id,
		StripePaymentIntentID: &intentID,
	})
	if err != nil {
		return nil, fmt.Errorf("update order stripe payment intent id: %w", err)
	}
	return orderFromRow(row), nil
}

// GetOrderByStripePaymentIntentID returns an order by its Stripe PaymentIntent ID.
func (s *OrderStore) GetOrderByStripePaymentIntentID(ctx context.Context, tx pgx.Tx, intentID string) (*domain.Order, error) {
	row, err := sqlcgen.New(tx).GetOrderByStripePaymentIntentID(ctx, &intentID)
	if err != nil {
		return nil, fmt.Errorf("get order by stripe payment intent id: %w", err)
	}
	return orderFromRow(row), nil
}

// DeleteOrder removes an order by ID.
func (s *OrderStore) DeleteOrder(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	if err := sqlcgen.New(tx).DeleteOrder(ctx, id); err != nil {
		return fmt.Errorf("delete order: %w", err)
	}
	return nil
}

// OrderFilter holds optional filters for listing orders.
type OrderFilter struct {
	Status               *domain.OrderStatus
	FulfillmentStatus    *domain.FulfillmentStatus
	FulfillmentStatuses  []domain.FulfillmentStatus // IN filter (takes precedence over singular)
	CustomerID           *uuid.UUID
	PlacedFrom           *time.Time
	PlacedTo             *time.Time
	Limit                int
	Offset               int
}

// ListOrders returns orders matching the given filter (hand-written for dynamic WHERE).
func (s *OrderStore) ListOrders(ctx context.Context, tx pgx.Tx, f OrderFilter) ([]domain.Order, error) {
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

// --- QuickBooks sync methods ---

// SetQBInvoice stores the QB invoice ID and number on an order.
func (s *OrderStore) SetQBInvoice(ctx context.Context, tx pgx.Tx, id uuid.UUID, qbInvoiceID, qbInvoiceNo string) error {
	_, err := tx.Exec(ctx,
		`UPDATE orders SET qb_invoice_id = $2, qb_invoice_no = $3, qb_synced_at = now(), updated_at = now() WHERE id = $1`,
		id, qbInvoiceID, qbInvoiceNo,
	)
	if err != nil {
		return fmt.Errorf("set qb invoice: %w", err)
	}
	return nil
}

// GetOrderByQBInvoiceID returns an order by its QB invoice ID.
func (s *OrderStore) GetOrderByQBInvoiceID(ctx context.Context, tx pgx.Tx, qbInvoiceID string) (*domain.Order, error) {
	var o domain.Order
	var status, paymentStatus, fulfillmentStatus string
	var shippingMethod *string
	var requestedDeliveryDate pgtype.Timestamptz
	var subtotal, discountTotal, shippingTotal, taxTotal, total int32
	var metadata json.RawMessage
	err := tx.QueryRow(ctx,
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
func (s *OrderStore) CreateCart(ctx context.Context, tx pgx.Tx, p CreateCartParams) (*domain.Cart, error) {
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

// GetCartByID returns a cart by ID.
func (s *OrderStore) GetCartByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Cart, error) {
	row, err := sqlcgen.New(tx).GetCartByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get cart %s: %w", id, err)
	}
	return cartFromRow(row), nil
}

// GetCartByCustomerID returns a cart by customer ID.
func (s *OrderStore) GetCartByCustomerID(ctx context.Context, tx pgx.Tx, customerID uuid.UUID) (*domain.Cart, error) {
	row, err := sqlcgen.New(tx).GetCartByCustomerID(ctx, &customerID)
	if err != nil {
		return nil, fmt.Errorf("get cart by customer: %w", err)
	}
	return cartFromRow(row), nil
}

// UpdateCartAddresses updates a cart's shipping and billing addresses.
func (s *OrderStore) UpdateCartAddresses(ctx context.Context, tx pgx.Tx, id, shippingAddressID, billingAddressID uuid.UUID) (*domain.Cart, error) {
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
func (s *OrderStore) UpdateCartDiscount(ctx context.Context, tx pgx.Tx, id uuid.UUID, discountID, couponCodeID *uuid.UUID) (*domain.Cart, error) {
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
func (s *OrderStore) DeleteCart(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
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
func (s *OrderStore) CreateLineItem(ctx context.Context, tx pgx.Tx, p CreateLineItemParams) (*domain.LineItem, error) {
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
func (s *OrderStore) ListLineItems(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) ([]domain.LineItem, error) {
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
func (s *OrderStore) DeleteLineItem(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	if err := sqlcgen.New(tx).DeleteLineItem(ctx, id); err != nil {
		return fmt.Errorf("delete line item: %w", err)
	}
	return nil
}

// DeleteLineItemsByOrder removes all line items for an order.
func (s *OrderStore) DeleteLineItemsByOrder(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) error {
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
func (s *OrderStore) CreateAdjustment(ctx context.Context, tx pgx.Tx, p CreateAdjustmentParams) (*domain.Adjustment, error) {
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
func (s *OrderStore) ListAdjustmentsByOrder(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) ([]domain.Adjustment, error) {
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
func (s *OrderStore) ListAdjustmentsByLineItem(ctx context.Context, tx pgx.Tx, lineItemID uuid.UUID) ([]domain.Adjustment, error) {
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
func (s *OrderStore) DeleteAdjustment(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
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
