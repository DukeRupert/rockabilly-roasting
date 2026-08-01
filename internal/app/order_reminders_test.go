package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/testutil"
)

// wholesaleOrderer creates an approved wholesale customer with one wholesale
// order placed daysAgo days back, and returns the customer ID.
func wholesaleOrderer(t *testing.T, tx pgx.Tx, daysAgo int, opts ...testutil.OrderOption) uuid.UUID {
	t.Helper()
	c := testutil.CreateCustomer(t, tx)
	approveWholesaleCustomer(t, tx, c.ID)
	addr := testutil.CreateAddress(t, tx, c.ID)

	opts = append([]testutil.OrderOption{
		testutil.WithOrderChannel(domain.OrderChannelWholesale),
		testutil.WithPlacedAt(time.Now().AddDate(0, 0, -daysAgo)),
	}, opts...)
	testutil.CreateOrder(t, tx, c.ID, addr.ID, addr.ID, opts...)
	return c.ID
}

func containsCustomer(recipients []domain.OrderReminderRecipient, id uuid.UUID) bool {
	for _, r := range recipients {
		if r.CustomerID == id {
			return true
		}
	}
	return false
}

func TestListOrderReminderRecipients_IncludesRecentWholesaleOrderer(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newWholesaleService()
	ctx := context.Background()

	id := wholesaleOrderer(t, tx, 5)

	recipients, err := svc.ListOrderReminderRecipients(ctx, tx, time.Now())
	require.NoError(t, err)
	require.True(t, containsCustomer(recipients, id), "recent wholesale orderer should be reminded")
}

func TestListOrderReminderRecipients_ExcludesOutsideWindow(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newWholesaleService()
	ctx := context.Background()

	// The window is 21 days; 30 days back is dormant.
	stale := wholesaleOrderer(t, tx, 30)
	// Just inside the boundary must still qualify.
	fresh := wholesaleOrderer(t, tx, 20)

	recipients, err := svc.ListOrderReminderRecipients(ctx, tx, time.Now())
	require.NoError(t, err)
	require.False(t, containsCustomer(recipients, stale), "dormant account should not be reminded")
	require.True(t, containsCustomer(recipients, fresh), "account inside the window should be reminded")
}

func TestListOrderReminderRecipients_ExcludesCancelledAndRefundedOnly(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newWholesaleService()
	ctx := context.Background()

	cancelled := wholesaleOrderer(t, tx, 3, testutil.WithOrderStatus(domain.OrderStatusCancelled))
	refunded := wholesaleOrderer(t, tx, 3, testutil.WithOrderStatus(domain.OrderStatusRefunded))

	recipients, err := svc.ListOrderReminderRecipients(ctx, tx, time.Now())
	require.NoError(t, err)
	require.False(t, containsCustomer(recipients, cancelled), "a cancelled order is not buying activity")
	require.False(t, containsCustomer(recipients, refunded), "a refunded order is not buying activity")
}

func TestListOrderReminderRecipients_ExcludesRetailChannelOrders(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newWholesaleService()
	ctx := context.Background()

	c := testutil.CreateCustomer(t, tx)
	approveWholesaleCustomer(t, tx, c.ID)
	addr := testutil.CreateAddress(t, tx, c.ID)
	testutil.CreateOrder(t, tx, c.ID, addr.ID, addr.ID,
		testutil.WithOrderChannel(domain.OrderChannelRetail),
		testutil.WithPlacedAt(time.Now().AddDate(0, 0, -2)),
	)

	recipients, err := svc.ListOrderReminderRecipients(ctx, tx, time.Now())
	require.NoError(t, err)
	require.False(t, containsCustomer(recipients, c.ID),
		"a retail-channel order should not put a wholesale account on the list")
}

func TestListOrderReminderRecipients_ExcludesNonWholesaleAccount(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newWholesaleService()
	ctx := context.Background()

	c := testutil.CreateCustomer(t, tx) // retail, never approved
	addr := testutil.CreateAddress(t, tx, c.ID)
	testutil.CreateOrder(t, tx, c.ID, addr.ID, addr.ID,
		testutil.WithOrderChannel(domain.OrderChannelWholesale),
		testutil.WithPlacedAt(time.Now().AddDate(0, 0, -2)),
	)

	recipients, err := svc.ListOrderReminderRecipients(ctx, tx, time.Now())
	require.NoError(t, err)
	require.False(t, containsCustomer(recipients, c.ID))
}

func TestListOrderReminderRecipients_ExcludesSuspendedAccount(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newWholesaleService()
	ctx := context.Background()

	id := wholesaleOrderer(t, tx, 3)
	_, err := tx.Exec(ctx, "UPDATE customers SET wholesale_status = 'suspended' WHERE id = $1", id)
	require.NoError(t, err)

	recipients, err := svc.ListOrderReminderRecipients(ctx, tx, time.Now())
	require.NoError(t, err)
	require.False(t, containsCustomer(recipients, id), "suspended account should not be reminded")
}

func TestSetOrderRemindersEnabled_RemovesFromAudience(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newWholesaleService()
	ctx := context.Background()

	id := wholesaleOrderer(t, tx, 3)
	actor := app.Actor{Type: domain.AuditActorTypeSystem, Name: "test"}

	recipients, err := svc.ListOrderReminderRecipients(ctx, tx, time.Now())
	require.NoError(t, err)
	require.True(t, containsCustomer(recipients, id), "precondition: on the list by default")

	require.NoError(t, svc.SetOrderRemindersEnabled(ctx, tx, actor, id, false))

	recipients, err = svc.ListOrderReminderRecipients(ctx, tx, time.Now())
	require.NoError(t, err)
	require.False(t, containsCustomer(recipients, id), "opted-out account should drop off the list")

	// And back on again.
	require.NoError(t, svc.SetOrderRemindersEnabled(ctx, tx, actor, id, true))
	recipients, err = svc.ListOrderReminderRecipients(ctx, tx, time.Now())
	require.NoError(t, err)
	require.True(t, containsCustomer(recipients, id))
}

func TestListOrderReminderRecipients_DeduplicatesMultipleOrders(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newWholesaleService()
	ctx := context.Background()

	c := testutil.CreateCustomer(t, tx)
	approveWholesaleCustomer(t, tx, c.ID)
	addr := testutil.CreateAddress(t, tx, c.ID)
	for _, d := range []int{2, 9, 16} {
		testutil.CreateOrder(t, tx, c.ID, addr.ID, addr.ID,
			testutil.WithOrderChannel(domain.OrderChannelWholesale),
			testutil.WithPlacedAt(time.Now().AddDate(0, 0, -d)),
		)
	}

	recipients, err := svc.ListOrderReminderRecipients(ctx, tx, time.Now())
	require.NoError(t, err)

	var count int
	var last time.Time
	for _, r := range recipients {
		if r.CustomerID == c.ID {
			count++
			last = r.LastOrderAt
		}
	}
	require.Equal(t, 1, count, "a customer with three orders must be emailed once, not three times")
	require.WithinDuration(t, time.Now().AddDate(0, 0, -2), last, time.Minute,
		"LastOrderAt should be the most recent order")
}
