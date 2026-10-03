package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// What a second payment intent on one cart does to the coupon on it.
//
// /api/checkout/payment-intent is not a one-shot. Payment.svelte re-runs it
// whenever the shipping method or the pricing version changes — a pickup/
// delivery toggle, a coupon applied or removed — and its own comment says the
// order left behind is expected to sit in awaiting until cleanup cancels it.
//
// What that comment did not account for was the coupon. PlaceOrder redeemed it,
// in the same transaction that created the order, and the only thing that ever
// released it was CancelOrder — which for an abandoned order is the 24-hour
// sweep. So the second intent found the code already spent, by an order the
// same customer was still trying to pay for.
//
// It did not refuse. Phase 1 skips a redeemed coupon (`cc.RedeemedAt == nil`)
// and priced the cart without it, so the customer was quietly charged full
// price for the cart they were shown a discount on, and the code stayed locked
// for a day. A refusal would at least have been legible. That was the discount
// disappearing with nothing said about it — which is why the assertion below is
// on the money, not on the status.
//
// The fix moved redemption to capture: PlaceOrder records the coupon on the
// order and ConfirmCheckoutPayment claims it. See redeemOrderCoupon in
// internal/app/checkout.go, and internal/app/checkout_coupon_lifecycle_test.go
// for the service-level facts. This test is the seam that pins the behaviour —
// it fails with a discount of 0 and an amount of 5000 the moment redemption
// moves back.
//
// These commit, like everything else driving a handler here — see setup_test.go.

type checkoutCouponFixture struct {
	customer  *domain.Customer
	cartID    uuid.UUID
	addressID uuid.UUID
	code      string
	// variantID is the one line in the cart. Exposed so a test can move its
	// catalog price out from under an in-flight checkout — see
	// checkout_failure_mapping_test.go.
	variantID uuid.UUID
}

// newCheckoutCouponFixture builds a customer with a one-line cart and a
// single-use coupon already applied to it — the state a customer is in when
// they reach the payment step having entered a code on the cart page.
func newCheckoutCouponFixture(t *testing.T) checkoutCouponFixture {
	t.Helper()
	ctx := context.Background()
	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx) //nolint:errcheck

	product := testutil.CreateProduct(t, tx)
	variant := testutil.CreateVariant(t, tx, product.ID)
	testutil.SetBasePriceForVariant(t, tx, variant.ID, 5000, "USD")

	customer := testutil.CreateCustomer(t, tx)
	address := testutil.CreateAddress(t, tx, customer.ID,
		testutil.WithAddressPostalCode("59601"), testutil.WithAddressDefault())

	// Percentage off, no minimum: the discount itself is not what is under
	// test, so it is the least conditional one available.
	discount := testutil.CreateDiscount(t, tx,
		testutil.WithDiscountType(domain.DiscountTypePercentage),
		testutil.WithDiscountValue(10),
		testutil.WithDiscountActive(true))
	coupon := testutil.CreateCouponCode(t, tx, discount.ID,
		testutil.WithCouponCode("REUSE-"+uuid.New().String()[:8]))

	carts := app.NewCartService(store.NewCartStore(), store.NewCatalogStore(),
		app.NewPricingService(store.NewPricingStore(), store.NewCustomerStore()),
		app.NewCatalogService(store.NewCatalogStore(), store.NewCustomerStore(),
			audit.NewAuditWriter(), metrics.NewRegistry()))
	cart, err := carts.GetOrCreateCart(ctx, tx, nil)
	require.NoError(t, err)
	_, err = carts.AddItemForCustomer(ctx, tx, cart.ID, variant.ID, 1, customer.ID, "USD")
	require.NoError(t, err)

	orders := app.NewOrderService(store.NewOrderStore(nil), audit.NewAuditWriter(), metrics.NewRegistry())
	_, err = orders.UpdateCartDiscount(ctx, tx, cart.ID, &discount.ID, &coupon.ID)
	require.NoError(t, err)

	require.NoError(t, tx.Commit(ctx))
	return checkoutCouponFixture{
		customer: customer, cartID: cart.ID, addressID: address.ID,
		code: coupon.Code, variantID: variant.ID,
	}
}

// newCheckoutPaymentDeps wires what the retail payment-intent endpoint reaches.
// It is newSubscribeDeps plus the discount service — phase 1 reads the cart's
// applied discount through it — and a payment provider that records rather
// than calls out.
func newCheckoutPaymentDeps(t *testing.T) (*Deps, *fakePaymentProvider) {
	t.Helper()
	d := newSubscribeDeps(t)
	d.DiscountService = app.NewDiscountService(store.NewDiscountStore(),
		audit.NewAuditWriter(), metrics.NewRegistry())
	fake := &fakePaymentProvider{}
	d.PaymentProvider = fake
	return d, fake
}

func (f checkoutCouponFixture) postIntent(t *testing.T, d *Deps, method string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(checkoutPaymentIntentRequest{
		CartID:         f.cartID.String(),
		AddressID:      f.addressID.String(),
		CustomerID:     f.customer.ID.String(),
		ShippingMethod: method,
	})
	require.NoError(t, err)
	r := httptest.NewRequest(http.MethodPost, "/api/checkout/payment-intent", strings.NewReader(string(body)))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	d.handleCheckoutPaymentIntent(w, r)
	return w
}

// A customer who changes their mind about anything on the payment step, while
// holding a coupon, is told the code was used by someone else. It was used by
// them, moments earlier, on an order that does not exist as far as they are
// concerned — and it stays locked until the abandoned-order sweep releases it.
func TestCheckoutPaymentIntent_SecondIntentKeepsTheCoupon(t *testing.T) {
	f := newCheckoutCouponFixture(t)
	d, _ := newCheckoutPaymentDeps(t)

	first := f.postIntent(t, d, "")
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	firstResp := decodeCheckoutIntentResponse(t, first)
	require.Equal(t, 500, firstResp.DiscountTotal, "10% of 5000, so the coupon did apply")

	// The same cart, priced again. Nothing about the customer's intent has
	// changed; the page has simply re-run the endpoint the way it does on any
	// method or pricing change.
	second := f.postIntent(t, d, "")

	assert.Equal(t, http.StatusOK, second.Code,
		"a second intent on the same cart must not be refused over a coupon this customer holds: %s",
		second.Body.String())

	secondResp := decodeCheckoutIntentResponse(t, second)
	assert.Equal(t, firstResp.DiscountTotal, secondResp.DiscountTotal,
		"the coupon has to still be worth what it was worth a moment ago")
	assert.Equal(t, firstResp.Amount, secondResp.Amount,
		"and the customer has to be charged the same total for the same cart")
}
