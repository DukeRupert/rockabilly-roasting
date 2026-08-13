package app

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
)

// scheduleLocalDelivery resolves the delivery run an order should be promised,
// or nil when no promise applies.
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
) *time.Time {
	if shipping == nil || method == nil || *method != domain.ShippingMethodLocalDelivery {
		return nil
	}
	cfg, err := shipping.GetConfig(ctx, tx)
	if err != nil || cfg == nil {
		return nil
	}
	date, ok := cfg.NextDeliveryDate(placedAt, loc)
	if !ok {
		return nil
	}
	return &date
}
