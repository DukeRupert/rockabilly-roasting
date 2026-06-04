package app_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/testutil"
)

// A variant hidden from the wholesale channel (e.g. a 1lb size) is refused at
// wholesale add-to-cart, but the same variant remains addable on the retail storefront.
func TestAddItem_ChannelAvailability_WholesaleHidden(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCartService()
	ctx := context.Background()

	customer := testutil.CreateCustomer(t, tx)
	_, err := tx.Exec(ctx,
		"UPDATE customers SET account_type = 'wholesale', wholesale_status = 'approved' WHERE id = $1",
		customer.ID)
	require.NoError(t, err)

	// Public product so retail (anonymous) can also add it; variant is retail-only.
	product := testutil.CreateProduct(t, tx)
	variant := testutil.CreateVariant(t, tx, product.ID, testutil.WithChannelAvailability(true, false))
	testutil.SetBasePriceForVariant(t, tx, variant.ID, 1500, "USD")

	cart, err := svc.GetOrCreateCart(ctx, tx, nil)
	require.NoError(t, err)

	// Wholesale customer cannot add a wholesale-hidden variant.
	_, err = svc.AddItemForCustomer(ctx, tx, cart.ID, variant.ID, 1, customer.ID, "USD")
	require.ErrorIs(t, err, app.ErrVariantNotInChannel)

	// Retail/anonymous add path still works for the same variant.
	item, err := svc.AddItem(ctx, tx, cart.ID, variant.ID, 1)
	require.NoError(t, err)
	require.Equal(t, 1500, item.UnitPrice)
}

// A variant hidden from the retail channel is refused on the anonymous/retail add path.
func TestAddItem_ChannelAvailability_RetailHidden(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCartService()
	ctx := context.Background()

	product := testutil.CreateProduct(t, tx) // public
	variant := testutil.CreateVariant(t, tx, product.ID, testutil.WithChannelAvailability(false, true))
	testutil.SetBasePriceForVariant(t, tx, variant.ID, 1500, "USD")

	cart, err := svc.GetOrCreateCart(ctx, tx, nil)
	require.NoError(t, err)

	_, err = svc.AddItem(ctx, tx, cart.ID, variant.ID, 1)
	require.ErrorIs(t, err, app.ErrVariantNotInChannel)
}

// A private (white-label) product is only purchasable by a customer explicitly granted
// access to it.
func TestAddItemForCustomer_PrivateProductRequiresGrant(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCartService()
	ctx := context.Background()

	customer := testutil.CreateCustomer(t, tx)
	_, err := tx.Exec(ctx,
		"UPDATE customers SET account_type = 'wholesale', wholesale_status = 'approved' WHERE id = $1",
		customer.ID)
	require.NoError(t, err)

	product := testutil.CreateProduct(t, tx, testutil.WithProductVisibility(domain.ProductVisibilityPrivate))
	variant := testutil.CreateVariant(t, tx, product.ID)
	testutil.SetBasePriceForVariant(t, tx, variant.ID, 1500, "USD")

	cart, err := svc.GetOrCreateCart(ctx, tx, nil)
	require.NoError(t, err)

	// No grant yet → not accessible.
	_, err = svc.AddItemForCustomer(ctx, tx, cart.ID, variant.ID, 1, customer.ID, "USD")
	require.ErrorIs(t, err, app.ErrProductNotAccessible)

	// Granting this customer access makes it purchasable.
	testutil.AddProductCustomerVisibility(t, tx, product.ID, customer.ID)

	item, err := svc.AddItemForCustomer(ctx, tx, cart.ID, variant.ID, 1, customer.ID, "USD")
	require.NoError(t, err)
	require.Equal(t, 1500, item.UnitPrice)
}
