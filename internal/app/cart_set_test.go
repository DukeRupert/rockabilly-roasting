package app_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/testutil"
)

// SetItemForCustomer is the write behind the wholesale order sheet and the
// checkout price refresh: it must pin the line to exactly the given quantity
// at the current effective price — never increment, never keep a stale price.
func TestSetItemForCustomer_ReplacesQuantityAndPrice(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCartService()
	ctx := context.Background()

	priceList := testutil.CreatePriceList(t, tx)
	customer := testutil.CreateCustomer(t, tx, testutil.WithPriceList(priceList.ID))
	product := testutil.CreateProduct(t, tx)
	variant := testutil.CreateVariant(t, tx, product.ID)
	testutil.SetBasePriceForVariant(t, tx, variant.ID, 1500, "USD")

	cart, err := svc.GetOrCreateCart(ctx, tx, nil)
	require.NoError(t, err)

	// Line exists at the base price (no list price yet).
	item, err := svc.AddItemForCustomer(ctx, tx, cart.ID, variant.ID, 3, customer.ID, "USD")
	require.NoError(t, err)
	testutil.AssertResolvedPrice(t, 1500, item.UnitPrice)

	// The customer's price list gains a lower price; a set must both replace
	// the quantity (not add to it) and re-resolve to the fresh price.
	testutil.CreatePriceListPrice(t, tx, priceList.ID, variant.ID, 1100, "USD")

	item, err = svc.SetItemForCustomer(ctx, tx, cart.ID, variant.ID, 5, customer.ID, "USD")
	require.NoError(t, err)
	assert.Equal(t, 5, item.Quantity)
	testutil.AssertResolvedPrice(t, 1100, item.UnitPrice)

	items, err := svc.ListItems(ctx, tx, cart.ID)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, 5, items[0].Quantity)
}

func TestSetItemForCustomer_ZeroRemovesLine(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCartService()
	ctx := context.Background()

	customer := testutil.CreateCustomer(t, tx)
	product := testutil.CreateProduct(t, tx)
	variant := testutil.CreateVariant(t, tx, product.ID)
	testutil.SetBasePriceForVariant(t, tx, variant.ID, 1500, "USD")

	cart, err := svc.GetOrCreateCart(ctx, tx, nil)
	require.NoError(t, err)

	_, err = svc.AddItemForCustomer(ctx, tx, cart.ID, variant.ID, 3, customer.ID, "USD")
	require.NoError(t, err)

	item, err := svc.SetItemForCustomer(ctx, tx, cart.ID, variant.ID, 0, customer.ID, "USD")
	require.NoError(t, err)
	assert.Nil(t, item)

	items, err := svc.ListItems(ctx, tx, cart.ID)
	require.NoError(t, err)
	assert.Empty(t, items)

	// Zero for a variant not in the cart is a no-op, not an error — the order
	// sheet submits every blank row.
	_, err = svc.SetItemForCustomer(ctx, tx, cart.ID, variant.ID, 0, customer.ID, "USD")
	require.NoError(t, err)
}

func TestSetItemForCustomer_NegativeQuantity(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCartService()
	ctx := context.Background()

	customer := testutil.CreateCustomer(t, tx)
	product := testutil.CreateProduct(t, tx)
	variant := testutil.CreateVariant(t, tx, product.ID)
	testutil.SetBasePriceForVariant(t, tx, variant.ID, 1500, "USD")

	cart, err := svc.GetOrCreateCart(ctx, tx, nil)
	require.NoError(t, err)

	_, err = svc.SetItemForCustomer(ctx, tx, cart.ID, variant.ID, -1, customer.ID, "USD")
	assert.ErrorIs(t, err, app.ErrInvalidQuantity)
}
