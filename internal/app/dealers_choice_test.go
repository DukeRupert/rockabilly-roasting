package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

func newDealersChoiceService() *app.DealersChoiceService {
	return app.NewDealersChoiceService(store.NewPickStore(nil), store.NewOrderStore(nil), store.NewCatalogStore(), audit.NewAuditWriter())
}

// createCoffee makes an active, public product with a 12oz variant, which is
// what puts a coffee in the dealer's choice pool. decaf tags it through the
// caffeine-level attribute, the one the pool reads.
func createCoffee(t *testing.T, tx pgx.Tx, title string, decaf bool) *domain.Product {
	t.Helper()
	ctx := context.Background()
	p := testutil.CreateProduct(t, tx,
		testutil.WithProductTitle(title),
		testutil.WithProductStatus(domain.ProductStatusActive),
		testutil.WithProductVisibility(domain.ProductVisibilityPublic))
	v := testutil.CreateVariant(t, tx, p.ID)
	tag12oz(t, tx, p.ID, v.ID)

	if decaf {
		var setID, keyID uuid.UUID
		require.NoError(t, tx.QueryRow(ctx,
			`INSERT INTO attribute_sets (name, slug) VALUES ('Coffee', $1) RETURNING id`, "coffee-"+uuid.NewString()).Scan(&setID))
		require.NoError(t, tx.QueryRow(ctx,
			`INSERT INTO attribute_keys (attribute_set_id, name, slug) VALUES ($1, 'Caffeine', 'caffeine-level') RETURNING id`, setID).Scan(&keyID))
		_, err := tx.Exec(ctx,
			`INSERT INTO product_attribute_values (product_id, attribute_key_id, value) VALUES ($1, $2, 'decaf')`, p.ID, keyID)
		require.NoError(t, err)
	}
	return p
}

// tag12oz gives a variant a Size option value of "12 oz".
func tag12oz(t *testing.T, tx pgx.Tx, productID, variantID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	var optID, valID uuid.UUID
	require.NoError(t, tx.QueryRow(ctx,
		`INSERT INTO product_options (product_id, name) VALUES ($1, 'Size') RETURNING id`, productID).Scan(&optID))
	require.NoError(t, tx.QueryRow(ctx,
		`INSERT INTO product_option_values (product_option_id, value) VALUES ($1, '12 oz') RETURNING id`, optID).Scan(&valID))
	_, err := tx.Exec(ctx,
		`INSERT INTO variant_option_values (variant_id, product_option_value_id) VALUES ($1, $2)`, variantID, valID)
	require.NoError(t, err)
}

func createLineItem(t *testing.T, tx pgx.Tx, orderID, variantID uuid.UUID, qty int) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, tx.QueryRow(context.Background(),
		`INSERT INTO line_items (order_id, variant_id, quantity, unit_price, subtotal, total)
		 VALUES ($1, $2, $3, 4500, 4500, 4500) RETURNING id`,
		orderID, variantID, qty).Scan(&id))
	return id
}

type dealersChoiceFixture struct {
	svc       *app.DealersChoiceService
	order     *domain.Order
	lineID    uuid.UUID
	packID    uuid.UUID // the dealer's choice product
	variantID uuid.UUID
	coffees   []*domain.Product
	decaf     *domain.Product
	customer  uuid.UUID
	shipID    uuid.UUID
	billID    uuid.UUID
}

// newDealersChoiceFixture builds four regular coffees, one decaf, and a
// 3-bag no-decaf pack ordered once.
func newDealersChoiceFixture(t *testing.T, tx pgx.Tx) dealersChoiceFixture {
	t.Helper()
	ctx := context.Background()
	f := dealersChoiceFixture{svc: newDealersChoiceService()}
	for _, title := range []string{"Cloud 9", "Rebel Blend", "Hot Rod", "Midnight Ride"} {
		f.coffees = append(f.coffees, createCoffee(t, tx, title, false))
	}
	f.decaf = createCoffee(t, tx, "Sleepwalker Decaf", true)

	pack := testutil.CreateProduct(t, tx,
		testutil.WithProductTitle("Dealer's Choice"),
		testutil.WithProductStatus(domain.ProductStatusActive),
		testutil.WithProductVisibility(domain.ProductVisibilityPublic))
	f.packID = pack.ID
	f.variantID = testutil.CreateVariant(t, tx, pack.ID).ID
	// Give the pack a 12oz option value too, so it would land in the pool if
	// the query stopped excluding packs.
	tag12oz(t, tx, pack.ID, f.variantID)
	require.NoError(t, f.svc.SetPickRule(ctx, tx, pack.ID, f.variantID, 3, true, testutil.TestActor()))

	f.customer, f.shipID, f.billID = orderFixtures(t, tx)
	f.order = testutil.CreateOrder(t, tx, f.customer, f.shipID, f.billID,
		testutil.WithOrderStatus(domain.OrderStatusConfirmed),
		testutil.WithPaymentStatus(domain.PaymentStatusCaptured))
	f.lineID = createLineItem(t, tx, f.order.ID, f.variantID, 1)
	return f
}

func (f dealersChoiceFixture) ids(ps ...*domain.Product) []uuid.UUID {
	out := make([]uuid.UUID, len(ps))
	for i, p := range ps {
		out[i] = p.ID
	}
	return out
}

func TestDealersChoice_FulfillRefusedUntilPicksSaved(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := context.Background()
	f := newDealersChoiceFixture(t, tx)
	orders := newOrderService()

	_, err := orders.FulfillOrder(ctx, tx, f.order.ID, testutil.TestActor())
	require.ErrorIs(t, err, app.ErrDealersChoicePicksMissing)

	require.NoError(t, f.svc.SavePicks(ctx, tx, f.order.ID, f.lineID,
		f.ids(f.coffees[0], f.coffees[1], f.coffees[2]), testutil.TestActor()))

	_, err = orders.FulfillOrder(ctx, tx, f.order.ID, testutil.TestActor())
	require.NoError(t, err)
}

func TestDealersChoice_PickupHandoffRefusedUntilPicksSaved(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := context.Background()
	f := newDealersChoiceFixture(t, tx)

	pickup := testutil.CreateOrder(t, tx, f.customer, f.shipID, f.billID,
		testutil.WithShippingMethod(domain.ShippingMethodPickup),
		testutil.WithPaymentStatus(domain.PaymentStatusCaptured))
	createLineItem(t, tx, pickup.ID, f.variantID, 1)

	_, err := newOrderService().MarkReadyForPickup(ctx, tx, pickup.ID, testutil.TestActor())
	require.ErrorIs(t, err, app.ErrDealersChoicePicksMissing)
}

func TestDealersChoice_OrdersWithoutPacksAreUnaffected(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := context.Background()
	f := newDealersChoiceFixture(t, tx)

	plain := testutil.CreateOrder(t, tx, f.customer, f.shipID, f.billID)
	coffeeVariant := testutil.CreateVariant(t, tx, f.coffees[0].ID)
	createLineItem(t, tx, plain.ID, coffeeVariant.ID, 2)

	_, err := newOrderService().FulfillOrder(ctx, tx, plain.ID, testutil.TestActor())
	require.NoError(t, err)

	plans, err := f.svc.PlanOrderPicks(ctx, tx, plain, mustLineItems(t, tx, plain.ID))
	require.NoError(t, err)
	assert.Empty(t, plans)
}

func TestDealersChoice_SavePicksValidates(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := context.Background()
	f := newDealersChoiceFixture(t, tx)

	err := f.svc.SavePicks(ctx, tx, f.order.ID, f.lineID, f.ids(f.coffees[0], f.coffees[1]), testutil.TestActor())
	assert.ErrorIs(t, err, app.ErrInvalidPicks, "two picks for a three-bag pack")

	err = f.svc.SavePicks(ctx, tx, f.order.ID, f.lineID, f.ids(f.coffees[0], f.coffees[1], f.decaf), testutil.TestActor())
	assert.ErrorIs(t, err, app.ErrInvalidPicks, "decaf in a no-decaf pack")

	err = f.svc.SavePicks(ctx, tx, uuid.New(), f.lineID, f.ids(f.coffees[0], f.coffees[1], f.coffees[2]), testutil.TestActor())
	assert.ErrorIs(t, err, app.ErrOrderNotFound)

	// Duplicates are allowed: a small shelf can mean two of the same.
	require.NoError(t, f.svc.SavePicks(ctx, tx, f.order.ID, f.lineID,
		f.ids(f.coffees[0], f.coffees[0], f.coffees[1]), testutil.TestActor()))
}

func TestDealersChoice_PlanSuggestsThenReflectsSavedPicks(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := context.Background()
	f := newDealersChoiceFixture(t, tx)
	items := mustLineItems(t, tx, f.order.ID)

	plans, err := f.svc.PlanOrderPicks(ctx, tx, f.order, items)
	require.NoError(t, err)
	require.Len(t, plans, 1)
	p := plans[0]
	assert.Nil(t, p.Previous, "first dealer's choice order has no last box")
	assert.Equal(t, 3, p.Required)
	assert.False(t, p.Confirmed)
	assert.Len(t, p.Selected, 3)
	assert.Equal(t, p.Suggested, p.Selected, "unsaved lines preselect the suggestion")
	for _, o := range p.Options {
		assert.NotEqual(t, f.decaf.ID, o.ProductID, "no-decaf pack must not offer decaf")
		assert.NotEqual(t, f.packID, o.ProductID, "a pack is never picked into another pack")
	}

	again, err := f.svc.PlanOrderPicks(ctx, tx, f.order, items)
	require.NoError(t, err)
	assert.Equal(t, p.Suggested, again[0].Suggested, "suggestions are stable across reloads")

	chosen := f.ids(f.coffees[3], f.coffees[2], f.coffees[1])
	require.NoError(t, f.svc.SavePicks(ctx, tx, f.order.ID, f.lineID, chosen, testutil.TestActor()))

	plans, err = f.svc.PlanOrderPicks(ctx, tx, f.order, items)
	require.NoError(t, err)
	assert.True(t, plans[0].Confirmed)
	assert.Equal(t, chosen, plans[0].Selected)
}

func TestDealersChoice_SuggestionsAvoidLastBox(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := context.Background()
	f := newDealersChoiceFixture(t, tx)

	last := f.ids(f.coffees[0], f.coffees[1], f.coffees[2])
	require.NoError(t, f.svc.SavePicks(ctx, tx, f.order.ID, f.lineID, last, testutil.TestActor()))

	// A renewal for the same customer: three bags, and the shelf has three
	// coffees the customer did not just get (Midnight Ride plus two more).
	createCoffee(t, tx, "Grease Monkey", false)
	createCoffee(t, tx, "Daddy-O", false)
	next := testutil.CreateOrder(t, tx, f.customer, f.shipID, f.billID)
	createLineItem(t, tx, next.ID, f.variantID, 1)

	plans, err := f.svc.PlanOrderPicks(ctx, tx, next, mustLineItems(t, tx, next.ID))
	require.NoError(t, err)
	require.Len(t, plans, 1)
	assert.True(t, plans[0].AvoidedPrevious)
	require.NotNil(t, plans[0].Previous, "staff see what went out last time")
	assert.Equal(t, f.order.ID, plans[0].Previous.OrderID)
	assert.Equal(t, f.order.Number, plans[0].Previous.OrderNumber)
	assert.Equal(t, []string{"Cloud 9", "Rebel Blend", "Hot Rod"}, plans[0].Previous.Titles, "last box in bag order")
	for _, id := range plans[0].Suggested {
		assert.NotContains(t, last, id, "last box's coffees stay out while fresh ones remain")
	}
}

func TestDealersChoice_SetPickRule(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := context.Background()
	f := newDealersChoiceFixture(t, tx)

	assert.ErrorIs(t, f.svc.SetPickRule(ctx, tx, f.packID, f.variantID, domain.MaxPickCount+1, false, testutil.TestActor()), app.ErrInvalidPickCount)

	other := testutil.CreateProduct(t, tx)
	assert.ErrorIs(t, f.svc.SetPickRule(ctx, tx, other.ID, f.variantID, 3, false, testutil.TestActor()), app.ErrVariantNotFound,
		"a variant can only be configured through its own product")

	require.NoError(t, f.svc.SetPickRule(ctx, tx, f.packID, f.variantID, 0, false, testutil.TestActor()))
	rules, err := f.svc.ListRulesByProduct(ctx, tx, f.packID)
	require.NoError(t, err)
	assert.Empty(t, rules, "zero bags turns the variant back into an ordinary one")

	_, err = newOrderService().FulfillOrder(ctx, tx, f.order.ID, testutil.TestActor())
	assert.NoError(t, err, "with no rule the order fulfills without picks")
}

func mustLineItems(t *testing.T, tx pgx.Tx, orderID uuid.UUID) []domain.LineItem {
	t.Helper()
	items, err := store.NewOrderStore(nil).ListLineItems(context.Background(), tx, orderID)
	require.NoError(t, err)
	return items
}

func TestDealersChoice_OutForDeliveryRefusedUntilPicksSaved(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := context.Background()
	f := newDealersChoiceFixture(t, tx)

	local := testutil.CreateOrder(t, tx, f.customer, f.shipID, f.billID,
		testutil.WithShippingMethod(domain.ShippingMethodLocalDelivery),
		testutil.WithPaymentStatus(domain.PaymentStatusCaptured))
	line := createLineItem(t, tx, local.ID, f.variantID, 1)
	orders := newOrderService()

	_, err := orders.MarkOutForDelivery(ctx, tx, local.ID, testutil.TestActor())
	require.ErrorIs(t, err, app.ErrDealersChoicePicksMissing)

	require.NoError(t, f.svc.SavePicks(ctx, tx, local.ID, line, f.ids(f.coffees[0], f.coffees[1], f.coffees[2]), testutil.TestActor()))
	_, err = orders.MarkOutForDelivery(ctx, tx, local.ID, testutil.TestActor())
	require.NoError(t, err)
}

// A line ordered twice is two packs: twice the bags, and the gate counts all
// of them, not one pack's worth.
func TestDealersChoice_QuantityMultipliesBags(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := context.Background()
	f := newDealersChoiceFixture(t, tx)

	order := testutil.CreateOrder(t, tx, f.customer, f.shipID, f.billID,
		testutil.WithPaymentStatus(domain.PaymentStatusCaptured))
	line := createLineItem(t, tx, order.ID, f.variantID, 2)

	plans, err := f.svc.PlanOrderPicks(ctx, tx, order, mustLineItems(t, tx, order.ID))
	require.NoError(t, err)
	require.Len(t, plans, 1)
	assert.Equal(t, 6, plans[0].Required)
	assert.Len(t, plans[0].Selected, 6)

	three := f.ids(f.coffees[0], f.coffees[1], f.coffees[2])
	assert.ErrorIs(t, f.svc.SavePicks(ctx, tx, order.ID, line, three, testutil.TestActor()), app.ErrInvalidPicks)

	six := append(three, f.ids(f.coffees[3], f.coffees[0], f.coffees[1])...)
	require.NoError(t, f.svc.SavePicks(ctx, tx, order.ID, line, six, testutil.TestActor()))
	_, err = newOrderService().FulfillOrder(ctx, tx, order.ID, testutil.TestActor())
	require.NoError(t, err)
}

// The gate also refuses a short set saved directly, not only an empty one.
func TestDealersChoice_PartialPicksStillRefused(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := context.Background()
	f := newDealersChoiceFixture(t, tx)

	require.NoError(t, store.NewPickStore(nil).ReplacePicks(ctx, tx, f.lineID, f.ids(f.coffees[0], f.coffees[1])))
	_, err := newOrderService().FulfillOrder(ctx, tx, f.order.ID, testutil.TestActor())
	require.ErrorIs(t, err, app.ErrDealersChoicePicksMissing)
}

// "Last box" is the newest earlier order with picks: never a cancelled or
// refunded one, and never one placed after the order being packed.
func TestDealersChoice_PreviousBoxIsEarlierLiveOrder(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := context.Background()
	f := newDealersChoiceFixture(t, tx)
	base := f.order.PlacedAt

	require.NoError(t, f.svc.SavePicks(ctx, tx, f.order.ID, f.lineID, f.ids(f.coffees[0], f.coffees[1], f.coffees[2]), testutil.TestActor()))

	cancelled := testutil.CreateOrder(t, tx, f.customer, f.shipID, f.billID,
		testutil.WithOrderStatus(domain.OrderStatusCancelled), testutil.WithPlacedAt(base.Add(time.Hour)))
	cLine := createLineItem(t, tx, cancelled.ID, f.variantID, 1)
	require.NoError(t, f.svc.SavePicks(ctx, tx, cancelled.ID, cLine, f.ids(f.coffees[3], f.coffees[3], f.coffees[3]), testutil.TestActor()))

	current := testutil.CreateOrder(t, tx, f.customer, f.shipID, f.billID, testutil.WithPlacedAt(base.Add(2*time.Hour)))
	createLineItem(t, tx, current.ID, f.variantID, 1)

	later := testutil.CreateOrder(t, tx, f.customer, f.shipID, f.billID, testutil.WithPlacedAt(base.Add(3*time.Hour)))
	lLine := createLineItem(t, tx, later.ID, f.variantID, 1)
	require.NoError(t, f.svc.SavePicks(ctx, tx, later.ID, lLine, f.ids(f.coffees[3], f.coffees[2], f.coffees[1]), testutil.TestActor()))

	plans, err := f.svc.PlanOrderPicks(ctx, tx, current, mustLineItems(t, tx, current.ID))
	require.NoError(t, err)
	require.NotNil(t, plans[0].Previous)
	assert.Equal(t, f.order.ID, plans[0].Previous.OrderID, "skips the cancelled order and the later one")
}

// Two packs on one order spread across the shelf rather than each drawing the
// same coffees.
func TestDealersChoice_TwoPacksOnOneOrderDoNotRepeat(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := context.Background()
	f := newDealersChoiceFixture(t, tx)
	createCoffee(t, tx, "Grease Monkey", false)
	createCoffee(t, tx, "Daddy-O", false)

	fresh := testutil.CreateCustomer(t, tx)
	addr := testutil.CreateAddress(t, tx, fresh.ID)
	order := testutil.CreateOrder(t, tx, fresh.ID, addr.ID, addr.ID)
	createLineItem(t, tx, order.ID, f.variantID, 1)
	second := testutil.CreateVariant(t, tx, f.packID)
	require.NoError(t, f.svc.SetPickRule(ctx, tx, f.packID, second.ID, 3, true, testutil.TestActor()))
	createLineItem(t, tx, order.ID, second.ID, 1)

	plans, err := f.svc.PlanOrderPicks(ctx, tx, order, mustLineItems(t, tx, order.ID))
	require.NoError(t, err)
	require.Len(t, plans, 2)
	seen := map[uuid.UUID]bool{}
	for _, p := range plans {
		for _, id := range p.Suggested {
			assert.False(t, seen[id], "six bags from a pool of at least six non-decaf coffees should not repeat")
			seen[id] = true
		}
	}
}

// Route activation is where a local delivery leaves the shop: the driver's
// Delivered tap afterwards is not gated, so activation must refuse a route
// carrying a pack with no saved picks.
func TestDealersChoice_RouteActivationRefusedUntilPicksSaved(t *testing.T) {
	ctx := context.Background()
	seedDeliveryOrders(t, []string{"100 First St", "200 Second St"})
	svc := newPersistingRouteService(t, routeTestGeocoder(), stubSortingRouter(t))
	saved, _, err := svc.PlanAndSaveRoute(ctx, testPool, routeDate,
		app.PlanRouteOptions{Roundtrip: true}, testutil.TestActor())
	require.NoError(t, err)
	require.NotEmpty(t, saved.Stops)

	tx := testutil.NewTestTx(t, testPool)
	f := newDealersChoiceFixture(t, tx)
	line := createLineItem(t, tx, saved.Stops[0].OrderID, f.variantID, 1)

	_, err = svc.ActivateRoute(ctx, tx, saved.Route.ID, testutil.TestActor())
	require.ErrorIs(t, err, app.ErrRoutePicksMissing)

	require.NoError(t, f.svc.SavePicks(ctx, tx, saved.Stops[0].OrderID, line,
		f.ids(f.coffees[0], f.coffees[1], f.coffees[2]), testutil.TestActor()))
	_, err = svc.ActivateRoute(ctx, tx, saved.Route.ID, testutil.TestActor())
	require.NoError(t, err)
}

// The storefront advertises pack sizes from ListOfferedRules, so an archived
// or wholesale-only pack variant must not be offered.
func TestDealersChoice_OfferedRulesSkipUnbuyableVariants(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := context.Background()
	f := newDealersChoiceFixture(t, tx)

	archived := testutil.CreateVariant(t, tx, f.packID)
	require.NoError(t, f.svc.SetPickRule(ctx, tx, f.packID, archived.ID, 12, false, testutil.TestActor()))
	_, err := tx.Exec(ctx, `UPDATE variants SET archived_at = now() WHERE id = $1`, archived.ID)
	require.NoError(t, err)

	wholesaleOnly := testutil.CreateVariant(t, tx, f.packID, testutil.WithChannelAvailability(false, true))
	require.NoError(t, f.svc.SetPickRule(ctx, tx, f.packID, wholesaleOnly.ID, 24, false, testutil.TestActor()))

	offered, err := f.svc.ListOfferedRules(ctx, tx, f.packID)
	require.NoError(t, err)
	require.Len(t, offered, 1)
	assert.Equal(t, f.variantID, offered[0].VariantID)
	assert.Equal(t, 3, offered[0].PickCount)
}
