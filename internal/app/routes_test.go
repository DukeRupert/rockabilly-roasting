package app_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/geocode"
	"github.com/dukerupert/hiri/internal/platform/routing"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// Route planning commits its own transactions — it has to, since geocoding and
// OSRM are HTTP calls that must not happen inside one. That rules out the
// usual rollback-per-test isolation: fixtures have to be committed to be
// visible, and cleaned up explicitly afterwards.
type routeFixture struct {
	customerID uuid.UUID
	addressIDs []uuid.UUID
	orderIDs   []uuid.UUID
	orders     map[string]uuid.UUID // order number -> id
}

// seedDeliveryOrders commits one local-delivery order per address line and
// registers cleanup that removes exactly what it created. It also points the
// shipping config at a real Kennewick origin, restoring the previous values on
// cleanup so neighbouring tests see the config they expect.
func seedDeliveryOrders(t *testing.T, addressLines []string) *routeFixture {
	t.Helper()
	ctx := context.Background()
	shippingStore := store.NewShippingStore()

	// Snapshot and override the origin.
	var original domain.ShippingConfig
	require.NoError(t, store.Tx(ctx, testPool, func(tx pgx.Tx) error {
		cfg, err := shippingStore.GetConfig(ctx, tx)
		if err != nil {
			return err
		}
		original = *cfg
		updated := *cfg
		updated.OriginStreet1 = "1234 W 4th Ave"
		updated.OriginCity = "Kennewick"
		updated.OriginState = "WA"
		updated.OriginZip = "99336"
		return shippingStore.UpdateConfig(ctx, tx, updated)
	}))

	f := &routeFixture{orders: make(map[string]uuid.UUID, len(addressLines))}

	require.NoError(t, store.Tx(ctx, testPool, func(tx pgx.Tx) error {
		cust := testutil.CreateCustomer(t, tx)
		f.customerID = cust.ID
		for _, line := range addressLines {
			addr := testutil.CreateAddress(t, tx, cust.ID, testutil.WithAddressLine1(line))
			f.addressIDs = append(f.addressIDs, addr.ID)
			o := testutil.CreateOrder(t, tx, cust.ID, addr.ID, addr.ID,
				testutil.WithShippingMethod(domain.ShippingMethodLocalDelivery),
				testutil.WithFulfillmentStatus(domain.FulfillmentStatusUnfulfilled),
				testutil.WithOrderStatus(domain.OrderStatusConfirmed),
				testutil.WithPaymentStatus(domain.PaymentStatusCaptured),
			)
			f.orderIDs = append(f.orderIDs, o.ID)
			f.orders[o.Number] = o.ID
		}
		return nil
	}))

	t.Cleanup(func() {
		_ = store.Tx(ctx, testPool, func(tx pgx.Tx) error {
			// Children first: orders reference addresses, addresses reference
			// the customer.
			if _, err := tx.Exec(ctx, `DELETE FROM orders WHERE id = ANY($1)`, f.orderIDs); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `DELETE FROM addresses WHERE id = ANY($1)`, f.addressIDs); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `DELETE FROM customers WHERE id = $1`, f.customerID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `TRUNCATE geocoded_addresses`); err != nil {
				return err
			}
			return shippingStore.UpdateConfig(ctx, tx, original)
		})
	})

	return f
}

// stubTripServer returns a router client backed by a canned /trip response.
func stubTripServer(t *testing.T, body string) *routing.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return routing.NewClient(srv.URL)
}

// stubSortingRouter is a fake OSRM that actually reads the coordinates off the
// request and orders the stops north-to-south (latitude descending), origin
// pinned first.
//
// Deliberately not a canned response: asserting against a fixed permutation
// would depend on the order ListOrders happened to return, which is
// newest-first and not something this test should encode. Sorting by a property
// of the data gives a stable expectation, and parsing the URL means this double
// also fails if the planner ever sends lat,lng instead of lng,lat.
func stubSortingRouter(t *testing.T) *routing.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/trip/v1/driving/")
		raw := strings.Split(path, ";")

		type pt struct {
			idx int
			lat float64
		}
		stops := make([]pt, 0, len(raw))
		for i, c := range raw {
			parts := strings.Split(c, ",")
			if len(parts) != 2 {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"code":"InvalidUrl"}`))
				return
			}
			lat, err := strconv.ParseFloat(parts[1], 64)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"code":"InvalidValue"}`))
				return
			}
			if i == 0 {
				continue // origin is pinned by source=first
			}
			stops = append(stops, pt{idx: i, lat: lat})
		}
		sort.Slice(stops, func(a, b int) bool { return stops[a].lat > stops[b].lat })

		position := make([]int, len(raw))
		for pos, s := range stops {
			position[s.idx] = pos + 1
		}

		var sb strings.Builder
		sb.WriteString(`{"code":"Ok","trips":[{"duration":1800,"distance":25000}],"waypoints":[`)
		for i := range raw {
			if i > 0 {
				sb.WriteByte(',')
			}
			fmt.Fprintf(&sb, `{"waypoint_index":%d}`, position[i])
		}
		sb.WriteString(`]}`)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(sb.String()))
	}))
	t.Cleanup(srv.Close)
	return routing.NewClient(srv.URL)
}

// tripReversing returns a /trip response for n coordinates that visits the
// stops in reverse input order (origin still first). Distinct from the input
// order, so a planner that ignores the router's answer fails the test.
func tripReversing(n int) string {
	// waypoint_index for input i: origin stays 0, stop i gets position n-i.
	body := `{"code":"Ok","trips":[{"duration":1800,"distance":25000}],"waypoints":[`
	for i := 0; i < n; i++ {
		if i > 0 {
			body += ","
		}
		pos := 0
		if i > 0 {
			pos = n - i
		}
		body += `{"waypoint_index":` + itoa(pos) + `}`
	}
	return body + `]}`
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func geocoderFor(coords map[string]geocode.Result) *stubGeocoder {
	return &stubGeocoder{results: coords}
}

func newRouteService(t *testing.T, g geocode.Geocoder, router *routing.Client) *app.RouteService {
	t.Helper()
	return app.NewRouteService(
		store.NewOrderStore(nil),
		store.NewCustomerStore(),
		store.NewShippingStore(),
		app.NewGeocodingService(store.NewGeocodeStore(nil), g),
		router,
	)
}

func TestPlanRoute_OrdersStopsByRouterAnswer(t *testing.T) {
	ctx := context.Background()
	seedDeliveryOrders(t, []string{"100 First St", "200 Second St", "300 Third St"})

	svc := newRouteService(t, geocoderFor(map[string]geocode.Result{
		"1234 W 4th Ave, Kennewick, WA 99336": {Lat: 46.2087, Lng: -119.1372, Confidence: "ROOFTOP"},
		"100 First St, Portland, OR 97201":    {Lat: 46.21, Lng: -119.14, Confidence: "ROOFTOP"},
		"200 Second St, Portland, OR 97201":   {Lat: 46.22, Lng: -119.15, Confidence: "ROOFTOP"},
		"300 Third St, Portland, OR 97201":    {Lat: 46.23, Lng: -119.16, Confidence: "ROOFTOP"},
	}), stubSortingRouter(t))

	plan, err := svc.PlanRoute(ctx, testPool, app.PlanRouteOptions{Roundtrip: true})
	require.NoError(t, err)

	require.Len(t, plan.Stops, 3)
	assert.Empty(t, plan.Unroutable)

	// Positions are 1-based and contiguous; the roastery is not a stop.
	for i, s := range plan.Stops {
		assert.Equal(t, i+1, s.Position)
		assert.NotEqual(t, plan.OriginAddress, s.Address)
	}

	// The stub router orders stops north to south, so the plan must come back
	// in descending latitude regardless of what order the queue listed them in.
	assert.Equal(t, "300 Third St, Portland, OR 97201", plan.Stops[0].Address)
	assert.Equal(t, "200 Second St, Portland, OR 97201", plan.Stops[1].Address)
	assert.Equal(t, "100 First St, Portland, OR 97201", plan.Stops[2].Address)
	assert.InDelta(t, 46.23, plan.Stops[0].Lat, 0.0001)
	assert.InDelta(t, -119.16, plan.Stops[0].Lng, 0.0001)

	assert.InDelta(t, 1800, plan.TotalDurationSeconds, 0.01)
	assert.InDelta(t, 25000, plan.TotalDistanceMeters, 0.01)
	assert.True(t, plan.Roundtrip)
	assert.InDelta(t, 46.2087, plan.OriginLat, 0.0001)
}

// One bad address must not cost the driver the rest of the route — but it must
// be reported, because a stop that silently disappears is a missed delivery.
func TestPlanRoute_UngeocodableStopIsSurfacedNotDropped(t *testing.T) {
	ctx := context.Background()
	seedDeliveryOrders(t, []string{"100 First St", "Nowhere At All"})

	svc := newRouteService(t, geocoderFor(map[string]geocode.Result{
		"1234 W 4th Ave, Kennewick, WA 99336": {Lat: 46.2087, Lng: -119.1372, Confidence: "ROOFTOP"},
		"100 First St, Portland, OR 97201":    {Lat: 46.21, Lng: -119.14, Confidence: "ROOFTOP"},
		// "Nowhere At All" deliberately absent -> ErrNotFound from the stub.
	}), stubTripServer(t, tripReversing(2)))

	plan, err := svc.PlanRoute(ctx, testPool, app.PlanRouteOptions{Roundtrip: true})
	require.NoError(t, err)

	assert.Len(t, plan.Stops, 1)
	require.Len(t, plan.Unroutable, 1)
	assert.Contains(t, plan.Unroutable[0].Address, "Nowhere At All")
	assert.NotEmpty(t, plan.Unroutable[0].Reason)
	assert.NotEmpty(t, plan.Unroutable[0].OrderNumber)
}

func TestPlanRoute_LowConfidenceStopsAreFlagged(t *testing.T) {
	ctx := context.Background()
	seedDeliveryOrders(t, []string{"100 First St", "200 Second St"})

	svc := newRouteService(t, geocoderFor(map[string]geocode.Result{
		"1234 W 4th Ave, Kennewick, WA 99336": {Lat: 46.2087, Lng: -119.1372, Confidence: "ROOFTOP"},
		"100 First St, Portland, OR 97201":    {Lat: 46.21, Lng: -119.14, Confidence: "ROOFTOP"},
		"200 Second St, Portland, OR 97201":   {Lat: 46.22, Lng: -119.15, Confidence: "APPROXIMATE"},
	}), stubTripServer(t, tripReversing(3)))

	plan, err := svc.PlanRoute(ctx, testPool, app.PlanRouteOptions{Roundtrip: true})
	require.NoError(t, err)
	require.Len(t, plan.Stops, 2)

	low := plan.LowConfidenceStops()
	require.Len(t, low, 1)
	assert.Equal(t, domain.GeocodeApproximate, low[0].Confidence)
	assert.Equal(t, "200 Second St, Portland, OR 97201", low[0].Address)
}

// The load list's checkbox selection narrows the route.
func TestPlanRoute_RespectsOrderIDSelection(t *testing.T) {
	ctx := context.Background()
	f := seedDeliveryOrders(t, []string{"100 First St", "200 Second St", "300 Third St"})

	svc := newRouteService(t, geocoderFor(map[string]geocode.Result{
		"1234 W 4th Ave, Kennewick, WA 99336": {Lat: 46.2087, Lng: -119.1372, Confidence: "ROOFTOP"},
		"100 First St, Portland, OR 97201":    {Lat: 46.21, Lng: -119.14, Confidence: "ROOFTOP"},
		"200 Second St, Portland, OR 97201":   {Lat: 46.22, Lng: -119.15, Confidence: "ROOFTOP"},
		"300 Third St, Portland, OR 97201":    {Lat: 46.23, Lng: -119.16, Confidence: "ROOFTOP"},
	}), stubTripServer(t, tripReversing(3)))

	plan, err := svc.PlanRoute(ctx, testPool, app.PlanRouteOptions{
		OrderIDs:  f.orderIDs[:2],
		Roundtrip: true,
	})
	require.NoError(t, err)
	assert.Len(t, plan.Stops, 2)
}

// Cache hits versus billable lookups are surfaced so the admin view can show
// what a plan cost.
func TestPlanRoute_ReportsGeocodeCosts(t *testing.T) {
	ctx := context.Background()
	seedDeliveryOrders(t, []string{"100 First St"})

	results := map[string]geocode.Result{
		"1234 W 4th Ave, Kennewick, WA 99336": {Lat: 46.2087, Lng: -119.1372, Confidence: "ROOFTOP"},
		"100 First St, Portland, OR 97201":    {Lat: 46.21, Lng: -119.14, Confidence: "ROOFTOP"},
	}
	stub := geocoderFor(results)
	svc := newRouteService(t, stub, stubTripServer(t, tripReversing(2)))

	first, err := svc.PlanRoute(ctx, testPool, app.PlanRouteOptions{Roundtrip: true})
	require.NoError(t, err)
	assert.Equal(t, 2, first.GeocodeLookups, "origin + one stop, both cold")
	assert.Equal(t, 0, first.GeocodeCacheHits)

	second, err := svc.PlanRoute(ctx, testPool, app.PlanRouteOptions{Roundtrip: true})
	require.NoError(t, err)
	assert.Equal(t, 0, second.GeocodeLookups, "replanning must not re-bill")
	assert.Equal(t, 2, second.GeocodeCacheHits)
	assert.Equal(t, 2, stub.callCount(), "provider called once per address, ever")
}

func TestPlanRoute_EmptyQueue(t *testing.T) {
	ctx := context.Background()
	seedDeliveryOrders(t, nil) // sets the origin, creates no orders

	svc := newRouteService(t, geocoderFor(map[string]geocode.Result{
		"1234 W 4th Ave, Kennewick, WA 99336": {Lat: 46.2087, Lng: -119.1372, Confidence: "ROOFTOP"},
	}), stubTripServer(t, tripReversing(1)))

	_, err := svc.PlanRoute(ctx, testPool, app.PlanRouteOptions{Roundtrip: true})
	assert.ErrorIs(t, err, app.ErrNoDeliveryStops)
}

// Every stop failing is not a route with zero stops — it is a planning failure
// the admin needs told about.
func TestPlanRoute_AllStopsUngeocodable(t *testing.T) {
	ctx := context.Background()
	seedDeliveryOrders(t, []string{"Nowhere At All"})

	svc := newRouteService(t, geocoderFor(map[string]geocode.Result{
		"1234 W 4th Ave, Kennewick, WA 99336": {Lat: 46.2087, Lng: -119.1372, Confidence: "ROOFTOP"},
	}), stubTripServer(t, tripReversing(1)))

	_, err := svc.PlanRoute(ctx, testPool, app.PlanRouteOptions{Roundtrip: true})
	assert.ErrorIs(t, err, app.ErrNoDeliveryStops)
}

// An origin that cannot be placed is a settings problem, and must not be
// confused with a customer address problem.
func TestPlanRoute_OriginNotGeocodable(t *testing.T) {
	ctx := context.Background()
	seedDeliveryOrders(t, []string{"100 First St"})

	svc := newRouteService(t, geocoderFor(map[string]geocode.Result{
		"100 First St, Portland, OR 97201": {Lat: 46.21, Lng: -119.14, Confidence: "ROOFTOP"},
		// origin deliberately absent
	}), stubTripServer(t, tripReversing(2)))

	_, err := svc.PlanRoute(ctx, testPool, app.PlanRouteOptions{Roundtrip: true})
	assert.ErrorIs(t, err, app.ErrOriginNotGeocodable)
}

// A router outage is transient and must surface as such — the addresses are
// fine, the box isn't.
func TestPlanRoute_RouterUnavailable(t *testing.T) {
	ctx := context.Background()
	seedDeliveryOrders(t, []string{"100 First St"})

	down := routing.NewClient("http://192.0.2.1:1")
	down.HTTP = &http.Client{Timeout: 100 * 1e6} // 100ms

	svc := newRouteService(t, geocoderFor(map[string]geocode.Result{
		"1234 W 4th Ave, Kennewick, WA 99336": {Lat: 46.2087, Lng: -119.1372, Confidence: "ROOFTOP"},
		"100 First St, Portland, OR 97201":    {Lat: 46.21, Lng: -119.14, Confidence: "ROOFTOP"},
	}), down)

	_, err := svc.PlanRoute(ctx, testPool, app.PlanRouteOptions{Roundtrip: true})
	assert.ErrorIs(t, err, routing.ErrUnavailable)
}

// Pickup and shipped orders share the fulfillment queue with deliveries. Only
// local delivery belongs on a van route.
func TestPlanRoute_ExcludesNonDeliveryOrders(t *testing.T) {
	ctx := context.Background()
	f := seedDeliveryOrders(t, []string{"100 First St"})

	// Add a pickup order at a different address, committed the same way.
	var pickupOrderID, pickupAddrID uuid.UUID
	require.NoError(t, store.Tx(ctx, testPool, func(tx pgx.Tx) error {
		addr := testutil.CreateAddress(t, tx, f.customerID, testutil.WithAddressLine1("999 Pickup Ln"))
		pickupAddrID = addr.ID
		o := testutil.CreateOrder(t, tx, f.customerID, addr.ID, addr.ID,
			testutil.WithShippingMethod(domain.ShippingMethodPickup),
			testutil.WithFulfillmentStatus(domain.FulfillmentStatusUnfulfilled),
			testutil.WithOrderStatus(domain.OrderStatusConfirmed),
		)
		pickupOrderID = o.ID
		return nil
	}))
	t.Cleanup(func() {
		_ = store.Tx(ctx, testPool, func(tx pgx.Tx) error {
			_, _ = tx.Exec(ctx, `DELETE FROM orders WHERE id = $1`, pickupOrderID)
			_, _ = tx.Exec(ctx, `DELETE FROM addresses WHERE id = $1`, pickupAddrID)
			return nil
		})
	})

	svc := newRouteService(t, geocoderFor(map[string]geocode.Result{
		"1234 W 4th Ave, Kennewick, WA 99336": {Lat: 46.2087, Lng: -119.1372, Confidence: "ROOFTOP"},
		"100 First St, Portland, OR 97201":    {Lat: 46.21, Lng: -119.14, Confidence: "ROOFTOP"},
		"999 Pickup Ln, Portland, OR 97201":   {Lat: 46.24, Lng: -119.17, Confidence: "ROOFTOP"},
	}), stubTripServer(t, tripReversing(2)))

	plan, err := svc.PlanRoute(ctx, testPool, app.PlanRouteOptions{Roundtrip: true})
	require.NoError(t, err)
	require.Len(t, plan.Stops, 1)
	assert.Equal(t, "100 First St, Portland, OR 97201", plan.Stops[0].Address)
}
