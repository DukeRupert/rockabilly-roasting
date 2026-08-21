package app_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/testutil"
)

// The anonymous/retail add path may only add public products, even by a guessed
// variant ID for a non-public product.
func TestAddItem_RejectsNonPublicForAnonymous(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCartService()
	ctx := context.Background()

	product := testutil.CreateProduct(t, tx, testutil.WithProductVisibility(domain.ProductVisibilityWholesale))
	variant := testutil.CreateVariant(t, tx, product.ID)
	testutil.SetBasePriceForVariant(t, tx, variant.ID, 1500, "USD")

	cart, err := svc.GetOrCreateCart(ctx, tx, nil)
	require.NoError(t, err)

	_, err = svc.AddItem(ctx, tx, cart.ID, variant.ID, 1)
	require.ErrorIs(t, err, app.ErrProductNotAccessible)
}

func TestAddItem_AllowsPublicForAnonymous(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCartService()
	ctx := context.Background()

	product := testutil.CreateProduct(t, tx) // defaults to public
	variant := testutil.CreateVariant(t, tx, product.ID)
	testutil.SetBasePriceForVariant(t, tx, variant.ID, 1500, "USD")

	cart, err := svc.GetOrCreateCart(ctx, tx, nil)
	require.NoError(t, err)

	_, err = svc.AddItem(ctx, tx, cart.ID, variant.ID, 1)
	require.NoError(t, err)
}

// A private product is only purchasable by the specific customers granted access —
// the white-label case.
func TestAddItemForCustomer_PrivateRequiresGrant(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCartService()
	ctx := context.Background()

	// account_type / wholesale_status are not settable via the fixture, so promote
	// the customer to approved-wholesale directly.
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

	// Not granted access → rejected, even as an approved wholesale account.
	_, err = svc.AddItemForCustomer(ctx, tx, cart.ID, variant.ID, 1, customer.ID, "USD")
	require.ErrorIs(t, err, app.ErrProductNotAccessible)

	// Granting this customer access makes it purchasable.
	testutil.AddProductCustomerVisibility(t, tx, product.ID, customer.ID)

	item, err := svc.AddItemForCustomer(ctx, tx, cart.ID, variant.ID, 1, customer.ID, "USD")
	require.NoError(t, err)
	require.Equal(t, 1500, item.UnitPrice)
}
