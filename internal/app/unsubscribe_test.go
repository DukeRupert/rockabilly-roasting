package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/testutil"
)

// The emailed opt-out must have exactly the same effect as staff flipping the
// toggle in the admin panel: the customer drops off the reminder audience.
func TestSetOrderRemindersFromEmailLink_RemovesFromAudience(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newWholesaleService()
	ctx := context.Background()

	id := wholesaleOrderer(t, tx, dueDaysAgo)

	recipients, err := svc.ListOrderReminderRecipients(ctx, tx, time.Now())
	require.NoError(t, err)
	require.True(t, containsCustomer(recipients, id), "precondition: subscribed by default")

	require.NoError(t, svc.SetOrderRemindersFromEmailLink(ctx, tx, id, false))

	recipients, err = svc.ListOrderReminderRecipients(ctx, tx, time.Now())
	require.NoError(t, err)
	require.False(t, containsCustomer(recipients, id), "opt-out link should remove them")
}

// Mis-clicks happen; the undo button has to actually put them back.
func TestSetOrderRemindersFromEmailLink_Resubscribe(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newWholesaleService()
	ctx := context.Background()

	id := wholesaleOrderer(t, tx, dueDaysAgo)
	require.NoError(t, svc.SetOrderRemindersFromEmailLink(ctx, tx, id, false))
	require.NoError(t, svc.SetOrderRemindersFromEmailLink(ctx, tx, id, true))

	recipients, err := svc.ListOrderReminderRecipients(ctx, tx, time.Now())
	require.NoError(t, err)
	require.True(t, containsCustomer(recipients, id), "undo should restore them")
}

// Unsubscribing twice (double-click, or a scanner following the one-click POST
// after the customer already did) must be a no-op, not an error.
func TestSetOrderRemindersFromEmailLink_Idempotent(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newWholesaleService()
	ctx := context.Background()

	id := wholesaleOrderer(t, tx, dueDaysAgo)
	require.NoError(t, svc.SetOrderRemindersFromEmailLink(ctx, tx, id, false))
	require.NoError(t, svc.SetOrderRemindersFromEmailLink(ctx, tx, id, false))

	recipients, err := svc.ListOrderReminderRecipients(ctx, tx, time.Now())
	require.NoError(t, err)
	require.False(t, containsCustomer(recipients, id))
}

// The opt-out is recorded against the customer, not staff or the system, so
// the audit log shows who actually asked to stop receiving these.
func TestSetOrderRemindersFromEmailLink_AuditsCustomerAsActor(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newWholesaleService()
	ctx := context.Background()

	id := wholesaleOrderer(t, tx, dueDaysAgo)
	require.NoError(t, svc.SetOrderRemindersFromEmailLink(ctx, tx, id, false))

	var actorType string
	var actorID *string
	err := tx.QueryRow(ctx,
		`SELECT actor_type, actor_id::text FROM audit_log
		 WHERE resource_id = $1 AND action = 'customer.order_reminders_disabled'
		 ORDER BY created_at DESC LIMIT 1`, id).Scan(&actorType, &actorID)
	require.NoError(t, err)
	require.Equal(t, "customer", actorType)
	require.NotNil(t, actorID)
	require.Equal(t, id.String(), *actorID)
}
