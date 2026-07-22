package app_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/emailtemplates"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/email"
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
		store.NewStaffInviteTokenStore(),
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

	// Audit row should record the change. Filter by action because the prior
	// SetPassword call also wrote a row for this customer in the same tx, and
	// the audit_log primary key is a random UUID — ordering between same-tx
	// entries is non-deterministic.
	entry := testutil.LastAuditEntryWithAction(t, tx, "customer", customer.ID, audit.AuditCustomerPasswordChanged)
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

// --- SendPasswordSetupEmail ---

func newAuthServiceWithEmail(t *testing.T) (*app.AuthService, *email.TestSender) {
	t.Helper()
	renderer, err := emailtemplates.New()
	require.NoError(t, err)
	sender := email.NewTestSender()
	svc := newAuthService().WithEmail(app.EmailEnv{
		Mailer:    sender,
		Renderer:  renderer,
		FromAddr:  "test@example.com",
		BaseURL:   "https://example.test",
		StoreName: "Test Store",
	})
	return svc, sender
}

// createCommittedCustomer inserts a minimal customer using the pool (visible
// across new transactions opened by service code) and registers a cleanup that
// deletes the row + any audit + magic_link_tokens rows it accumulates.
func createCommittedCustomer(t *testing.T, ctx context.Context, withPassword bool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	email := "test-" + id.String()[:8] + "@example.com"
	var passwordHash *string
	if withPassword {
		hash, err := bcrypt.GenerateFromPassword([]byte("existingpassword"), bcrypt.DefaultCost)
		require.NoError(t, err)
		s := string(hash)
		passwordHash = &s
	}
	_, err := testPool.Exec(ctx,
		`INSERT INTO customers (id, email, first_name, last_name, password_hash)
		 VALUES ($1, $2, 'Test', 'Customer', $3)`,
		id, email, passwordHash,
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM customers WHERE id = $1`, id)
	})
	return id
}

func TestSendPasswordSetupEmail_NoExistingPassword(t *testing.T) {
	ctx := context.Background()
	svc, sender := newAuthServiceWithEmail(t)
	customerID := createCommittedCustomer(t, ctx, false)

	actor := testutil.TestActor()
	require.NoError(t, svc.SendPasswordSetupEmail(ctx, testPool, customerID, actor))

	require.Len(t, sender.Sent, 1)
	msg := sender.Sent[0]
	assert.Equal(t, "Set your password", msg.Subject)
	assert.Contains(t, msg.HTML, "https://example.test/account/password-setup?token=")
	assert.Contains(t, msg.HTML, "Set your password")
	assert.NotContains(t, msg.HTML, "Pick a new password")

	var (
		actorType string
		metaReset *bool
	)
	require.NoError(t, testPool.QueryRow(ctx,
		`SELECT actor_type, (metadata->>'reset')::bool FROM audit_log
		 WHERE resource_type = 'customer' AND resource_id = $1 AND action = $2
		 ORDER BY created_at DESC LIMIT 1`,
		customerID, audit.AuditEmailPasswordSetupSent,
	).Scan(&actorType, &metaReset))
	assert.Equal(t, string(actor.Type), actorType)
	if assert.NotNil(t, metaReset) {
		assert.False(t, *metaReset, "no password set -> reset=false")
	}
}

func TestSendPasswordSetupEmail_ExistingPassword_FlagsReset(t *testing.T) {
	ctx := context.Background()
	svc, sender := newAuthServiceWithEmail(t)
	customerID := createCommittedCustomer(t, ctx, true)

	require.NoError(t, svc.SendPasswordSetupEmail(ctx, testPool, customerID, testutil.TestActor()))

	require.Len(t, sender.Sent, 1)
	msg := sender.Sent[0]
	assert.Equal(t, "Reset your password", msg.Subject)
	assert.Contains(t, msg.HTML, "Pick a new password")

	var metaReset *bool
	require.NoError(t, testPool.QueryRow(ctx,
		`SELECT (metadata->>'reset')::bool FROM audit_log
		 WHERE resource_type = 'customer' AND resource_id = $1 AND action = $2
		 ORDER BY created_at DESC LIMIT 1`,
		customerID, audit.AuditEmailPasswordSetupSent,
	).Scan(&metaReset))
	if assert.NotNil(t, metaReset) {
		assert.True(t, *metaReset, "existing password -> reset=true")
	}
}

// --- SendPasswordResetEmail (self-service) ---

// mintSetupToken creates a committed setup token for customerID so
// SendPasswordResetEmail (which expects a pre-minted token) can send it.
func mintSetupToken(t *testing.T, ctx context.Context, svc *app.AuthService, customerID uuid.UUID) string {
	t.Helper()
	var rawToken string
	require.NoError(t, store.Tx(ctx, testPool, func(tx pgx.Tx) error {
		var err error
		rawToken, err = svc.CreateSetupToken(ctx, tx, customerID)
		return err
	}))
	return rawToken
}

func TestSendPasswordResetEmail_NoExistingPassword_SetsWording(t *testing.T) {
	ctx := context.Background()
	svc, sender := newAuthServiceWithEmail(t)
	customerID := createCommittedCustomer(t, ctx, false)
	rawToken := mintSetupToken(t, ctx, svc, customerID)

	require.NoError(t, svc.SendPasswordResetEmail(ctx, testPool, customerID, rawToken))

	require.Len(t, sender.Sent, 1)
	msg := sender.Sent[0]
	assert.Equal(t, "Set your password", msg.Subject)
	assert.Contains(t, msg.HTML, "https://example.test/account/password-setup?token=")

	var (
		actorType string
		metaReset *bool
		metaSelf  *bool
	)
	require.NoError(t, testPool.QueryRow(ctx,
		`SELECT actor_type, (metadata->>'reset')::bool, (metadata->>'self_service')::bool FROM audit_log
		 WHERE resource_type = 'customer' AND resource_id = $1 AND action = $2
		 ORDER BY created_at DESC LIMIT 1`,
		customerID, audit.AuditEmailPasswordSetupSent,
	).Scan(&actorType, &metaReset, &metaSelf))
	assert.Equal(t, string(domain.AuditActorTypeSystem), actorType)
	if assert.NotNil(t, metaReset) {
		assert.False(t, *metaReset, "no password set -> reset=false")
	}
	if assert.NotNil(t, metaSelf) {
		assert.True(t, *metaSelf, "self-service reset -> self_service=true")
	}
}

func TestSendPasswordResetEmail_ExistingPassword_FlagsReset(t *testing.T) {
	ctx := context.Background()
	svc, sender := newAuthServiceWithEmail(t)
	customerID := createCommittedCustomer(t, ctx, true)
	rawToken := mintSetupToken(t, ctx, svc, customerID)

	require.NoError(t, svc.SendPasswordResetEmail(ctx, testPool, customerID, rawToken))

	require.Len(t, sender.Sent, 1)
	assert.Equal(t, "Reset your password", sender.Sent[0].Subject)

	var metaReset *bool
	require.NoError(t, testPool.QueryRow(ctx,
		`SELECT (metadata->>'reset')::bool FROM audit_log
		 WHERE resource_type = 'customer' AND resource_id = $1 AND action = $2
		 ORDER BY created_at DESC LIMIT 1`,
		customerID, audit.AuditEmailPasswordSetupSent,
	).Scan(&metaReset))
	if assert.NotNil(t, metaReset) {
		assert.True(t, *metaReset, "existing password -> reset=true")
	}
}

// pgx is used implicitly via the auth service.
var _ = pgx.ErrNoRows
