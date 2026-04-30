package jobs

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
)

// Enqueuer is the concrete app.JobEnqueuer used to enqueue jobs from inside
// service transactions. Wraps a river client so the enqueue rides on the
// caller's tx.
type Enqueuer struct {
	client *river.Client[pgx.Tx]
}

// NewEnqueuer returns an Enqueuer that uses client for transactional inserts.
func NewEnqueuer(client *river.Client[pgx.Tx]) *Enqueuer {
	return &Enqueuer{client: client}
}

// EnqueueRenewalReceipt enqueues a renewal-receipt email job in tx.
func (e *Enqueuer) EnqueueRenewalReceipt(ctx context.Context, tx pgx.Tx, orderID, customerID uuid.UUID) error {
	_, err := e.client.InsertTx(ctx, tx, SubscriptionRenewalReceiptArgs{
		OrderID:    orderID,
		CustomerID: customerID,
	}, nil)
	return err
}

// EnqueuePastDueNotice enqueues a past-due notification email job in tx.
func (e *Enqueuer) EnqueuePastDueNotice(ctx context.Context, tx pgx.Tx, subscriptionID, customerID uuid.UUID) error {
	_, err := e.client.InsertTx(ctx, tx, SubscriptionPastDueArgs{
		SubscriptionID: subscriptionID,
		CustomerID:     customerID,
	}, nil)
	return err
}

// EnqueueOrderConfirm enqueues an order-confirmation email job in tx.
func (e *Enqueuer) EnqueueOrderConfirm(ctx context.Context, tx pgx.Tx, orderID, customerID uuid.UUID) error {
	_, err := e.client.InsertTx(ctx, tx, OrderConfirmEmailArgs{
		OrderID:    orderID,
		CustomerID: customerID,
	}, nil)
	return err
}

// EnqueueOrderShipped enqueues an "order shipped" notification job in tx.
// Used by the Pirate Ship CSV import path so the email rides on the same
// transaction as the shipment write — if the tx rolls back, no email goes.
func (e *Enqueuer) EnqueueOrderShipped(ctx context.Context, tx pgx.Tx, orderID, customerID, shipmentID uuid.UUID) error {
	_, err := e.client.InsertTx(ctx, tx, OrderShippedEmailArgs{
		OrderID:    orderID,
		CustomerID: customerID,
		ShipmentID: shipmentID,
	}, nil)
	return err
}

// EnqueueOrderReadyForPickup enqueues a "your order is ready" notification
// for a pickup order, in tx so it rides on the staff transition's commit.
func (e *Enqueuer) EnqueueOrderReadyForPickup(ctx context.Context, tx pgx.Tx, orderID, customerID uuid.UUID) error {
	_, err := e.client.InsertTx(ctx, tx, OrderReadyForPickupEmailArgs{
		OrderID:    orderID,
		CustomerID: customerID,
	}, nil)
	return err
}

// EnqueueOrderOutForDelivery enqueues an "out for local delivery today"
// notification for a local-delivery order, in tx so it rides on the staff
// transition's commit.
func (e *Enqueuer) EnqueueOrderOutForDelivery(ctx context.Context, tx pgx.Tx, orderID, customerID uuid.UUID) error {
	_, err := e.client.InsertTx(ctx, tx, OrderOutForDeliveryEmailArgs{
		OrderID:    orderID,
		CustomerID: customerID,
	}, nil)
	return err
}
