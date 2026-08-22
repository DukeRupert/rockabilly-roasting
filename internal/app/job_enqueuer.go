package app

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// JobEnqueuer enqueues background jobs that originate from inside a service
// transaction. The interface lives in app (rather than importing jobs
// directly) to avoid an import cycle — jobs imports app for the services it
// wraps. The concrete implementation lives in the jobs package and is
// injected at wiring time via With* setters.
type JobEnqueuer interface {
	EnqueueRenewalReceipt(ctx context.Context, tx pgx.Tx, orderID, customerID uuid.UUID) error
	EnqueuePastDueNotice(ctx context.Context, tx pgx.Tx, subscriptionID, customerID uuid.UUID) error
	EnqueueSubscriptionEnded(ctx context.Context, tx pgx.Tx, subscriptionID, customerID uuid.UUID) error
	EnqueueOrderConfirm(ctx context.Context, tx pgx.Tx, orderID, customerID uuid.UUID) error
	EnqueueOrderShipped(ctx context.Context, tx pgx.Tx, orderID, customerID, shipmentID uuid.UUID) error
	EnqueueOrderReadyForPickup(ctx context.Context, tx pgx.Tx, orderID, customerID uuid.UUID) error
	EnqueueOrderOutForDelivery(ctx context.Context, tx pgx.Tx, orderID, customerID uuid.UUID) error
	EnqueueInvoicePaid(ctx context.Context, tx pgx.Tx, orderID, customerID uuid.UUID) error
	EnqueueInvoicePastDue(ctx context.Context, tx pgx.Tx, orderID, customerID uuid.UUID, stage int, dueDate time.Time) error
	// EnqueueAnnouncementDispatch schedules an announcement's audience fan-out.
	// A zero sendAt means immediately.
	EnqueueAnnouncementDispatch(ctx context.Context, tx pgx.Tx, announcementID uuid.UUID, sendAt time.Time) error
	EnqueueAnnouncementSend(ctx context.Context, tx pgx.Tx, announcementID, customerID uuid.UUID) error
}
