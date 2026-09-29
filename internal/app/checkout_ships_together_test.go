package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/testutil"
)

// A subscription signup takes one product, so a customer who wants three
// coffees places three orders in a row. They go out in one box; only the
// first should pay for it. OpenShipmentTo is how the second and third find
// the box they will be packed into.
func TestCheckoutService_OpenShipmentTo(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCheckoutService()
	ctx := context.Background()
	now := time.Now()

	customer := testutil.CreateCustomer(t, tx)
	home := testutil.CreateAddress(t, tx, customer.ID)
	other := testutil.CreateAddress(t, tx, customer.ID, testutil.WithAddressLine1("2 Elsewhere St"))

	t.Run("nothing on the shelf", func(t *testing.T) {
		got, err := svc.OpenShipmentTo(ctx, tx, customer.ID, home.ID, now)
		require.NoError(t, err)
		require.Nil(t, got)
	})

	// None of these is the box a new order to `home` goes into.
	testutil.CreateOrder(t, tx, customer.ID, other.ID, other.ID) // different address
	testutil.CreateOrder(t, tx, customer.ID, home.ID, home.ID,
		testutil.WithFulfillmentStatus(domain.FulfillmentStatusFulfilled)) // already packed
	testutil.CreateOrder(t, tx, customer.ID, home.ID, home.ID,
		testutil.WithShippingMethod(domain.ShippingMethodLocalDelivery)) // rides the van, not the mail
	testutil.CreateOrder(t, tx, customer.ID, home.ID, home.ID,
		testutil.WithOrderStatus(domain.OrderStatusPending),
		testutil.WithPaymentStatus(domain.PaymentStatusAwaiting)) // not paid for
	testutil.CreateOrder(t, tx, customer.ID, home.ID, home.ID,
		testutil.WithOrderStatus(domain.OrderStatusCancelled)) // gone
	testutil.CreateOrder(t, tx, customer.ID, home.ID, home.ID,
		testutil.WithPlacedAt(now.Add(-4*24*time.Hour))) // stuck, not waiting
	stranger := testutil.CreateCustomer(t, tx, testutil.WithEmail("stranger@example.com"))
	testutil.CreateOrder(t, tx, stranger.ID, home.ID, home.ID) // someone else's

	t.Run("none of the near misses count", func(t *testing.T) {
		got, err := svc.OpenShipmentTo(ctx, tx, customer.ID, home.ID, now)
		require.NoError(t, err)
		require.Nil(t, got)
	})

	older := testutil.CreateOrder(t, tx, customer.ID, home.ID, home.ID,
		testutil.WithPlacedAt(now.Add(-2*time.Hour)))
	newer := testutil.CreateOrder(t, tx, customer.ID, home.ID, home.ID,
		testutil.WithPlacedAt(now.Add(-time.Minute)))

	t.Run("finds the most recent open box", func(t *testing.T) {
		got, err := svc.OpenShipmentTo(ctx, tx, customer.ID, home.ID, now)
		require.NoError(t, err)
		require.NotNil(t, got)
		require.Equal(t, newer.ID, got.ID)
		require.NotEqual(t, older.ID, got.ID)
	})
}
