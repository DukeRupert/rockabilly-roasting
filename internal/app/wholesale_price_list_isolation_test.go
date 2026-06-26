package app_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// TestWholesalePriceListIsolation_TwoCustomers is the side-by-side proof that two
// approved wholesale customers on two different price lists each see ONLY their own
// list's prices when they purchase — never the other customer's list, and never
// the base price for a variant their list actually prices.
//
// It drives the real purchase path end to end:
//
//	AddItemForCustomer (resolves + snapshots price into the cart)
//	  → ListItems (read back what the customer would see in their cart)
//	    → PlaceWholesaleOrder (denormalizes the cart price onto the order line)
//
// Catalog:
//
//	variant1: base $20.00 | list A $15.00 | list B $12.00   (both lists price it)
//	variant2: base $25.00 | list A $22.00 | (list B absent) (B must fall back to base)
//
// Expected:
//
//	Customer A (list A): v1 $15.00, v2 $22.00
//	Customer B (list B): v1 $12.00, v2 $25.00 (fallback to base)
func TestWholesalePriceListIsolation_TwoCustomers(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := context.Background()

	cartSvc := newCartService()
	wholesaleSvc := newWholesaleService()

	staffID := testutil.CreateStaff(t, tx)
	actor := testutil.TestActorFromStaff(staffID)

	// Two distinct price lists.
	listA := testutil.CreatePriceList(t, tx, testutil.WithPriceListName("List A"))
	listB := testutil.CreatePriceList(t, tx, testutil.WithPriceListName("List B"))

	// Two approved wholesale customers, each on a different list.
	custA := testutil.CreateCustomer(t, tx, testutil.WithPriceList(listA.ID))
	custB := testutil.CreateCustomer(t, tx, testutil.WithPriceList(listB.ID))
	approveWholesaleCustomer(t, tx, custA.ID)
	approveWholesaleCustomer(t, tx, custB.ID)

	// Shared wholesale-visible product with two variants.
	product := testutil.CreateProduct(t, tx, testutil.WithProductVisibility(domain.ProductVisibilityWholesale))
	variant1 := testutil.CreateVariant(t, tx, product.ID, testutil.WithSKU("ISO-V1"))
	variant2 := testutil.CreateVariant(t, tx, product.ID, testutil.WithSKU("ISO-V2"))

	// Base prices.
	testutil.SetBasePriceForVariant(t, tx, variant1.ID, 2000, "USD")
	testutil.SetBasePriceForVariant(t, tx, variant2.ID, 2500, "USD")

	// List A prices both variants.
	testutil.CreatePriceListPrice(t, tx, listA.ID, variant1.ID, 1500, "USD")
	testutil.CreatePriceListPrice(t, tx, listA.ID, variant2.ID, 2200, "USD")

	// List B prices only variant1 — variant2 must fall back to base for B.
	testutil.CreatePriceListPrice(t, tx, listB.ID, variant1.ID, 1200, "USD")

	// buyCart adds both variants to a fresh cart for the given customer through the
	// real customer-scoped cart path, then reads back the snapshotted prices keyed
	// by variant ID — exactly what the storefront cart would show.
	buyCart := func(t *testing.T, customerID uuid.UUID) map[uuid.UUID]int {
		t.Helper()
		cart, err := cartSvc.GetOrCreateCart(ctx, tx, nil)
		require.NoError(t, err)
		_, err = cartSvc.AddItemForCustomer(ctx, tx, cart.ID, variant1.ID, 2, customerID, "USD")
		require.NoError(t, err)
		_, err = cartSvc.AddItemForCustomer(ctx, tx, cart.ID, variant2.ID, 1, customerID, "USD")
		require.NoError(t, err)

		items, err := cartSvc.ListItems(ctx, tx, cart.ID)
		require.NoError(t, err)
		prices := make(map[uuid.UUID]int, len(items))
		for _, ci := range items {
			prices[ci.VariantID] = ci.UnitPrice
		}
		return prices
	}

	// placeOrder turns a customer's resolved cart prices into a wholesale order and
	// returns the order line prices keyed by variant ID, proving isolation survives
	// all the way onto the persisted order.
	placeOrder := func(t *testing.T, customerID uuid.UUID, cartPrices map[uuid.UUID]int) map[uuid.UUID]int {
		t.Helper()
		ship := testutil.CreateAddress(t, tx, customerID)
		bill := testutil.CreateAddress(t, tx, customerID)
		order, err := wholesaleSvc.PlaceWholesaleOrder(ctx, tx, app.PlaceWholesaleOrderParams{
			CustomerID:        customerID,
			ShippingAddressID: ship.ID,
			BillingAddressID:  bill.ID,
			CurrencyCode:      "USD",
			Items: []app.CartItem{
				{VariantID: variant1.ID, Quantity: 2, UnitPrice: cartPrices[variant1.ID]},
				{VariantID: variant2.ID, Quantity: 1, UnitPrice: cartPrices[variant2.ID]},
			},
		}, actor)
		require.NoError(t, err)

		lines, err := store.NewOrderStore(nil).ListLineItems(ctx, tx, order.ID)
		require.NoError(t, err)
		prices := make(map[uuid.UUID]int, len(lines))
		for _, l := range lines {
			prices[l.VariantID] = l.UnitPrice
		}
		return prices
	}

	// --- Customer A sees ONLY list A prices ---
	t.Run("customer A on list A", func(t *testing.T) {
		cart := buyCart(t, custA.ID)
		assert.Equal(t, 1500, cart[variant1.ID], "A v1 should be list A price ($15)")
		assert.Equal(t, 2200, cart[variant2.ID], "A v2 should be list A price ($22)")
		// Never list B's price or the base for a variant list A prices.
		assert.NotEqual(t, 1200, cart[variant1.ID], "A must not see list B's v1 price")
		assert.NotEqual(t, 2000, cart[variant1.ID], "A must not see v1 base price")

		lines := placeOrder(t, custA.ID, cart)
		assert.Equal(t, 1500, lines[variant1.ID], "A order line v1 = list A price")
		assert.Equal(t, 2200, lines[variant2.ID], "A order line v2 = list A price")
	})

	// --- Customer B sees ONLY list B prices, falling back to base where B is silent ---
	t.Run("customer B on list B", func(t *testing.T) {
		cart := buyCart(t, custB.ID)
		assert.Equal(t, 1200, cart[variant1.ID], "B v1 should be list B price ($12)")
		assert.Equal(t, 2500, cart[variant2.ID], "B v2 not on list B → base price ($25)")
		// Never list A's prices.
		assert.NotEqual(t, 1500, cart[variant1.ID], "B must not see list A's v1 price")
		assert.NotEqual(t, 2200, cart[variant2.ID], "B must not see list A's v2 price")

		lines := placeOrder(t, custB.ID, cart)
		assert.Equal(t, 1200, lines[variant1.ID], "B order line v1 = list B price")
		assert.Equal(t, 2500, lines[variant2.ID], "B order line v2 = base price")
	})
}
