package app

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/emailtemplates"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/email"
	"github.com/dukerupert/hiri/internal/store"
)

// SendConfirmationEmail sends an order confirmation email. Data is loaded
// in a read tx, the external email send happens outside any tx, and audit
// is recorded in a second tx — matching the pattern in RenewalService.
func (s *OrderService) SendConfirmationEmail(ctx context.Context, pool *pgxpool.Pool, orderID, customerID uuid.UUID) error {
	var (
		order        *domain.Order
		customer     *domain.Customer
		items        []emailtemplates.OrderLineItemData
		shippingAddr string
	)

	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		o, err := s.orders.GetOrderByIDAsStaff(ctx, tx, orderID)
		if err != nil {
			return fmt.Errorf("get order %s: %w", orderID, err)
		}
		order = o

		c, err := s.customers.GetByID(ctx, tx, customerID)
		if err != nil {
			return fmt.Errorf("get customer %s: %w", customerID, err)
		}
		customer = c

		lineItems, err := s.orders.ListLineItems(ctx, tx, order.ID)
		if err != nil {
			return fmt.Errorf("list line items: %w", err)
		}

		items = make([]emailtemplates.OrderLineItemData, len(lineItems))
		for i, li := range lineItems {
			productName := "Product"
			if variant, err := s.catalog.GetVariantByID(ctx, tx, li.VariantID); err == nil {
				if product, err := s.catalog.GetProductByID(ctx, tx, variant.ProductID); err == nil {
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

		if addr, err := s.customers.GetAddressByIDAsStaff(ctx, tx, order.ShippingAddressID); err == nil {
			shippingAddr = emailtemplates.FormatAddress(addr.Line1, addr.City, addr.State, addr.PostalCode)
		}
		return nil
	}); err != nil {
		return err
	}

	html, text, err := s.email.Renderer.Render("order_confirm", emailtemplates.OrderConfirmData{
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
		StoreName:     s.email.StoreName,
		StoreURL:      s.email.BaseURL,
	})
	if err != nil {
		s.metrics.EmailsSent.WithLabelValues("order_confirm", "failed").Inc()
		return fmt.Errorf("render order confirm template: %w", err)
	}

	if _, err := s.email.Mailer.Send(ctx, email.Message{
		From:    s.email.FromAddr,
		To:      customer.Email,
		Subject: fmt.Sprintf("Order confirmed — %s", order.Number),
		HTML:    html,
		Text:    text,
		Tag:     "order-confirm",
	}); err != nil {
		s.metrics.EmailsSent.WithLabelValues("order_confirm", "failed").Inc()
		return fmt.Errorf("send order confirm email: %w", err)
	}

	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		return s.audit.Record(ctx, tx, audit.AuditEntry{
			ActorType:    domain.AuditActorTypeSystem,
			ActorName:    "order_confirm_worker",
			Action:       audit.AuditEmailOrderConfirmed,
			ResourceType: "order",
			ResourceID:   order.ID,
			Metadata:     map[string]any{"order_number": order.Number},
		})
	}); err != nil {
		return fmt.Errorf("audit order confirm sent: %w", err)
	}

	s.metrics.EmailsSent.WithLabelValues("order_confirm", "sent").Inc()
	return nil
}
