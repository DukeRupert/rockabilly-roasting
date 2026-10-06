package web

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// The retail cart is priced at base when an item goes in and is never repriced,
// and the checkout page sums its lines from the cart while taking the total
// from /api/checkout/payment-intent. So the endpoint has to price from base
// too, whatever list the customer is on, or the page shows lines that do not
// add up to the charge. Found in review: with PriceLines resolving through the
// customer's list, a customer staff had put on a list priced below base saw
// 5000 on the page and was charged 3000 — and above base, the reverse.
func TestCheckoutPaymentIntent_PricesTheRetailCartFromBaseWhateverTheList(t *testing.T) {
	ctx := t.Context()
	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx) //nolint:errcheck

	product := testutil.CreateProduct(t, tx)
	variant := testutil.CreateVariant(t, tx, product.ID)
	testutil.SetBasePriceForVariant(t, tx, variant.ID, 5000, "USD")

	list := testutil.CreatePriceList(t, tx)
	testutil.CreatePriceListPrice(t, tx, list.ID, variant.ID, 3000, "USD")
	customer := testutil.CreateCustomer(t, tx, testutil.WithPriceList(list.ID))
	address := testutil.CreateAddress(t, tx, customer.ID,
		testutil.WithAddressPostalCode("59601"), testutil.WithAddressDefault())

	// The storefront's add-to-cart path: anonymous, priced at base. The
	// customer is only known later, at the address step.
	carts := app.NewCartService(store.NewCartStore(), store.NewCatalogStore(),
		app.NewPricingService(store.NewPricingStore(), store.NewCustomerStore()),
		app.NewCatalogService(store.NewCatalogStore(), store.NewCustomerStore(),
			audit.NewAuditWriter(), metrics.NewRegistry()))
	cart, err := carts.GetOrCreateCart(ctx, tx, nil)
	require.NoError(t, err)
	item, err := carts.AddItem(ctx, tx, cart.ID, variant.ID, 1)
	require.NoError(t, err)
	require.Equal(t, 5000, item.UnitPrice, "the cart holds the base price; the test needs that to mean anything")
	require.NoError(t, tx.Commit(ctx))

	f := checkoutCouponFixture{customer: customer, cartID: cart.ID, addressID: address.ID, variantID: variant.ID}
	d, fake := newCheckoutPaymentDeps(t)

	w := f.postIntent(t, d, "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var resp checkoutPaymentIntentResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 5000, resp.Subtotal, "the subtotal is what the cart page shows, not the customer's list price")
	assert.Equal(t, resp.Subtotal-resp.DiscountTotal+resp.ShippingTotal+resp.TaxTotal, resp.Amount,
		"the amount is the sum of the parts the page shows")

	req, ok := fake.lastIntent()
	require.True(t, ok, "an intent was created")
	assert.Equal(t, int64(resp.Amount), req.AmountCents, "the provider is quoted the same amount the page is told")
}
