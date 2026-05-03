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
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// approveWholesaleCustomer stamps the customer's account_type/wholesale_status to
// the approved state directly. Cribs from the SetWholesale* store methods.
func approveWholesaleCustomer(t *testing.T, tx pgx.Tx, customerID uuid.UUID) {
	t.Helper()
	_, err := tx.Exec(context.Background(),
		`UPDATE customers
		 SET account_type = 'wholesale', wholesale_status = 'approved',
		     company_name = 'Test Co', approved_at = $2, updated_at = now()
		 WHERE id = $1`,
		customerID, time.Now(),
	)
	require.NoError(t, err)
}

func TestQuickOrderCatalog_FiltersByGroup_RestrictedHidden(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newWholesaleService()
	pricing := newPricingService()
	ctx := context.Background()

	customer := testutil.CreateCustomer(t, tx)
	restricted := testutil.CreateProduct(t, tx, testutil.WithProductVisibility(domain.ProductVisibilityRestricted))
	v := testutil.CreateVariant(t, tx, restricted.ID)
	testutil.SetBasePriceForVariant(t, tx, v.ID, 1000, "USD")

	products, err := svc.QuickOrderCatalog(ctx, tx, nil, customer.ID, pricing, "USD")
	require.NoError(t, err)
	for _, p := range products {
		assert.NotEqual(t, restricted.ID, p.ID, "restricted product should be hidden")
	}
}

func TestQuickOrderCatalog_FiltersByGroup_RestrictedShownToMember(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newWholesaleService()
	pricing := newPricingService()
	ctx := context.Background()

	group := testutil.CreateCustomerGroup(t, tx, "")
	customer := testutil.CreateCustomer(t, tx)
	restricted := testutil.CreateProduct(t, tx, testutil.WithProductVisibility(domain.ProductVisibilityRestricted))
	v := testutil.CreateVariant(t, tx, restricted.ID)
	testutil.SetBasePriceForVariant(t, tx, v.ID, 1000, "USD")
	testutil.AddProductGroupVisibility(t, tx, restricted.ID, group.ID)

	products, err := svc.QuickOrderCatalog(ctx, tx, []uuid.UUID{group.ID}, customer.ID, pricing, "USD")
	require.NoError(t, err)
	found := false
	for _, p := range products {
		if p.ID == restricted.ID {
			found = true
		}
	}
	assert.True(t, found, "restricted product should be visible to a member of its assigned group")
}

func TestQuickOrderCatalog_NoGroupShowsPublicAndWholesale(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newWholesaleService()
	pricing := newPricingService()
	ctx := context.Background()

	customer := testutil.CreateCustomer(t, tx)
	public := testutil.CreateProduct(t, tx, testutil.WithProductVisibility(domain.ProductVisibilityPublic))
	wholesale := testutil.CreateProduct(t, tx, testutil.WithProductVisibility(domain.ProductVisibilityWholesale))
	restricted := testutil.CreateProduct(t, tx, testutil.WithProductVisibility(domain.ProductVisibilityRestricted))
	for _, p := range []*domain.Product{public, wholesale, restricted} {
		v := testutil.CreateVariant(t, tx, p.ID)
		testutil.SetBasePriceForVariant(t, tx, v.ID, 1000, "USD")
	}

	products, err := svc.QuickOrderCatalog(ctx, tx, nil, customer.ID, pricing, "USD")
	require.NoError(t, err)

	seen := make(map[uuid.UUID]bool, len(products))
	for _, p := range products {
		seen[p.ID] = true
	}
	assert.True(t, seen[public.ID], "public product should be visible")
	assert.True(t, seen[wholesale.ID], "wholesale product should be visible")
	assert.False(t, seen[restricted.ID], "restricted product should be hidden when no group")
}

func TestQuickOrderCatalog_PricesUsePriceList(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newWholesaleService()
	pricing := newPricingService()
	ctx := context.Background()

	priceList := testutil.CreatePriceList(t, tx)
	customer := testutil.CreateCustomer(t, tx, testutil.WithPriceList(priceList.ID))
	product := testutil.CreateProduct(t, tx, testutil.WithProductVisibility(domain.ProductVisibilityWholesale))
	variant := testutil.CreateVariant(t, tx, product.ID)
	testutil.SetBasePriceForVariant(t, tx, variant.ID, 1500, "USD")
	testutil.CreatePriceListPrice(t, tx, priceList.ID, variant.ID, 1100, "USD")

	products, err := svc.QuickOrderCatalog(ctx, tx, nil, customer.ID, pricing, "USD")
	require.NoError(t, err)
	require.Len(t, products, 1)
	require.Len(t, products[0].Variants, 1)
	testutil.AssertResolvedPrice(t, 1100, products[0].Variants[0].UnitPrice)
}

func TestQuickOrderCatalog_PricesFallBackToBase(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newWholesaleService()
	pricing := newPricingService()
	ctx := context.Background()

	customer := testutil.CreateCustomer(t, tx) // no price list
	product := testutil.CreateProduct(t, tx, testutil.WithProductVisibility(domain.ProductVisibilityWholesale))
	variant := testutil.CreateVariant(t, tx, product.ID)
	testutil.SetBasePriceForVariant(t, tx, variant.ID, 1500, "USD")

	products, err := svc.QuickOrderCatalog(ctx, tx, nil, customer.ID, pricing, "USD")
	require.NoError(t, err)
	require.Len(t, products, 1)
	require.Len(t, products[0].Variants, 1)
	testutil.AssertResolvedPrice(t, 1500, products[0].Variants[0].UnitPrice)
}

// TestPlaceWholesaleOrder_DenormalizesCartPrice proves the cart price is the contract:
// PlaceWholesaleOrder writes CartItem.UnitPrice verbatim to the line item with no
// internal re-resolve.
func TestPlaceWholesaleOrder_DenormalizesCartPrice(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newWholesaleService()
	ctx := context.Background()
	staffID := testutil.CreateStaff(t, tx)
	actor := testutil.TestActorFromStaff(staffID)

	customer := testutil.CreateCustomer(t, tx)
	approveWholesaleCustomer(t, tx, customer.ID)
	shipping := testutil.CreateAddress(t, tx, customer.ID)
	billing := testutil.CreateAddress(t, tx, customer.ID)

	product := testutil.CreateProduct(t, tx, testutil.WithProductVisibility(domain.ProductVisibilityWholesale))
	variant := testutil.CreateVariant(t, tx, product.ID)
	// Stamp a base price that DIFFERS from the cart price — this is the proof:
	// the line item must end up at the cart price (999), not the base (1500).
	testutil.SetBasePriceForVariant(t, tx, variant.ID, 1500, "USD")

	order, err := svc.PlaceWholesaleOrder(ctx, tx, app.PlaceWholesaleOrderParams{
		CustomerID:        customer.ID,
		ShippingAddressID: shipping.ID,
		BillingAddressID:  billing.ID,
		CurrencyCode:      "USD",
		Items: []app.CartItem{
			{VariantID: variant.ID, Quantity: 2, UnitPrice: 999},
		},
	}, actor)
	require.NoError(t, err)

	orders := store.NewOrderStore(nil)
	lines, err := orders.ListLineItems(ctx, tx, order.ID)
	require.NoError(t, err)
	require.Len(t, lines, 1)
	assert.Equal(t, 999, lines[0].UnitPrice)
	assert.Equal(t, 1998, lines[0].Subtotal)
	assert.Equal(t, 1998, order.Subtotal)
}
