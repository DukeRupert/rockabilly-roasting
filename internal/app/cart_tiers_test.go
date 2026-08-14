package app_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/testutil"
)

// The cart's volume-pricing invariant: a line's stored unit price is always the
// customer's price at that line's current quantity. Every write that can change
// a quantity has to hold it.

// tieredCart returns a wholesale customer on a laddered price list
// (1100 / 12+ 1000 / 24+ 950) and an empty cart.
func tieredCart(t *testing.T, tx pgx.Tx, svc *app.CartService) (customerID, variantID, cartID uuid.UUID) {
	t.Helper()
	ctx := context.Background()

	customerID, variantID = newTieredCustomerFixture(t, tx)
	cart, err := svc.GetOrCreateCart(ctx, tx, nil)
	require.NoError(t, err)
	return customerID, variantID, cart.ID
}

func TestSetItemForCustomer_PricesAtTheLineQuantity(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCartService()
	ctx := context.Background()
	customerID, variantID, cartID := tieredCart(t, tx, svc)

	for _, tc := range []struct{ qty, want int }{
		{6, 1100}, {12, 1000}, {24, 950}, {11, 1100},
	} {
		item, err := svc.SetItemForCustomer(ctx, tx, cartID, variantID, tc.qty, customerID, "USD")
		require.NoError(t, err)
		assert.Equal(t, tc.want, item.UnitPrice, "qty %d", tc.qty)
		assert.Equal(t, tc.qty, item.Quantity)
	}
}

func TestAddItemForCustomer_PricesTheResultingQuantity(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCartService()
	ctx := context.Background()
	customerID, variantID, cartID := tieredCart(t, tx, svc)

	first, err := svc.AddItemForCustomer(ctx, tx, cartID, variantID, 12, customerID, "USD")
	require.NoError(t, err)
	assert.Equal(t, 12, first.Quantity)
	assert.Equal(t, 1000, first.UnitPrice)

	// Adding 12 to an existing 12 is 24 units and earns the 24 rung. Pricing the
	// delta would leave the line on the 12 rung while holding 24.
	second, err := svc.AddItemForCustomer(ctx, tx, cartID, variantID, 12, customerID, "USD")
	require.NoError(t, err)
	assert.Equal(t, 24, second.Quantity)
	assert.Equal(t, 950, second.UnitPrice)
}

func TestAddItemForCustomer_DoesNotDoubleTheQuantity(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCartService()
	ctx := context.Background()
	customerID, variantID, cartID := tieredCart(t, tx, svc)

	_, err := svc.AddItemForCustomer(ctx, tx, cartID, variantID, 5, customerID, "USD")
	require.NoError(t, err)
	item, err := svc.AddItemForCustomer(ctx, tx, cartID, variantID, 3, customerID, "USD")
	require.NoError(t, err)

	// The switch from upsert-increment to set semantics must still add, not set.
	assert.Equal(t, 8, item.Quantity)

	items, err := svc.ListItems(ctx, tx, cartID)
	require.NoError(t, err)
	require.Len(t, items, 1, "adding twice must not split the variant across two lines")
}

func TestUpdateItemQuantityForCustomer_RepricesUp(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCartService()
	ctx := context.Background()
	customerID, variantID, cartID := tieredCart(t, tx, svc)

	line, err := svc.SetItemForCustomer(ctx, tx, cartID, variantID, 6, customerID, "USD")
	require.NoError(t, err)
	require.Equal(t, 1100, line.UnitPrice)

	got, err := svc.UpdateItemQuantityForCustomer(ctx, tx, cartID, line.ID, 24, customerID, "USD")
	require.NoError(t, err)
	assert.Equal(t, 24, got.Item.Quantity)
	assert.Equal(t, 950, got.Item.UnitPrice, "raising quantity must earn the rung")
	assert.Nil(t, got.Drop, "going up is not a drop")
}

func TestUpdateItemQuantityForCustomer_RepricesDownAndReportsIt(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCartService()
	ctx := context.Background()
	customerID, variantID, cartID := tieredCart(t, tx, svc)

	line, err := svc.SetItemForCustomer(ctx, tx, cartID, variantID, 24, customerID, "USD")
	require.NoError(t, err)
	require.Equal(t, 950, line.UnitPrice)

	got, err := svc.UpdateItemQuantityForCustomer(ctx, tx, cartID, line.ID, 23, customerID, "USD")
	require.NoError(t, err)

	// Advisory, never blocking: the write succeeded at the higher price and the
	// drop is reported so the buyer can be told.
	assert.Equal(t, 23, got.Item.Quantity)
	assert.Equal(t, 1000, got.Item.UnitPrice)
	require.NotNil(t, got.Drop)
	assert.Equal(t, 950, got.Drop.FromUnitPrice)
	assert.Equal(t, 1000, got.Drop.ToUnitPrice)
	assert.Equal(t, 50, got.Drop.UnitLossCents)
	assert.Equal(t, 24, got.Drop.LostTierMinQty)
}

func TestUpdateItemQuantityForCustomer_SilentWithinARung(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCartService()
	ctx := context.Background()
	customerID, variantID, cartID := tieredCart(t, tx, svc)

	line, err := svc.SetItemForCustomer(ctx, tx, cartID, variantID, 30, customerID, "USD")
	require.NoError(t, err)

	got, err := svc.UpdateItemQuantityForCustomer(ctx, tx, cartID, line.ID, 25, customerID, "USD")
	require.NoError(t, err)
	assert.Equal(t, 950, got.Item.UnitPrice)
	assert.Nil(t, got.Drop, "reducing without crossing a break is not worth a notice")
}

func TestUpdateItemQuantityForCustomer_DropAgreesWithTheCharge(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCartService()
	ctx := context.Background()
	customerID, variantID, cartID := tieredCart(t, tx, svc)

	// Whatever the notice says the buyer now pays must be what the line holds —
	// the whole point of reading both off one ladder.
	for _, to := range []int{23, 12, 11, 1} {
		line, err := svc.SetItemForCustomer(ctx, tx, cartID, variantID, 30, customerID, "USD")
		require.NoError(t, err)

		got, err := svc.UpdateItemQuantityForCustomer(ctx, tx, cartID, line.ID, to, customerID, "USD")
		require.NoError(t, err)
		require.NotNil(t, got.Drop, "30 -> %d crosses a break", to)
		assert.Equal(t, got.Item.UnitPrice, got.Drop.ToUnitPrice, "30 -> %d", to)
	}
}

func TestUpdateItemQuantityForCustomer_RejectsForeignItem(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCartService()
	ctx := context.Background()
	customerID, variantID, cartID := tieredCart(t, tx, svc)

	line, err := svc.SetItemForCustomer(ctx, tx, cartID, variantID, 12, customerID, "USD")
	require.NoError(t, err)

	other, err := svc.GetOrCreateCart(ctx, tx, nil)
	require.NoError(t, err)

	_, err = svc.UpdateItemQuantityForCustomer(ctx, tx, other.ID, line.ID, 24, customerID, "USD")
	assert.ErrorIs(t, err, app.ErrCartNotFound, "ownership is still enforced by cart ID")

	_, err = svc.UpdateItemQuantityForCustomer(ctx, tx, cartID, line.ID, 0, customerID, "USD")
	assert.ErrorIs(t, err, app.ErrInvalidQuantity)
}

func TestRepriceCart_RewritesLinesThatMoved(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCartService()
	pricing := newPricingService()
	ctx := context.Background()
	customerID, variantID, cartID := tieredCart(t, tx, svc)

	line, err := svc.SetItemForCustomer(ctx, tx, cartID, variantID, 12, customerID, "USD")
	require.NoError(t, err)
	require.Equal(t, 1000, line.UnitPrice)

	// Staff re-cut the ladder while this cart sat.
	listID := mustPriceListID(t, tx, customerID)
	require.NoError(t, pricing.SetTierPrice(ctx, tx, variantID, listID, 12, 900, "USD"))

	moved, err := svc.RepriceCart(ctx, tx, cartID, customerID, "USD")
	require.NoError(t, err)
	require.Len(t, moved, 1)
	assert.Equal(t, 900, moved[0].Item.UnitPrice)
	assert.Equal(t, 12, moved[0].Item.Quantity, "quantity is the buyer's, not ours to change")
	assert.Nil(t, moved[0].Drop, "nothing about the buyer's quantity changed")
}

func TestRepriceCart_IsIdempotent(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCartService()
	ctx := context.Background()
	customerID, variantID, cartID := tieredCart(t, tx, svc)

	_, err := svc.SetItemForCustomer(ctx, tx, cartID, variantID, 24, customerID, "USD")
	require.NoError(t, err)

	moved, err := svc.RepriceCart(ctx, tx, cartID, customerID, "USD")
	require.NoError(t, err)
	assert.Empty(t, moved, "a cart already at the right price has nothing to move")

	moved, err = svc.RepriceCart(ctx, tx, cartID, customerID, "USD")
	require.NoError(t, err)
	assert.Empty(t, moved)
}

func TestRepriceCart_EmptyCart(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCartService()
	ctx := context.Background()

	customer := testutil.CreateCustomer(t, tx)
	cart, err := svc.GetOrCreateCart(ctx, tx, nil)
	require.NoError(t, err)

	moved, err := svc.RepriceCart(ctx, tx, cart.ID, customer.ID, "USD")
	require.NoError(t, err)
	assert.Empty(t, moved)
}

func TestRepriceCart_LeavesUntieredLinesAlone(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCartService()
	ctx := context.Background()

	customer := testutil.CreateCustomer(t, tx)
	product := testutil.CreateProduct(t, tx)
	variant := testutil.CreateVariant(t, tx, product.ID)
	testutil.SetBasePriceForVariant(t, tx, variant.ID, 1500, "USD")

	cart, err := svc.GetOrCreateCart(ctx, tx, nil)
	require.NoError(t, err)
	_, err = svc.SetItemForCustomer(ctx, tx, cart.ID, variant.ID, 50, customer.ID, "USD")
	require.NoError(t, err)

	moved, err := svc.RepriceCart(ctx, tx, cart.ID, customer.ID, "USD")
	require.NoError(t, err)
	assert.Empty(t, moved, "a retail line on a base price never moves, at any quantity")
}

func TestUpdateItemQuantity_RetailPathStillWorks(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCartService()
	ctx := context.Background()

	product := testutil.CreateProduct(t, tx)
	variant := testutil.CreateVariant(t, tx, product.ID)
	testutil.SetBasePriceForVariant(t, tx, variant.ID, 1500, "USD")

	cart, err := svc.GetOrCreateCart(ctx, tx, nil)
	require.NoError(t, err)
	item, err := svc.AddItem(ctx, tx, cart.ID, variant.ID, 2)
	require.NoError(t, err)

	// Anonymous carts price off base prices, which are single-rung by schema, so
	// the non-repricing path stays correct for them.
	updated, err := svc.UpdateItemQuantity(ctx, tx, cart.ID, item.ID, 40)
	require.NoError(t, err)
	assert.Equal(t, 40, updated.Quantity)
	assert.Equal(t, 1500, updated.UnitPrice)
}
