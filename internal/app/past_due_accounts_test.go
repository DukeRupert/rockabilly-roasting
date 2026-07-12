package app_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/testutil"
)

func TestPastDueAccounts(t *testing.T) {
	ctx := context.Background()
	tx := testutil.NewTestTx(t, testPool)
	svc, _ := newReconcileService(&fakeEnqueuer{})

	newOrder := func(custID, shipID, billID uuid.UUID, payment domain.PaymentStatus, total int, opts ...testutil.OrderOption) *domain.Order {
		opts = append([]testutil.OrderOption{
			testutil.WithOrderChannel(domain.OrderChannelWholesale),
			testutil.WithOrderStatus(domain.OrderStatusConfirmed),
			testutil.WithPaymentStatus(payment),
			testutil.WithOrderTotals(total, 0, 0, 0, total),
		}, opts...)
		return testutil.CreateOrder(t, tx, custID, shipID, billID, opts...)
	}

	// Customer A: two live overdue invoices, one paid, one cancelled-overdue
	// (must not count) — past due, with only the live overdue pair aggregated.
	custA := testutil.CreateCustomer(t, tx)
	addrA := testutil.CreateAddress(t, tx, custA.ID)
	newOrder(custA.ID, addrA.ID, addrA.ID, domain.PaymentStatusOverdue, 10000)
	newOrder(custA.ID, addrA.ID, addrA.ID, domain.PaymentStatusOverdue, 5000)
	newOrder(custA.ID, addrA.ID, addrA.ID, domain.PaymentStatusCaptured, 7000)
	newOrder(custA.ID, addrA.ID, addrA.ID, domain.PaymentStatusOverdue, 9999,
		testutil.WithOrderStatus(domain.OrderStatusCancelled))

	// Customer B: invoiced within terms — current, never listed.
	custB := testutil.CreateCustomer(t, tx)
	addrB := testutil.CreateAddress(t, tx, custB.ID)
	newOrder(custB.ID, addrB.ID, addrB.ID, domain.PaymentStatusInvoiced, 4000)

	accounts, err := svc.ListPastDueAccounts(ctx, tx, 10)
	require.NoError(t, err)

	var found bool
	for _, a := range accounts {
		require.NotEqual(t, custB.ID, a.CustomerID, "current account must not be listed")
		if a.CustomerID == custA.ID {
			found = true
			assert.Equal(t, 2, a.OverdueOrders, "cancelled and captured orders must not count")
			assert.Equal(t, 15000, a.OverdueTotal)
		}
	}
	assert.True(t, found, "past-due account listed")

	// The true count matches the number of distinct past-due customers, not
	// the display-limited row count.
	count, err := svc.CountPastDueAccounts(ctx, tx)
	require.NoError(t, err)
	assert.Equal(t, len(accounts), count)
	assert.GreaterOrEqual(t, count, 1)

	flags, err := svc.PastDueCustomerFlags(ctx, tx, []uuid.UUID{custA.ID, custB.ID})
	require.NoError(t, err)
	assert.True(t, flags[custA.ID])
	assert.False(t, flags[custB.ID])

	empty, err := svc.PastDueCustomerFlags(ctx, tx, nil)
	require.NoError(t, err)
	assert.Empty(t, empty)
}
