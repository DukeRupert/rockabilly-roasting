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

// dueDaysAgo is old enough to clear the 7-day suppression window but well
// inside the 21-day lookback — i.e. a customer who genuinely is due a nudge.
// Tests that assert on some OTHER exclusion reason (suspended, wrong channel,
// cancelled) must use this, or they pass because of suppression and stop
// testing what they claim to.
const dueDaysAgo = 10

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

	id := wholesaleOrderer(t, tx, dueDaysAgo)

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

	cancelled := wholesaleOrderer(t, tx, dueDaysAgo, testutil.WithOrderStatus(domain.OrderStatusCancelled))
	refunded := wholesaleOrderer(t, tx, dueDaysAgo, testutil.WithOrderStatus(domain.OrderStatusRefunded))

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
		testutil.WithPlacedAt(time.Now().AddDate(0, 0, -dueDaysAgo)),
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
		testutil.WithPlacedAt(time.Now().AddDate(0, 0, -dueDaysAgo)),
	)

	recipients, err := svc.ListOrderReminderRecipients(ctx, tx, time.Now())
	require.NoError(t, err)
	require.False(t, containsCustomer(recipients, c.ID))
}

func TestListOrderReminderRecipients_ExcludesSuspendedAccount(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newWholesaleService()
	ctx := context.Background()

	id := wholesaleOrderer(t, tx, dueDaysAgo)
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

	id := wholesaleOrderer(t, tx, dueDaysAgo)
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
	for _, d := range []int{9, 13, 17} {
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
	require.WithinDuration(t, time.Now().AddDate(0, 0, -9), last, time.Minute,
		"LastOrderAt should be the most recent order")
}

func TestListOrderReminderRecipients_SuppressesAlreadyOrderedThisCycle(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newWholesaleService()
	ctx := context.Background()

	// Ordered 2 days ago — this week's order is already in, no nudge needed.
	justOrdered := wholesaleOrderer(t, tx, 2)
	// Ordered 14 days ago — due for a nudge.
	due := wholesaleOrderer(t, tx, 14)

	recipients, err := svc.ListOrderReminderRecipients(ctx, tx, time.Now())
	require.NoError(t, err)
	require.False(t, containsCustomer(recipients, justOrdered),
		"a customer who already ordered this cycle should not be nagged")
	require.True(t, containsCustomer(recipients, due))
}

// Suppression must key off the MOST RECENT order, not any order in the window.
// A customer who ordered three weeks ago and again on Wednesday is covered.
func TestListOrderReminderRecipients_SuppressionUsesLatestOrder(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newWholesaleService()
	ctx := context.Background()

	c := testutil.CreateCustomer(t, tx)
	approveWholesaleCustomer(t, tx, c.ID)
	addr := testutil.CreateAddress(t, tx, c.ID)
	for _, d := range []int{18, 2} { // one old (qualifying), one recent (suppressing)
		testutil.CreateOrder(t, tx, c.ID, addr.ID, addr.ID,
			testutil.WithOrderChannel(domain.OrderChannelWholesale),
			testutil.WithPlacedAt(time.Now().AddDate(0, 0, -d)),
		)
	}

	recipients, err := svc.ListOrderReminderRecipients(ctx, tx, time.Now())
	require.NoError(t, err)
	require.False(t, containsCustomer(recipients, c.ID),
		"the recent order should suppress even though an older qualifying order exists")
}

func TestListOrderReminderRecipients_SuppressionBoundary(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newWholesaleService()
	ctx := context.Background()

	// 8 days ago is older than the 7-day suppression window — still due.
	due := wholesaleOrderer(t, tx, 8)
	// 6 days ago falls inside it — suppressed.
	suppressed := wholesaleOrderer(t, tx, 6)

	recipients, err := svc.ListOrderReminderRecipients(ctx, tx, time.Now())
	require.NoError(t, err)
	require.True(t, containsCustomer(recipients, due))
	require.False(t, containsCustomer(recipients, suppressed))
}

func TestLastWholesaleOrderID_PicksMostRecentCompleted(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newWholesaleService()
	ctx := context.Background()

	c := testutil.CreateCustomer(t, tx)
	approveWholesaleCustomer(t, tx, c.ID)
	addr := testutil.CreateAddress(t, tx, c.ID)

	older := testutil.CreateOrder(t, tx, c.ID, addr.ID, addr.ID,
		testutil.WithOrderChannel(domain.OrderChannelWholesale),
		testutil.WithPlacedAt(time.Now().AddDate(0, 0, -10)))
	newest := testutil.CreateOrder(t, tx, c.ID, addr.ID, addr.ID,
		testutil.WithOrderChannel(domain.OrderChannelWholesale),
		testutil.WithPlacedAt(time.Now().AddDate(0, 0, -3)))
	// A cancelled order placed after both must not win — reordering an
	// abandoned basket would hand back something they already dropped.
	testutil.CreateOrder(t, tx, c.ID, addr.ID, addr.ID,
		testutil.WithOrderChannel(domain.OrderChannelWholesale),
		testutil.WithOrderStatus(domain.OrderStatusCancelled),
		testutil.WithPlacedAt(time.Now().AddDate(0, 0, -1)))

	got, err := svc.LastWholesaleOrderID(ctx, tx, c.ID)
	require.NoError(t, err)
	require.Equal(t, newest.ID, got)
	require.NotEqual(t, older.ID, got)
}

func TestLastWholesaleOrderID_NoOrders(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newWholesaleService()
	ctx := context.Background()

	c := testutil.CreateCustomer(t, tx)
	approveWholesaleCustomer(t, tx, c.ID)

	_, err := svc.LastWholesaleOrderID(ctx, tx, c.ID)
	require.ErrorIs(t, err, app.ErrNoPreviousOrder)
}

func TestLastWholesaleOrderID_IgnoresRetailChannel(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newWholesaleService()
	ctx := context.Background()

	c := testutil.CreateCustomer(t, tx)
	approveWholesaleCustomer(t, tx, c.ID)
	addr := testutil.CreateAddress(t, tx, c.ID)
	testutil.CreateOrder(t, tx, c.ID, addr.ID, addr.ID,
		testutil.WithOrderChannel(domain.OrderChannelRetail),
		testutil.WithPlacedAt(time.Now().AddDate(0, 0, -2)))

	_, err := svc.LastWholesaleOrderID(ctx, tx, c.ID)
	require.ErrorIs(t, err, app.ErrNoPreviousOrder)
}
