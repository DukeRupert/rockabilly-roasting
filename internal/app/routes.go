package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/routing"
	"github.com/dukerupert/hiri/internal/store"
)

// routeQueueStatuses is the fulfillment states that belong on a delivery run:
// everything pre-handoff that still wants staff attention. It mirrors the
// fulfillment queue's "needs action" bucket, which is what the Load list shows
// — the route and the load sheet must describe the same van or the driver ends
// up with coffee for a stop that isn't on the route.
var routeQueueStatuses = []domain.FulfillmentStatus{
	domain.FulfillmentStatusUnfulfilled,
	domain.FulfillmentStatusPartiallyFulfilled,
	domain.FulfillmentStatusFulfilled,
}

// maxRouteStops is the deployed router's limit minus the origin. See
// --max-table-size in ops/osrm/docker-compose.osrm.yml.
const maxRouteStops = 99

// RouteService plans delivery routes: it takes the local-delivery orders
// waiting to go out, resolves their addresses to coordinates, and asks OSRM for
// the order to drive them in.
//
// Channel-unscoped by design. The Load list is split into retail and wholesale
// tabs because staff pack those separately, but one van makes one run — a cafe
// and a house on the same street should be adjacent stops, not two routes that
// cross each other.
//
// Like GeocodingService, every method here takes *pgxpool.Pool rather than a
// pgx.Tx: planning calls out to the geocoder and to OSRM, and neither may
// happen inside a transaction.
type RouteService struct {
	orders    *store.OrderStore
	customers *store.CustomerStore
	shipping  *store.ShippingStore
	geocoding *GeocodingService
	router    *routing.Client

	// Persistence is optional at construction: planning is read-only, and the
	// admin flow attaches these with WithPersistence at wiring time.
	routes   *store.RouteStore
	audit    *audit.AuditWriter
	orderSvc *OrderService
}

// NewRouteService creates a RouteService.
func NewRouteService(
	orders *store.OrderStore,
	customers *store.CustomerStore,
	shipping *store.ShippingStore,
	geocoding *GeocodingService,
	router *routing.Client,
) *RouteService {
	return &RouteService{
		orders:    orders,
		customers: customers,
		shipping:  shipping,
		geocoding: geocoding,
		router:    router,
	}
}

// PlanRouteOptions narrows what goes on the route.
type PlanRouteOptions struct {
	// OrderIDs restricts the route to an explicit set of orders — the load
	// list's checkbox selection. Empty means the whole delivery queue.
	OrderIDs []uuid.UUID
	// Roundtrip returns the driver to the roastery at the end of the run.
	Roundtrip bool
}

// PlannedStop is one delivery on an optimized route.
type PlannedStop struct {
	// Position is 1-based in visiting order. The roastery is not a stop.
	Position     int
	OrderID      uuid.UUID
	OrderNumber  string
	Channel      domain.OrderChannel
	CustomerName string
	// Address is the formatted single-line address, as geocoded.
	Address string
	Lat     float64
	Lng     float64
	// Confidence is how precisely the geocoder placed this address. Anything
	// that fails Precise() should be flagged to staff before a driver is sent.
	Confidence domain.GeocodeConfidence
	// Notes carries the order's delivery instructions, if any.
	Notes string
}

// UnroutableStop is an order that belongs on the run but could not be placed on
// the map. These are surfaced, never dropped: a stop that silently vanishes
// from the route is a missed delivery discovered by the customer.
type UnroutableStop struct {
	OrderID     uuid.UUID
	OrderNumber string
	Address     string
	Reason      string
}

// RoutePlan is the result of planning: an ordered list of stops plus whatever
// could not be placed.
type RoutePlan struct {
	OriginAddress string
	OriginLat     float64
	OriginLng     float64
	Roundtrip     bool

	Stops      []PlannedStop
	Unroutable []UnroutableStop

	TotalDurationSeconds float64
	TotalDistanceMeters  float64

	// GeocodeCacheHits and GeocodeLookups report where the coordinates came
	// from. Useful in the admin view as a cost signal — a plan that fires
	// dozens of billable lookups means the cache needs warming.
	GeocodeCacheHits int
	GeocodeLookups   int
}

// LowConfidenceStops returns stops whose coordinates the geocoder could not pin
// precisely — the ones worth eyeballing before the van leaves.
func (p RoutePlan) LowConfidenceStops() []PlannedStop {
	var out []PlannedStop
	for _, s := range p.Stops {
		if !s.Confidence.Precise() {
			out = append(out, s)
		}
	}
	return out
}

// routeCandidate is an order plus the address details planning needs, gathered
// in the read phase so the transaction can close before any network call.
type routeCandidate struct {
	orderID      uuid.UUID
	orderNumber  string
	channel      domain.OrderChannel
	customerName string
	address      string
	notes        string
}

// PlanRoute builds an optimized route over the local-delivery queue.
//
// Three phases, in this order and for this reason: read the orders and
// addresses in one transaction and close it; geocode (external HTTP, cached);
// ask OSRM for the stop order (external HTTP). Nothing is written — planning is
// a read-only operation, and persistence arrives with the admin flow.
//
// Must not be called inside a transaction.
func (s *RouteService) PlanRoute(ctx context.Context, pool *pgxpool.Pool, opts PlanRouteOptions) (*RoutePlan, error) {
	// --- Phase 1: read ---
	var originAddress string
	var candidates []routeCandidate

	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		cfg, err := s.shipping.GetConfig(ctx, tx)
		if err != nil {
			return fmt.Errorf("load shipping config: %w", err)
		}
		originAddress = formatOriginAddress(cfg)
		if originAddress == "" {
			return ErrOriginNotConfigured
		}

		filter := store.OrderFilter{
			ShippingMethod:           ptr(domain.ShippingMethodLocalDelivery),
			FulfillmentStatuses:      routeQueueStatuses,
			ExcludeUnconfirmed:       true,
			ExcludeCancelledRefunded: true,
			OrderIDs:                 opts.OrderIDs,
			Limit:                    maxRouteStops + 1, // +1 so an over-cap queue is detectable
		}
		orders, err := s.orders.ListOrders(ctx, tx, filter)
		if err != nil {
			return fmt.Errorf("list delivery orders: %w", err)
		}

		candidates = make([]routeCandidate, 0, len(orders))
		for _, o := range orders {
			c := routeCandidate{
				orderID:     o.ID,
				orderNumber: o.Number,
				channel:     o.Channel,
			}
			if o.Notes != nil {
				c.notes = strings.TrimSpace(*o.Notes)
			}
			// scoping: staff-only route planning; there is no customer in this
			// request to scope to, and the order set is already narrowed to the
			// delivery queue.
			addr, addrErr := s.customers.GetAddressByIDAsStaff(ctx, tx, o.ShippingAddressID)
			if addrErr != nil {
				if errors.Is(addrErr, pgx.ErrNoRows) {
					// No address at all — record it as unroutable rather than
					// failing the whole plan.
					c.address = ""
					candidates = append(candidates, c)
					continue
				}
				return fmt.Errorf("load address for order %s: %w", o.Number, addrErr)
			}
			c.address = domain.FormatAddressForGeocoding(*addr)
			c.customerName = recipientName(addr)
			candidates = append(candidates, c)
		}
		return nil
	}); err != nil {
		return nil, err
	}

	if len(candidates) == 0 {
		return nil, ErrNoDeliveryStops
	}
	if len(candidates) > maxRouteStops {
		return nil, fmt.Errorf("%w: %d orders in the delivery queue, limit %d per route",
			routing.ErrTooManyStops, len(candidates), maxRouteStops)
	}

	// --- Phase 2: geocode (no transaction open) ---
	addresses := make([]string, 0, len(candidates)+1)
	addresses = append(addresses, originAddress)
	for _, c := range candidates {
		if c.address != "" {
			addresses = append(addresses, c.address)
		}
	}
	resolved, err := s.geocoding.ResolveMany(ctx, pool, addresses)
	if err != nil {
		return nil, fmt.Errorf("geocode route addresses: %w", err)
	}

	origin, ok := resolved.Resolved[originAddress]
	if !ok {
		// Without an origin there is no route to plan — this is the roastery
		// address from shipping config, so it is a settings problem, not a
		// customer data problem.
		reason := resolved.Failed[originAddress]
		return nil, fmt.Errorf("%w: %s: %v", ErrOriginNotGeocodable, originAddress, reason)
	}

	plan := &RoutePlan{
		OriginAddress:    originAddress,
		OriginLat:        origin.Lat,
		OriginLng:        origin.Lng,
		Roundtrip:        opts.Roundtrip,
		GeocodeCacheHits: resolved.CacheHits,
		GeocodeLookups:   resolved.Lookups,
	}

	// Origin is always coordinate 0; source=first pins it as the start.
	coords := []routing.Coordinate{{Lat: origin.Lat, Lng: origin.Lng}}
	routable := make([]routeCandidate, 0, len(candidates))
	for _, c := range candidates {
		if c.address == "" {
			plan.Unroutable = append(plan.Unroutable, UnroutableStop{
				OrderID: c.orderID, OrderNumber: c.orderNumber,
				Reason: "order has no shipping address on file",
			})
			continue
		}
		g, found := resolved.Resolved[c.address]
		if !found {
			reason := "address could not be geocoded"
			if e := resolved.Failed[c.address]; e != nil {
				reason = e.Error()
			}
			plan.Unroutable = append(plan.Unroutable, UnroutableStop{
				OrderID: c.orderID, OrderNumber: c.orderNumber,
				Address: c.address, Reason: reason,
			})
			continue
		}
		coords = append(coords, routing.Coordinate{Lat: g.Lat, Lng: g.Lng})
		routable = append(routable, c)
	}

	if len(routable) == 0 {
		// Every stop failed to geocode. Returning the unroutable list inside an
		// error would hide it, so this is the one case where the caller gets a
		// sentinel and should show plan-less guidance.
		return nil, ErrNoDeliveryStops
	}

	// --- Phase 3: order the stops (no transaction open) ---
	trip, err := s.router.Trip(ctx, coords, routing.TripOptions{Roundtrip: opts.Roundtrip})
	if err != nil {
		return nil, fmt.Errorf("plan trip: %w", err)
	}

	// trip.Order lists coordinate indices in visiting order. Index 0 is the
	// origin and is guaranteed first by source=first; the rest map back onto
	// routable by (coordinate index - 1).
	plan.Stops = make([]PlannedStop, 0, len(routable))
	position := 0
	for _, coordIdx := range trip.Order {
		if coordIdx == 0 {
			continue // the roastery is not a delivery
		}
		c := routable[coordIdx-1]
		g := resolved.Resolved[c.address]
		position++
		plan.Stops = append(plan.Stops, PlannedStop{
			Position:     position,
			OrderID:      c.orderID,
			OrderNumber:  c.orderNumber,
			Channel:      c.channel,
			CustomerName: c.customerName,
			Address:      c.address,
			Lat:          g.Lat,
			Lng:          g.Lng,
			Confidence:   g.Confidence,
			Notes:        c.notes,
		})
	}
	plan.TotalDurationSeconds = trip.DurationSeconds
	plan.TotalDistanceMeters = trip.DistanceMeters

	return plan, nil
}

// formatOriginAddress renders the roastery address from shipping config — the
// same address EasyPost uses as the ship-from, so there is no separate setting
// to keep in sync.
func formatOriginAddress(cfg *domain.ShippingConfig) string {
	if cfg == nil {
		return ""
	}
	parts := make([]string, 0, 4)
	if s := strings.TrimSpace(cfg.OriginStreet1); s != "" {
		parts = append(parts, s)
	}
	if s := strings.TrimSpace(cfg.OriginCity); s != "" {
		parts = append(parts, s)
	}
	stateZip := strings.TrimSpace(strings.TrimSpace(cfg.OriginState) + " " + strings.TrimSpace(cfg.OriginZip))
	if stateZip != "" {
		parts = append(parts, stateZip)
	}
	// A street line alone is not a geocodable address; require at least a
	// street and one locality component before claiming an origin exists.
	if len(parts) < 2 {
		return ""
	}
	return strings.Join(parts, ", ")
}

// recipientName is who the driver is handing coffee to. The address name wins
// over the account holder: wholesale orders are placed by a buyer but received
// at a cafe, and the driver needs the name on the door.
func recipientName(a *domain.Address) string {
	if a == nil {
		return ""
	}
	if a.Company != nil {
		if c := strings.TrimSpace(*a.Company); c != "" {
			return c
		}
	}
	name := strings.TrimSpace(strings.TrimSpace(a.FirstName) + " " + strings.TrimSpace(a.LastName))
	return name
}

func ptr[T any](v T) *T { return &v }
