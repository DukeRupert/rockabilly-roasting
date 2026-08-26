package app

import (
	"context"
	"errors"
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
	// RouteMoved reports that the run's planned delivery route followed it onto
	// the new day, stops and order intact.
	RouteMoved bool
	// RouteDropped says why the run's planned route could not follow it and was
	// discarded instead. Empty when nothing was dropped. Staff have to re-plan;
	// the flash says so, and says which of the two reasons it was.
	RouteDropped RouteDropReason
}

// RouteDropReason is why a run's planned route could not travel with it.
//
// Both outcomes are the same for staff — the route is gone and needs building
// again — but they differ in which day is now short of a plan, which is the
// part the flash has to get right.
type RouteDropReason string

const (
	// RouteDropTargetOccupied — the day the run moved onto already had a live
	// route of its own. One day, one stop list for the driver.
	RouteDropTargetOccupied RouteDropReason = "target_occupied"
	// RouteDropSharedRun — the route was carrying another run's stops too,
	// because two runs had collapsed onto the day it was planned for. Taking it
	// along would have stolen the sheet for the run that stayed behind.
	RouteDropSharedRun RouteDropReason = "shared_run"
)

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
// now is passed in rather than read from the clock so the "has this run already
// gone?" judgement is testable, and so one request judges every date against a
// single instant.
func (s *CheckoutService) PostponeDeliveryRun(
	ctx context.Context,
	tx pgx.Tx,
	now time.Time,
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
	// Calendar arithmetic, not a duration. Both sides are local midnights, so
	// subtracting them across a daylight-saving transition yields 337 hours for
	// an exact fortnight and would reject a move the database constraint
	// accepts.
	if moved.After(original.AddDate(0, 0, domain.MaxDeliveryPostponementDays)) {
		return nil, ErrPostponeTooFar
	}
	// A run that has already gone cannot be moved. Without this, marking a
	// Monday from three months ago rewrites the promised date on orders that
	// were delivered that day — silently corrupting the record of what happened
	// while changing nothing about any future run.
	//
	// Judged on when the run actually goes out, not on its scheduled day. Those
	// differ precisely when it has already been postponed once, which is the
	// case where a correction is most likely: a Monday holiday moved to Tuesday,
	// and on Tuesday morning the van still cannot go. The run has not gone, and
	// refusing to move it again would leave staff with no correct action —
	// Restore would only put the orders back on the closed Monday.
	today := dateOnly(now.In(original.Location()))
	// Where the run goes out as things stand — its scheduled day unless it has
	// already been moved once. Both the guard below and the route reconciliation
	// need it: the route was planned for the day the van actually leaves.
	oldEffective := cfg.EffectiveRunDate(original, original.Location())
	if oldEffective.Before(today) {
		return nil, ErrPostponeAlreadyRun
	}
	// And it cannot be moved onto a day that has gone. The checks above are all
	// measured against the run's own scheduled day — "later than it was", "not
	// more than a fortnight" — none of which says anything about today. Without
	// this, correcting a run postponed a week out to a date now behind us stamps
	// yesterday onto every order riding it, and neither half of the pair will
	// touch it again: postpone sees a run that has gone, restore sees a
	// scheduled day that has passed. The orders are then stranded on a dead date
	// with the panel reporting the run as settled.
	if moved.Before(today) {
		return nil, ErrPostponeIntoPast
	}
	// A postponement resolves one hop and deliberately does not chase chains: a
	// run moved onto Thursday happens Thursday, whatever Thursday's own run
	// does. That is the right reading of any single row, and it is exactly why a
	// chain must never be allowed to form — nothing downstream would notice one,
	// and the panel would show two innocuous-looking rows.
	//
	// Refused in both directions, because this is the same shape that has bitten
	// this feature three times: a rule written for one half of a symmetric pair.
	//
	//   - Moving a run onto a day whose own run has already been moved away. The
	//     shop is shut that day — that is why the other run left — so this lands
	//     the van on precisely the closed day the feature exists to avoid.
	//   - Moving a run off a day that another run has already been moved onto.
	//     The same closed day, reached by doing the two in the other order.
	//
	// The fix for staff is to restore the other postponement, then move both.
	// Enforced here rather than in the schema because a cross-row rule is not a
	// CHECK, and this service is the only writer.
	for _, p := range cfg.DeliveryPostponements {
		if sameDate(p.OriginalDate, original) {
			// The row being corrected. It is about to be replaced, so it cannot
			// chain with itself.
			continue
		}
		if sameDate(p.OriginalDate, moved) {
			return nil, ErrPostponeTargetRunMoved
		}
		if sameDate(p.MovedTo, original) {
			return nil, ErrPostponeStrandsMovedRun
		}
	}

	// Decided before anything is written. An active route refuses the whole
	// postponement, and a refusal has to leave the schedule as it found it.
	routeMove, err := s.planRouteMove(ctx, tx, original, oldEffective, moved)
	if err != nil {
		return nil, err
	}

	if err := s.shipping.UpsertDeliveryPostponement(ctx, tx, original, moved, note); err != nil {
		return nil, err
	}

	// Selected by the run they ride, not by the date they currently show. That
	// is what makes correcting an earlier postponement work — the orders have
	// already moved once and no longer sit on the scheduled date — and what
	// keeps a run postponed onto another run day from sweeping up that day's
	// own orders.
	moveCount, err := s.shipping.RescheduleDeliveryRun(ctx, tx, original, moved)
	if err != nil {
		return nil, err
	}

	routeMoved, routeDropped, err := s.applyRouteMove(ctx, tx, routeMove)
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
			"note":          note,
			"orders_moved":  moveCount,
			"route_moved":   routeMoved,
			"route_dropped": string(routeDropped),
		},
	}); err != nil {
		return nil, fmt.Errorf("audit delivery run postponed: %w", err)
	}

	return &PostponeDeliveryRunResult{
		OriginalDate: original,
		MovedTo:      moved,
		OrdersMoved:  moveCount,
		RouteMoved:   routeMoved,
		RouteDropped: routeDropped,
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
	now time.Time,
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
		// Checked before the date guard below so a repeated click on a run that
		// has since passed still reads as done rather than as a refusal.
		return &PostponeDeliveryRunResult{OriginalDate: original}, nil
	}

	// Restoring puts the run — and its orders — back on the scheduled day, so
	// that day has to still be ahead of us. Judged on the scheduled date rather
	// than the moved one, which is the opposite of the postpone guard and for
	// the opposite reason: postponing asks "has the run gone?", restoring asks
	// "is there still a day to put it back on?".
	//
	// Without this, the Restore button on a holiday that has been and gone
	// rewrites the promised date on orders delivered days ago, moving them onto
	// the closed day the shop postponed away from.
	if original.Before(dateOnly(now.In(original.Location()))) {
		return nil, ErrRestoreRunPassed
	}

	// The route was planned for the day the run currently goes out, and follows
	// it back. Same pre-flight as postpone, for the same reason.
	routeMove, err := s.planRouteMove(ctx, tx, original, moved, original)
	if err != nil {
		return nil, err
	}

	if err := s.shipping.DeleteDeliveryPostponement(ctx, tx, original); err != nil {
		return nil, err
	}

	// Back onto the scheduled day, again selected by run rather than by date.
	// Selecting on the date they were moved to would sweep up the orders of any
	// run that legitimately falls on that day.
	moveCount, err := s.shipping.RescheduleDeliveryRun(ctx, tx, original, original)
	if err != nil {
		return nil, err
	}

	routeMoved, routeDropped, err := s.applyRouteMove(ctx, tx, routeMove)
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
			"route_moved":   routeMoved,
			"route_dropped": string(routeDropped),
		},
	}); err != nil {
		return nil, fmt.Errorf("audit delivery run restored: %w", err)
	}

	return &PostponeDeliveryRunResult{
		OriginalDate: original,
		MovedTo:      moved,
		OrdersMoved:  moveCount,
		RouteMoved:   routeMoved,
		RouteDropped: routeDropped,
	}, nil
}

// ListDeliveryPostponements returns the recorded postponements for display.
func (s *CheckoutService) ListDeliveryPostponements(ctx context.Context, tx pgx.Tx) ([]domain.DeliveryPostponement, error) {
	return s.shipping.ListDeliveryPostponements(ctx, tx)
}

// routeMove is what a postponement is going to do to the run's planned delivery
// route, decided before anything is written.
//
// Split from doing it because a route that is already out with a driver has to
// refuse the whole postponement, and a refusal must leave the schedule exactly
// as it found it — deciding halfway through the writes would mean unpicking
// them.
type routeMove struct {
	// id is the route to act on. Zero means there is nothing to do: no route
	// store wired, no route planned for that day, or the run is not actually
	// changing days.
	id uuid.UUID
	// to is the day the route follows the run onto. Ignored when drop is set.
	to time.Time
	// drop, when set, says the route cannot follow and is discarded instead,
	// and why. A route is a plan over orders rather than a record of anything —
	// deleting it loses the stop order and nothing else (see migration 067) —
	// so dropping it and telling staff to re-plan beats blocking a holiday move
	// on it.
	drop RouteDropReason
}

// planRouteMove works out what happens to the live route for the run of runDate
// as it moves from one day to another. Returns the zero routeMove when there is
// nothing to do.
//
// A run that moves leaves its planned route behind on the old day otherwise:
// routes key on route_date, so the driver's sheet would sit on a day the van no
// longer goes out, and the orders it lists would have moved on without it.
//
// runDate is the run's own identity — orders.delivery_run_date, the scheduled
// day whatever day the van currently leaves — and is not interchangeable with
// from. Two runs may collapse onto one day, and then the route sitting on that
// day belongs to both of them; only the run that owns it outright may take it.
func (s *CheckoutService) planRouteMove(ctx context.Context, tx pgx.Tx, runDate, from, to time.Time) (routeMove, error) {
	if s.routes == nil || sameDate(from, to) {
		return routeMove{}, nil
	}

	// Re-anchored to UTC before any of it reaches the route store. route_date is
	// a bare calendar day, and the planner normalises the same way before it
	// writes (planRouteDate in web/admin_routes.go); the dates here are merchant
	// midnights instead, and pgx encodes a date by the UTC day the instant falls
	// in — so a shop east of UTC would look up the day before its own.
	runDay, fromDay, toDay := utcDay(runDate), utcDay(from), utcDay(to)

	route, err := s.routes.GetLiveRouteForDate(ctx, tx, fromDay)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Nothing planned for that run. The ordinary case — staff usually
			// mark a holiday well before anyone plans the route.
			return routeMove{}, nil
		}
		return routeMove{}, fmt.Errorf("look up route for run: %w", err)
	}
	if route.Status == domain.RouteStatusActive {
		// The driver has this one open. Moving the day under an active route
		// would change the sheet in someone's hand.
		return routeMove{}, ErrRunRouteActive
	}

	existing, err := s.routes.GetLiveRouteForDate(ctx, tx, toDay)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// The new day is clear. The route may follow, but only if it is this
		// run's alone — see below.
	case err != nil:
		return routeMove{}, fmt.Errorf("look up route for new run day: %w", err)
	case existing.Status == domain.RouteStatusActive:
		// That day's own route is already out. Two runs collapsing onto one day
		// is allowed, but not while a driver is working it.
		return routeMove{}, ErrRunRouteActive
	default:
		// That day already has a plan of its own, and one day gets one stop
		// list. Ours is the one that gives way.
		return routeMove{id: route.ID, drop: RouteDropTargetOccupied}, nil
	}

	// Whose route is it? If another run had already collapsed onto this day,
	// the route covers both, and re-dating it would take the staying run's
	// driver sheet along with ours. Neither day can keep it: the moving run's
	// stops are leaving, so what is left behind would list orders that are not
	// going out. It is a plan over orders, so it is dropped and re-planned.
	onlyRun, err := s.routes.RouteCoversOnlyRun(ctx, tx, route.ID, runDay)
	if err != nil {
		return routeMove{}, err
	}
	if !onlyRun {
		return routeMove{id: route.ID, drop: RouteDropSharedRun}, nil
	}

	return routeMove{id: route.ID, to: toDay}, nil
}

// applyRouteMove carries out what planRouteMove decided, reporting which of the
// two things happened — and, for a drop, why — so the flash can say it.
func (s *CheckoutService) applyRouteMove(ctx context.Context, tx pgx.Tx, m routeMove) (moved bool, dropped RouteDropReason, err error) {
	if m.id == uuid.Nil {
		return false, "", nil
	}
	if m.drop != "" {
		if err := s.routes.DeleteRoute(ctx, tx, m.id); err != nil {
			return false, "", err
		}
		return false, m.drop, nil
	}
	if _, err := s.routes.UpdateRouteDate(ctx, tx, m.id, m.to); err != nil {
		return false, "", fmt.Errorf("move route with run: %w", err)
	}
	return true, "", nil
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

// utcDay is the same calendar day anchored at UTC midnight, which is how a bare
// `date` column round-trips and how routes are stored. Use it on the way into a
// query that compares against one.
func utcDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// sameDate compares two times by the calendar day each names, ignoring clock
// and zone — a date read back from Postgres arrives in UTC, while one parsed
// from an admin form is in the merchant's zone.
func sameDate(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}
