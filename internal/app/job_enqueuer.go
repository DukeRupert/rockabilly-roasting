package app

import (
	"context"

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
	EnqueueOrderConfirm(ctx context.Context, tx pgx.Tx, orderID, customerID uuid.UUID) error
}
