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

func newShippingExportService() *app.ShippingExportService {
	return app.NewShippingExportService(
		store.NewOrderStore(nil),
		store.NewCustomerStore(),
		store.NewCatalogStore(),
		store.NewFulfillmentStore(),
		store.NewShippingStore(),
	)
}

// Local-fulfillment orders (pickup, local_delivery) must never reach the
// Pirate Ship CSV — the lifecycle for those orders is driven by the admin
// pickup/delivery actions on the order page, not by handing them to the
// carrier label flow. The exclusion lives in loadCandidateOrders and applies
// to both the status-filter path and the explicit-IDs path.
//
// To isolate the filter we create three unfulfilled+captured orders with no
// line items. The filter runs before row building, so the local orders
// disappear silently; the shipped order makes it as far as the row-building
// "no shippable items" skip. Asserting which numbers land in `skipped` is
// what proves the filter — a shipping_method=shipped order without items is
// always reported, but pickup/local_delivery orders are never reported.
func TestShippingExport_ExcludesLocalFulfillment(t *testing.T) {
	ctx := context.Background()
	svc := newShippingExportService()

	t.Run("status filter path", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		custID, shipID, billID := orderFixtures(t, tx)

		shipped := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithShippingMethod(domain.ShippingMethodShipped))
		testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithShippingMethod(domain.ShippingMethodPickup))
		testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithShippingMethod(domain.ShippingMethodLocalDelivery))

		_, skipped, err := svc.BuildPirateShipCSV(ctx, tx, nil)
		require.NoError(t, err)

		assertOnlyShippedOrderConsidered(t, skipped, shipped.Number)
	})

	t.Run("explicit ids path", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		custID, shipID, billID := orderFixtures(t, tx)

		shipped := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithShippingMethod(domain.ShippingMethodShipped))
		pickup := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithShippingMethod(domain.ShippingMethodPickup))
		local := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithShippingMethod(domain.ShippingMethodLocalDelivery))

		_, skipped, err := svc.BuildPirateShipCSV(ctx, tx,
			[]uuid.UUID{shipped.ID, pickup.ID, local.ID})
		require.NoError(t, err)

		assertOnlyShippedOrderConsidered(t, skipped, shipped.Number)
	})
}

// assertOnlyShippedOrderConsidered checks that pickup/local_delivery orders
// were filtered before row building (not in skipped) and that the
// shipping_method=shipped order was the only one row-building considered
// (skipped with "no shippable items" since the test fixtures omit line items).
func assertOnlyShippedOrderConsidered(t *testing.T, skipped []app.SkippedOrder, shippedNumber string) {
	t.Helper()
	require.Len(t, skipped, 1, "only the non-local order should reach row building")
	assert.Equal(t, shippedNumber, skipped[0].Number)
	assert.Equal(t, "no shippable items", skipped[0].Reason)
}
