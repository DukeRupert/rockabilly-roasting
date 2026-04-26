package app_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/platform/sessions"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

func newAuthService() *app.AuthService {
	sessionStore := store.NewSessionStore()
	sessionMgr := sessions.NewManager(sessionStore)
	return app.NewAuthService(
		store.NewStaffStore(),
		store.NewCustomerStore(),
		store.NewMagicLinkStore(),
		sessionMgr,
		audit.NewAuditWriter(),
		metrics.NewRegistry(),
	)
}

// --- SetPassword ---

func TestSetPassword_Success(t *testing.T) {
	pool := testPool
	tx := testutil.NewTestTx(t, pool)
	svc := newAuthService()
	ctx := context.Background()

	customer := testutil.CreateCustomer(t, tx)
	actor := testutil.TestActor()

	err := svc.SetPassword(ctx, tx, customer.ID, "supersecret99", actor)
	require.NoError(t, err)

	// Verify hash was stored and is a valid bcrypt hash.
	var stored *string
	require.NoError(t, tx.QueryRow(ctx,
		`SELECT password_hash FROM customers WHERE id = $1`, customer.ID,
	).Scan(&stored))
	require.NotNil(t, stored, "password_hash should be set")
	require.NoError(t, bcrypt.CompareHashAndPassword([]byte(*stored), []byte("supersecret99")))

	// Verify audit row was written.
	entry := testutil.LastAuditEntry(t, tx, "customer", customer.ID)
	assert.Equal(t, audit.AuditCustomerPasswordSet, entry.Action)
}

func TestSetPassword_TooShort(t *testing.T) {
	pool := testPool
	tx := testutil.NewTestTx(t, pool)
	svc := newAuthService()
	ctx := context.Background()

	customer := testutil.CreateCustomer(t, tx)
	actor := testutil.TestActor()

	// 9 characters — one short of the minimum.
	err := svc.SetPassword(ctx, tx, customer.ID, "tooshort9", actor)
	assert.ErrorIs(t, err, app.ErrPasswordTooShort)

	// No password should have been written.
	var stored *string
	require.NoError(t, tx.QueryRow(ctx,
		`SELECT password_hash FROM customers WHERE id = $1`, customer.ID,
	).Scan(&stored))
	assert.Nil(t, stored, "password_hash should remain NULL after too-short error")
}

// --- ChangePassword ---

func TestChangePassword_Success(t *testing.T) {
	pool := testPool
	tx := testutil.NewTestTx(t, pool)
	svc := newAuthService()
	ctx := context.Background()

	customer := testutil.CreateCustomer(t, tx)
	actor := testutil.TestActor()

	// Set an initial password first.
	require.NoError(t, svc.SetPassword(ctx, tx, customer.ID, "initialpass1", actor))

	// Now change it.
	err := svc.ChangePassword(ctx, tx, customer.ID, "initialpass1", "newpassword1", actor)
	require.NoError(t, err)

	// New hash should verify against new password.
	var stored *string
	require.NoError(t, tx.QueryRow(ctx,
		`SELECT password_hash FROM customers WHERE id = $1`, customer.ID,
	).Scan(&stored))
	require.NotNil(t, stored)
	require.NoError(t, bcrypt.CompareHashAndPassword([]byte(*stored), []byte("newpassword1")))

	// Audit row should record the change.
	entry := testutil.LastAuditEntry(t, tx, "customer", customer.ID)
	assert.Equal(t, audit.AuditCustomerPasswordChanged, entry.Action)
}

func TestChangePassword_WrongCurrent(t *testing.T) {
	pool := testPool
	tx := testutil.NewTestTx(t, pool)
	svc := newAuthService()
	ctx := context.Background()

	customer := testutil.CreateCustomer(t, tx)
	actor := testutil.TestActor()

	// Set an initial password.
	require.NoError(t, svc.SetPassword(ctx, tx, customer.ID, "correctpass1", actor))

	// Attempt change with wrong current password.
	err := svc.ChangePassword(ctx, tx, customer.ID, "wrongpassword", "newpassword1", actor)
	assert.ErrorIs(t, err, app.ErrInvalidCredentials)
}

func TestChangePassword_NoExistingPassword(t *testing.T) {
	pool := testPool
	tx := testutil.NewTestTx(t, pool)
	svc := newAuthService()
	ctx := context.Background()

	// Customer created with no password (retail default).
	customer := testutil.CreateCustomer(t, tx)
	actor := testutil.TestActor()

	err := svc.ChangePassword(ctx, tx, customer.ID, "anything", "newpassword1", actor)
	// Must return ErrInvalidCredentials — never leak that no password is set.
	assert.ErrorIs(t, err, app.ErrInvalidCredentials)
}

// --- RedeemMagicLink: email_verified flip ---

func TestRedeemMagicLink_FlipsEmailVerified(t *testing.T) {
	pool := testPool
	tx := testutil.NewTestTx(t, pool)
	svc := newAuthService()
	ctx := context.Background()

	// Create customer with email_verified = false (default).
	customer := testutil.CreateCustomer(t, tx)
	require.False(t, customer.EmailVerified)

	// Create and immediately redeem a magic link token.
	rawToken, err := svc.CreateMagicLinkToken(ctx, tx, customer.ID)
	require.NoError(t, err)

	_, _, err = svc.RedeemMagicLink(ctx, tx, rawToken, nil, nil)
	require.NoError(t, err)

	// email_verified should now be true.
	var verified bool
	require.NoError(t, tx.QueryRow(ctx,
		`SELECT email_verified FROM customers WHERE id = $1`, customer.ID,
	).Scan(&verified))
	assert.True(t, verified, "email_verified should be flipped to true after magic link redeem")

	// AuditCustomerEmailVerified entry should exist.
	entry := testutil.LastAuditEntryWithAction(t, tx, "customer", customer.ID, audit.AuditCustomerEmailVerified)
	assert.Equal(t, string(domain.AuditActorTypeSystem), entry.ActorType)
	assert.Equal(t, "magic_link_redeem", entry.ActorName)
}

func TestRedeemMagicLink_NoFlipIfAlreadyVerified(t *testing.T) {
	pool := testPool
	tx := testutil.NewTestTx(t, pool)
	svc := newAuthService()
	ctx := context.Background()

	// Create customer and manually mark email_verified = true.
	customer := testutil.CreateCustomer(t, tx)
	_, err := tx.Exec(ctx, `UPDATE customers SET email_verified = true WHERE id = $1`, customer.ID)
	require.NoError(t, err)

	// Create and redeem a magic link.
	rawToken, err := svc.CreateMagicLinkToken(ctx, tx, customer.ID)
	require.NoError(t, err)

	_, _, err = svc.RedeemMagicLink(ctx, tx, rawToken, nil, nil)
	require.NoError(t, err)

	// Verify NO AuditCustomerEmailVerified entry was written.
	var count int
	require.NoError(t, tx.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE resource_type = 'customer' AND resource_id = $1 AND action = $2`,
		customer.ID, audit.AuditCustomerEmailVerified,
	).Scan(&count))
	assert.Equal(t, 0, count, "AuditCustomerEmailVerified should NOT be written when email was already verified")
}

// --- SetPasswordWithToken: email_verified flip ---

func TestSetPasswordWithToken_FlipsEmailVerified(t *testing.T) {
	pool := testPool
	tx := testutil.NewTestTx(t, pool)
	svc := newAuthService()
	ctx := context.Background()

	// Wholesale customer: email_verified = false by default.
	customer := testutil.CreateCustomer(t, tx)
	require.False(t, customer.EmailVerified)

	// Mint a setup token and set the password.
	rawToken, err := svc.CreateSetupToken(ctx, tx, customer.ID)
	require.NoError(t, err)

	got, err := svc.SetPasswordWithToken(ctx, tx, rawToken, "securepass99")
	require.NoError(t, err)
	assert.True(t, got.EmailVerified, "returned customer should have email_verified = true")

	// Confirm it is persisted.
	var verified bool
	require.NoError(t, tx.QueryRow(ctx,
		`SELECT email_verified FROM customers WHERE id = $1`, customer.ID,
	).Scan(&verified))
	assert.True(t, verified)

	// AuditCustomerEmailVerified entry should exist.
	entry := testutil.LastAuditEntryWithAction(t, tx, "customer", customer.ID, audit.AuditCustomerEmailVerified)
	assert.Equal(t, string(domain.AuditActorTypeSystem), entry.ActorType)
	assert.Equal(t, "setup_token_redeem", entry.ActorName)
}
