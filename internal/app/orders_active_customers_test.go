package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// activeWindows builds the same trailing windows the dashboard does, anchored on
// a fixed "now" so the test doesn't drift with the clock.
func activeWindows(now time.Time) store.ActiveCustomerWindows {
	end := now.AddDate(0, 0, 1)
	return store.ActiveCustomerWindows{
		End:               end,
		WeekStart:         end.AddDate(0, 0, -7),
		WeekPriorStart:    end.AddDate(0, 0, -14),
		MonthStart:        end.AddDate(0, 0, -30),
		MonthPriorStart:   end.AddDate(0, 0, -60),
		QuarterStart:      end.AddDate(0, 0, -90),
		QuarterPriorStart: end.AddDate(0, 0, -180),
	}
}

// placeOrder creates one order for a fresh customer at the given age in days.
func placeOrder(t *testing.T, tx pgx.Tx, now time.Time, daysAgo int, opts ...testutil.OrderOption) {
	t.Helper()
	customer := testutil.CreateCustomer(t, tx)
	ship := testutil.CreateAddress(t, tx, customer.ID)
	bill := testutil.CreateAddress(t, tx, customer.ID)
	opts = append(opts, testutil.WithPlacedAt(now.AddDate(0, 0, -daysAgo)))
	testutil.CreateOrder(t, tx, customer.ID, ship.ID, bill.ID, opts...)
}

// A customer counts once per window no matter how many orders they placed, and
// each window nests inside the wider ones.
func TestCountActiveCustomers_WindowsNest(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newOrderService()
	ctx := context.Background()
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

	// One customer, two orders inside the week — still one active customer.
	repeat := testutil.CreateCustomer(t, tx)
	ship := testutil.CreateAddress(t, tx, repeat.ID)
	bill := testutil.CreateAddress(t, tx, repeat.ID)
	testutil.CreateOrder(t, tx, repeat.ID, ship.ID, bill.ID, testutil.WithPlacedAt(now.AddDate(0, 0, -1)))
	testutil.CreateOrder(t, tx, repeat.ID, ship.ID, bill.ID, testutil.WithPlacedAt(now.AddDate(0, 0, -3)))

	placeOrder(t, tx, now, 20) // month + quarter only
	placeOrder(t, tx, now, 70) // quarter only
	placeOrder(t, tx, now, 95) // outside every current window (prior quarter)

	got, err := svc.CountActiveCustomers(ctx, tx, activeWindows(now), nil)
	require.NoError(t, err)

	assert.Equal(t, 1, got.Week)
	assert.Equal(t, 2, got.Month)
	assert.Equal(t, 3, got.Quarter)
	assert.Equal(t, 1, got.QuarterPrior)
}

// The channel filter reads orders.channel, so retail and wholesale counts are
// disjoint and sum to the unscoped count.
func TestCountActiveCustomers_ByChannel(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newOrderService()
	ctx := context.Background()
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

	placeOrder(t, tx, now, 2, testutil.WithOrderChannel(domain.OrderChannelRetail))
	placeOrder(t, tx, now, 4, testutil.WithOrderChannel(domain.OrderChannelRetail))
	placeOrder(t, tx, now, 3, testutil.WithOrderChannel(domain.OrderChannelWholesale))

	windows := activeWindows(now)
	retail := domain.OrderChannelRetail
	wholesale := domain.OrderChannelWholesale

	all, err := svc.CountActiveCustomers(ctx, tx, windows, nil)
	require.NoError(t, err)
	r, err := svc.CountActiveCustomers(ctx, tx, windows, &retail)
	require.NoError(t, err)
	w, err := svc.CountActiveCustomers(ctx, tx, windows, &wholesale)
	require.NoError(t, err)

	assert.Equal(t, 3, all.Week)
	assert.Equal(t, 2, r.Week)
	assert.Equal(t, 1, w.Week)
}

// A customer whose only order was cancelled, refunded, or never paid for is not
// an active customer — the card must agree with the revenue card's exclusions.
func TestCountActiveCustomers_ExcludesDeadOrders(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newOrderService()
	ctx := context.Background()
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

	placeOrder(t, tx, now, 1, testutil.WithOrderStatus(domain.OrderStatusCancelled))
	placeOrder(t, tx, now, 1, testutil.WithOrderStatus(domain.OrderStatusRefunded))
	placeOrder(t, tx, now, 1,
		testutil.WithOrderStatus(domain.OrderStatusPending),
		testutil.WithPaymentStatus(domain.PaymentStatusAwaiting))
	placeOrder(t, tx, now, 1) // the only real one

	got, err := svc.CountActiveCustomers(ctx, tx, activeWindows(now), nil)
	require.NoError(t, err)
	assert.Equal(t, 1, got.Week)
	assert.Equal(t, 1, got.Quarter)
}

// Boundaries are half-open: an order exactly at the window start counts, and the
// order just before it lands in the prior window instead. Without this the two
// windows would double-count (or drop) the same day.
func TestCountActiveCustomers_WindowBoundaries(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newOrderService()
	ctx := context.Background()
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	windows := activeWindows(now)

	onBoundary := testutil.CreateCustomer(t, tx)
	ship := testutil.CreateAddress(t, tx, onBoundary.ID)
	bill := testutil.CreateAddress(t, tx, onBoundary.ID)
	testutil.CreateOrder(t, tx, onBoundary.ID, ship.ID, bill.ID, testutil.WithPlacedAt(windows.WeekStart))

	justBefore := testutil.CreateCustomer(t, tx)
	ship2 := testutil.CreateAddress(t, tx, justBefore.ID)
	bill2 := testutil.CreateAddress(t, tx, justBefore.ID)
	testutil.CreateOrder(t, tx, justBefore.ID, ship2.ID, bill2.ID,
		testutil.WithPlacedAt(windows.WeekStart.Add(-time.Second)))

	got, err := svc.CountActiveCustomers(ctx, tx, windows, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, got.Week)
	assert.Equal(t, 1, got.WeekPrior)
}
