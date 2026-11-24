package app_test

import (
	"context"
	"testing"
	"time"

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

// Correcting an order's ship-to: the production case was a subscription
// checkout with the ZIP "9", caught by staff after the bag was packed.

func newAddressOrderService() *app.OrderService {
	return app.NewOrderService(store.NewOrderStore(nil), audit.NewAuditWriter(), metrics.NewRegistry()).
		WithEmail(app.EmailEnv{}, store.NewCustomerStore(), store.NewCustomerUserStore(), store.NewCatalogStore(), store.NewSubscriptionStore(nil)).
		WithShipments(store.NewShippingStore())
}

type addressFixture struct {
	customer *domain.Customer
	bad      *domain.Address // the typo'd ship-to the order was placed with
	good     *domain.Address // the corrected one
	order    *domain.Order
}

func newAddressFixture(t *testing.T, tx pgx.Tx, opts ...testutil.OrderOption) addressFixture {
	t.Helper()
	c := testutil.CreateCustomer(t, tx)
	bad := testutil.CreateAddress(t, tx, c.ID, testutil.WithAddressPostalCode("9"))
	good := testutil.CreateAddress(t, tx, c.ID, testutil.WithAddressPostalCode("93555"))
	order := testutil.CreateOrder(t, tx, c.ID, bad.ID, bad.ID, opts...)
	return addressFixture{customer: c, bad: bad, good: good, order: order}
}

func liveSubscriptionAt(t *testing.T, tx pgx.Tx, customerID, addressID uuid.UUID) *domain.Subscription {
	t.Helper()
	product := testutil.CreateProduct(t, tx)
	variant := testutil.CreateVariant(t, tx, product.ID)
	plan := planEvery(t, tx, domain.SubscriptionIntervalEvery30Days)
	sub, err := newSubscriptionService().CreateSubscription(context.Background(), tx, app.CreateSubscriptionParams{
		CustomerID: customerID, PlanID: plan.ID, VariantID: variant.ID,
		Quantity: 1, ShippingAddressID: addressID,
	}, testutil.TestActor())
	require.NoError(t, err)
	return sub
}

func reloadOrder(t *testing.T, tx pgx.Tx, svc *app.OrderService, id uuid.UUID) *domain.Order {
	t.Helper()
	o, err := svc.GetOrderAsStaff(context.Background(), tx, id)
	require.NoError(t, err)
	return o
}

func subscriptionAddress(t *testing.T, tx pgx.Tx, id uuid.UUID) uuid.UUID {
	t.Helper()
	var addr uuid.UUID
	require.NoError(t, tx.QueryRow(context.Background(), `SELECT shipping_address_id FROM subscriptions WHERE id = $1`, id).Scan(&addr))
	return addr
}

func TestChangeShippingAddress_PackedOrderMovesWithSubscriptions(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := context.Background()
	svc := newAddressOrderService()
	f := newAddressFixture(t, tx,
		testutil.WithOrderStatus(domain.OrderStatusProcessing),
		testutil.WithFulfillmentStatus(domain.FulfillmentStatusFulfilled))
	live := liveSubscriptionAt(t, tx, f.customer.ID, f.bad.ID)
	cancelled := liveSubscriptionAt(t, tx, f.customer.ID, f.bad.ID)
	_, err := tx.Exec(ctx, `UPDATE subscriptions SET status = 'cancelled', cancelled_at = now() WHERE id = $1`, cancelled.ID)
	require.NoError(t, err)

	n, err := svc.LiveSubscriptionsAtShippingAddress(ctx, tx, f.order)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "the cancelled subscription is not offered")

	change, err := svc.ChangeShippingAddress(ctx, tx, f.order.ID, f.good.ID, true, testutil.TestActor())
	require.NoError(t, err)

	got := reloadOrder(t, tx, svc, f.order.ID)
	assert.Equal(t, f.good.ID, got.ShippingAddressID)
	assert.Equal(t, f.bad.ID, got.BillingAddressID, "billing is what Stripe charged; it stays")
	assert.Equal(t, []uuid.UUID{live.ID}, change.MovedSubscriptions)
	assert.Equal(t, f.good.ID, subscriptionAddress(t, tx, live.ID))
	assert.Equal(t, f.bad.ID, subscriptionAddress(t, tx, cancelled.ID), "a cancelled subscription keeps its history")

	var actions []string
	rows, err := tx.Query(ctx, `SELECT action FROM audit_log WHERE resource_id = ANY($1) ORDER BY action`,
		[]uuid.UUID{f.order.ID, live.ID})
	require.NoError(t, err)
	for rows.Next() {
		var a string
		require.NoError(t, rows.Scan(&a))
		actions = append(actions, a)
	}
	require.NoError(t, rows.Err())
	assert.Contains(t, actions, audit.AuditOrderShippingAddressChanged)
	assert.Contains(t, actions, audit.AuditSubscriptionAddressChanged)
}

func TestChangeShippingAddress_LeavesSubscriptionsUnlessAsked(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newAddressOrderService()
	f := newAddressFixture(t, tx)
	sub := liveSubscriptionAt(t, tx, f.customer.ID, f.bad.ID)

	change, err := svc.ChangeShippingAddress(context.Background(), tx, f.order.ID, f.good.ID, false, testutil.TestActor())
	require.NoError(t, err)
	assert.Empty(t, change.MovedSubscriptions)
	assert.Equal(t, f.bad.ID, subscriptionAddress(t, tx, sub.ID))
}

func TestChangeShippingAddress_RefusesAnotherCustomersAddress(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newAddressOrderService()
	f := newAddressFixture(t, tx)
	stranger := testutil.CreateCustomer(t, tx)
	theirs := testutil.CreateAddress(t, tx, stranger.ID)

	_, err := svc.ChangeShippingAddress(context.Background(), tx, f.order.ID, theirs.ID, false, testutil.TestActor())
	assert.ErrorIs(t, err, app.ErrAddressNotFound)
	assert.Equal(t, f.bad.ID, reloadOrder(t, tx, svc, f.order.ID).ShippingAddressID)
}

func TestChangeShippingAddress_LockedStates(t *testing.T) {
	ctx := context.Background()
	svc := newAddressOrderService()

	locked := map[string][]testutil.OrderOption{
		"shipped":   {testutil.WithFulfillmentStatus(domain.FulfillmentStatusShipped)},
		"delivered": {testutil.WithFulfillmentStatus(domain.FulfillmentStatusDelivered)},
		"cancelled": {testutil.WithOrderStatus(domain.OrderStatusCancelled)},
		"refunded":  {testutil.WithOrderStatus(domain.OrderStatusRefunded)},
		"complete":  {testutil.WithOrderStatus(domain.OrderStatusComplete)},
	}
	for name, opts := range locked {
		t.Run(name, func(t *testing.T) {
			tx := testutil.NewTestTx(t, testPool)
			f := newAddressFixture(t, tx, opts...)
			_, err := svc.ChangeShippingAddress(ctx, tx, f.order.ID, f.good.ID, false, testutil.TestActor())
			assert.ErrorIs(t, err, app.ErrOrderAddressLocked)
		})
	}

	t.Run("live label", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		f := newAddressFixture(t, tx)
		staffID := testutil.CreateStaff(t, tx)
		_, err := store.NewShippingStore().CreateShipment(ctx, tx, store.CreateShipmentParams{
			OrderID: f.order.ID, Status: domain.ShipmentStatusLabelCreated, Provider: "shippo",
			TrackingNumber: "TRACK-" + uuid.NewString(), CarrierName: "USPS", ServiceName: "Ground Advantage",
			LabelCostCents: 758, LabelCurrency: "USD", WeightOz: 12, CreatedBy: staffID,
		})
		require.NoError(t, err)
		_, err = svc.ChangeShippingAddress(ctx, tx, f.order.ID, f.good.ID, false, testutil.TestActor())
		assert.ErrorIs(t, err, app.ErrOrderHasActiveLabel)
	})

	t.Run("pending stop on an open route", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		f := newAddressFixture(t, tx, testutil.WithShippingMethod(domain.ShippingMethodLocalDelivery))
		planRouteOver(t, tx, time.Now().AddDate(0, 0, 3), f.order.ID)
		_, err := svc.ChangeShippingAddress(ctx, tx, f.order.ID, f.good.ID, false, testutil.TestActor())
		assert.ErrorIs(t, err, app.ErrOrderOnRoute)
	})
}
