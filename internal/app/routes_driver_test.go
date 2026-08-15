package app_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
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

// newDriverRouteService wires the full driver path: planning, persistence, and
// the order service that closes deliveries out.
func newDriverRouteService(t *testing.T, g geocode.Geocoder, router *routing.Client) *app.RouteService {
	t.Helper()
	t.Cleanup(func() {
		_, err := testPool.Exec(context.Background(), `TRUNCATE route_stops, delivery_routes CASCADE`)
		require.NoError(t, err)
	})
	orderSvc := app.NewOrderService(store.NewOrderStore(nil), audit.NewAuditWriter(), nil)
	return app.NewRouteService(
		store.NewOrderStore(nil),
		store.NewCustomerStore(),
		store.NewShippingStore(),
		app.NewGeocodingService(store.NewGeocodeStore(nil), g),
		router,
	).WithPersistence(store.NewRouteStore(nil), audit.NewAuditWriter()).
		WithOrderService(orderSvc)
}

// planAndActivate gets a route into the state a driver actually sees.
func planAndActivate(t *testing.T, svc *app.RouteService, addressLines []string) *app.SavedRoute {
	t.Helper()
	ctx := context.Background()

	saved, _, err := svc.PlanAndSaveRoute(ctx, testPool, routeDate,
		app.PlanRouteOptions{Roundtrip: true}, testutil.TestActor())
	require.NoError(t, err)
	require.Len(t, saved.Stops, len(addressLines))

	var activated *app.SavedRoute
	require.NoError(t, store.Tx(ctx, testPool, func(tx pgx.Tx) error {
		var aErr error
		activated, aErr = svc.ActivateRoute(ctx, tx, saved.Route.ID, testutil.TestActor())
		return aErr
	}))
	return activated
}

func orderStatus(t *testing.T, orderID uuid.UUID) (domain.FulfillmentStatus, domain.OrderStatus) {
	t.Helper()
	var fs, os string
	require.NoError(t, testPool.QueryRow(context.Background(),
		`SELECT fulfillment_status, status FROM orders WHERE id = $1`, orderID).Scan(&fs, &os))
	return domain.FulfillmentStatus(fs), domain.OrderStatus(os)
}

// The core promise: the driver's tap closes the actual order, so the
// fulfillment queue and the phone never disagree.
func TestMarkStopDelivered_ClosesTheOrder(t *testing.T) {
	ctx := context.Background()
	lines := []string{"100 First St", "200 Second St"}
	seedDeliveryOrders(t, lines)
	svc := newDriverRouteService(t, routeTestGeocoder(), stubSortingRouter(t))
	route := planAndActivate(t, svc, lines)
	stop := route.Stops[0]

	var updated *app.SavedRoute
	require.NoError(t, store.Tx(ctx, testPool, func(tx pgx.Tx) error {
		var mErr error
		updated, mErr = svc.MarkStopDelivered(ctx, tx, route.Route.ID, stop.ID, testutil.TestActor())
		return mErr
	}))

	assert.Equal(t, domain.RouteStopDelivered, updated.Stops[0].Status)
	assert.NotNil(t, updated.Stops[0].DeliveredAt)

	fulfillment, status := orderStatus(t, stop.OrderID)
	assert.Equal(t, domain.FulfillmentStatusDelivered, fulfillment)
	assert.Equal(t, domain.OrderStatusComplete, status)

	tx := testutil.NewTestTx(t, testPool)
	entry := testutil.LastAuditEntry(t, tx, "order", stop.OrderID)
	assert.Equal(t, audit.AuditOrderDelivered, entry.Action)
}

// Phones double-fire taps. A second one must be a no-op, not an error page in
// a van.
func TestMarkStopDelivered_IsIdempotent(t *testing.T) {
	ctx := context.Background()
	lines := []string{"100 First St", "200 Second St"}
	seedDeliveryOrders(t, lines)
	svc := newDriverRouteService(t, routeTestGeocoder(), stubSortingRouter(t))
	route := planAndActivate(t, svc, lines)
	stop := route.Stops[0]

	for i := 0; i < 2; i++ {
		require.NoError(t, store.Tx(ctx, testPool, func(tx pgx.Tx) error {
			_, mErr := svc.MarkStopDelivered(ctx, tx, route.Route.ID, stop.ID, testutil.TestActor())
			return mErr
		}), "tap %d", i+1)
	}

	fulfillment, _ := orderStatus(t, stop.OrderID)
	assert.Equal(t, domain.FulfillmentStatusDelivered, fulfillment)
}

// A skip is route-level only: the order stays exactly where it was, so it
// rolls onto the next run's route without anyone re-queueing it.
func TestMarkStopSkipped_LeavesOrderInQueue(t *testing.T) {
	ctx := context.Background()
	lines := []string{"100 First St", "200 Second St"}
	seedDeliveryOrders(t, lines)
	svc := newDriverRouteService(t, routeTestGeocoder(), stubSortingRouter(t))
	route := planAndActivate(t, svc, lines)
	stop := route.Stops[0]

	beforeFulfillment, beforeStatus := orderStatus(t, stop.OrderID)

	var updated *app.SavedRoute
	require.NoError(t, store.Tx(ctx, testPool, func(tx pgx.Tx) error {
		var mErr error
		updated, mErr = svc.MarkStopSkipped(ctx, tx, route.Route.ID, stop.ID,
			"wrong address on the order", testutil.TestActor())
		return mErr
	}))

	assert.Equal(t, domain.RouteStopSkipped, updated.Stops[0].Status)
	assert.Equal(t, "wrong address on the order", updated.Stops[0].SkipReason)
	assert.Nil(t, updated.Stops[0].DeliveredAt, "a skipped stop was never delivered")

	afterFulfillment, afterStatus := orderStatus(t, stop.OrderID)
	assert.Equal(t, beforeFulfillment, afterFulfillment, "the order must be untouched")
	assert.Equal(t, beforeStatus, afterStatus)

	// The skip is recorded against the order, where "why wasn't this
	// delivered?" gets asked.
	tx := testutil.NewTestTx(t, testPool)
	entry := testutil.LastAuditEntry(t, tx, "order", stop.OrderID)
	assert.Equal(t, audit.AuditRouteStopSkipped, entry.Action)
}

// The order still shows up for the next run — the whole point of a skip.
func TestSkippedOrderIsStillRoutableNextRun(t *testing.T) {
	ctx := context.Background()
	lines := []string{"100 First St", "200 Second St"}
	seedDeliveryOrders(t, lines)
	svc := newDriverRouteService(t, routeTestGeocoder(), stubSortingRouter(t))
	route := planAndActivate(t, svc, lines)

	// Skip one, deliver the other — that completes the run.
	require.NoError(t, store.Tx(ctx, testPool, func(tx pgx.Tx) error {
		if _, err := svc.MarkStopSkipped(ctx, tx, route.Route.ID, route.Stops[0].ID, "nobody home", testutil.TestActor()); err != nil {
			return err
		}
		_, err := svc.MarkStopDelivered(ctx, tx, route.Route.ID, route.Stops[1].ID, testutil.TestActor())
		return err
	}))

	// Plan the next run: the skipped order is back, the delivered one is gone.
	plan, err := svc.PlanRoute(ctx, testPool, app.PlanRouteOptions{Roundtrip: true})
	require.NoError(t, err)
	require.Len(t, plan.Stops, 1)
	assert.Equal(t, route.Stops[0].OrderID, plan.Stops[0].OrderID)
}

// Resolving the last stop ends the run, which retires the token — there is no
// logout on a page with no login.
func TestRouteAutoCompletesAndClosesTheLink(t *testing.T) {
	ctx := context.Background()
	lines := []string{"100 First St", "200 Second St"}
	seedDeliveryOrders(t, lines)
	svc := newDriverRouteService(t, routeTestGeocoder(), stubSortingRouter(t))
	route := planAndActivate(t, svc, lines)
	token := *route.Route.ShareToken

	var final *app.SavedRoute
	require.NoError(t, store.Tx(ctx, testPool, func(tx pgx.Tx) error {
		if _, err := svc.MarkStopDelivered(ctx, tx, route.Route.ID, route.Stops[0].ID, testutil.TestActor()); err != nil {
			return err
		}
		var mErr error
		final, mErr = svc.MarkStopDelivered(ctx, tx, route.Route.ID, route.Stops[1].ID, testutil.TestActor())
		return mErr
	}))

	assert.Equal(t, domain.RouteStatusCompleted, final.Route.Status)
	assert.True(t, final.Progress().Complete())

	tx := testutil.NewTestTx(t, testPool)
	_, err := svc.GetRouteByShareToken(ctx, tx, token)
	assert.ErrorIs(t, err, app.ErrRouteNotFound)
}

// A run whose last stop was skipped is still a finished run.
func TestRouteAutoCompletesWithSkips(t *testing.T) {
	ctx := context.Background()
	lines := []string{"100 First St"}
	seedDeliveryOrders(t, lines)
	svc := newDriverRouteService(t, routeTestGeocoder(), stubSortingRouter(t))
	route := planAndActivate(t, svc, lines)

	var final *app.SavedRoute
	require.NoError(t, store.Tx(ctx, testPool, func(tx pgx.Tx) error {
		var mErr error
		final, mErr = svc.MarkStopSkipped(ctx, tx, route.Route.ID, route.Stops[0].ID, "shop closed", testutil.TestActor())
		return mErr
	}))
	assert.Equal(t, domain.RouteStatusCompleted, final.Route.Status)
}

// The share token grants exactly one route. A stop id from another route must
// look like a stop that does not exist.
func TestStopFromAnotherRouteIsNotFound(t *testing.T) {
	ctx := context.Background()
	lines := []string{"100 First St"}
	seedDeliveryOrders(t, lines)
	svc := newDriverRouteService(t, routeTestGeocoder(), stubSortingRouter(t))
	route := planAndActivate(t, svc, lines)

	err := store.Tx(ctx, testPool, func(tx pgx.Tx) error {
		_, mErr := svc.MarkStopDelivered(ctx, tx, route.Route.ID, uuid.New(), testutil.TestActor())
		return mErr
	})
	assert.ErrorIs(t, err, app.ErrRouteStopNotFound)
}

// Un-delivering would mean un-completing the order behind it — a staff action
// in admin, not something to do from a phone.
func TestSkipRejectsAlreadyDeliveredStop(t *testing.T) {
	ctx := context.Background()
	lines := []string{"100 First St", "200 Second St"}
	seedDeliveryOrders(t, lines)
	svc := newDriverRouteService(t, routeTestGeocoder(), stubSortingRouter(t))
	route := planAndActivate(t, svc, lines)
	stop := route.Stops[0]

	require.NoError(t, store.Tx(ctx, testPool, func(tx pgx.Tx) error {
		_, err := svc.MarkStopDelivered(ctx, tx, route.Route.ID, stop.ID, testutil.TestActor())
		return err
	}))

	err := store.Tx(ctx, testPool, func(tx pgx.Tx) error {
		_, mErr := svc.MarkStopSkipped(ctx, tx, route.Route.ID, stop.ID, "changed my mind", testutil.TestActor())
		return mErr
	})
	assert.ErrorIs(t, err, app.ErrStopAlreadyDelivered)
}

func TestSkipReasonIsBounded(t *testing.T) {
	ctx := context.Background()
	lines := []string{"100 First St", "200 Second St"}
	seedDeliveryOrders(t, lines)
	svc := newDriverRouteService(t, routeTestGeocoder(), stubSortingRouter(t))
	route := planAndActivate(t, svc, lines)

	long := make([]byte, 900)
	for i := range long {
		long[i] = 'x'
	}

	var updated *app.SavedRoute
	require.NoError(t, store.Tx(ctx, testPool, func(tx pgx.Tx) error {
		var mErr error
		updated, mErr = svc.MarkStopSkipped(ctx, tx, route.Route.ID, route.Stops[0].ID, string(long), testutil.TestActor())
		return mErr
	}))
	assert.LessOrEqual(t, len(updated.Stops[0].SkipReason), 500)
}

// MarkLocallyDelivered exists because the carrier path does not fit. Guard the
// states it accepts, so it can't be used to "deliver" a shipped or cancelled
// order out from under the tracking flow.
func TestMarkLocallyDelivered_RejectsNonQueuedOrders(t *testing.T) {
	ctx := context.Background()
	tx := testutil.NewTestTx(t, testPool)
	orderSvc := app.NewOrderService(store.NewOrderStore(nil), audit.NewAuditWriter(), nil)

	cust := testutil.CreateCustomer(t, tx)
	addr := testutil.CreateAddress(t, tx, cust.ID)
	order := testutil.CreateOrder(t, tx, cust.ID, addr.ID, addr.ID,
		testutil.WithShippingMethod(domain.ShippingMethodLocalDelivery),
		testutil.WithFulfillmentStatus(domain.FulfillmentStatusShipped),
		testutil.WithOrderStatus(domain.OrderStatusComplete),
	)

	_, err := orderSvc.MarkLocallyDelivered(ctx, tx, order.ID, testutil.TestActor())
	assert.ErrorIs(t, err, app.ErrInvalidOrderStatus)
}
