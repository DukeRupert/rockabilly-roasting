package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/platform/auth"
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

// An announcement opt-out link must silence announcements and nothing else.
// Reminders and announcements are separate subscriptions; a recipient who
// clicks one has said nothing about the other, and collapsing them is the
// mistake the separate flag exists to prevent.
func TestSetAnnouncementsFromEmailLink_LeavesRemindersAlone(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc, _ := newAnnouncementService()
	wholesale := newWholesaleService()
	ctx := context.Background()

	id := wholesaleOrderer(t, tx, dueDaysAgo)

	require.NoError(t, svc.SetAnnouncementsFromEmailLink(ctx, tx, auth.UnsubscribeTarget{
		Audience: auth.UnsubscribeAudienceAnnouncementCustomer,
		ID:       id,
	}, false))

	enabled, err := svc.GetAnnouncementsEnabled(ctx, tx, id)
	require.NoError(t, err)
	require.False(t, enabled, "announcement opt-out should stick")

	recipients, err := wholesale.ListOrderReminderRecipients(ctx, tx, time.Now())
	require.NoError(t, err)
	require.True(t, containsCustomer(recipients, id), "the weekly reminder must be unaffected")
}

// The signer must round-trip the announcement audiences, so a token minted for
// one topic can never be read back as another.
func TestUnsubscribeSigner_RoundTripsAnnouncementAudiences(t *testing.T) {
	signer := auth.NewUnsubscribeSigner("test-secret")

	for _, audience := range []auth.UnsubscribeAudience{
		auth.UnsubscribeAudienceAnnouncementCustomer,
		auth.UnsubscribeAudienceAnnouncementCustomerUser,
	} {
		id := uuid.New()
		target, err := signer.Verify(signer.Sign(auth.UnsubscribeTarget{Audience: audience, ID: id}))
		require.NoError(t, err)
		require.Equal(t, audience, target.Audience)
		require.Equal(t, id, target.ID)
	}
}
