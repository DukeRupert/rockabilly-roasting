package app_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

func newPricingService() *app.PricingService {
	return app.NewPricingService(store.NewPricingStore(), store.NewCustomerStore()).
		WithSettings(store.NewSettingsStore())
}

func TestResolveForCustomer_BaseWhenNoList(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newPricingService()
	ctx := context.Background()

	customer := testutil.CreateCustomer(t, tx)
	product := testutil.CreateProduct(t, tx)
	variant := testutil.CreateVariant(t, tx, product.ID)
	testutil.SetBasePriceForVariant(t, tx, variant.ID, 1500, "USD")

	got, err := svc.ResolveForCustomer(ctx, tx, variant.ID, customer.ID, "USD")
	require.NoError(t, err)
	testutil.AssertResolvedPrice(t, 1500, int(got))
}

func TestResolveForCustomer_PriceListOverridesBase(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newPricingService()
	ctx := context.Background()

	priceList := testutil.CreatePriceList(t, tx)
	customer := testutil.CreateCustomer(t, tx, testutil.WithPriceList(priceList.ID))
	product := testutil.CreateProduct(t, tx)
	variant := testutil.CreateVariant(t, tx, product.ID)
	testutil.SetBasePriceForVariant(t, tx, variant.ID, 1500, "USD")
	testutil.CreatePriceListPrice(t, tx, priceList.ID, variant.ID, 1100, "USD")

	got, err := svc.ResolveForCustomer(ctx, tx, variant.ID, customer.ID, "USD")
	require.NoError(t, err)
	testutil.AssertResolvedPrice(t, 1100, int(got))
}

func TestResolveForCustomer_FallsBackToBaseWhenVariantMissingFromList(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newPricingService()
	ctx := context.Background()

	priceList := testutil.CreatePriceList(t, tx)
	customer := testutil.CreateCustomer(t, tx, testutil.WithPriceList(priceList.ID))
	product := testutil.CreateProduct(t, tx)
	variant := testutil.CreateVariant(t, tx, product.ID)
	testutil.SetBasePriceForVariant(t, tx, variant.ID, 1500, "USD")
	// Note: no list price for this variant — falls back to base.

	got, err := svc.ResolveForCustomer(ctx, tx, variant.ID, customer.ID, "USD")
	require.NoError(t, err)
	testutil.AssertResolvedPrice(t, 1500, int(got))
}

func TestResolveForCustomer_ErrPriceNotFound(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newPricingService()
	ctx := context.Background()

	priceList := testutil.CreatePriceList(t, tx)
	customer := testutil.CreateCustomer(t, tx, testutil.WithPriceList(priceList.ID))
	product := testutil.CreateProduct(t, tx)
	variant := testutil.CreateVariant(t, tx, product.ID)
	// No base, no list price.

	_, err := svc.ResolveForCustomer(ctx, tx, variant.ID, customer.ID, "USD")
	assert.ErrorIs(t, err, app.ErrPriceNotFound)
}

func TestResolveForCustomer_ErrCustomerNotFound(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newPricingService()
	ctx := context.Background()

	product := testutil.CreateProduct(t, tx)
	variant := testutil.CreateVariant(t, tx, product.ID)
	testutil.SetBasePriceForVariant(t, tx, variant.ID, 1500, "USD")

	_, err := svc.ResolveForCustomer(ctx, tx, variant.ID, uuid.New(), "USD")
	assert.ErrorIs(t, err, app.ErrCustomerNotFound)
}

func TestResolveForCustomerBatch_NoListUsesBase(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newPricingService()
	ctx := context.Background()

	customer := testutil.CreateCustomer(t, tx)
	product := testutil.CreateProduct(t, tx)
	v1 := testutil.CreateVariant(t, tx, product.ID, testutil.WithSKU("SKU-1"))
	v2 := testutil.CreateVariant(t, tx, product.ID, testutil.WithSKU("SKU-2"))
	v3 := testutil.CreateVariant(t, tx, product.ID, testutil.WithSKU("SKU-3"))
	testutil.SetBasePriceForVariant(t, tx, v1.ID, 1000, "USD")
	testutil.SetBasePriceForVariant(t, tx, v2.ID, 2000, "USD")
	testutil.SetBasePriceForVariant(t, tx, v3.ID, 3000, "USD")

	got, err := svc.ResolveForCustomerBatch(ctx, tx, customer.ID, []uuid.UUID{v1.ID, v2.ID, v3.ID}, "USD")
	require.NoError(t, err)
	assert.Equal(t, 1000, got[v1.ID])
	assert.Equal(t, 2000, got[v2.ID])
	assert.Equal(t, 3000, got[v3.ID])
}

func TestResolveForCustomerBatch_PriceListOverridesBase(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newPricingService()
	ctx := context.Background()

	priceList := testutil.CreatePriceList(t, tx)
	customer := testutil.CreateCustomer(t, tx, testutil.WithPriceList(priceList.ID))
	product := testutil.CreateProduct(t, tx)
	v1 := testutil.CreateVariant(t, tx, product.ID, testutil.WithSKU("SKU-1"))
	v2 := testutil.CreateVariant(t, tx, product.ID, testutil.WithSKU("SKU-2"))
	v3 := testutil.CreateVariant(t, tx, product.ID, testutil.WithSKU("SKU-3"))
	for _, v := range []uuid.UUID{v1.ID, v2.ID, v3.ID} {
		testutil.SetBasePriceForVariant(t, tx, v, 9999, "USD")
	}
	testutil.CreatePriceListPrice(t, tx, priceList.ID, v1.ID, 1000, "USD")
	testutil.CreatePriceListPrice(t, tx, priceList.ID, v2.ID, 2000, "USD")
	testutil.CreatePriceListPrice(t, tx, priceList.ID, v3.ID, 3000, "USD")

	got, err := svc.ResolveForCustomerBatch(ctx, tx, customer.ID, []uuid.UUID{v1.ID, v2.ID, v3.ID}, "USD")
	require.NoError(t, err)
	assert.Equal(t, 1000, got[v1.ID])
	assert.Equal(t, 2000, got[v2.ID])
	assert.Equal(t, 3000, got[v3.ID])
}

func TestResolveForCustomerBatch_VariantWithoutBasePrice(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newPricingService()
	ctx := context.Background()

	customer := testutil.CreateCustomer(t, tx)
	product := testutil.CreateProduct(t, tx)
	v1 := testutil.CreateVariant(t, tx, product.ID, testutil.WithSKU("SKU-1"))
	v2 := testutil.CreateVariant(t, tx, product.ID, testutil.WithSKU("SKU-2"))
	v3 := testutil.CreateVariant(t, tx, product.ID, testutil.WithSKU("SKU-3"))
	testutil.SetBasePriceForVariant(t, tx, v1.ID, 1000, "USD")
	testutil.SetBasePriceForVariant(t, tx, v3.ID, 3000, "USD")
	// v2 has no base price — should be omitted from the map.

	got, err := svc.ResolveForCustomerBatch(ctx, tx, customer.ID, []uuid.UUID{v1.ID, v2.ID, v3.ID}, "USD")
	require.NoError(t, err)
	assert.Equal(t, 1000, got[v1.ID])
	assert.Equal(t, 3000, got[v3.ID])
	_, present := got[v2.ID]
	assert.False(t, present, "variant without base price should be omitted from the map")
}

func TestSetPriceListPrice_Inserts(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newPricingService()
	ctx := context.Background()

	priceList := testutil.CreatePriceList(t, tx)
	customer := testutil.CreateCustomer(t, tx, testutil.WithPriceList(priceList.ID))
	product := testutil.CreateProduct(t, tx)
	variant := testutil.CreateVariant(t, tx, product.ID)
	testutil.SetBasePriceForVariant(t, tx, variant.ID, 1500, "USD")

	price, err := svc.SetPriceListPrice(ctx, tx, variant.ID, priceList.ID, 1100, "USD")
	require.NoError(t, err)
	assert.Equal(t, 1100, price.Amount)
	require.NotNil(t, price.PriceListID)
	assert.Equal(t, priceList.ID, *price.PriceListID)

	got, err := svc.ResolveForCustomer(ctx, tx, variant.ID, customer.ID, "USD")
	require.NoError(t, err)
	assert.Equal(t, int64(1100), got)
}

func TestSetPriceListPrice_OverwritesExisting(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newPricingService()
	ctx := context.Background()

	priceList := testutil.CreatePriceList(t, tx)
	product := testutil.CreateProduct(t, tx)
	variant := testutil.CreateVariant(t, tx, product.ID)
	testutil.CreatePriceListPrice(t, tx, priceList.ID, variant.ID, 1100, "USD")

	updated, err := svc.SetPriceListPrice(ctx, tx, variant.ID, priceList.ID, 999, "USD")
	require.NoError(t, err)
	assert.Equal(t, 999, updated.Amount)
}

func TestSetPriceListPrice_RejectsNegative(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newPricingService()
	ctx := context.Background()

	priceList := testutil.CreatePriceList(t, tx)
	product := testutil.CreateProduct(t, tx)
	variant := testutil.CreateVariant(t, tx, product.ID)

	_, err := svc.SetPriceListPrice(ctx, tx, variant.ID, priceList.ID, -1, "USD")
	assert.ErrorIs(t, err, app.ErrInvalidPrice)
}

func TestDeletePriceListPrice_RemovesRow(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newPricingService()
	ctx := context.Background()

	priceList := testutil.CreatePriceList(t, tx)
	customer := testutil.CreateCustomer(t, tx, testutil.WithPriceList(priceList.ID))
	product := testutil.CreateProduct(t, tx)
	variant := testutil.CreateVariant(t, tx, product.ID)
	testutil.SetBasePriceForVariant(t, tx, variant.ID, 1500, "USD")
	testutil.CreatePriceListPrice(t, tx, priceList.ID, variant.ID, 1100, "USD")

	require.NoError(t, svc.DeletePriceListPrice(ctx, tx, variant.ID, priceList.ID, "USD"))

	got, err := svc.ResolveForCustomer(ctx, tx, variant.ID, customer.ID, "USD")
	require.NoError(t, err)
	// Falls back to base.
	assert.Equal(t, int64(1500), got)
}

func TestListPriceListPricesByProduct_GroupsByVariantAndList(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newPricingService()
	ctx := context.Background()

	listA := testutil.CreatePriceList(t, tx, testutil.WithPriceListName("A"))
	listB := testutil.CreatePriceList(t, tx, testutil.WithPriceListName("B"))
	product := testutil.CreateProduct(t, tx)
	v1 := testutil.CreateVariant(t, tx, product.ID, testutil.WithSKU("V1"))
	v2 := testutil.CreateVariant(t, tx, product.ID, testutil.WithSKU("V2"))
	testutil.CreatePriceListPrice(t, tx, listA.ID, v1.ID, 1000, "USD")
	testutil.CreatePriceListPrice(t, tx, listB.ID, v1.ID, 1100, "USD")
	testutil.CreatePriceListPrice(t, tx, listA.ID, v2.ID, 2000, "USD")

	got, err := svc.ListPriceListPricesByProduct(ctx, tx, product.ID, "USD")
	require.NoError(t, err)
	assert.Equal(t, 1000, got[v1.ID][listA.ID])
	assert.Equal(t, 1100, got[v1.ID][listB.ID])
	assert.Equal(t, 2000, got[v2.ID][listA.ID])
	_, hasB := got[v2.ID][listB.ID]
	assert.False(t, hasB)
}

func TestResolveForCustomerBatch_MixedListAndBase(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newPricingService()
	ctx := context.Background()

	priceList := testutil.CreatePriceList(t, tx)
	customer := testutil.CreateCustomer(t, tx, testutil.WithPriceList(priceList.ID))
	product := testutil.CreateProduct(t, tx)
	v1 := testutil.CreateVariant(t, tx, product.ID, testutil.WithSKU("SKU-1"))
	v2 := testutil.CreateVariant(t, tx, product.ID, testutil.WithSKU("SKU-2"))
	v3 := testutil.CreateVariant(t, tx, product.ID, testutil.WithSKU("SKU-3"))
	testutil.SetBasePriceForVariant(t, tx, v1.ID, 1500, "USD")
	testutil.SetBasePriceForVariant(t, tx, v2.ID, 2500, "USD")
	testutil.SetBasePriceForVariant(t, tx, v3.ID, 3500, "USD")
	testutil.CreatePriceListPrice(t, tx, priceList.ID, v1.ID, 1000, "USD")
	testutil.CreatePriceListPrice(t, tx, priceList.ID, v2.ID, 2000, "USD")
	// v3 has only base — list miss should fall back to base.

	got, err := svc.ResolveForCustomerBatch(ctx, tx, customer.ID, []uuid.UUID{v1.ID, v2.ID, v3.ID}, "USD")
	require.NoError(t, err)
	assert.Equal(t, 1000, got[v1.ID])
	assert.Equal(t, 2000, got[v2.ID])
	assert.Equal(t, 3500, got[v3.ID])
}
