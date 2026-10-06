package app_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/testutil"
)

// What PlaceOrder must not take on faith.
//
// PlaceOrderParams is not internal input: the checkout endpoints build it from
// the request body, and POST /api/checkout/payment-intent reads customer_id and
// address_id straight out of the JSON. Two of its fields used to be copied into
// the order unexamined, and the subtotal loop multiplied the line numbers
// without asking.
//
// Guards added in "app: stop PlaceOrder trusting its caller's line items and
// address ids"; these are the tests that commit said were coming. They belong at
// the service rather than the handler because the handler is not the only caller
// — a gate living in one handler is a gate the next order path walks around.
//
// The third field, UnitPrice, is no longer trusted either, and is covered in
// order_pricing_test.go: PlaceOrder prices its own lines through
// CheckoutService.PriceLines and refuses an order whose prices have moved.

// TestPlaceOrder_RefusesAnotherCustomersAddress is the ownership half.
//
// A made-up address id fails the foreign key loudly, so the dangerous input is
// another customer's *real* address: it satisfies the constraint, and the order
// used to ship there and read back off the order afterwards. Enforced by scoping
// the read, which is how every other piece of customer data in this codebase is
// enforced, and answered as not-found rather than forbidden — from the caller's
// side an address they do not own is one that does not exist, and saying
// otherwise confirms the id to whoever guessed it.
func TestPlaceOrder_RefusesAnotherCustomersAddress(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := context.Background()
	svc := newCheckoutService()
	actor := testutil.TestActor()

	buyer := testutil.CreateCustomer(t, tx)
	buyerAddr := testutil.CreateAddress(t, tx, buyer.ID)
	stranger := testutil.CreateCustomer(t, tx)
	strangerAddr := testutil.CreateAddress(t, tx, stranger.ID)

	product := testutil.CreateProduct(t, tx)
	variant := testutil.CreateVariant(t, tx, product.ID)
	testutil.SetBasePriceForVariant(t, tx, variant.ID, 5000, "USD")
	items := []app.CartItem{{VariantID: variant.ID, Quantity: 1, UnitPrice: 5000}}

	t.Run("shipping to it", func(t *testing.T) {
		_, err := svc.PlaceOrder(ctx, tx, app.PlaceOrderParams{
			CustomerID:        buyer.ID,
			ShippingAddressID: strangerAddr.ID,
			BillingAddressID:  buyerAddr.ID,
			CurrencyCode:      "USD",
			Items:             items,
		}, actor)
		assert.ErrorIs(t, err, app.ErrAddressNotFound,
			"an address that is not this customer's is not an address this customer has")
	})

	t.Run("billing to it", func(t *testing.T) {
		// Billing matters less to the parcel and more to the paper trail: the
		// admin order page and the receipt both render it. It is also the one
		// the shipping-equals-billing shortcut could skip.
		_, err := svc.PlaceOrder(ctx, tx, app.PlaceOrderParams{
			CustomerID:        buyer.ID,
			ShippingAddressID: buyerAddr.ID,
			BillingAddressID:  strangerAddr.ID,
			CurrencyCode:      "USD",
			Items:             items,
		}, actor)
		assert.ErrorIs(t, err, app.ErrAddressNotFound)
	})

	t.Run("their own address places", func(t *testing.T) {
		// The control. Without it the two refusals above are satisfied by a
		// guard that refuses everything.
		order, err := svc.PlaceOrder(ctx, tx, app.PlaceOrderParams{
			CustomerID:        buyer.ID,
			ShippingAddressID: buyerAddr.ID,
			BillingAddressID:  buyerAddr.ID,
			CurrencyCode:      "USD",
			Items:             items,
		}, actor)
		require.NoError(t, err)
		assert.Equal(t, 5000, order.Subtotal)
	})
}

// TestPlaceOrder_RefusesANonPositiveLine is the line-shape half.
//
// The two cases are not symmetrical. A zero quantity charges nothing for goods
// the order still ships. A negative unit price is a credit, which is the one
// that costs money. Both sentinels already existed as the cart's answer
// (cart_pricing_test.go); this asks the order path the same question.
func TestPlaceOrder_RefusesANonPositiveLine(t *testing.T) {
	ctx := context.Background()
	svc := newCheckoutService()
	actor := testutil.TestActor()

	for _, tc := range []struct {
		name string
		item app.CartItem
		want error
	}{
		{"quantity zero", app.CartItem{Quantity: 0, UnitPrice: 5000}, app.ErrInvalidQuantity},
		{"quantity negative", app.CartItem{Quantity: -1, UnitPrice: 5000}, app.ErrInvalidQuantity},
		{"price negative", app.CartItem{Quantity: 1, UnitPrice: -5000}, app.ErrInvalidPrice},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := testutil.NewTestTx(t, testPool)
			customer := testutil.CreateCustomer(t, tx)
			addr := testutil.CreateAddress(t, tx, customer.ID)
			product := testutil.CreateProduct(t, tx)
			variant := testutil.CreateVariant(t, tx, product.ID)
			testutil.SetBasePriceForVariant(t, tx, variant.ID, 5000, "USD")

			item := tc.item
			item.VariantID = variant.ID
			_, err := svc.PlaceOrder(ctx, tx, app.PlaceOrderParams{
				CustomerID:        customer.ID,
				ShippingAddressID: addr.ID,
				BillingAddressID:  addr.ID,
				CurrencyCode:      "USD",
				Items:             []app.CartItem{item},
			}, actor)
			assert.ErrorIs(t, err, tc.want)
		})
	}
}

// TestPlaceOrder_RefusesALineThatPaysForAnother is why the refusal is per line
// and not on the total.
//
// Each line is priced on its own and the subtotal is their sum, so two lines
// that cancel out place an order for real goods at a total of zero — and a total
// that comes out positive says nothing about how it got there. A guard on the
// subtotal passes both of these.
func TestPlaceOrder_RefusesALineThatPaysForAnother(t *testing.T) {
	ctx := context.Background()
	svc := newCheckoutService()
	actor := testutil.TestActor()

	for _, tc := range []struct {
		name  string
		items func(variantID func() app.CartItem) []app.CartItem
	}{
		{
			name: "cancelling out to zero",
			items: func(line func() app.CartItem) []app.CartItem {
				a, b := line(), line()
				a.Quantity, a.UnitPrice = 2, 5000
				b.Quantity, b.UnitPrice = -2, 5000
				return []app.CartItem{a, b}
			},
		},
		{
			name: "a credit line hiding inside an ordinary total",
			items: func(line func() app.CartItem) []app.CartItem {
				// Sums to 1000, which is a perfectly plausible order total.
				a, b := line(), line()
				a.Quantity, a.UnitPrice = 2, 5000
				b.Quantity, b.UnitPrice = 1, -9000
				return []app.CartItem{a, b}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := testutil.NewTestTx(t, testPool)
			customer := testutil.CreateCustomer(t, tx)
			addr := testutil.CreateAddress(t, tx, customer.ID)
			product := testutil.CreateProduct(t, tx)
			variant := testutil.CreateVariant(t, tx, product.ID)
			testutil.SetBasePriceForVariant(t, tx, variant.ID, 5000, "USD")
			line := func() app.CartItem { return app.CartItem{VariantID: variant.ID} }

			_, err := svc.PlaceOrder(ctx, tx, app.PlaceOrderParams{
				CustomerID:        customer.ID,
				ShippingAddressID: addr.ID,
				BillingAddressID:  addr.ID,
				CurrencyCode:      "USD",
				Items:             tc.items(line),
			}, actor)
			require.Error(t, err, "a credit line is not an order")

			// And nothing was written on the way to refusing. PlaceOrder runs
			// inside its caller's transaction, so a half-built order would
			// survive alongside a returned error.
			var orders int
			require.NoError(t, tx.QueryRow(ctx,
				`SELECT count(*) FROM orders WHERE customer_id = $1`, customer.ID).Scan(&orders))
			assert.Zero(t, orders, "the refused order left nothing behind")
		})
	}
}
