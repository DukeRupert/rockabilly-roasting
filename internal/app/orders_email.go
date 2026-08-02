package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/emailtemplates"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/email"
	"github.com/dukerupert/hiri/internal/store"
)

// emailLineItems builds the line-item rows for order emails. The product
// name carries the variant's option summary ("Bonneville Blend — Whole Bean
// · 12oz") because grind and size are what a coffee buyer needs to verify;
// lookups are best-effort so a deleted variant never blocks the email.
func (s *OrderService) emailLineItems(ctx context.Context, tx pgx.Tx, lineItems []domain.LineItem) []emailtemplates.OrderLineItemData {
	return emailLineItemsFrom(ctx, tx, s.catalog, lineItems)
}

// emailLineItemsFrom is the catalog-driven implementation, shared with
// WholesaleService (the order reminder prints the customer's last order, and
// must label items exactly as the confirmation email did).
func emailLineItemsFrom(ctx context.Context, tx pgx.Tx, catalog *store.CatalogStore, lineItems []domain.LineItem) []emailtemplates.OrderLineItemData {
	items := make([]emailtemplates.OrderLineItemData, len(lineItems))
	for i, li := range lineItems {
		productName := "Product"
		if variant, err := catalog.GetVariantByID(ctx, tx, li.VariantID); err == nil {
			if product, err := catalog.GetProductByID(ctx, tx, variant.ProductID); err == nil {
				productName = product.Title
			}
			if labels, err := catalog.ListVariantOptionLabels(ctx, tx, li.VariantID); err == nil && len(labels) > 0 {
				productName += " — " + strings.Join(labels, " · ")
			}
		}
		items[i] = emailtemplates.OrderLineItemData{
			ProductName: productName,
			Quantity:    li.Quantity,
			UnitPrice:   li.UnitPrice,
			Total:       li.Total,
		}
	}
	return items
}

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

		items = s.emailLineItems(ctx, tx, lineItems)

		if addr, err := s.customers.GetAddressByIDAsStaff(ctx, tx, order.ShippingAddressID); err == nil {
			shippingAddr = emailtemplates.FormatRecipientAddress(addr.FirstName, addr.LastName, addr.Line1, addr.Line2, addr.City, addr.State, addr.PostalCode)
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

// SendRenewalReceiptEmail sends a subscription-renewal receipt for a renewal
// order. Reads order, customer, line items, and (for subscription orders) the
// subscription's next billing date. Uses the read → send → audit pattern.
func (s *OrderService) SendRenewalReceiptEmail(ctx context.Context, pool *pgxpool.Pool, orderID, customerID uuid.UUID) error {
	var (
		order        *domain.Order
		customer     *domain.Customer
		items        []emailtemplates.OrderLineItemData
		shippingAddr string
		nextChargeOn *time.Time
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

		items = s.emailLineItems(ctx, tx, lineItems)

		if addr, err := s.customers.GetAddressByIDAsStaff(ctx, tx, order.ShippingAddressID); err == nil {
			shippingAddr = emailtemplates.FormatRecipientAddress(addr.FirstName, addr.LastName, addr.Line1, addr.Line2, addr.City, addr.State, addr.PostalCode)
		}

		// Single-subscription renewal — pull the next billing date directly.
		// Batched renewals leave SubscriptionID nil (linked via subscription_orders);
		// we omit the next-charge line in that case rather than guessing.
		if order.SubscriptionID != nil && s.subscriptions != nil {
			if sub, err := s.subscriptions.GetByIDAsStaff(ctx, tx, *order.SubscriptionID); err == nil {
				nbd := sub.NextOrderAt
				nextChargeOn = &nbd
			}
		}
		return nil
	}); err != nil {
		return err
	}

	html, text, err := s.email.Renderer.Render("subscription_renewal_receipt", emailtemplates.SubscriptionRenewalReceiptData{
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
		NextChargeOn:  nextChargeOn,
		StoreName:     s.email.StoreName,
		StoreURL:      s.email.BaseURL,
		AccountURL:    s.email.BaseURL + "/account/subscriptions",
	})
	if err != nil {
		s.metrics.EmailsSent.WithLabelValues("subscription_renewal_receipt", "failed").Inc()
		return fmt.Errorf("render renewal receipt template: %w", err)
	}

	if _, err := s.email.Mailer.Send(ctx, email.Message{
		From:    s.email.FromAddr,
		To:      customer.Email,
		Subject: fmt.Sprintf("Subscription renewed — %s", order.Number),
		HTML:    html,
		Text:    text,
		Tag:     "subscription-renewal-receipt",
	}); err != nil {
		s.metrics.EmailsSent.WithLabelValues("subscription_renewal_receipt", "failed").Inc()
		return fmt.Errorf("send renewal receipt email: %w", err)
	}

	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		return s.audit.Record(ctx, tx, audit.AuditEntry{
			ActorType:    domain.AuditActorTypeSystem,
			ActorName:    "renewal_receipt_worker",
			Action:       audit.AuditEmailSubscriptionRenewed,
			ResourceType: "order",
			ResourceID:   order.ID,
			Metadata:     map[string]any{"order_number": order.Number},
		})
	}); err != nil {
		return fmt.Errorf("audit renewal receipt sent: %w", err)
	}

	s.metrics.EmailsSent.WithLabelValues("subscription_renewal_receipt", "sent").Inc()
	return nil
}

// SendOrderShippedEmail tells the customer their order has shipped, with
// tracking. Loaded with the read → send → audit pattern. The CSV import path
// enqueues this from inside its per-row tx — at job runtime we read order +
// customer + shipment fresh.
func (s *OrderService) SendOrderShippedEmail(ctx context.Context, pool *pgxpool.Pool, orderID, customerID, shipmentID uuid.UUID) error {
	var (
		order        *domain.Order
		customer     *domain.Customer
		shipment     *domain.Shipment
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

		sh, err := s.shipments.GetShipmentByIDAsStaff(ctx, tx, shipmentID)
		if err != nil {
			return fmt.Errorf("get shipment %s: %w", shipmentID, err)
		}
		shipment = sh

		if addr, err := s.customers.GetAddressByIDAsStaff(ctx, tx, order.ShippingAddressID); err == nil {
			shippingAddr = emailtemplates.FormatRecipientAddress(addr.FirstName, addr.LastName, addr.Line1, addr.Line2, addr.City, addr.State, addr.PostalCode)
		}
		return nil
	}); err != nil {
		return err
	}

	html, text, err := s.email.Renderer.Render("order_shipped", emailtemplates.OrderShippedData{
		CustomerName:   customer.FirstName,
		OrderNumber:    order.Number,
		CarrierName:    shipment.CarrierName,
		ServiceName:    shipment.ServiceName,
		TrackingNumber: shipment.TrackingNumber,
		TrackingURL:    domain.TrackingURL(shipment.CarrierName, shipment.TrackingNumber),
		ShippedOn:      shipment.ShippedAt,
		ShippingAddr:   shippingAddr,
		StoreName:      s.email.StoreName,
		StoreURL:       s.email.BaseURL,
		AccountURL:     s.email.BaseURL + "/account/orders/" + order.ID.String(),
	})
	if err != nil {
		s.metrics.EmailsSent.WithLabelValues("order_shipped", "failed").Inc()
		return fmt.Errorf("render order shipped template: %w", err)
	}

	if _, err := s.email.Mailer.Send(ctx, email.Message{
		From:    s.email.FromAddr,
		To:      customer.Email,
		Subject: fmt.Sprintf("Your order's on the road — %s", order.Number),
		HTML:    html,
		Text:    text,
		Tag:     "order-shipped",
	}); err != nil {
		s.metrics.EmailsSent.WithLabelValues("order_shipped", "failed").Inc()
		return fmt.Errorf("send order shipped email: %w", err)
	}

	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		return s.audit.Record(ctx, tx, audit.AuditEntry{
			ActorType:    domain.AuditActorTypeSystem,
			ActorName:    "order_shipped_worker",
			Action:       audit.AuditEmailOrderShipped,
			ResourceType: "shipment",
			ResourceID:   shipment.ID,
			Metadata: map[string]any{
				"order_number":    order.Number,
				"tracking_number": shipment.TrackingNumber,
				"carrier":         shipment.CarrierName,
			},
		})
	}); err != nil {
		return fmt.Errorf("audit order shipped sent: %w", err)
	}

	s.metrics.EmailsSent.WithLabelValues("order_shipped", "sent").Inc()
	return nil
}

// SendRefundConfirmationEmail sends a refund-issued notice. The refund amount
// is passed in by the caller (the webhook knows the actual refunded amount,
// which may be partial). Uses the read → send → audit pattern.
func (s *OrderService) SendRefundConfirmationEmail(ctx context.Context, pool *pgxpool.Pool, orderID, customerID uuid.UUID, refundAmountCents int) error {
	var (
		order    *domain.Order
		customer *domain.Customer
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
		return nil
	}); err != nil {
		return err
	}

	html, text, err := s.email.Renderer.Render("refund_confirmation", emailtemplates.RefundConfirmationData{
		CustomerName: customer.FirstName,
		OrderNumber:  order.Number,
		RefundAmount: refundAmountCents,
		StoreName:    s.email.StoreName,
		StoreURL:     s.email.BaseURL,
	})
	if err != nil {
		s.metrics.EmailsSent.WithLabelValues("refund_confirmation", "failed").Inc()
		return fmt.Errorf("render refund confirmation template: %w", err)
	}

	if _, err := s.email.Mailer.Send(ctx, email.Message{
		From:    s.email.FromAddr,
		To:      customer.Email,
		Subject: fmt.Sprintf("Refund issued — %s", order.Number),
		HTML:    html,
		Text:    text,
		Tag:     "refund-confirmation",
	}); err != nil {
		s.metrics.EmailsSent.WithLabelValues("refund_confirmation", "failed").Inc()
		return fmt.Errorf("send refund confirmation email: %w", err)
	}

	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		return s.audit.Record(ctx, tx, audit.AuditEntry{
			ActorType:    domain.AuditActorTypeSystem,
			ActorName:    "refund_confirmation_worker",
			Action:       audit.AuditEmailRefundIssued,
			ResourceType: "order",
			ResourceID:   order.ID,
			Metadata: map[string]any{
				"order_number":  order.Number,
				"refund_amount": refundAmountCents,
			},
		})
	}); err != nil {
		return fmt.Errorf("audit refund confirmation sent: %w", err)
	}

	s.metrics.EmailsSent.WithLabelValues("refund_confirmation", "sent").Inc()
	return nil
}

// SendOrderReadyForPickupEmail tells a pickup customer their order is packed
// and waiting at the shop. Loaded with the read → send → audit pattern.
// The pickup instructions come from shipping_config so the merchant can
// update them without a code change.
func (s *OrderService) SendOrderReadyForPickupEmail(ctx context.Context, pool *pgxpool.Pool, orderID, customerID uuid.UUID) error {
	var (
		order              *domain.Order
		customer           *domain.Customer
		pickupInstructions string
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

		if s.shipments != nil {
			cfg, err := s.shipments.GetConfig(ctx, tx)
			if err == nil {
				pickupInstructions = cfg.LocalPickupInstructions
			}
		}
		return nil
	}); err != nil {
		return err
	}

	html, text, err := s.email.Renderer.Render("order_ready_for_pickup", emailtemplates.OrderReadyForPickupData{
		CustomerName:       customer.FirstName,
		OrderNumber:        order.Number,
		PickupInstructions: pickupInstructions,
		StoreName:          s.email.StoreName,
		StoreURL:           s.email.BaseURL,
		AccountURL:         s.email.BaseURL + "/account/orders/" + order.ID.String(),
	})
	if err != nil {
		s.metrics.EmailsSent.WithLabelValues("order_ready_for_pickup", "failed").Inc()
		return fmt.Errorf("render ready-for-pickup template: %w", err)
	}

	if _, err := s.email.Mailer.Send(ctx, email.Message{
		From:    s.email.FromAddr,
		To:      customer.Email,
		Subject: fmt.Sprintf("Your order's ready for pickup — %s", order.Number),
		HTML:    html,
		Text:    text,
		Tag:     "order-ready-for-pickup",
	}); err != nil {
		s.metrics.EmailsSent.WithLabelValues("order_ready_for_pickup", "failed").Inc()
		return fmt.Errorf("send ready-for-pickup email: %w", err)
	}

	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		return s.audit.Record(ctx, tx, audit.AuditEntry{
			ActorType:    domain.AuditActorTypeSystem,
			ActorName:    "order_ready_for_pickup_worker",
			Action:       audit.AuditEmailOrderReadyForPickup,
			ResourceType: "order",
			ResourceID:   order.ID,
			Metadata:     map[string]any{"order_number": order.Number},
		})
	}); err != nil {
		return fmt.Errorf("audit ready-for-pickup sent: %w", err)
	}

	s.metrics.EmailsSent.WithLabelValues("order_ready_for_pickup", "sent").Inc()
	return nil
}

// SendOrderOutForDeliveryEmail tells a local-delivery customer their order is
// on the route today. The configured display string for delivery days is
// surfaced so customers see consistent wording across receipt and email.
func (s *OrderService) SendOrderOutForDeliveryEmail(ctx context.Context, pool *pgxpool.Pool, orderID, customerID uuid.UUID) error {
	var (
		order        *domain.Order
		customer     *domain.Customer
		deliveryDays string
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

		if s.shipments != nil {
			cfg, err := s.shipments.GetConfig(ctx, tx)
			if err == nil {
				deliveryDays = cfg.LocalDeliveryDays
			}
		}
		if addr, err := s.customers.GetAddressByIDAsStaff(ctx, tx, order.ShippingAddressID); err == nil {
			shippingAddr = emailtemplates.FormatRecipientAddress(addr.FirstName, addr.LastName, addr.Line1, addr.Line2, addr.City, addr.State, addr.PostalCode)
		}
		return nil
	}); err != nil {
		return err
	}

	html, text, err := s.email.Renderer.Render("order_out_for_delivery", emailtemplates.OrderOutForDeliveryData{
		CustomerName: customer.FirstName,
		OrderNumber:  order.Number,
		DeliveryDays: deliveryDays,
		ShippingAddr: shippingAddr,
		StoreName:    s.email.StoreName,
		StoreURL:     s.email.BaseURL,
		AccountURL:   s.email.BaseURL + "/account/orders/" + order.ID.String(),
	})
	if err != nil {
		s.metrics.EmailsSent.WithLabelValues("order_out_for_delivery", "failed").Inc()
		return fmt.Errorf("render out-for-delivery template: %w", err)
	}

	if _, err := s.email.Mailer.Send(ctx, email.Message{
		From:    s.email.FromAddr,
		To:      customer.Email,
		Subject: fmt.Sprintf("Out for local delivery today — %s", order.Number),
		HTML:    html,
		Text:    text,
		Tag:     "order-out-for-delivery",
	}); err != nil {
		s.metrics.EmailsSent.WithLabelValues("order_out_for_delivery", "failed").Inc()
		return fmt.Errorf("send out-for-delivery email: %w", err)
	}

	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		return s.audit.Record(ctx, tx, audit.AuditEntry{
			ActorType:    domain.AuditActorTypeSystem,
			ActorName:    "order_out_for_delivery_worker",
			Action:       audit.AuditEmailOrderOutForDelivery,
			ResourceType: "order",
			ResourceID:   order.ID,
			Metadata:     map[string]any{"order_number": order.Number},
		})
	}); err != nil {
		return fmt.Errorf("audit out-for-delivery sent: %w", err)
	}

	s.metrics.EmailsSent.WithLabelValues("order_out_for_delivery", "sent").Inc()
	return nil
}

