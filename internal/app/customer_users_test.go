package app_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/platform/sessions"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

func newCustomerUserService() *app.CustomerUserService {
	sessionMgr := sessions.NewManager(store.NewSessionStore())
	return app.NewCustomerUserService(
		store.NewCustomerStore(),
		store.NewCustomerUserStore(),
		sessionMgr,
		audit.NewAuditWriter(),
		metrics.NewRegistry(),
	).WithEmail(app.EmailEnv{}, newAuthService())
}

func testActor(id uuid.UUID) app.Actor {
	return app.Actor{Type: domain.AuditActorTypeCustomer, ID: &id, Name: "owner@cafe.com"}
}

// --- Invite ---

func TestInvite_CreatesPendingMember(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCustomerUserService()
	ctx := context.Background()

	account := testutil.CreateCustomer(t, tx)
	approveWholesaleCustomer(t, tx, account.ID)

	user, rawToken, err := svc.Invite(ctx, tx, testActor(account.ID), account.ID, "Manager@Cafe.com", "Sam Rivera", false)
	require.NoError(t, err)

	// Normalized on the way in, matching customers.email (migration 061).
	assert.Equal(t, "manager@cafe.com", user.Email)
	assert.Equal(t, "Sam Rivera", user.Name)
	assert.Equal(t, domain.CustomerUserRoleMember, user.Role)
	assert.False(t, user.ReceivesNotifications)
	assert.NotEmpty(t, rawToken)

	// Cannot sign in until the invite is accepted.
	assert.False(t, user.HasAcceptedInvite())
}

func TestInvite_RejectsRetailAccount(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCustomerUserService()
	ctx := context.Background()

	account := testutil.CreateCustomer(t, tx) // retail by default

	_, _, err := svc.Invite(ctx, tx, testActor(account.ID), account.ID, "manager@cafe.com", "", false)
	assert.ErrorIs(t, err, app.ErrNotWholesaleAccount)
}

// An invite on an address that already belongs to a customers row must be
// refused: CustomerLogin checks customers.email first, so the invitee would
// otherwise be silently signed into the wrong account.
func TestInvite_RejectsEmailBelongingToACustomer(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCustomerUserService()
	ctx := context.Background()

	account := testutil.CreateCustomer(t, tx)
	approveWholesaleCustomer(t, tx, account.ID)
	existing := testutil.CreateCustomer(t, tx, testutil.WithEmail("someone@example.com"))

	_, _, err := svc.Invite(ctx, tx, testActor(account.ID), account.ID, existing.Email, "", false)
	assert.ErrorIs(t, err, app.ErrCustomerUserEmailTaken)
}

func TestInvite_RejectsDuplicateMemberEmail(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCustomerUserService()
	ctx := context.Background()

	account := testutil.CreateCustomer(t, tx)
	approveWholesaleCustomer(t, tx, account.ID)

	_, _, err := svc.Invite(ctx, tx, testActor(account.ID), account.ID, "manager@cafe.com", "", false)
	require.NoError(t, err)

	// Different capitalization must still collide.
	_, _, err = svc.Invite(ctx, tx, testActor(account.ID), account.ID, "MANAGER@cafe.com", "", false)
	assert.ErrorIs(t, err, app.ErrCustomerUserEmailTaken)
}

// --- Scoping ---

// The account id is the authorization boundary: holding a member's id is not
// enough to touch it from another account.
func TestCustomerUserService_ScopedToOwningAccount(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCustomerUserService()
	ctx := context.Background()

	owner := testutil.CreateCustomer(t, tx)
	approveWholesaleCustomer(t, tx, owner.ID)
	stranger := testutil.CreateCustomer(t, tx)
	approveWholesaleCustomer(t, tx, stranger.ID)

	member, _, err := svc.Invite(ctx, tx, testActor(owner.ID), owner.ID, "manager@cafe.com", "", false)
	require.NoError(t, err)

	t.Run("revoke", func(t *testing.T) {
		err := svc.Revoke(ctx, tx, testActor(stranger.ID), member.ID, stranger.ID)
		assert.ErrorIs(t, err, app.ErrCustomerUserNotFound)
	})

	t.Run("resend invite", func(t *testing.T) {
		_, _, err := svc.ResendInvite(ctx, tx, testActor(stranger.ID), member.ID, stranger.ID)
		assert.ErrorIs(t, err, app.ErrCustomerUserNotFound)
	})

	t.Run("notifications", func(t *testing.T) {
		err := svc.SetNotifications(ctx, tx, testActor(stranger.ID), member.ID, stranger.ID, true)
		assert.ErrorIs(t, err, app.ErrCustomerUserNotFound)
	})

	// Still intact for its real owner.
	members, err := svc.List(ctx, tx, owner.ID)
	require.NoError(t, err)
	require.Len(t, members, 1)
	assert.Equal(t, member.ID, members[0].ID)
}

func TestRevoke_RemovesMember(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCustomerUserService()
	ctx := context.Background()

	account := testutil.CreateCustomer(t, tx)
	approveWholesaleCustomer(t, tx, account.ID)

	member, _, err := svc.Invite(ctx, tx, testActor(account.ID), account.ID, "manager@cafe.com", "", false)
	require.NoError(t, err)

	require.NoError(t, svc.Revoke(ctx, tx, testActor(account.ID), member.ID, account.ID))

	members, err := svc.List(ctx, tx, account.ID)
	require.NoError(t, err)
	assert.Empty(t, members)
}

// --- Notification recipients ---

func TestNotificationRecipients(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCustomerUserService()
	ctx := context.Background()

	account := testutil.CreateCustomer(t, tx, testutil.WithEmail("owner@cafe.com"))
	approveWholesaleCustomer(t, tx, account.ID)
	account.Email = "owner@cafe.com"

	// Opted out — must not appear.
	_, _, err := svc.Invite(ctx, tx, testActor(account.ID), account.ID, "quiet@cafe.com", "", false)
	require.NoError(t, err)
	// Opted in.
	_, _, err = svc.Invite(ctx, tx, testActor(account.ID), account.ID, "loud@cafe.com", "", true)
	require.NoError(t, err)

	got, err := svc.NotificationRecipients(ctx, tx, account)
	require.NoError(t, err)

	// Primary contact always first, always present.
	assert.Equal(t, []string{"owner@cafe.com", "loud@cafe.com"}, got)
}

// Order confirmations run this helper for every order, retail included. A
// retail customer has no customer_users rows, so the recipient list must
// collapse to exactly their own address — this is the regression guard for the
// retail send path.
func TestNotificationRecipients_RetailIsUnchanged(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCustomerUserService()
	ctx := context.Background()

	// Not approved for wholesale and never invited anyone — the default state
	// of every retail shopper.
	retail := testutil.CreateCustomer(t, tx, testutil.WithEmail("shopper@example.com"))

	got, err := svc.NotificationRecipients(ctx, tx, retail)
	require.NoError(t, err)
	assert.Equal(t, []string{"shopper@example.com"}, got)
}

func TestNotificationRecipients_PrimaryOnlyWhenNoTeam(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCustomerUserService()
	ctx := context.Background()

	account := testutil.CreateCustomer(t, tx, testutil.WithEmail("solo@cafe.com"))
	approveWholesaleCustomer(t, tx, account.ID)
	account.Email = "solo@cafe.com"

	got, err := svc.NotificationRecipients(ctx, tx, account)
	require.NoError(t, err)
	assert.Equal(t, []string{"solo@cafe.com"}, got)
}
