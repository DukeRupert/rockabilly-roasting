package app_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// newLoadListOrderService builds a bare OrderService — the load list is a pure
// read, so none of the optional collaborators are needed.
func newLoadListOrderService() *app.OrderService {
	return app.NewOrderService(store.NewOrderStore(nil), audit.NewAuditWriter(), metrics.NewRegistry())
}

// setVariantWeight sets (or clears, with nil) a variant's shipping weight.
// The load list's pound totals come straight off this column, so the tests
// need to control it directly — there's no fixture option for it.
func setVariantWeight(t *testing.T, tx pgx.Tx, variantID uuid.UUID, grams *int) {
	t.Helper()
	_, err := tx.Exec(context.Background(),
		`UPDATE variants SET weight_grams = $2 WHERE id = $1`, variantID, grams)
	require.NoError(t, err)
}

// addLineItem attaches a line item to an order. Prices are irrelevant to the
// load list (it counts coffee, not money) so they're left at zero.
func addLineItem(t *testing.T, tx pgx.Tx, orderID, variantID uuid.UUID, qty int) {
	t.Helper()
	_, err := tx.Exec(context.Background(),
		`INSERT INTO line_items (id, order_id, variant_id, quantity, unit_price, subtotal, discount_total, tax_total, total, metadata)
		 VALUES ($1, $2, $3, $4, 0, 0, 0, 0, 0, '{}'::jsonb)`,
		uuid.New(), orderID, variantID, qty)
	require.NoError(t, err)
}

// oneLbVariant creates a product with a single variant weighing `grams`,
// returning both ids. Titles are unique per call so grouped rows are
// distinguishable.
func oneLbVariant(t *testing.T, tx pgx.Tx, title string, grams *int) (productID, variantID uuid.UUID) {
	t.Helper()
	p := testutil.CreateProduct(t, tx, testutil.WithProductTitle(title))
	v := testutil.CreateVariant(t, tx, p.ID)
	setVariantWeight(t, tx, v.ID, grams)
	return p.ID, v.ID
}

func TestOrderService_ListDeliveryLoad(t *testing.T) {
	ctx := context.Background()
	svc := newLoadListOrderService()
	localDelivery := domain.ShippingMethodLocalDelivery
	retail := domain.OrderChannelRetail

	// baseFilter mirrors what the load-list handler builds: this channel's
	// local-delivery orders that are still waiting to go out.
	baseFilter := func() store.OrderFilter {
		return store.OrderFilter{
			Channel:        &retail,
			ShippingMethod: &localDelivery,
			FulfillmentStatuses: []domain.FulfillmentStatus{
				domain.FulfillmentStatusUnfulfilled,
				domain.FulfillmentStatusFulfilled,
			},
			ExcludeUnconfirmed:       true,
			ExcludeCancelledRefunded: true,
		}
	}

	// lineFor finds a product's row in the rollup.
	lineFor := func(lines []domain.DeliveryLoadLine, id uuid.UUID) *domain.DeliveryLoadLine {
		for i := range lines {
			if lines[i].ProductID == id {
				return &lines[i]
			}
		}
		return nil
	}

	t.Run("sums units and weight per product across delivery orders", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		custID, shipID, billID := orderFixtures(t, tx)
		g340, g2268 := 340, 2268 // a 12 oz bag and a 5 lb bag
		rebelID, rebelVar := oneLbVariant(t, tx, "Rebel Blend", &g340)
		ironID, ironVar := oneLbVariant(t, tx, "Iron Horse", &g2268)

		o1 := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithShippingMethod(localDelivery),
			testutil.WithFulfillmentStatus(domain.FulfillmentStatusUnfulfilled))
		addLineItem(t, tx, o1.ID, rebelVar, 4)
		addLineItem(t, tx, o1.ID, ironVar, 1)

		o2 := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithShippingMethod(localDelivery),
			testutil.WithFulfillmentStatus(domain.FulfillmentStatusFulfilled))
		addLineItem(t, tx, o2.ID, rebelVar, 2)

		lines, err := svc.ListDeliveryLoad(ctx, tx, baseFilter())
		require.NoError(t, err)

		rebel := lineFor(lines, rebelID)
		require.NotNil(t, rebel, "expected Rebel Blend in the load")
		assert.Equal(t, 6, rebel.Units, "4 bags on one order + 2 on the other")
		assert.Equal(t, 6*340, rebel.WeightGrams)
		assert.Zero(t, rebel.UnitsMissingWeight)

		iron := lineFor(lines, ironID)
		require.NotNil(t, iron)
		assert.Equal(t, 1, iron.Units)
		assert.Equal(t, 2268, iron.WeightGrams)

		// Heaviest first — the 5 lb bag outweighs six 12 oz bags.
		require.Len(t, lines, 2)
		assert.Equal(t, ironID, lines[0].ProductID)
	})

	t.Run("excludes orders that are not local delivery", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		custID, shipID, billID := orderFixtures(t, tx)
		g := 340
		_, variantID := oneLbVariant(t, tx, "Rebel Blend", &g)

		shipped := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithShippingMethod(domain.ShippingMethodShipped),
			testutil.WithFulfillmentStatus(domain.FulfillmentStatusUnfulfilled))
		addLineItem(t, tx, shipped.ID, variantID, 10)

		pickup := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithShippingMethod(domain.ShippingMethodPickup),
			testutil.WithFulfillmentStatus(domain.FulfillmentStatusUnfulfilled))
		addLineItem(t, tx, pickup.ID, variantID, 10)

		lines, err := svc.ListDeliveryLoad(ctx, tx, baseFilter())
		require.NoError(t, err)
		assert.Empty(t, lines, "carrier and pickup orders never ride on the van")
	})

	t.Run("excludes cancelled orders", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		custID, shipID, billID := orderFixtures(t, tx)
		g := 340
		_, variantID := oneLbVariant(t, tx, "Rebel Blend", &g)

		cancelled := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithShippingMethod(localDelivery),
			testutil.WithOrderStatus(domain.OrderStatusCancelled),
			testutil.WithFulfillmentStatus(domain.FulfillmentStatusUnfulfilled))
		addLineItem(t, tx, cancelled.ID, variantID, 8)

		lines, err := svc.ListDeliveryLoad(ctx, tx, baseFilter())
		require.NoError(t, err)
		assert.Empty(t, lines)
	})

	t.Run("OrderIDs narrows the load to the selected orders", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		custID, shipID, billID := orderFixtures(t, tx)
		g := 340
		productID, variantID := oneLbVariant(t, tx, "Rebel Blend", &g)

		keep := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithShippingMethod(localDelivery),
			testutil.WithFulfillmentStatus(domain.FulfillmentStatusUnfulfilled))
		addLineItem(t, tx, keep.ID, variantID, 3)

		drop := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithShippingMethod(localDelivery),
			testutil.WithFulfillmentStatus(domain.FulfillmentStatusUnfulfilled))
		addLineItem(t, tx, drop.ID, variantID, 9)

		f := baseFilter()
		f.OrderIDs = []uuid.UUID{keep.ID}
		lines, err := svc.ListDeliveryLoad(ctx, tx, f)
		require.NoError(t, err)

		require.Len(t, lines, 1)
		assert.Equal(t, productID, lines[0].ProductID)
		assert.Equal(t, 3, lines[0].Units, "the unchecked order's 9 bags stay off the sheet")
	})

	t.Run("variants with no weight are counted as unweighed, not as zero pounds", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		custID, shipID, billID := orderFixtures(t, tx)
		g := 340
		productID := func() uuid.UUID {
			p := testutil.CreateProduct(t, tx, testutil.WithProductTitle("Rebel Blend"))
			weighed := testutil.CreateVariant(t, tx, p.ID)
			setVariantWeight(t, tx, weighed.ID, &g)
			unweighed := testutil.CreateVariant(t, tx, p.ID)
			setVariantWeight(t, tx, unweighed.ID, nil)

			o := testutil.CreateOrder(t, tx, custID, shipID, billID,
				testutil.WithShippingMethod(localDelivery),
				testutil.WithFulfillmentStatus(domain.FulfillmentStatusUnfulfilled))
			addLineItem(t, tx, o.ID, weighed.ID, 2)
			addLineItem(t, tx, o.ID, unweighed.ID, 5)
			return p.ID
		}()

		lines, err := svc.ListDeliveryLoad(ctx, tx, baseFilter())
		require.NoError(t, err)

		line := lineFor(lines, productID)
		require.NotNil(t, line)
		assert.Equal(t, 7, line.Units, "every bag is still a bag")
		assert.Equal(t, 2*340, line.WeightGrams, "only weighed variants contribute pounds")
		assert.Equal(t, 5, line.UnitsMissingWeight, "the shortfall is reported, not hidden")
	})
}
