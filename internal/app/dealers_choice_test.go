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
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

func newDealersChoiceService() *app.DealersChoiceService {
	return app.NewDealersChoiceService(store.NewPickStore(nil), store.NewOrderStore(nil), store.NewCatalogStore(), audit.NewAuditWriter())
}

// createCoffee makes an active, public product with a 12oz variant, which is
// what puts a coffee in the dealer's choice pool. decaf tags it through the
// caffeine-level attribute the storefront filter also reads.
func createCoffee(t *testing.T, tx pgx.Tx, title string, decaf bool) *domain.Product {
	t.Helper()
	ctx := context.Background()
	p := testutil.CreateProduct(t, tx,
		testutil.WithProductTitle(title),
		testutil.WithProductStatus(domain.ProductStatusActive),
		testutil.WithProductVisibility(domain.ProductVisibilityPublic))
	v := testutil.CreateVariant(t, tx, p.ID)

	var optID, valID uuid.UUID
	require.NoError(t, tx.QueryRow(ctx,
		`INSERT INTO product_options (product_id, name) VALUES ($1, 'Size') RETURNING id`, p.ID).Scan(&optID))
	require.NoError(t, tx.QueryRow(ctx,
		`INSERT INTO product_option_values (product_option_id, value) VALUES ($1, '12 oz') RETURNING id`, optID).Scan(&valID))
	_, err := tx.Exec(ctx,
		`INSERT INTO variant_option_values (variant_id, product_option_value_id) VALUES ($1, $2)`, v.ID, valID)
	require.NoError(t, err)

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
		testutil.WithProductStatus(domain.ProductStatusActive))
	f.packID = pack.ID
	f.variantID = testutil.CreateVariant(t, tx, pack.ID).ID
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
