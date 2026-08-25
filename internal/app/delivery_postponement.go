package app

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
)

// PostponeDeliveryRunResult reports what a postponement actually did, so the
// admin can tell staff rather than leaving them to guess whether the orders
// already on the books followed the van.
type PostponeDeliveryRunResult struct {
	OriginalDate time.Time
	MovedTo      time.Time
	// OrdersMoved is how many orders already promised the original date were
	// re-dated onto the new one.
	OrdersMoved int64
}

// PostponeDeliveryRun moves the delivery run scheduled for originalDate onto
// movedTo, and drags the orders already promised that date along with it.
//
// Both halves matter. The schedule rule governs what future customers are
// quoted; the stored scheduled_delivery_date on existing orders governs what
// the fulfillment queue and the load list show staff on the morning. Changing
// only the rule would leave the van's own paperwork pointing at a day the shop
// is shut.
//
// Customers are deliberately not emailed. The confirmation they already hold
// names the old date, and a shop that wants to explain a moved holiday has the
// Announcements composer for exactly that — sending an automatic "your delivery
// moved" on top of it would be two messages about one change.
func (s *CheckoutService) PostponeDeliveryRun(
	ctx context.Context,
	tx pgx.Tx,
	originalDate, movedTo time.Time,
	note string,
	actor Actor,
) (*PostponeDeliveryRunResult, error) {
	cfg, err := s.shipping.GetConfig(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("load shipping config: %w", err)
	}
	if cfg == nil || !cfg.HasDeliverySchedule() {
		return nil, ErrPostponeNoSchedule
	}

	original := dateOnly(originalDate)
	moved := dateOnly(movedTo)

	// Only a day the van actually runs can be postponed. Marking a Wednesday
	// would record a rule that never fires, and read to staff as though
	// something had been handled.
	if !cfg.DeliversOn(original.Weekday()) {
		return nil, ErrPostponeNotDeliveryDay
	}
	if !moved.After(original) {
		return nil, ErrPostponeNotForward
	}
	if moved.Sub(original) > domain.MaxDeliveryPostponementDays*24*time.Hour {
		return nil, ErrPostponeTooFar
	}

	// Where the orders for this run are sitting *now*, which is not necessarily
	// the scheduled date. Correcting an earlier postponement — staff marked
	// Tuesday, meant Wednesday — has already moved them once, and looking for
	// them on the original date would find nothing and quietly strand them on
	// the first answer while the schedule moved on to the second.
	from := original
	existing, err := s.shipping.ListDeliveryPostponements(ctx, tx)
	if err != nil {
		return nil, err
	}
	for _, p := range existing {
		if sameDate(p.OriginalDate, original) {
			from = dateOnly(p.MovedTo)
			break
		}
	}

	if err := s.shipping.UpsertDeliveryPostponement(ctx, tx, original, moved, note); err != nil {
		return nil, err
	}

	moveCount, err := s.shipping.RescheduleOrdersOnDate(ctx, tx, from, moved)
	if err != nil {
		return nil, err
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditDeliveryRunPostponed,
		ResourceType: "delivery_run",
		ResourceID:   uuid.Nil,
		After: map[string]any{
			"original_date": original.Format(dateLayout),
			"moved_to":      moved.Format(dateLayout),
			// moved_from records where the orders actually came from, which
			// differs from original_date when this corrects an earlier move.
			"moved_from":   from.Format(dateLayout),
			"note":         note,
			"orders_moved": moveCount,
		},
	}); err != nil {
		return nil, fmt.Errorf("audit delivery run postponed: %w", err)
	}

	return &PostponeDeliveryRunResult{
		OriginalDate: original,
		MovedTo:      moved,
		OrdersMoved:  moveCount,
	}, nil
}

// RestoreDeliveryRun removes a postponement, putting the run back on its
// scheduled day and returning any orders that followed it.
//
// The orders move back for the same reason they moved in the first place: the
// stored date is what staff pack to, and leaving it on a day with no run would
// strand those orders in the queue.
func (s *CheckoutService) RestoreDeliveryRun(
	ctx context.Context,
	tx pgx.Tx,
	originalDate time.Time,
	actor Actor,
) (*PostponeDeliveryRunResult, error) {
	original := dateOnly(originalDate)

	// Read the postponement before deleting it: undoing the order move needs to
	// know where they went, and after the delete nothing records that.
	existing, err := s.shipping.ListDeliveryPostponements(ctx, tx)
	if err != nil {
		return nil, err
	}
	var moved time.Time
	for _, p := range existing {
		if sameDate(p.OriginalDate, original) {
			moved = dateOnly(p.MovedTo)
			break
		}
	}
	if moved.IsZero() {
		// Nothing recorded for that day. Treated as done rather than as an
		// error: a second click on Restore should be a no-op, not a failure.
		return &PostponeDeliveryRunResult{OriginalDate: original}, nil
	}

	if err := s.shipping.DeleteDeliveryPostponement(ctx, tx, original); err != nil {
		return nil, err
	}

	moveCount, err := s.shipping.RescheduleOrdersOnDate(ctx, tx, moved, original)
	if err != nil {
		return nil, err
	}

	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       audit.AuditDeliveryRunRestored,
		ResourceType: "delivery_run",
		ResourceID:   uuid.Nil,
		After: map[string]any{
			"original_date": original.Format(dateLayout),
			"was_moved_to":  moved.Format(dateLayout),
			"orders_moved":  moveCount,
		},
	}); err != nil {
		return nil, fmt.Errorf("audit delivery run restored: %w", err)
	}

	return &PostponeDeliveryRunResult{
		OriginalDate: original,
		MovedTo:      moved,
		OrdersMoved:  moveCount,
	}, nil
}

// ListDeliveryPostponements returns the recorded postponements for display.
func (s *CheckoutService) ListDeliveryPostponements(ctx context.Context, tx pgx.Tx) ([]domain.DeliveryPostponement, error) {
	return s.shipping.ListDeliveryPostponements(ctx, tx)
}

// dateLayout is how postponement dates are written into audit metadata: a plain
// calendar day, because that is what they are.
const dateLayout = "2006-01-02"

// dateOnly strips the clock, keeping the zone. Postponements name days, and a
// stray afternoon on one side of a comparison would make two records of the
// same day look different.
func dateOnly(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// sameDate compares two times by the calendar day each names, ignoring clock
// and zone — a date read back from Postgres arrives in UTC, while one parsed
// from an admin form is in the merchant's zone.
func sameDate(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}
