package app

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
)

// scheduleLocalDelivery resolves the delivery run an order should be promised.
// promised is the date the customer is told; runDate is which run that is, and
// the two differ only when the run has been postponed. Both are nil when no
// promise applies.
//
// The run date is stored alongside the promise because a date cannot identify a
// run once two of them can share one — see the delivery_run_date column in
// migration 072. Returning them together is what stops a caller recording one
// without the other.
//
// Nil is returned — rather than an error — for every ordinary "no date" case:
// the order isn't going out on the van, no schedule is configured, or the
// shipping config could not be read. A missing delivery date degrades to
// method-only messaging at checkout and in the confirmation email, which is the
// behaviour that shipped before this feature existed. Failing the whole order
// placement because a display date could not be computed would trade a cosmetic
// gap for a lost sale.
//
// placedAt is passed in rather than read from the clock here so the date stamped
// on the order matches the order's own placed_at, and so tests can pin it.
func scheduleLocalDelivery(
	ctx context.Context,
	tx pgx.Tx,
	shipping *store.ShippingStore,
	method *domain.ShippingMethod,
	placedAt time.Time,
	loc *time.Location,
) (promised, runDate *time.Time) {
	if shipping == nil || method == nil || *method != domain.ShippingMethodLocalDelivery {
		return nil, nil
	}
	cfg, err := shipping.GetConfig(ctx, tx)
	if err != nil || cfg == nil {
		return nil, nil
	}
	scheduled, effective, ok := cfg.NextDeliveryRun(placedAt, loc)
	if !ok {
		return nil, nil
	}
	return &effective, &scheduled
}
