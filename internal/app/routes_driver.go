package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
)

// WithOrderService attaches the order service the driver page needs to close
// out deliveries. Wiring-time only.
//
// Route stops deliberately do not own delivery state: marking a stop delivered
// drives the order's own fulfillment transition, so the fulfillment queue and
// the driver's phone can never disagree about what has been delivered.
func (s *RouteService) WithOrderService(orders *OrderService) *RouteService {
	s.orderSvc = orders
	return s
}

// maxSkipReasonLen bounds what the driver can type. Long enough for "wrong
// address, called the shop", short enough that the field can't be used as
// storage.
const maxSkipReasonLen = 500

// MarkStopDelivered records a delivery from the driver page and closes out the
// underlying order.
//
// Everything happens in the caller's transaction: the stop, the order, the
// audit entries, and any auto-completion of the route. A driver tapping
// "Delivered" on a phone with one bar should never leave the stop marked and
// the order not, or vice versa.
//
// Idempotent. Phones double-fire taps, and a second tap must be a no-op rather
// than an error page in a van.
func (s *RouteService) MarkStopDelivered(
	ctx context.Context,
	tx pgx.Tx,
	routeID, stopID uuid.UUID,
	actor Actor,
) (*SavedRoute, error) {
	stop, err := s.loadStopForRoute(ctx, tx, routeID, stopID)
	if err != nil {
		return nil, err
	}

	if stop.Status != domain.RouteStopDelivered {
		// The order transition comes first: if it fails, the stop must not
		// claim a delivery that the fulfillment queue disagrees with.
		if s.orderSvc != nil {
			_, orderErr := s.orderSvc.MarkLocallyDelivered(ctx, tx, stop.OrderID, actor)
			// Already delivered (a retry, or staff closed it from admin first)
			// is success, not failure.
			if orderErr != nil && !errors.Is(orderErr, ErrInvalidOrderStatus) {
				return nil, orderErr
			}
		}
		if _, err := s.routes.MarkStopDelivered(ctx, tx, stopID); err != nil {
			return nil, fmt.Errorf("mark stop delivered: %w", err)
		}
	}

	return s.finishStopUpdate(ctx, tx, routeID, actor)
}

// MarkStopSkipped records that the driver passed a stop today, with a reason.
//
// Route-level only, by design: the order is untouched, stays in the delivery
// queue, and rolls onto the next run's route automatically — no re-queue step
// to forget, because the route is derived from the queue rather than the other
// way round. The reason exists so staff can fix the underlying problem (a bad
// address, a wrong line item) before the next van goes out.
func (s *RouteService) MarkStopSkipped(
	ctx context.Context,
	tx pgx.Tx,
	routeID, stopID uuid.UUID,
	reason string,
	actor Actor,
) (*SavedRoute, error) {
	stop, err := s.loadStopForRoute(ctx, tx, routeID, stopID)
	if err != nil {
		return nil, err
	}
	if stop.Status == domain.RouteStopDelivered {
		// Undoing a delivery would mean un-completing the order behind it.
		// Out of scope for a phone in a van; staff can fix it in admin.
		return nil, ErrStopAlreadyDelivered
	}

	reason = strings.TrimSpace(reason)
	if len(reason) > maxSkipReasonLen {
		reason = reason[:maxSkipReasonLen]
	}

	updated, err := s.routes.MarkStopSkipped(ctx, tx, stopID, reason)
	if err != nil {
		return nil, fmt.Errorf("mark stop skipped: %w", err)
	}

	if s.audit != nil {
		// Audited against the order, not the route: "why didn't this get
		// delivered?" is a question asked of an order, and the answer should
		// be on its timeline rather than buried in a route that gets replaced
		// every delivery day.
		if err := s.audit.Record(ctx, tx, audit.AuditEntry{
			ActorType:    actor.Type,
			ActorID:      actor.ID,
			ActorName:    actor.Name,
			Action:       audit.AuditRouteStopSkipped,
			ResourceType: "order",
			ResourceID:   updated.OrderID,
			After:        updated,
			Metadata: map[string]any{
				"route_id":       routeID.String(),
				"stop_id":        stopID.String(),
				"reason":         reason,
				"stays_in_queue": true,
			},
		}); err != nil {
			return nil, fmt.Errorf("audit stop skip: %w", err)
		}
	}

	return s.finishStopUpdate(ctx, tx, routeID, actor)
}

// loadStopForRoute fetches a stop and proves it belongs to the route the
// caller's token authenticates.
//
// The ownership check is the whole authorization story for the driver page: the
// share token grants access to one route, so a stop id from another route must
// look exactly like a stop that does not exist.
func (s *RouteService) loadStopForRoute(ctx context.Context, tx pgx.Tx, routeID, stopID uuid.UUID) (*domain.RouteStop, error) {
	stop, err := s.routes.GetStop(ctx, tx, stopID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrRouteStopNotFound
		}
		return nil, fmt.Errorf("get route stop: %w", err)
	}
	if stop.RouteID != routeID {
		return nil, ErrRouteStopNotFound
	}
	return stop, nil
}

// finishStopUpdate reloads the route and auto-completes it once every stop is
// resolved.
//
// "Resolved" counts skips: a skip is a decision the driver made, not an
// omission, so a run where the last stop was skipped is still a finished run.
// Auto-completing retires the share token, which is what ends the driver's
// session — there is no logout on a page with no login.
func (s *RouteService) finishStopUpdate(ctx context.Context, tx pgx.Tx, routeID uuid.UUID, actor Actor) (*SavedRoute, error) {
	route, err := s.routes.GetRouteByID(ctx, tx, routeID)
	if err != nil {
		return nil, fmt.Errorf("reload route: %w", err)
	}
	stops, err := s.routes.ListStops(ctx, tx, routeID)
	if err != nil {
		return nil, err
	}

	if route.Status == domain.RouteStatusActive && domain.Progress(stops).Complete() {
		completed, err := s.CompleteRoute(ctx, tx, routeID, actor)
		if err != nil {
			return nil, err
		}
		return completed, nil
	}

	return &SavedRoute{Route: *route, Stops: stops}, nil
}
