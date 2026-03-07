package jobs

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/emailtemplates"
	"github.com/dukerupert/hiri/internal/platform/email"
	"github.com/dukerupert/hiri/internal/store"
)

// OrderConfirmEmailWorker sends an order confirmation email to the customer.
type OrderConfirmEmailWorker struct {
	river.WorkerDefaults[OrderConfirmEmailArgs]
	orders    *store.OrderStore
	customers *store.CustomerStore
	catalog   *store.CatalogStore
	pool      *pgxpool.Pool
	mailer    email.Sender
	renderer  *emailtemplates.Renderer
	fromAddr  string
	baseURL   string
	storeName string
}

// NewOrderConfirmEmailWorker creates a new OrderConfirmEmailWorker.
func NewOrderConfirmEmailWorker(
	orders *store.OrderStore,
	customers *store.CustomerStore,
	catalog *store.CatalogStore,
	pool *pgxpool.Pool,
	mailer email.Sender,
	renderer *emailtemplates.Renderer,
	fromAddr, baseURL, storeName string,
) *OrderConfirmEmailWorker {
	return &OrderConfirmEmailWorker{
		orders:    orders,
		customers: customers,
		catalog:   catalog,
		pool:      pool,
		mailer:    mailer,
		renderer:  renderer,
		fromAddr:  fromAddr,
		baseURL:   baseURL,
		storeName: storeName,
	}
}

// Work processes an order confirmation email job.
func (w *OrderConfirmEmailWorker) Work(ctx context.Context, job *river.Job[OrderConfirmEmailArgs]) error {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	order, err := w.orders.GetOrderByID(ctx, tx, job.Args.OrderID)
	if err != nil {
		return fmt.Errorf("get order %s: %w", job.Args.OrderID, err)
	}

	customer, err := w.customers.GetByID(ctx, tx, job.Args.CustomerID)
	if err != nil {
		return fmt.Errorf("get customer %s: %w", job.Args.CustomerID, err)
	}

	lineItems, err := w.orders.ListLineItems(ctx, tx, order.ID)
	if err != nil {
		return fmt.Errorf("list line items: %w", err)
	}

	// Build line item data with product names.
	items := make([]emailtemplates.OrderLineItemData, len(lineItems))
	for i, li := range lineItems {
		productName := "Product"
		variant, err := w.catalog.GetVariantByID(ctx, tx, li.VariantID)
		if err == nil {
			product, err := w.catalog.GetProductByID(ctx, tx, variant.ProductID)
			if err == nil {
				productName = product.Title
			}
		}
		items[i] = emailtemplates.OrderLineItemData{
			ProductName: productName,
			Quantity:    li.Quantity,
			UnitPrice:   li.UnitPrice,
			Total:       li.Total,
		}
	}

	// Build shipping address string.
	shippingAddr := ""
	addr, err := w.customers.GetAddressByID(ctx, tx, order.ShippingAddressID)
	if err == nil {
		shippingAddr = emailtemplates.FormatAddress(addr.Line1, addr.City, addr.State, addr.PostalCode)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}

	html, text, err := w.renderer.Render("order_confirm", emailtemplates.OrderConfirmData{
		CustomerName:  customer.FirstName,
		OrderNumber:   order.Number,
		OrderDate:     order.PlacedAt,
		Items:         items,
		Subtotal:      order.Subtotal,
		DiscountTotal: order.DiscountTotal,
		ShippingTotal: order.ShippingTotal,
		TaxTotal:      order.TaxTotal,
		OrderTotal:    order.Total,
		ShippingAddr:  shippingAddr,
		StoreName:     w.storeName,
		StoreURL:      w.baseURL,
	})
	if err != nil {
		return fmt.Errorf("render order confirm template: %w", err)
	}

	result, err := w.mailer.Send(ctx, email.Message{
		From:    w.fromAddr,
		To:      customer.Email,
		Subject: fmt.Sprintf("Order confirmed — %s", order.Number),
		HTML:    html,
		Text:    text,
		Tag:     "order-confirm",
	})
	if err != nil {
		return fmt.Errorf("send order confirm email: %w", err)
	}

	slog.Info("order confirmation email sent",
		"order_id", order.ID,
		"order_number", order.Number,
		"customer_email", customer.Email,
		"message_id", result.MessageID,
		"river_job_id", job.ID,
	)

	return nil
}
