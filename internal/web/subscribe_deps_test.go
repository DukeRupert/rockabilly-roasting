package web

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
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
		store.NewSettingsStore(), store.NewShippingStore(), nil,
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
