package app_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// CustomerLogin resolves customers.email first and only falls back to
// customer_users. These tests pin that ordering and the invariant that a
// teammate's session carries the MEMBER id, not the account id.

func TestCustomerLogin_FallsBackToCustomerUser(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	authSvc := newAuthService()
	userSvc := newCustomerUserService()
	ctx := context.Background()

	account := testutil.CreateCustomer(t, tx)
	approveWholesaleCustomer(t, tx, account.ID)

	member, rawToken, err := userSvc.Invite(ctx, tx, testActor(account.ID), account.ID, "manager@cafe.com", "Sam", false)
	require.NoError(t, err)

	// Cannot sign in before accepting the invite — no password is set.
	_, _, err = authSvc.CustomerLogin(ctx, tx, "manager@cafe.com", "hunter2hunter2", false, nil, nil)
	assert.ErrorIs(t, err, app.ErrInvalidCredentials)

	// Accept the invite.
	accepted, err := authSvc.SetCustomerUserPasswordWithToken(ctx, tx, rawToken, "hunter2hunter2")
	require.NoError(t, err)
	assert.Equal(t, member.ID, accepted.ID)
	assert.True(t, accepted.HasAcceptedInvite())

	sess, sessionToken, err := authSvc.CustomerLogin(ctx, tx, "manager@cafe.com", "hunter2hunter2", false, nil, nil)
	require.NoError(t, err)
	assert.NotEmpty(t, sessionToken)

	// The session identifies the PERSON; resolving to the account is the
	// middleware's job.
	assert.Equal(t, domain.SessionActorTypeCustomerUser, sess.ActorType)
	assert.Equal(t, member.ID, sess.ActorID)
	assert.NotEqual(t, account.ID, sess.ActorID)
}

func TestCustomerLogin_CustomerUserWrongPassword(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	authSvc := newAuthService()
	userSvc := newCustomerUserService()
	ctx := context.Background()

	account := testutil.CreateCustomer(t, tx)
	approveWholesaleCustomer(t, tx, account.ID)

	_, rawToken, err := userSvc.Invite(ctx, tx, testActor(account.ID), account.ID, "manager@cafe.com", "", false)
	require.NoError(t, err)
	_, err = authSvc.SetCustomerUserPasswordWithToken(ctx, tx, rawToken, "hunter2hunter2")
	require.NoError(t, err)

	_, _, err = authSvc.CustomerLogin(ctx, tx, "manager@cafe.com", "wrongpassword", false, nil, nil)
	assert.ErrorIs(t, err, app.ErrInvalidCredentials)
}

// A capitalized address must reach the same row — the bug migration 061 fixed
// for customers, now guarded for members too.
func TestCustomerLogin_CustomerUserEmailIsCaseInsensitive(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	authSvc := newAuthService()
	userSvc := newCustomerUserService()
	ctx := context.Background()

	account := testutil.CreateCustomer(t, tx)
	approveWholesaleCustomer(t, tx, account.ID)

	_, rawToken, err := userSvc.Invite(ctx, tx, testActor(account.ID), account.ID, "manager@cafe.com", "", false)
	require.NoError(t, err)
	_, err = authSvc.SetCustomerUserPasswordWithToken(ctx, tx, rawToken, "hunter2hunter2")
	require.NoError(t, err)

	_, _, err = authSvc.CustomerLogin(ctx, tx, "Manager@Cafe.COM", "hunter2hunter2", false, nil, nil)
	assert.NoError(t, err)
}

// The primary sign-in must be unaffected by the fallback existing at all.
func TestCustomerLogin_PrimaryStillWins(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	authSvc := newAuthService()
	ctx := context.Background()

	account := testutil.CreateCustomer(t, tx, testutil.WithEmail("owner@cafe.com"))
	approveWholesaleCustomer(t, tx, account.ID)
	require.NoError(t, authSvc.SetPassword(ctx, tx, account.ID, "primarypassword", testActor(account.ID)))

	sess, _, err := authSvc.CustomerLogin(ctx, tx, "owner@cafe.com", "primarypassword", false, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, domain.SessionActorTypeCustomer, sess.ActorType)
	assert.Equal(t, account.ID, sess.ActorID)
}

func TestSetCustomerUserPasswordWithToken_TokenIsSingleUse(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	authSvc := newAuthService()
	userSvc := newCustomerUserService()
	ctx := context.Background()

	account := testutil.CreateCustomer(t, tx)
	approveWholesaleCustomer(t, tx, account.ID)

	_, rawToken, err := userSvc.Invite(ctx, tx, testActor(account.ID), account.ID, "manager@cafe.com", "", false)
	require.NoError(t, err)

	_, err = authSvc.SetCustomerUserPasswordWithToken(ctx, tx, rawToken, "hunter2hunter2")
	require.NoError(t, err)

	_, err = authSvc.SetCustomerUserPasswordWithToken(ctx, tx, rawToken, "somethingelse1")
	assert.ErrorIs(t, err, app.ErrCustomerUserInviteInvalid)
}

func TestSetCustomerUserPasswordWithToken_RejectsShortPassword(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	authSvc := newAuthService()
	userSvc := newCustomerUserService()
	ctx := context.Background()

	account := testutil.CreateCustomer(t, tx)
	approveWholesaleCustomer(t, tx, account.ID)

	_, rawToken, err := userSvc.Invite(ctx, tx, testActor(account.ID), account.ID, "manager@cafe.com", "", false)
	require.NoError(t, err)

	_, err = authSvc.SetCustomerUserPasswordWithToken(ctx, tx, rawToken, "short")
	assert.ErrorIs(t, err, app.ErrPasswordTooShort)
}

// Revoking a member must kill their live sessions in the same transaction —
// otherwise a removed teammate keeps portal access until the cookie expires,
// which is up to 30 days on a remember-me session.
func TestRevoke_KillsLiveSessions(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	authSvc := newAuthService()
	userSvc := newCustomerUserService()
	sessionStore := store.NewSessionStore()
	ctx := context.Background()

	account := testutil.CreateCustomer(t, tx)
	approveWholesaleCustomer(t, tx, account.ID)

	member, rawToken, err := userSvc.Invite(ctx, tx, testActor(account.ID), account.ID, "manager@cafe.com", "", false)
	require.NoError(t, err)
	_, err = authSvc.SetCustomerUserPasswordWithToken(ctx, tx, rawToken, "hunter2hunter2")
	require.NoError(t, err)

	_, sessionToken, err := authSvc.CustomerLogin(ctx, tx, "manager@cafe.com", "hunter2hunter2", true, nil, nil)
	require.NoError(t, err)

	// Session is live before revocation.
	_, err = sessionStore.GetByToken(ctx, tx, sessionToken)
	require.NoError(t, err)

	require.NoError(t, userSvc.Revoke(ctx, tx, testActor(account.ID), member.ID, account.ID))

	_, err = sessionStore.GetByToken(ctx, tx, sessionToken)
	assert.Error(t, err, "revoked member's session must no longer resolve")
}
