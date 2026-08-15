package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/store"
)

// WithPersistence attaches the route store and audit writer. Wiring-time only;
// PlanRoute works without them, since planning is read-only.
func (s *RouteService) WithPersistence(routes *store.RouteStore, auditWriter *audit.AuditWriter) *RouteService {
	s.routes = routes
	s.audit = auditWriter
	return s
}

// SavedRoute is a persisted route with its stops.
type SavedRoute struct {
	Route domain.DeliveryRoute
	Stops []domain.RouteStop
}

// Progress summarises how far along the run is.
func (r SavedRoute) Progress() domain.RouteProgress {
	return domain.Progress(r.Stops)
}

// DriverURL is where the driver's phone goes for this route. Empty until the
// route is activated and has a token.
func (r SavedRoute) DriverURL(baseURL string) string {
	if r.Route.ShareToken == nil || *r.Route.ShareToken == "" {
		return ""
	}
	return fmt.Sprintf("%s/routes/%s", baseURL, *r.Route.ShareToken)
}

// PlanAndSaveRoute plans a route and stores it as a draft for the given
// delivery date.
//
// Re-planning the same date replaces the existing draft rather than adding a
// second one — staff adjust the selection and re-plan freely, and the partial
// unique index means there is only ever one live route per day to hand a
// driver. An already-active route is left alone: a driver may be working it,
// and silently swapping their stop list mid-run is the one thing this must
// never do.
//
// Must not be called inside a transaction — it plans, which calls out.
func (s *RouteService) PlanAndSaveRoute(
	ctx context.Context,
	pool *pgxpool.Pool,
	routeDate time.Time,
	opts PlanRouteOptions,
	actor Actor,
) (*SavedRoute, *RoutePlan, error) {
	if s.routes == nil {
		return nil, nil, errors.New("route persistence is not configured")
	}

	// Refuse before doing any paid geocoding if there's an active route to
	// protect.
	var existing *domain.DeliveryRoute
	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		r, err := s.routes.GetLiveRouteForDate(ctx, tx, routeDate)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		existing = r
		return nil
	}); err != nil {
		return nil, nil, fmt.Errorf("check existing route: %w", err)
	}
	if existing != nil && existing.Status == domain.RouteStatusActive {
		s.recordPlanOutcome("refused_active")
		return nil, nil, ErrRouteAlreadyActive
	}

	plan, err := s.PlanRoute(ctx, pool, opts)
	if err != nil {
		s.recordPlanOutcome("failed")
		return nil, nil, err
	}

	var saved *SavedRoute
	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		// Replace the previous draft. Deleting cascades its stops.
		if existing != nil {
			if err := s.routes.DeleteRoute(ctx, tx, existing.ID); err != nil {
				return err
			}
		}

		route, err := s.routes.CreateRoute(ctx, tx, store.CreateRouteParams{
			RouteDate:           routeDate,
			OriginLat:           plan.OriginLat,
			OriginLng:           plan.OriginLng,
			OriginAddress:       plan.OriginAddress,
			TotalDistanceMeters: int(plan.TotalDistanceMeters),
			TotalDurationSecs:   int(plan.TotalDurationSeconds),
			Roundtrip:           plan.Roundtrip,
		})
		if err != nil {
			return err
		}

		stops := make([]store.CreateRouteStopParams, 0, len(plan.Stops))
		for _, st := range plan.Stops {
			stops = append(stops, store.CreateRouteStopParams{
				RouteID:      route.ID,
				OrderID:      st.OrderID,
				Position:     st.Position,
				Address:      st.Address,
				Lat:          st.Lat,
				Lng:          st.Lng,
				CustomerName: st.CustomerName,
				Channel:      st.Channel,
				Notes:        st.Notes,
			})
		}
		if err := s.routes.CreateStops(ctx, tx, stops); err != nil {
			return err
		}

		if err := s.recordRouteAudit(ctx, tx, actor, audit.AuditRoutePlanned, route, map[string]any{
			"stops":            len(plan.Stops),
			"unroutable":       len(plan.Unroutable),
			"replanned":        existing != nil,
			"geocode_lookups":  plan.GeocodeLookups,
			"duration_seconds": plan.TotalDurationSeconds,
		}); err != nil {
			return err
		}

		persisted, err := s.routes.ListStops(ctx, tx, route.ID)
		if err != nil {
			return err
		}
		saved = &SavedRoute{Route: *route, Stops: persisted}
		return nil
	}); err != nil {
		s.recordPlanOutcome("failed")
		return nil, nil, fmt.Errorf("save route: %w", err)
	}

	// After commit, per the metrics rule: a counter that moved for a rolled
	// back transaction is a lie that never corrects itself.
	if s.metrics != nil {
		s.metrics.RoutesPlanned.WithLabelValues("planned").Inc()
		s.metrics.RouteStops.Observe(float64(len(saved.Stops)))
		s.metrics.RouteDuration.Observe(plan.TotalDurationSeconds)
	}

	return saved, plan, nil
}

// GetRoute loads a route and its stops by id. Staff-facing.
func (s *RouteService) GetRoute(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*SavedRoute, error) {
	route, err := s.routes.GetRouteByID(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrRouteNotFound
		}
		return nil, fmt.Errorf("get route: %w", err)
	}
	stops, err := s.routes.ListStops(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	return &SavedRoute{Route: *route, Stops: stops}, nil
}

// GetRouteByShareToken loads the route behind a driver link. Returns
// ErrRouteNotFound for an unknown, draft, or completed route — the driver page
// must not distinguish between them, since a token that stops working is the
// intended end state and a more specific message only helps someone guessing.
func (s *RouteService) GetRouteByShareToken(ctx context.Context, tx pgx.Tx, token string) (*SavedRoute, error) {
	if token == "" {
		return nil, ErrRouteNotFound
	}
	route, err := s.routes.GetRouteByShareToken(ctx, tx, token)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrRouteNotFound
		}
		return nil, fmt.Errorf("get route by token: %w", err)
	}
	stops, err := s.routes.ListStops(ctx, tx, route.ID)
	if err != nil {
		return nil, err
	}
	return &SavedRoute{Route: *route, Stops: stops}, nil
}

// ListRoutes returns recent routes, newest first.
func (s *RouteService) ListRoutes(ctx context.Context, tx pgx.Tx, limit int) ([]domain.DeliveryRoute, error) {
	return s.routes.ListRoutes(ctx, tx, limit)
}

// ActivateRoute mints the share token and opens the route to the driver.
func (s *RouteService) ActivateRoute(ctx context.Context, tx pgx.Tx, id uuid.UUID, actor Actor) (*SavedRoute, error) {
	stops, err := s.routes.ListStops(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if len(stops) == 0 {
		// A route with no stops is a QR code pointing at an empty page.
		return nil, ErrRouteEmpty
	}

	token, err := newShareToken()
	if err != nil {
		return nil, err
	}

	route, err := s.routes.ActivateRoute(ctx, tx, id, token)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Guarded to draft in SQL: either the route is gone or it has
			// already been activated, and re-minting a token would invalidate
			// the link the driver already has.
			return nil, ErrRouteNotActivatable
		}
		return nil, fmt.Errorf("activate route: %w", err)
	}

	if err := s.recordRouteAudit(ctx, tx, actor, audit.AuditRouteActivated, route, map[string]any{
		"stops": len(stops),
	}); err != nil {
		return nil, err
	}
	return &SavedRoute{Route: *route, Stops: stops}, nil
}

// CompleteRoute closes the run and retires the driver's token.
func (s *RouteService) CompleteRoute(ctx context.Context, tx pgx.Tx, id uuid.UUID, actor Actor) (*SavedRoute, error) {
	route, err := s.routes.CompleteRoute(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrRouteNotFound
		}
		return nil, fmt.Errorf("complete route: %w", err)
	}
	stops, err := s.routes.ListStops(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	progress := domain.Progress(stops)
	if err := s.recordRouteAudit(ctx, tx, actor, audit.AuditRouteCompleted, route, map[string]any{
		"delivered": progress.Delivered,
		"skipped":   progress.Skipped,
		"remaining": progress.Remaining(),
	}); err != nil {
		return nil, err
	}
	return &SavedRoute{Route: *route, Stops: stops}, nil
}

// recordRouteAudit writes one route audit entry in the caller's transaction.
func (s *RouteService) recordRouteAudit(
	ctx context.Context,
	tx pgx.Tx,
	actor Actor,
	action string,
	route *domain.DeliveryRoute,
	metadata map[string]any,
) error {
	if s.audit == nil {
		return nil
	}
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadata["route_date"] = route.RouteDate.Format("2006-01-02")
	if err := s.audit.Record(ctx, tx, audit.AuditEntry{
		ActorType:    actor.Type,
		ActorID:      actor.ID,
		ActorName:    actor.Name,
		Action:       action,
		ResourceType: "delivery_route",
		ResourceID:   route.ID,
		After:        route,
		Metadata:     metadata,
	}); err != nil {
		return fmt.Errorf("audit %s: %w", action, err)
	}
	return nil
}

// newShareToken mints the driver page's credential: 32 random bytes, hex
// encoded.
//
// Stored raw rather than hashed, unlike the emailed order-action links. The
// difference is what the token is for — the driver page needs to look a route
// up *by* the token on every request, and it is a short-lived credential for a
// stop list that dies when the route completes, not a standing account
// credential.
func newShareToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate route share token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// recordPlanOutcome counts a planning attempt.
func (s *RouteService) recordPlanOutcome(outcome string) {
	if s.metrics == nil {
		return
	}
	s.metrics.RoutesPlanned.WithLabelValues(outcome).Inc()
}
