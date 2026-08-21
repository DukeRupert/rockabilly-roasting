package app_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/testutil"
)

// QuickOrderCatalog hides variants not available on the wholesale channel (e.g. a 1lb
// retail-only size) while still listing the product's wholesale-available variants.
func TestQuickOrderCatalog_HidesWholesaleUnavailableVariant(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newWholesaleService()
	pricing := newPricingService()
	ctx := context.Background()

	customer := testutil.CreateCustomer(t, tx)
	product := testutil.CreateProduct(t, tx) // public
	retailOnly := testutil.CreateVariant(t, tx, product.ID,
		testutil.WithSKU("RETAIL-1LB"), testutil.WithChannelAvailability(true, false))
	both := testutil.CreateVariant(t, tx, product.ID,
		testutil.WithSKU("WS-3LB"), testutil.WithChannelAvailability(true, true))
	testutil.SetBasePriceForVariant(t, tx, retailOnly.ID, 1000, "USD")
	testutil.SetBasePriceForVariant(t, tx, both.ID, 2500, "USD")

	products, err := svc.QuickOrderCatalog(ctx, tx, customer.ID, pricing, "USD")
	require.NoError(t, err)

	var skus []string
	for _, p := range products {
		if p.ID != product.ID {
			continue
		}
		for _, v := range p.Variants {
			skus = append(skus, v.SKU)
		}
	}
	assert.ElementsMatch(t, []string{"WS-3LB"}, skus,
		"only the wholesale-available variant should appear")
}

// A product whose variants are all wholesale-hidden drops out of the catalog entirely.
func TestQuickOrderCatalog_DropsProductWithNoWholesaleVariants(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newWholesaleService()
	pricing := newPricingService()
	ctx := context.Background()

	customer := testutil.CreateCustomer(t, tx)
	product := testutil.CreateProduct(t, tx)
	v := testutil.CreateVariant(t, tx, product.ID, testutil.WithChannelAvailability(true, false))
	testutil.SetBasePriceForVariant(t, tx, v.ID, 1000, "USD")

	products, err := svc.QuickOrderCatalog(ctx, tx, customer.ID, pricing, "USD")
	require.NoError(t, err)
	for _, p := range products {
		assert.NotEqual(t, product.ID, p.ID,
			"product with only wholesale-hidden variants should not appear")
	}
}

// A private (white-label) product appears in the wholesale catalog only for a customer
// explicitly granted access to it.
func TestQuickOrderCatalog_PrivateProductVisibleToGrantedCustomer(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newWholesaleService()
	pricing := newPricingService()
	ctx := context.Background()

	granted := testutil.CreateCustomer(t, tx)
	other := testutil.CreateCustomer(t, tx)

	product := testutil.CreateProduct(t, tx, testutil.WithProductVisibility(domain.ProductVisibilityPrivate))
	v := testutil.CreateVariant(t, tx, product.ID)
	testutil.SetBasePriceForVariant(t, tx, v.ID, 1000, "USD")
	testutil.AddProductCustomerVisibility(t, tx, product.ID, granted.ID)

	// Granted customer sees it.
	products, err := svc.QuickOrderCatalog(ctx, tx, granted.ID, pricing, "USD")
	require.NoError(t, err)
	found := false
	for _, p := range products {
		if p.ID == product.ID {
			found = true
		}
	}
	assert.True(t, found, "granted customer should see the private product")

	// A different customer does not.
	products, err = svc.QuickOrderCatalog(ctx, tx, other.ID, pricing, "USD")
	require.NoError(t, err)
	for _, p := range products {
		assert.NotEqual(t, product.ID, p.ID,
			"non-granted customer must not see the private product")
	}
}
