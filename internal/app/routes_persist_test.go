package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/geocode"
	"github.com/dukerupert/hiri/internal/platform/routing"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// newPersistingRouteService wires a planner with persistence, and clears the
// route tables afterwards. Routes are written in committed transactions (the
// plan phase calls out to HTTP), so rollback isolation isn't available.
func newPersistingRouteService(t *testing.T, g geocode.Geocoder, router *routing.Client) *app.RouteService {
	t.Helper()
	t.Cleanup(func() {
		_, err := testPool.Exec(context.Background(), `TRUNCATE route_stops, delivery_routes CASCADE`)
		require.NoError(t, err)
	})
	return app.NewRouteService(
		store.NewOrderStore(nil),
		store.NewCustomerStore(),
		store.NewShippingStore(),
		app.NewGeocodingService(store.NewGeocodeStore(nil), g),
		router,
	).WithPersistence(store.NewRouteStore(nil), audit.NewAuditWriter())
}

func routeTestGeocoder() *stubGeocoder {
	return geocoderFor(map[string]geocode.Result{
		"1234 W 4th Ave, Kennewick, WA 99336": {Lat: 46.2087, Lng: -119.1372, Confidence: "ROOFTOP"},
		"100 First St, Portland, OR 97201":    {Lat: 46.21, Lng: -119.14, Confidence: "ROOFTOP"},
		"200 Second St, Portland, OR 97201":   {Lat: 46.22, Lng: -119.15, Confidence: "ROOFTOP"},
		"300 Third St, Portland, OR 97201":    {Lat: 46.23, Lng: -119.16, Confidence: "ROOFTOP"},
	})
}

var routeDate = time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC)

func TestPlanAndSaveRoute_PersistsOrderedStops(t *testing.T) {
	ctx := context.Background()
	seedDeliveryOrders(t, []string{"100 First St", "200 Second St", "300 Third St"})
	svc := newPersistingRouteService(t, routeTestGeocoder(), stubSortingRouter(t))

	saved, plan, err := svc.PlanAndSaveRoute(ctx, testPool, routeDate,
		app.PlanRouteOptions{Roundtrip: true}, testutil.TestActor())
	require.NoError(t, err)
	require.Len(t, plan.Stops, 3)

	assert.Equal(t, domain.RouteStatusDraft, saved.Route.Status)
	assert.Nil(t, saved.Route.ShareToken, "a draft has no shareable URL")
	assert.Equal(t, 25000, saved.Route.TotalDistanceMeters)
	assert.Equal(t, 1800, saved.Route.TotalDurationSecs)
	assert.True(t, saved.Route.Roundtrip)

	require.Len(t, saved.Stops, 3)
	for i, st := range saved.Stops {
		assert.Equal(t, i+1, st.Position)
		assert.Equal(t, domain.RouteStopPending, st.Status)
		assert.NotZero(t, st.Lat)
	}
	// Stop order survives the round trip through the database.
	assert.Equal(t, "300 Third St, Portland, OR 97201", saved.Stops[0].Address)
	assert.Equal(t, "100 First St, Portland, OR 97201", saved.Stops[2].Address)

	tx := testutil.NewTestTx(t, testPool)
	entry := testutil.LastAuditEntry(t, tx, "delivery_route", saved.Route.ID)
	assert.Equal(t, audit.AuditRoutePlanned, entry.Action)
}

// Re-planning is the normal loop — staff adjust the selection and go again —
// so it must replace rather than accumulate.
func TestPlanAndSaveRoute_ReplacesExistingDraft(t *testing.T) {
	ctx := context.Background()
	f := seedDeliveryOrders(t, []string{"100 First St", "200 Second St", "300 Third St"})
	svc := newPersistingRouteService(t, routeTestGeocoder(), stubSortingRouter(t))

	first, _, err := svc.PlanAndSaveRoute(ctx, testPool, routeDate,
		app.PlanRouteOptions{Roundtrip: true}, testutil.TestActor())
	require.NoError(t, err)
	require.Len(t, first.Stops, 3)

	// Staff drop a stop and re-plan.
	second, _, err := svc.PlanAndSaveRoute(ctx, testPool, routeDate,
		app.PlanRouteOptions{OrderIDs: f.orderIDs[:2], Roundtrip: true}, testutil.TestActor())
	require.NoError(t, err)
	assert.Len(t, second.Stops, 2)
	assert.NotEqual(t, first.Route.ID, second.Route.ID)

	tx := testutil.NewTestTx(t, testPool)
	routes, err := svc.ListRoutes(ctx, tx, 50)
	require.NoError(t, err)
	assert.Len(t, routes, 1, "re-planning replaces the draft rather than piling up")
}

func TestActivateRoute_MintsTokenAndOpensDriverPage(t *testing.T) {
	ctx := context.Background()
	seedDeliveryOrders(t, []string{"100 First St", "200 Second St"})
	svc := newPersistingRouteService(t, routeTestGeocoder(), stubSortingRouter(t))

	saved, _, err := svc.PlanAndSaveRoute(ctx, testPool, routeDate,
		app.PlanRouteOptions{Roundtrip: true}, testutil.TestActor())
	require.NoError(t, err)

	var activated *app.SavedRoute
	require.NoError(t, store.Tx(ctx, testPool, func(tx pgx.Tx) error {
		var aErr error
		activated, aErr = svc.ActivateRoute(ctx, tx, saved.Route.ID, testutil.TestActor())
		return aErr
	}))

	assert.Equal(t, domain.RouteStatusActive, activated.Route.Status)
	require.NotNil(t, activated.Route.ShareToken)
	assert.Len(t, *activated.Route.ShareToken, 64, "32 random bytes, hex encoded")
	require.NotNil(t, activated.Route.ActivatedAt)
	assert.Contains(t, activated.DriverURL("https://shop.example"), "/routes/")

	// The token now resolves to the route.
	tx := testutil.NewTestTx(t, testPool)
	byToken, err := svc.GetRouteByShareToken(ctx, tx, *activated.Route.ShareToken)
	require.NoError(t, err)
	assert.Equal(t, saved.Route.ID, byToken.Route.ID)
	assert.Len(t, byToken.Stops, 2)
}

// Re-activating would mint a second token and silently break the link the
// driver already has open.
func TestActivateRoute_RejectsSecondActivation(t *testing.T) {
	ctx := context.Background()
	seedDeliveryOrders(t, []string{"100 First St"})
	svc := newPersistingRouteService(t, routeTestGeocoder(), stubSortingRouter(t))

	saved, _, err := svc.PlanAndSaveRoute(ctx, testPool, routeDate,
		app.PlanRouteOptions{Roundtrip: true}, testutil.TestActor())
	require.NoError(t, err)

	require.NoError(t, store.Tx(ctx, testPool, func(tx pgx.Tx) error {
		_, aErr := svc.ActivateRoute(ctx, tx, saved.Route.ID, testutil.TestActor())
		return aErr
	}))

	err = store.Tx(ctx, testPool, func(tx pgx.Tx) error {
		_, aErr := svc.ActivateRoute(ctx, tx, saved.Route.ID, testutil.TestActor())
		return aErr
	})
	assert.ErrorIs(t, err, app.ErrRouteNotActivatable)
}

// A driver may be working the route; swapping their stop list mid-run is the
// one thing planning must never do.
func TestPlanAndSaveRoute_RefusesWhileRouteIsActive(t *testing.T) {
	ctx := context.Background()
	seedDeliveryOrders(t, []string{"100 First St", "200 Second St"})
	stub := routeTestGeocoder()
	svc := newPersistingRouteService(t, stub, stubSortingRouter(t))

	saved, _, err := svc.PlanAndSaveRoute(ctx, testPool, routeDate,
		app.PlanRouteOptions{Roundtrip: true}, testutil.TestActor())
	require.NoError(t, err)
	require.NoError(t, store.Tx(ctx, testPool, func(tx pgx.Tx) error {
		_, aErr := svc.ActivateRoute(ctx, tx, saved.Route.ID, testutil.TestActor())
		return aErr
	}))

	callsBefore := stub.callCount()
	_, _, err = svc.PlanAndSaveRoute(ctx, testPool, routeDate,
		app.PlanRouteOptions{Roundtrip: true}, testutil.TestActor())
	assert.ErrorIs(t, err, app.ErrRouteAlreadyActive)
	assert.Equal(t, callsBefore, stub.callCount(),
		"must refuse before spending money on geocoding")
}

func TestActivateRoute_RejectsEmptyRoute(t *testing.T) {
	ctx := context.Background()
	seedDeliveryOrders(t, []string{"100 First St"})
	svc := newPersistingRouteService(t, routeTestGeocoder(), stubSortingRouter(t))

	saved, _, err := svc.PlanAndSaveRoute(ctx, testPool, routeDate,
		app.PlanRouteOptions{Roundtrip: true}, testutil.TestActor())
	require.NoError(t, err)

	// Strip the stops behind the service's back — the same state a route would
	// be in if every order were cancelled after planning.
	_, err = testPool.Exec(ctx, `DELETE FROM route_stops WHERE route_id = $1`, saved.Route.ID)
	require.NoError(t, err)

	err = store.Tx(ctx, testPool, func(tx pgx.Tx) error {
		_, aErr := svc.ActivateRoute(ctx, tx, saved.Route.ID, testutil.TestActor())
		return aErr
	})
	assert.ErrorIs(t, err, app.ErrRouteEmpty)
}

// The token dying at completion is the whole authentication model for the
// driver page.
func TestCompleteRoute_RetiresShareToken(t *testing.T) {
	ctx := context.Background()
	seedDeliveryOrders(t, []string{"100 First St"})
	svc := newPersistingRouteService(t, routeTestGeocoder(), stubSortingRouter(t))

	saved, _, err := svc.PlanAndSaveRoute(ctx, testPool, routeDate,
		app.PlanRouteOptions{Roundtrip: true}, testutil.TestActor())
	require.NoError(t, err)

	var token string
	require.NoError(t, store.Tx(ctx, testPool, func(tx pgx.Tx) error {
		activated, aErr := svc.ActivateRoute(ctx, tx, saved.Route.ID, testutil.TestActor())
		if aErr != nil {
			return aErr
		}
		token = *activated.Route.ShareToken
		return nil
	}))

	require.NoError(t, store.Tx(ctx, testPool, func(tx pgx.Tx) error {
		completed, cErr := svc.CompleteRoute(ctx, tx, saved.Route.ID, testutil.TestActor())
		if cErr != nil {
			return cErr
		}
		assert.Equal(t, domain.RouteStatusCompleted, completed.Route.Status)
		require.NotNil(t, completed.Route.CompletedAt)
		return nil
	}))

	tx := testutil.NewTestTx(t, testPool)
	_, err = svc.GetRouteByShareToken(ctx, tx, token)
	assert.ErrorIs(t, err, app.ErrRouteNotFound, "the driver link must stop working")
}

func TestGetRouteByShareToken_UnknownAndDraftTokens(t *testing.T) {
	ctx := context.Background()
	seedDeliveryOrders(t, []string{"100 First St"})
	svc := newPersistingRouteService(t, routeTestGeocoder(), stubSortingRouter(t))

	_, _, err := svc.PlanAndSaveRoute(ctx, testPool, routeDate,
		app.PlanRouteOptions{Roundtrip: true}, testutil.TestActor())
	require.NoError(t, err)

	tx := testutil.NewTestTx(t, testPool)
	for _, token := range []string{"", "not-a-real-token"} {
		_, err := svc.GetRouteByShareToken(ctx, tx, token)
		assert.ErrorIs(t, err, app.ErrRouteNotFound, "token %q", token)
	}
}

// Completing a run with skipped stops is normal: a skip is a decision, not an
// omission, and the order simply rolls onto the next run.
func TestRouteProgress(t *testing.T) {
	stops := []domain.RouteStop{
		{Status: domain.RouteStopDelivered},
		{Status: domain.RouteStopSkipped},
		{Status: domain.RouteStopPending},
	}
	p := domain.Progress(stops)
	assert.Equal(t, 3, p.Total)
	assert.Equal(t, 1, p.Delivered)
	assert.Equal(t, 1, p.Skipped)
	assert.Equal(t, 1, p.Remaining())
	assert.False(t, p.Complete())

	stops[2].Status = domain.RouteStopSkipped
	p = domain.Progress(stops)
	assert.True(t, p.Complete(), "all stops resolved, even with skips")
}

// The origin is the one address the customer-order sweep never covers, and it
// fails differently: a bad customer address costs one stop, an unroutable
// origin costs every route. So the warmer must resolve it too.
func TestWarmCache_IncludesTheRoasteryOrigin(t *testing.T) {
	ctx := context.Background()
	seedDeliveryOrders(t, []string{"100 First St"})

	stub := geocoderFor(map[string]geocode.Result{
		"1234 W 4th Ave, Kennewick, WA 99336": {Lat: 46.2087, Lng: -119.1372, Confidence: "ROOFTOP"},
		"100 First St, Portland, OR 97201":    {Lat: 46.21, Lng: -119.14, Confidence: "ROOFTOP"},
	})
	svc := newGeocodingService(t, stub).WithOrigin(store.NewShippingStore())

	res, err := svc.WarmCache(ctx, testPool, 100)
	require.NoError(t, err)

	assert.Equal(t, "1234 W 4th Ave, Kennewick, WA 99336", res.OriginAddress)
	origin, ok := res.Resolved[res.OriginAddress]
	require.True(t, ok, "the origin must be warmed, not just the customer addresses")
	assert.InDelta(t, 46.2087, origin.Lat, 0.0001)
	assert.Len(t, res.Resolved, 2, "origin + one delivery address")
}
