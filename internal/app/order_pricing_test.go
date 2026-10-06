package app_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/testutil"
)

// One function decides what a line costs, and PlaceOrder holds itself to it.
//
// Before this, every order's total was worked out twice: the checkout endpoint
// resolved prices, applied the plan discount, summed the
// lines and quoted that to Stripe — and PlaceOrder then summed the prices it was
// handed and wrote its own answer as the order's subtotal. Two calculations
// agreeing by convention, with nothing comparing them, so whichever one a change
// missed would charge one number and record another.

func TestPriceLines_ResolvesFromTheCatalog(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := context.Background()
	svc := newCheckoutService()

	customer := testutil.CreateCustomer(t, tx)
	product := testutil.CreateProduct(t, tx)
	variant := testutil.CreateVariant(t, tx, product.ID)
	testutil.SetBasePriceForVariant(t, tx, variant.ID, 1800, "USD")

	priced, err := svc.PriceLines(ctx, tx, app.PriceLinesParams{
		CustomerID:   customer.ID,
		CurrencyCode: "USD",
		Lines:        []app.OrderLine{{VariantID: variant.ID, Quantity: 3}},
	})
	require.NoError(t, err)
	require.Len(t, priced.Items, 1)
	assert.Equal(t, 1800, priced.Items[0].UnitPrice)
	assert.Equal(t, 5400, priced.Subtotal, "three at the catalog price")
}

// This shop prices by volume: a line's unit price depends on how many it holds.
// The cart stores the price at the line's quantity, and PlaceOrder compares
// against what PriceLines says, so the resolver has to choose the same rung.
// Pricing every line at one unit would call each tiered order a price that moved
// and refuse it.
func TestPriceLines_PricesAtTheLinesVolumeRung(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := context.Background()
	svc := newCheckoutService()

	// 1100 / 12+ 1000 / 24+ 950, the same ladder the cart tests use.
	customerID, variantID := newTieredCustomerFixture(t, tx)

	for _, tc := range []struct{ qty, want int }{
		{6, 1100}, {12, 1000}, {24, 950},
	} {
		priced, err := svc.PriceLines(ctx, tx, app.PriceLinesParams{
			CustomerID:   customerID,
			CurrencyCode: "USD",
			Lines:        []app.OrderLine{{VariantID: variantID, Quantity: tc.qty}},
		})
		require.NoError(t, err)
		assert.Equal(t, tc.want, priced.Items[0].UnitPrice, "qty %d", tc.qty)
		assert.Equal(t, tc.want*tc.qty, priced.Subtotal, "qty %d", tc.qty)
	}

	t.Run("a plan discount comes off the rung, not the base", func(t *testing.T) {
		priced, err := svc.PriceLines(ctx, tx, app.PriceLinesParams{
			CustomerID:   customerID,
			CurrencyCode: "USD",
			Lines:        []app.OrderLine{{VariantID: variantID, Quantity: 12, PlanDiscountPct: 10}},
		})
		require.NoError(t, err)
		assert.Equal(t, 900, priced.Items[0].UnitPrice, "10% off the 12+ rung of 1000")
		assert.Equal(t, 10, priced.Items[0].PlanDiscountPct,
			"the priced item carries the discount it was priced at, so PlaceOrder can re-check it")
	})
}

// A subscription is priced from the base price, because every renewal is:
// RenewalService charges GetBasePrice less the plan's discount. For a customer
// on a price list the signup resolved through that list would charge the first
// box one number and every box after it another.
func TestPriceLines_ASubscriptionPricesFromTheBaseLikeItsRenewals(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := context.Background()
	svc := newCheckoutService()

	// Base 1500; on the customer's list 1100, and 1000 at 12+.
	customerID, variantID := newTieredCustomerFixture(t, tx)
	lines := []app.OrderLine{{VariantID: variantID, Quantity: 12, PlanDiscountPct: 10}}

	priced, err := svc.PriceLines(ctx, tx, app.PriceLinesParams{
		CustomerID: customerID, CurrencyCode: "USD", Lines: lines, BasePrice: true,
	})
	require.NoError(t, err)
	assert.Equal(t, 1350, priced.Items[0].UnitPrice, "1500 less 10%, not the list's 12+ rung")

	t.Run("and PlaceOrder holds the signup to the same answer", func(t *testing.T) {
		addr := testutil.CreateAddress(t, tx, customerID)
		params := app.PlaceOrderParams{
			CustomerID:        customerID,
			ShippingAddressID: addr.ID,
			BillingAddressID:  addr.ID,
			CurrencyCode:      "USD",
			Items: []app.CartItem{{
				VariantID: variantID, Quantity: 12, UnitPrice: 1350, PlanDiscountPct: 10,
			}},
		}
		_, err := svc.PlaceOrder(ctx, tx, params, testutil.TestActor())
		assert.ErrorIs(t, err, app.ErrPriceMoved,
			"without BasePrice the service resolves the list price and refuses")

		params.BasePrice = true
		order, err := svc.PlaceOrder(ctx, tx, params, testutil.TestActor())
		require.NoError(t, err)
		assert.Equal(t, 16200, order.Subtotal)
	})
}

// A signup can mix plans, and a weekly line and a monthly line carry different
// percentages. One percentage for the whole order would price one of them
// wrong, so the discount is a property of the line.
func TestPriceLines_DiscountsEachLineByItsOwnPlan(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := context.Background()
	svc := newCheckoutService()

	customer := testutil.CreateCustomer(t, tx)
	product := testutil.CreateProduct(t, tx)
	discounted := testutil.CreateVariant(t, tx, product.ID)
	full := testutil.CreateVariant(t, tx, product.ID)
	testutil.SetBasePriceForVariant(t, tx, discounted.ID, 1800, "USD")
	testutil.SetBasePriceForVariant(t, tx, full.ID, 1000, "USD")

	priced, err := svc.PriceLines(ctx, tx, app.PriceLinesParams{
		CustomerID:   customer.ID,
		CurrencyCode: "USD",
		Lines: []app.OrderLine{
			{VariantID: discounted.ID, Quantity: 1, PlanDiscountPct: 10},
			{VariantID: full.ID, Quantity: 1},
		},
	})
	require.NoError(t, err)
	require.Len(t, priced.Items, 2)

	byVariant := map[uuid.UUID]app.CartItem{}
	for _, it := range priced.Items {
		byVariant[it.VariantID] = it
	}
	assert.Equal(t, 1620, byVariant[discounted.ID].UnitPrice, "10% off 1800")
	assert.Equal(t, 1000, byVariant[full.ID].UnitPrice, "no discount on the other line")
	assert.Equal(t, 2620, priced.Subtotal)
}

func TestPriceLines_RefusesWhatItCannotPrice(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := context.Background()
	svc := newCheckoutService()

	customer := testutil.CreateCustomer(t, tx)
	product := testutil.CreateProduct(t, tx)
	priceless := testutil.CreateVariant(t, tx, product.ID)

	_, err := svc.PriceLines(ctx, tx, app.PriceLinesParams{
		CustomerID:   customer.ID,
		CurrencyCode: "USD",
		Lines:        []app.OrderLine{{VariantID: priceless.ID, Quantity: 1}},
	})
	assert.ErrorIs(t, err, app.ErrPriceNotFound,
		"a variant the shop has not priced is not a variant the shop sells")

	_, err = svc.PriceLines(ctx, tx, app.PriceLinesParams{
		CustomerID:   customer.ID,
		CurrencyCode: "USD",
		Lines:        []app.OrderLine{{VariantID: priceless.ID, Quantity: 0}},
	})
	assert.ErrorIs(t, err, app.ErrInvalidQuantity)
}

// The seam the whole arrangement rests on. The caller quoted the payment
// provider from PriceLines; if pricing the same lines again in phase 3 gives a
// different answer, the amount being charged and the order about to be written
// have already diverged, and the only safe move is to place no order.
func TestPlaceOrder_RefusesAPriceThatMoved(t *testing.T) {
	ctx := context.Background()
	svc := newCheckoutService()
	actor := testutil.TestActor()

	t.Run("a stale page priced below the catalog", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		customer := testutil.CreateCustomer(t, tx)
		addr := testutil.CreateAddress(t, tx, customer.ID)
		product := testutil.CreateProduct(t, tx)
		variant := testutil.CreateVariant(t, tx, product.ID)
		testutil.SetBasePriceForVariant(t, tx, variant.ID, 5000, "USD")

		_, err := svc.PlaceOrder(ctx, tx, app.PlaceOrderParams{
			CustomerID:        customer.ID,
			ShippingAddressID: addr.ID,
			BillingAddressID:  addr.ID,
			CurrencyCode:      "USD",
			Items:             []app.CartItem{{VariantID: variant.ID, Quantity: 1, UnitPrice: 1}},
		}, actor)
		assert.ErrorIs(t, err, app.ErrPriceMoved)

		var orders int
		require.NoError(t, tx.QueryRow(ctx,
			`SELECT count(*) FROM orders WHERE customer_id = $1`, customer.ID).Scan(&orders))
		assert.Zero(t, orders, "the refused order left nothing behind")
	})

	t.Run("and above it, which is the merchant's loss rather than the customer's", func(t *testing.T) {
		// Refused in both directions. An order recording more than the card was
		// charged is a refund nobody can explain.
		tx := testutil.NewTestTx(t, testPool)
		customer := testutil.CreateCustomer(t, tx)
		addr := testutil.CreateAddress(t, tx, customer.ID)
		product := testutil.CreateProduct(t, tx)
		variant := testutil.CreateVariant(t, tx, product.ID)
		testutil.SetBasePriceForVariant(t, tx, variant.ID, 5000, "USD")

		_, err := svc.PlaceOrder(ctx, tx, app.PlaceOrderParams{
			CustomerID:        customer.ID,
			ShippingAddressID: addr.ID,
			BillingAddressID:  addr.ID,
			CurrencyCode:      "USD",
			Items:             []app.CartItem{{VariantID: variant.ID, Quantity: 1, UnitPrice: 9000}},
		}, actor)
		assert.ErrorIs(t, err, app.ErrPriceMoved)
	})

	t.Run("a plan discount the service was not told about is a price that moved", func(t *testing.T) {
		// The subscription signup's unit price is legitimately below the
		// catalog's. It is only legitimate because the caller declares the plan
		// it came from — PlanDiscountPct is what separates a discount from a
		// number somebody made up.
		tx := testutil.NewTestTx(t, testPool)
		customer := testutil.CreateCustomer(t, tx)
		addr := testutil.CreateAddress(t, tx, customer.ID)
		product := testutil.CreateProduct(t, tx)
		variant := testutil.CreateVariant(t, tx, product.ID)
		testutil.SetBasePriceForVariant(t, tx, variant.ID, 1800, "USD")

		params := app.PlaceOrderParams{
			CustomerID:        customer.ID,
			ShippingAddressID: addr.ID,
			BillingAddressID:  addr.ID,
			CurrencyCode:      "USD",
			Items:             []app.CartItem{{VariantID: variant.ID, Quantity: 1, UnitPrice: 1620}},
		}

		_, err := svc.PlaceOrder(ctx, tx, params, actor)
		assert.ErrorIs(t, err, app.ErrPriceMoved, "1620 against a catalog price of 1800")

		// Declared, and it places at the discounted price.
		params.Items[0].PlanDiscountPct = 10
		order, err := svc.PlaceOrder(ctx, tx, params, actor)
		require.NoError(t, err)
		assert.Equal(t, 1620, order.Subtotal)
	})

	t.Run("two lines on two discounts, each checked against its own", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		customer := testutil.CreateCustomer(t, tx)
		addr := testutil.CreateAddress(t, tx, customer.ID)
		product := testutil.CreateProduct(t, tx)
		weekly := testutil.CreateVariant(t, tx, product.ID)
		monthly := testutil.CreateVariant(t, tx, product.ID)
		testutil.SetBasePriceForVariant(t, tx, weekly.ID, 1800, "USD")
		testutil.SetBasePriceForVariant(t, tx, monthly.ID, 1000, "USD")

		params := func(monthlyPrice int) app.PlaceOrderParams {
			return app.PlaceOrderParams{
				CustomerID:        customer.ID,
				ShippingAddressID: addr.ID,
				BillingAddressID:  addr.ID,
				CurrencyCode:      "USD",
				Items: []app.CartItem{
					{VariantID: weekly.ID, Quantity: 1, UnitPrice: 1620, PlanDiscountPct: 10},
					{VariantID: monthly.ID, Quantity: 1, UnitPrice: monthlyPrice, PlanDiscountPct: 5},
				},
			}
		}

		// The monthly line quoted at the weekly line's 10% rather than its own 5%.
		_, err := svc.PlaceOrder(ctx, tx, params(900), actor)
		assert.ErrorIs(t, err, app.ErrPriceMoved, "900 is 10% off; this line's plan takes 5%")

		order, err := svc.PlaceOrder(ctx, tx, params(950), actor)
		require.NoError(t, err)
		assert.Equal(t, 2570, order.Subtotal, "1620 + 950")
	})
}
