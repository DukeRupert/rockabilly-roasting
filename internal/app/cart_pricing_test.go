package app_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

func newCartService() *app.CartService {
	pricing := newPricingService()
	return app.NewCartService(store.NewCartStore(), store.NewCatalogStore(), pricing, newCatalogService())
}

func TestAddItemForCustomer_UsesPriceListPrice(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCartService()
	ctx := context.Background()

	priceList := testutil.CreatePriceList(t, tx)
	customer := testutil.CreateCustomer(t, tx, testutil.WithPriceList(priceList.ID))
	product := testutil.CreateProduct(t, tx)
	variant := testutil.CreateVariant(t, tx, product.ID)
	testutil.SetBasePriceForVariant(t, tx, variant.ID, 1500, "USD")
	testutil.CreatePriceListPrice(t, tx, priceList.ID, variant.ID, 1100, "USD")

	cart, err := svc.GetOrCreateCart(ctx, tx, nil)
	require.NoError(t, err)

	item, err := svc.AddItemForCustomer(ctx, tx, cart.ID, variant.ID, 3, customer.ID, "USD")
	require.NoError(t, err)
	testutil.AssertResolvedPrice(t, 1100, item.UnitPrice)
	assert.Equal(t, 3, item.Quantity)
}

func TestAddItemForCustomer_FallsBackToBasePrice(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCartService()
	ctx := context.Background()

	priceList := testutil.CreatePriceList(t, tx)
	customer := testutil.CreateCustomer(t, tx, testutil.WithPriceList(priceList.ID))
	product := testutil.CreateProduct(t, tx)
	variant := testutil.CreateVariant(t, tx, product.ID)
	testutil.SetBasePriceForVariant(t, tx, variant.ID, 1500, "USD")
	// No list price for this variant — must fall back to base.

	cart, err := svc.GetOrCreateCart(ctx, tx, nil)
	require.NoError(t, err)

	item, err := svc.AddItemForCustomer(ctx, tx, cart.ID, variant.ID, 1, customer.ID, "USD")
	require.NoError(t, err)
	testutil.AssertResolvedPrice(t, 1500, item.UnitPrice)
}

func TestAddItemForCustomer_RetailCustomer(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCartService()
	ctx := context.Background()

	customer := testutil.CreateCustomer(t, tx)
	product := testutil.CreateProduct(t, tx)
	variant := testutil.CreateVariant(t, tx, product.ID)
	testutil.SetBasePriceForVariant(t, tx, variant.ID, 1500, "USD")

	cart, err := svc.GetOrCreateCart(ctx, tx, nil)
	require.NoError(t, err)

	item, err := svc.AddItemForCustomer(ctx, tx, cart.ID, variant.ID, 1, customer.ID, "USD")
	require.NoError(t, err)
	testutil.AssertResolvedPrice(t, 1500, item.UnitPrice)
}

func TestAddItem_StillUsesBasePrice(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCartService()
	ctx := context.Background()

	// Even with a price list configured against this variant, the retail AddItem
	// path snapshots base price — that's the contract.
	priceList := testutil.CreatePriceList(t, tx)
	product := testutil.CreateProduct(t, tx)
	variant := testutil.CreateVariant(t, tx, product.ID)
	testutil.SetBasePriceForVariant(t, tx, variant.ID, 1500, "USD")
	testutil.CreatePriceListPrice(t, tx, priceList.ID, variant.ID, 999, "USD")

	cart, err := svc.GetOrCreateCart(ctx, tx, nil)
	require.NoError(t, err)

	item, err := svc.AddItem(ctx, tx, cart.ID, variant.ID, 2)
	require.NoError(t, err)
	testutil.AssertResolvedPrice(t, 1500, item.UnitPrice)
}

func TestAddItemForCustomer_ErrInvalidQuantity(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCartService()
	ctx := context.Background()

	customer := testutil.CreateCustomer(t, tx)
	product := testutil.CreateProduct(t, tx)
	variant := testutil.CreateVariant(t, tx, product.ID)
	testutil.SetBasePriceForVariant(t, tx, variant.ID, 1500, "USD")
	cart, err := svc.GetOrCreateCart(ctx, tx, nil)
	require.NoError(t, err)

	for _, qty := range []int{0, -1} {
		_, err := svc.AddItemForCustomer(ctx, tx, cart.ID, variant.ID, qty, customer.ID, "USD")
		assert.ErrorIs(t, err, app.ErrInvalidQuantity, "quantity %d", qty)
	}
}

func TestAddItemForCustomer_ErrPriceNotFound(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCartService()
	ctx := context.Background()

	customer := testutil.CreateCustomer(t, tx)
	product := testutil.CreateProduct(t, tx)
	variant := testutil.CreateVariant(t, tx, product.ID)
	// No base price, no list price.
	cart, err := svc.GetOrCreateCart(ctx, tx, nil)
	require.NoError(t, err)

	_, err = svc.AddItemForCustomer(ctx, tx, cart.ID, variant.ID, 1, customer.ID, "USD")
	assert.ErrorIs(t, err, app.ErrPriceNotFound)
}

func TestAddItem_ArchivedVariant_Rejected(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	cartSvc := newCartService()
	catalogSvc := newCatalogService()
	ctx := context.Background()
	actor := testutil.TestActor()

	customer := testutil.CreateCustomer(t, tx)
	product := testutil.CreateProduct(t, tx)
	variant := testutil.CreateVariant(t, tx, product.ID)
	testutil.SetBasePriceForVariant(t, tx, variant.ID, 1500, "USD")

	_, err := catalogSvc.ArchiveVariant(ctx, tx, variant.ID, actor)
	require.NoError(t, err)

	cart, err := cartSvc.GetOrCreateCart(ctx, tx, nil)
	require.NoError(t, err)

	_, err = cartSvc.AddItem(ctx, tx, cart.ID, variant.ID, 1)
	assert.ErrorIs(t, err, app.ErrVariantArchived)

	_, err = cartSvc.AddItemForCustomer(ctx, tx, cart.ID, variant.ID, 1, customer.ID, "USD")
	assert.ErrorIs(t, err, app.ErrVariantArchived)
}
