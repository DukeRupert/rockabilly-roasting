package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// newSubscribeDeps wires what the subscribe and checkout payment-intent
// endpoints reach for. Ported from hiri-core, where it lives beside the recipe
// tests; this shop has no recipes, so the recipe wiring is left out.
//
// OrderService here is bare where production chains several decorators, and
// CheckoutService is built with a nil payments.Provider. Both are enough for
// these endpoints and neither is enough for the next one, so a test that needs
// a decorator should add it rather than assume it is there.
//
// The nil inside CheckoutService is a second payment seam, and setting
// Deps.PaymentProvider does not reach it: a charge issued through the checkout
// service panics rather than being recorded by the fake. Nothing on this path
// issues one.
//
// Deps.PaymentProvider is deliberately left nil too, so a test meant to stop
// before the card panics if it ever does not. A test that means to go past
// that point sets it to a fakePaymentProvider.
func newSubscribeDeps(t *testing.T) *Deps {
	t.Helper()
	d := newTestDeps()
	d.SubscriptionService = app.NewSubscriptionService(
		store.NewSubscriptionStore(nil), store.NewOrderStore(nil),
		audit.NewAuditWriter(), metrics.NewRegistry(),
	).WithCatalog(store.NewCatalogStore(), store.NewPricingStore())
	d.ModuleService = app.NewModuleService(store.NewModuleStore(), audit.NewAuditWriter())
	d.MerchantTZ = time.UTC

	orderStore := store.NewOrderStore(nil)
	d.OrderService = app.NewOrderService(orderStore, audit.NewAuditWriter(), metrics.NewRegistry())
	d.CustomerService = app.NewCustomerService(store.NewCustomerStore(), audit.NewAuditWriter(), metrics.NewRegistry())
	d.CheckoutService = app.NewCheckoutService(
		orderStore, store.NewCustomerStore(), store.NewDiscountStore(),
		store.NewSettingsStore(), store.NewShippingStore(), nil, d.PricingService,
		audit.NewAuditWriter(), metrics.NewRegistry(),
	).WithMerchantTZ(time.UTC)
	d.Metrics = metrics.NewRegistry()

	ctx := context.Background()
	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx) //nolint:errcheck
	require.NoError(t, d.ModuleService.Refresh(ctx, tx))
	return d
}

// decodeCheckoutIntentResponse reads the JSON a payment-intent endpoint wrote.
// In hiri-core it lives in subscribe_payment_intent_test.go, whose tests are
// about recipe signups and were not ported.
func decodeCheckoutIntentResponse(t *testing.T, w *httptest.ResponseRecorder) checkoutPaymentIntentResponse {
	t.Helper()
	var resp checkoutPaymentIntentResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp
}

// subscribeFixture is a plan and a priced variant a signup can name, committed
// so a handler's own transaction can read them.
type subscribeFixture struct {
	variantID uuid.UUID
	planID    uuid.UUID
}

func newSubscribeFixture(t *testing.T) subscribeFixture {
	t.Helper()
	ctx := context.Background()
	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx) //nolint:errcheck

	product := testutil.CreateProduct(t, tx)
	variant := testutil.CreateVariant(t, tx, product.ID)
	testutil.SetBasePriceForVariant(t, tx, variant.ID, 1800, "USD")

	plan, err := store.NewSubscriptionStore(nil).CreatePlan(ctx, tx, store.CreatePlanParams{
		Name:          "Monthly",
		Interval:      domain.SubscriptionIntervalEvery30Days,
		IntervalCount: 1,
		IsActive:      true,
		DiscountPct:   10,
	})
	require.NoError(t, err)

	require.NoError(t, tx.Commit(ctx))
	return subscribeFixture{variantID: variant.ID, planID: plan.ID}
}

// subscribePaymentIntentBody builds a signup request body. The address is the
// same in every case: none of these turn on it, and varying it would only make
// the shipping and tax numbers harder to reason about. The email is fresh per
// call, so each signup is a new guest rather than whoever the last test made.
func subscribePaymentIntentBody(planID, variantID uuid.UUID, quantity int, opts ...func(map[string]any)) string {
	body := map[string]any{
		"plan_id":     planID.String(),
		"variant_id":  variantID.String(),
		"quantity":    quantity,
		"email":       "signup-" + uuid.NewString()[:8] + "@example.test",
		"first_name":  "Ada",
		"last_name":   "Byron",
		"line1":       "1 Main St",
		"city":        "Helena",
		"state":       "MT",
		"postal_code": "59601",
		"country":     "US",
	}
	for _, opt := range opts {
		opt(body)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		panic(err) // a literal map; unmarshalable means this file is wrong
	}
	return string(raw)
}

// postSubscribePaymentIntent drives the handler as a signed-out visitor.
func postSubscribePaymentIntent(t *testing.T, d *Deps, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/subscribe/payment-intent", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	d.handleSubscribePaymentIntent(w, r)
	return w
}

// newSubscribePaymentDeps is newSubscribeDeps with a payment provider that
// records rather than calls out.
func newSubscribePaymentDeps(t *testing.T) (*Deps, *fakePaymentProvider) {
	t.Helper()
	d := newSubscribeDeps(t)
	fake := &fakePaymentProvider{}
	d.PaymentProvider = fake
	return d, fake
}

// withFlatShippingRate configures a flat rate for one test and restores what
// was there afterwards. The config is a single instance-wide row, so this is
// only safe because nothing in this package runs in parallel — see the note in
// setup_test.go about not adding t.Parallel() to a test that writes.
func withFlatShippingRate(t *testing.T, d *Deps, cents int) {
	t.Helper()
	set := func(cfg domain.ShippingConfig) {
		ctx := context.Background()
		tx, err := testPool.Begin(ctx)
		require.NoError(t, err)
		require.NoError(t, d.CheckoutService.UpdateShippingConfig(ctx, tx, cfg, testutil.TestActor()))
		require.NoError(t, tx.Commit(ctx))
	}

	ctx := context.Background()
	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	before, err := d.CheckoutService.GetShippingConfig(ctx, tx)
	require.NoError(t, err)
	require.NoError(t, tx.Rollback(ctx))

	restore := *before
	t.Cleanup(func() { set(restore) })

	cfg := *before
	cfg.FlatRateCents = cents
	// No threshold: a waived rate is a rate that never reaches the card, which
	// is the case the caller exists to rule out.
	cfg.FreeShippingThreshold = nil
	set(cfg)
}

func countOrders(t *testing.T, d *Deps, customerID uuid.UUID) int {
	t.Helper()
	ctx := context.Background()
	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx) //nolint:errcheck
	n, err := d.OrderService.CountCustomerOrders(ctx, tx, customerID)
	require.NoError(t, err)
	return n
}

// piIDFromClientSecret recovers the intent id from the client secret the
// handler returns, which is the only place the response carries it.
//
// This works on the fake's secrets, which are "<id>_secret", and on nothing
// else: a real Stripe secret is "<id>_secret_<random>" and would come back
// from here unchanged. It is a test helper reading test output, not a parser.
func piIDFromClientSecret(secret string) string {
	return strings.TrimSuffix(secret, "_secret")
}
