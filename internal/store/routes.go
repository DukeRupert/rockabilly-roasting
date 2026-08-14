package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
)

// RouteStore provides database access for delivery routes and their stops.
type RouteStore struct {
	metrics QueryRecorder
}

// NewRouteStore creates a new RouteStore. Pass nil for metrics to disable
// query timing instrumentation.
func NewRouteStore(metrics QueryRecorder) *RouteStore {
	return &RouteStore{metrics: metrics}
}

const routeColumns = `id, route_date, status, origin_lat, origin_lng, origin_address,
	total_distance_m, total_duration_s, roundtrip, share_token,
	created_at, activated_at, completed_at`

func scanRoute(row pgx.Row) (*domain.DeliveryRoute, error) {
	var r domain.DeliveryRoute
	var status string
	if err := row.Scan(&r.ID, &r.RouteDate, &status, &r.OriginLat, &r.OriginLng, &r.OriginAddress,
		&r.TotalDistanceMeters, &r.TotalDurationSecs, &r.Roundtrip, &r.ShareToken,
		&r.CreatedAt, &r.ActivatedAt, &r.CompletedAt); err != nil {
		return nil, err
	}
	r.Status = domain.RouteStatus(status)
	return &r, nil
}

const routeStopColumns = `id, route_id, order_id, position, address, lat, lng,
	customer_name, channel, status, skip_reason, notes, delivered_at`

func scanRouteStop(row pgx.Row) (*domain.RouteStop, error) {
	var s domain.RouteStop
	var channel, status string
	if err := row.Scan(&s.ID, &s.RouteID, &s.OrderID, &s.Position, &s.Address, &s.Lat, &s.Lng,
		&s.CustomerName, &channel, &status, &s.SkipReason, &s.Notes, &s.DeliveredAt); err != nil {
		return nil, err
	}
	s.Channel = domain.OrderChannel(channel)
	s.Status = domain.RouteStopStatus(status)
	return &s, nil
}

// CreateRouteParams holds the fields needed to save a planned route.
type CreateRouteParams struct {
	RouteDate           time.Time
	OriginLat           float64
	OriginLng           float64
	OriginAddress       string
	TotalDistanceMeters int
	TotalDurationSecs   int
	Roundtrip           bool
}

// CreateRoute inserts a draft route. Draft routes carry no share token — a
// route that nobody has activated should have no URL that works.
func (s *RouteStore) CreateRoute(ctx context.Context, tx pgx.Tx, p CreateRouteParams) (_ *domain.DeliveryRoute, err error) {
	defer trackQuery(s.metrics, "routes.create", time.Now(), &err)
	row := tx.QueryRow(ctx,
		`INSERT INTO delivery_routes
		   (id, route_date, status, origin_lat, origin_lng, origin_address,
		    total_distance_m, total_duration_s, roundtrip)
		 VALUES ($1, $2, 'draft', $3, $4, $5, $6, $7, $8)
		 RETURNING `+routeColumns,
		uuid.New(), p.RouteDate, p.OriginLat, p.OriginLng, p.OriginAddress,
		p.TotalDistanceMeters, p.TotalDurationSecs, p.Roundtrip,
	)
	r, err := scanRoute(row)
	if err != nil {
		return nil, fmt.Errorf("create delivery route: %w", err)
	}
	return r, nil
}

// CreateRouteStopParams holds one stop to persist.
type CreateRouteStopParams struct {
	RouteID      uuid.UUID
	OrderID      uuid.UUID
	Position     int
	Address      string
	Lat          float64
	Lng          float64
	CustomerName string
	Channel      domain.OrderChannel
	Notes        string
}

// CreateStops inserts a route's stops in one round trip.
func (s *RouteStore) CreateStops(ctx context.Context, tx pgx.Tx, stops []CreateRouteStopParams) (err error) {
	defer trackQuery(s.metrics, "routes.create_stops", time.Now(), &err)
	if len(stops) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, st := range stops {
		batch.Queue(
			`INSERT INTO route_stops
			   (id, route_id, order_id, position, address, lat, lng, customer_name, channel, notes)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			uuid.New(), st.RouteID, st.OrderID, st.Position, st.Address, st.Lat, st.Lng,
			st.CustomerName, string(st.Channel), st.Notes,
		)
	}
	results := tx.SendBatch(ctx, batch)
	defer results.Close() //nolint:errcheck
	for range stops {
		if _, err := results.Exec(); err != nil {
			return fmt.Errorf("insert route stop: %w", err)
		}
	}
	return nil
}

// GetRouteByID returns a route by id. Staff-facing.
func (s *RouteStore) GetRouteByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (_ *domain.DeliveryRoute, err error) {
	defer trackQuery(s.metrics, "routes.get_by_id", time.Now(), &err)
	row := tx.QueryRow(ctx, `SELECT `+routeColumns+` FROM delivery_routes WHERE id = $1`, id)
	return scanRoute(row)
}

// GetRouteByShareToken returns the route a driver's link points at.
//
// Completed routes are deliberately excluded: the token dying at completion is
// the whole authentication model for the driver page, so it is enforced in the
// query rather than left to a caller to remember.
func (s *RouteStore) GetRouteByShareToken(ctx context.Context, tx pgx.Tx, token string) (_ *domain.DeliveryRoute, err error) {
	defer trackQuery(s.metrics, "routes.get_by_share_token", time.Now(), &err)
	row := tx.QueryRow(ctx,
		`SELECT `+routeColumns+`
		   FROM delivery_routes
		  WHERE share_token = $1 AND status = 'active'`,
		token)
	return scanRoute(row)
}

// GetLiveRouteForDate returns the draft or active route for a delivery date,
// if one exists. Backed by the partial unique index, so there is at most one.
func (s *RouteStore) GetLiveRouteForDate(ctx context.Context, tx pgx.Tx, date time.Time) (_ *domain.DeliveryRoute, err error) {
	defer trackQuery(s.metrics, "routes.get_live_for_date", time.Now(), &err)
	row := tx.QueryRow(ctx,
		`SELECT `+routeColumns+`
		   FROM delivery_routes
		  WHERE route_date = $1 AND status <> 'completed'`,
		date)
	return scanRoute(row)
}

// ListRoutes returns routes newest first.
func (s *RouteStore) ListRoutes(ctx context.Context, tx pgx.Tx, limit int) (_ []domain.DeliveryRoute, err error) {
	defer trackQuery(s.metrics, "routes.list", time.Now(), &err)
	if limit <= 0 {
		limit = 50
	}
	rows, err := tx.Query(ctx,
		`SELECT `+routeColumns+` FROM delivery_routes ORDER BY route_date DESC, created_at DESC LIMIT $1`,
		limit)
	if err != nil {
		return nil, fmt.Errorf("list delivery routes: %w", err)
	}
	defer rows.Close()

	var out []domain.DeliveryRoute
	for rows.Next() {
		r, scanErr := scanRoute(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan delivery route: %w", scanErr)
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// ListStops returns a route's stops in visiting order.
func (s *RouteStore) ListStops(ctx context.Context, tx pgx.Tx, routeID uuid.UUID) (_ []domain.RouteStop, err error) {
	defer trackQuery(s.metrics, "routes.list_stops", time.Now(), &err)
	rows, err := tx.Query(ctx,
		`SELECT `+routeStopColumns+` FROM route_stops WHERE route_id = $1 ORDER BY position ASC`,
		routeID)
	if err != nil {
		return nil, fmt.Errorf("list route stops: %w", err)
	}
	defer rows.Close()

	var out []domain.RouteStop
	for rows.Next() {
		st, scanErr := scanRouteStop(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan route stop: %w", scanErr)
		}
		out = append(out, *st)
	}
	return out, rows.Err()
}

// GetStop returns one stop by id.
func (s *RouteStore) GetStop(ctx context.Context, tx pgx.Tx, id uuid.UUID) (_ *domain.RouteStop, err error) {
	defer trackQuery(s.metrics, "routes.get_stop", time.Now(), &err)
	row := tx.QueryRow(ctx, `SELECT `+routeStopColumns+` FROM route_stops WHERE id = $1`, id)
	return scanRouteStop(row)
}

// ActivateRoute marks a route active and attaches its share token.
// Guarded to draft so re-activating cannot mint a second token and silently
// invalidate the link a driver already has open.
func (s *RouteStore) ActivateRoute(ctx context.Context, tx pgx.Tx, id uuid.UUID, shareToken string) (_ *domain.DeliveryRoute, err error) {
	defer trackQuery(s.metrics, "routes.activate", time.Now(), &err)
	row := tx.QueryRow(ctx,
		`UPDATE delivery_routes
		    SET status = 'active', share_token = $2, activated_at = now()
		  WHERE id = $1 AND status = 'draft'
		 RETURNING `+routeColumns,
		id, shareToken)
	return scanRoute(row)
}

// CompleteRoute closes a route, which also retires its share token — the
// driver page checks for status='active'.
func (s *RouteStore) CompleteRoute(ctx context.Context, tx pgx.Tx, id uuid.UUID) (_ *domain.DeliveryRoute, err error) {
	defer trackQuery(s.metrics, "routes.complete", time.Now(), &err)
	row := tx.QueryRow(ctx,
		`UPDATE delivery_routes
		    SET status = 'completed', completed_at = now()
		  WHERE id = $1 AND status <> 'completed'
		 RETURNING `+routeColumns,
		id)
	return scanRoute(row)
}

// DeleteRoute removes a route and its stops (ON DELETE CASCADE). Used when
// staff re-plan: the previous draft is replaced rather than accumulated.
func (s *RouteStore) DeleteRoute(ctx context.Context, tx pgx.Tx, id uuid.UUID) (err error) {
	defer trackQuery(s.metrics, "routes.delete", time.Now(), &err)
	if _, err := tx.Exec(ctx, `DELETE FROM delivery_routes WHERE id = $1`, id); err != nil {
		return fmt.Errorf("delete delivery route: %w", err)
	}
	return nil
}

// MarkStopDelivered records a delivery and stamps the time.
func (s *RouteStore) MarkStopDelivered(ctx context.Context, tx pgx.Tx, id uuid.UUID) (_ *domain.RouteStop, err error) {
	defer trackQuery(s.metrics, "routes.mark_stop_delivered", time.Now(), &err)
	row := tx.QueryRow(ctx,
		`UPDATE route_stops
		    SET status = 'delivered', delivered_at = now(), skip_reason = ''
		  WHERE id = $1
		 RETURNING `+routeStopColumns,
		id)
	return scanRouteStop(row)
}

// MarkStopSkipped records that the driver passed this stop, with a reason.
// Deliberately does not touch delivered_at — a skipped stop was never
// delivered, and the order stays in the queue for the next run.
func (s *RouteStore) MarkStopSkipped(ctx context.Context, tx pgx.Tx, id uuid.UUID, reason string) (_ *domain.RouteStop, err error) {
	defer trackQuery(s.metrics, "routes.mark_stop_skipped", time.Now(), &err)
	row := tx.QueryRow(ctx,
		`UPDATE route_stops
		    SET status = 'skipped', skip_reason = $2, delivered_at = NULL
		  WHERE id = $1
		 RETURNING `+routeStopColumns,
		id, reason)
	return scanRouteStop(row)
}
