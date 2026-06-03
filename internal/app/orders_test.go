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

func newOrderService() *app.OrderService {
	return app.NewOrderService(store.NewOrderStore(nil), audit.NewAuditWriter(), metrics.NewRegistry())
}

// orderFixtures creates the prerequisite customer + addresses and returns them.
func orderFixtures(t *testing.T, tx pgx.Tx) (customerID, shippingID, billingID uuid.UUID) {
	t.Helper()
	customer := testutil.CreateCustomer(t, tx)
	shipping := testutil.CreateAddress(t, tx, customer.ID)
	billing := testutil.CreateAddress(t, tx, customer.ID)
	return customer.ID, shipping.ID, billing.ID
}

func TestOrderService_GetOrder(t *testing.T) {
	pool := testPool
	tx := testutil.NewTestTx(t, pool)
	svc := newOrderService()
	ctx := context.Background()

	custID, shipID, billID := orderFixtures(t, tx)
	order := testutil.CreateOrder(t, tx, custID, shipID, billID)

	got, err := svc.GetOrderAsStaff(ctx, tx, order.ID)
	require.NoError(t, err)
	assert.Equal(t, order.ID, got.ID)

	_, err = svc.GetOrderAsStaff(ctx, tx, uuid.New())
	assert.ErrorIs(t, err, app.ErrOrderNotFound)
}

// TestOrderService_ListOrders_ChannelScoping proves the retail and wholesale
// admin order pages see only their own channel: the Channel filter and the
// per-view tab counts are both scoped, so a wholesale order never leaks onto
// the retail list (and vice versa).
func TestOrderService_ListOrders_ChannelScoping(t *testing.T) {
	pool := testPool
	tx := testutil.NewTestTx(t, pool)
	svc := newOrderService()
	ctx := context.Background()

	custID, shipID, billID := orderFixtures(t, tx)

	// Two retail orders, one wholesale order — all "Open" (confirmed).
	retailA := testutil.CreateOrder(t, tx, custID, shipID, billID,
		testutil.WithOrderChannel(domain.OrderChannelRetail),
		testutil.WithOrderStatus(domain.OrderStatusConfirmed))
	retailB := testutil.CreateOrder(t, tx, custID, shipID, billID,
		testutil.WithOrderChannel(domain.OrderChannelRetail),
		testutil.WithOrderStatus(domain.OrderStatusConfirmed))
	wholesale := testutil.CreateOrder(t, tx, custID, shipID, billID,
		testutil.WithOrderChannel(domain.OrderChannelWholesale),
		testutil.WithOrderStatus(domain.OrderStatusConfirmed))

	retailCh := domain.OrderChannelRetail
	wholesaleCh := domain.OrderChannelWholesale

	// Retail list excludes the wholesale order.
	retailOrders, err := svc.ListOrders(ctx, tx, store.OrderFilter{Channel: &retailCh})
	require.NoError(t, err)
	retailIDs := orderIDSet(retailOrders)
	assert.Contains(t, retailIDs, retailA.ID)
	assert.Contains(t, retailIDs, retailB.ID)
	assert.NotContains(t, retailIDs, wholesale.ID)
	for _, o := range retailOrders {
		assert.Equal(t, domain.OrderChannelRetail, o.Channel)
	}

	// Wholesale list contains only the wholesale order.
	wholesaleOrders, err := svc.ListOrders(ctx, tx, store.OrderFilter{Channel: &wholesaleCh})
	require.NoError(t, err)
	wholesaleIDs := orderIDSet(wholesaleOrders)
	assert.Contains(t, wholesaleIDs, wholesale.ID)
	assert.NotContains(t, wholesaleIDs, retailA.ID)
	assert.NotContains(t, wholesaleIDs, retailB.ID)

	// Per-view tab counts are channel-scoped: 2 open retail, 1 open wholesale.
	retailCounts, err := svc.CountOrdersByView(ctx, tx, "", &retailCh)
	require.NoError(t, err)
	assert.Equal(t, 2, retailCounts.NeedsAction)

	wholesaleCounts, err := svc.CountOrdersByView(ctx, tx, "", &wholesaleCh)
	require.NoError(t, err)
	assert.Equal(t, 1, wholesaleCounts.NeedsAction)
}

func orderIDSet(orders []domain.Order) map[uuid.UUID]bool {
	s := make(map[uuid.UUID]bool, len(orders))
	for _, o := range orders {
		s[o.ID] = true
	}
	return s
}

func TestOrderService_CancelOrder(t *testing.T) {
	pool := testPool
	ctx := context.Background()
	svc := newOrderService()
	actor := testutil.TestActor()

	t.Run("cancel pending order", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		custID, shipID, billID := orderFixtures(t, tx)
		order := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithOrderStatus(domain.OrderStatusPending))

		cancelled, err := svc.CancelOrder(ctx, tx, order.ID, actor)
		require.NoError(t, err)
		assert.Equal(t, domain.OrderStatusCancelled, cancelled.Status)

		entry := testutil.LastAuditEntry(t, tx, "order", order.ID)
		assert.Equal(t, audit.AuditOrderCancelled, entry.Action)
	})

	t.Run("cancel confirmed order", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		custID, shipID, billID := orderFixtures(t, tx)
		order := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithOrderStatus(domain.OrderStatusConfirmed))

		cancelled, err := svc.CancelOrder(ctx, tx, order.ID, actor)
		require.NoError(t, err)
		assert.Equal(t, domain.OrderStatusCancelled, cancelled.Status)
	})

	t.Run("cannot cancel complete order", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		custID, shipID, billID := orderFixtures(t, tx)
		order := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithOrderStatus(domain.OrderStatusComplete))

		_, err := svc.CancelOrder(ctx, tx, order.ID, actor)
		assert.ErrorIs(t, err, app.ErrOrderNotCancellable)
	})

	t.Run("cancelling cancelled order is idempotent", func(t *testing.T) {
		// Second cancel webhook delivery (or the cleanup job racing the
		// payment_intent.canceled webhook) should not error — both want the
		// same end state. CancelOrder no-ops cleanly on already-cancelled
		// orders.
		tx := testutil.NewTestTx(t, pool)
		custID, shipID, billID := orderFixtures(t, tx)
		order := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithOrderStatus(domain.OrderStatusCancelled))

		got, err := svc.CancelOrder(ctx, tx, order.ID, actor)
		assert.NoError(t, err)
		assert.Equal(t, domain.OrderStatusCancelled, got.Status)
	})
}

func TestOrderService_RefundOrder(t *testing.T) {
	pool := testPool
	ctx := context.Background()
	svc := newOrderService()
	actor := testutil.TestActor()

	t.Run("refund captured complete order", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		custID, shipID, billID := orderFixtures(t, tx)
		order := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithOrderStatus(domain.OrderStatusComplete),
			testutil.WithPaymentStatus(domain.PaymentStatusCaptured))

		refunded, err := svc.RefundOrder(ctx, tx, order.ID, actor)
		require.NoError(t, err)
		assert.Equal(t, domain.OrderStatusRefunded, refunded.Status)
		assert.Equal(t, domain.PaymentStatusRefunded, refunded.PaymentStatus)

		entry := testutil.LastAuditEntry(t, tx, "order", order.ID)
		assert.Equal(t, audit.AuditOrderRefunded, entry.Action)
	})

	t.Run("refund captured confirmed order", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		custID, shipID, billID := orderFixtures(t, tx)
		order := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithOrderStatus(domain.OrderStatusConfirmed),
			testutil.WithPaymentStatus(domain.PaymentStatusCaptured))

		refunded, err := svc.RefundOrder(ctx, tx, order.ID, actor)
		require.NoError(t, err)
		assert.Equal(t, domain.OrderStatusRefunded, refunded.Status)
	})

	t.Run("cannot refund awaiting payment", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		custID, shipID, billID := orderFixtures(t, tx)
		order := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithOrderStatus(domain.OrderStatusComplete),
			testutil.WithPaymentStatus(domain.PaymentStatusAwaiting))

		_, err := svc.RefundOrder(ctx, tx, order.ID, actor)
		assert.ErrorIs(t, err, app.ErrOrderNotRefundable)
	})

	t.Run("cannot refund already refunded", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		custID, shipID, billID := orderFixtures(t, tx)
		order := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithOrderStatus(domain.OrderStatusRefunded),
			testutil.WithPaymentStatus(domain.PaymentStatusRefunded))

		_, err := svc.RefundOrder(ctx, tx, order.ID, actor)
		assert.ErrorIs(t, err, app.ErrOrderNotRefundable)
	})
}

func TestOrderService_UpdateFulfillmentStatus(t *testing.T) {
	pool := testPool
	ctx := context.Background()
	svc := newOrderService()
	actor := testutil.TestActor()

	t.Run("fulfilled writes audit", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		custID, shipID, billID := orderFixtures(t, tx)
		order := testutil.CreateOrder(t, tx, custID, shipID, billID)

		got, err := svc.UpdateFulfillmentStatus(ctx, tx, order.ID, domain.FulfillmentStatusFulfilled, actor)
		require.NoError(t, err)
		assert.Equal(t, domain.FulfillmentStatusFulfilled, got.FulfillmentStatus)

		entry := testutil.LastAuditEntry(t, tx, "order", order.ID)
		assert.Equal(t, audit.AuditOrderFulfilled, entry.Action)
	})

	t.Run("partial does not write audit", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		custID, shipID, billID := orderFixtures(t, tx)
		order := testutil.CreateOrder(t, tx, custID, shipID, billID)

		got, err := svc.UpdateFulfillmentStatus(ctx, tx, order.ID, domain.FulfillmentStatusPartiallyFulfilled, actor)
		require.NoError(t, err)
		assert.Equal(t, domain.FulfillmentStatusPartiallyFulfilled, got.FulfillmentStatus)

		testutil.AssertNoAuditEntry(t, tx, "order", order.ID)
	})
}

// changeLineItemFixture sets up an order with a single line item priced at
// 1500c, plus a same-product, same-priced sibling variant to swap to and a
// different-priced sibling. Returns the order, the line item ID, the matching
// sibling variant ID, and the mismatched sibling variant ID.
func changeLineItemFixture(t *testing.T, tx pgx.Tx) (orderID, lineItemID, samePriceVariantID, otherPriceVariantID, otherProductVariantID uuid.UUID) {
	t.Helper()
	custID, shipID, billID := orderFixtures(t, tx)
	product := testutil.CreateProduct(t, tx)
	v1 := testutil.CreateVariant(t, tx, product.ID, testutil.WithSKU("WHOLE-12"))
	v2 := testutil.CreateVariant(t, tx, product.ID, testutil.WithSKU("DRIP-12"))
	v3 := testutil.CreateVariant(t, tx, product.ID, testutil.WithSKU("ESPRESSO-12"))
	testutil.SetBasePriceForVariant(t, tx, v1.ID, 1500, "USD")
	testutil.SetBasePriceForVariant(t, tx, v2.ID, 1500, "USD")
	testutil.SetBasePriceForVariant(t, tx, v3.ID, 1800, "USD")

	otherProduct := testutil.CreateProduct(t, tx)
	other := testutil.CreateVariant(t, tx, otherProduct.ID, testutil.WithSKU("OTHER-12"))
	testutil.SetBasePriceForVariant(t, tx, other.ID, 1500, "USD")

	order := testutil.CreateOrder(t, tx, custID, shipID, billID,
		testutil.WithOrderStatus(domain.OrderStatusConfirmed),
		testutil.WithPaymentStatus(domain.PaymentStatusCaptured))
	li, err := store.NewOrderStore(nil).CreateLineItem(context.Background(), tx, store.CreateLineItemParams{
		OrderID:   order.ID,
		VariantID: v1.ID,
		Quantity:  1,
		UnitPrice: 1500,
		Subtotal:  1500,
		Total:     1500,
	})
	require.NoError(t, err)
	return order.ID, li.ID, v2.ID, v3.ID, other.ID
}

func newOrderServiceWithCatalogPricing() *app.OrderService {
	customerStore := store.NewCustomerStore()
	catalogStore := store.NewCatalogStore()
	subscriptionStore := store.NewSubscriptionStore(nil)
	pricingStore := store.NewPricingStore()
	return app.NewOrderService(store.NewOrderStore(nil), audit.NewAuditWriter(), metrics.NewRegistry()).
		WithEmail(app.EmailEnv{}, customerStore, catalogStore, subscriptionStore).
		WithPricing(pricingStore)
}

func TestOrderService_ChangeLineItemVariant(t *testing.T) {
	pool := testPool
	ctx := context.Background()
	svc := newOrderServiceWithCatalogPricing()
	actor := testutil.TestActor()

	t.Run("happy path swaps variant and writes audit", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		orderID, liID, samePriceID, _, _ := changeLineItemFixture(t, tx)

		updated, err := svc.ChangeLineItemVariant(ctx, tx, orderID, liID, samePriceID, actor)
		require.NoError(t, err)
		assert.Equal(t, samePriceID, updated.VariantID)
		assert.Equal(t, 1500, updated.UnitPrice, "unit price preserved")

		entry := testutil.LastAuditEntry(t, tx, "order", orderID)
		assert.Equal(t, audit.AuditOrderLineItemVariantChanged, entry.Action)
	})

	t.Run("rejects variant on different product", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		orderID, liID, _, _, otherProductID := changeLineItemFixture(t, tx)

		_, err := svc.ChangeLineItemVariant(ctx, tx, orderID, liID, otherProductID, actor)
		assert.ErrorIs(t, err, app.ErrVariantNotOnSameProduct)
	})

	t.Run("rejects variant with different price", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		orderID, liID, _, otherPriceID, _ := changeLineItemFixture(t, tx)

		_, err := svc.ChangeLineItemVariant(ctx, tx, orderID, liID, otherPriceID, actor)
		assert.ErrorIs(t, err, app.ErrVariantPriceMismatch)
	})

	t.Run("rejects when order is shipped", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		custID, shipID, billID := orderFixtures(t, tx)
		product := testutil.CreateProduct(t, tx)
		v1 := testutil.CreateVariant(t, tx, product.ID, testutil.WithSKU("A"))
		v2 := testutil.CreateVariant(t, tx, product.ID, testutil.WithSKU("B"))
		testutil.SetBasePriceForVariant(t, tx, v1.ID, 1500, "USD")
		testutil.SetBasePriceForVariant(t, tx, v2.ID, 1500, "USD")
		order := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithOrderStatus(domain.OrderStatusComplete),
			testutil.WithFulfillmentStatus(domain.FulfillmentStatusShipped))
		li, err := store.NewOrderStore(nil).CreateLineItem(ctx, tx, store.CreateLineItemParams{
			OrderID: order.ID, VariantID: v1.ID, Quantity: 1, UnitPrice: 1500, Subtotal: 1500, Total: 1500,
		})
		require.NoError(t, err)

		_, err = svc.ChangeLineItemVariant(ctx, tx, order.ID, li.ID, v2.ID, actor)
		assert.ErrorIs(t, err, app.ErrOrderNotEditable)
	})

	t.Run("allows swap when line item unit price is discounted below base", func(t *testing.T) {
		// Subscription renewals bake a plan discount into the line item's
		// UnitPrice. The swap guard must compare base-to-base, not base to
		// the (already discounted) UnitPrice — otherwise no subscription
		// order would ever pass the price-match check.
		tx := testutil.NewTestTx(t, pool)
		custID, shipID, billID := orderFixtures(t, tx)
		product := testutil.CreateProduct(t, tx)
		whole := testutil.CreateVariant(t, tx, product.ID, testutil.WithSKU("WHOLE-12-SUB"))
		drip := testutil.CreateVariant(t, tx, product.ID, testutil.WithSKU("DRIP-12-SUB"))
		testutil.SetBasePriceForVariant(t, tx, whole.ID, 1500, "USD")
		testutil.SetBasePriceForVariant(t, tx, drip.ID, 1500, "USD")
		order := testutil.CreateOrder(t, tx, custID, shipID, billID,
			testutil.WithOrderStatus(domain.OrderStatusConfirmed),
			testutil.WithPaymentStatus(domain.PaymentStatusCaptured))
		li, err := store.NewOrderStore(nil).CreateLineItem(ctx, tx, store.CreateLineItemParams{
			OrderID:   order.ID,
			VariantID: whole.ID,
			Quantity:  1,
			UnitPrice: 1350, // 10% subscription discount off 1500 base
			Subtotal:  1350,
			Total:     1350,
		})
		require.NoError(t, err)

		updated, err := svc.ChangeLineItemVariant(ctx, tx, order.ID, li.ID, drip.ID, actor)
		require.NoError(t, err)
		assert.Equal(t, drip.ID, updated.VariantID)
		assert.Equal(t, 1350, updated.UnitPrice, "discounted unit price preserved through swap")
	})

	t.Run("same-variant swap is a no-op", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		orderID, liID, _, _, _ := changeLineItemFixture(t, tx)

		// Look up the current variant ID via GetLineItem so we don't depend
		// on the fixture's internals.
		current, err := store.NewOrderStore(nil).GetLineItem(ctx, tx, liID)
		require.NoError(t, err)

		_, err = svc.ChangeLineItemVariant(ctx, tx, orderID, liID, current.VariantID, actor)
		require.NoError(t, err)
		testutil.AssertNoAuditEntry(t, tx, "order", orderID)
	})
}
