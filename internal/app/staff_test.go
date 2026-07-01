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
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

func newStaffService() *app.StaffService {
	return app.NewStaffService(
		store.NewStaffStore(),
		audit.NewAuditWriter(),
		metrics.NewRegistry(),
	)
}

// --- Invite ---

func TestStaffInvite_Success(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newStaffService()
	ctx := context.Background()

	staff, err := svc.Invite(ctx, tx, app.InviteStaffParams{
		Name:  "Dale Watson",
		Email: "Dale@Example.com",
		Role:  domain.StaffRoleFulfillment,
	}, testutil.TestActor())
	require.NoError(t, err)

	assert.Equal(t, "dale@example.com", staff.Email, "email should be normalized to lowercase")
	assert.Equal(t, "Dale Watson", staff.Name)
	assert.Equal(t, domain.StaffRoleFulfillment, staff.Role)
	assert.True(t, staff.IsActive)

	// The placeholder password must be unusable — no password matches it.
	assert.Error(t, bcrypt.CompareHashAndPassword([]byte(staff.PasswordHash), []byte("anything")))

	testutil.LastAuditEntryWithAction(t, tx, "staff", staff.ID, audit.AuditStaffCreated)
}

func TestStaffInvite_DuplicateEmail(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newStaffService()
	ctx := context.Background()

	_, err := svc.Invite(ctx, tx, app.InviteStaffParams{
		Name: "First", Email: "dupe@example.com", Role: domain.StaffRoleSupport,
	}, testutil.TestActor())
	require.NoError(t, err)

	_, err = svc.Invite(ctx, tx, app.InviteStaffParams{
		Name: "Second", Email: "DUPE@example.com", Role: domain.StaffRoleSupport,
	}, testutil.TestActor())
	assert.ErrorIs(t, err, app.ErrStaffEmailExists)
}

func TestStaffInvite_Validation(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newStaffService()
	ctx := context.Background()

	_, err := svc.Invite(ctx, tx, app.InviteStaffParams{
		Name: "  ", Email: "x@example.com", Role: domain.StaffRoleSupport,
	}, testutil.TestActor())
	assert.ErrorIs(t, err, app.ErrStaffNameRequired)

	_, err = svc.Invite(ctx, tx, app.InviteStaffParams{
		Name: "No Email", Email: "  ", Role: domain.StaffRoleSupport,
	}, testutil.TestActor())
	assert.ErrorIs(t, err, app.ErrStaffEmailRequired)

	_, err = svc.Invite(ctx, tx, app.InviteStaffParams{
		Name: "Bad Role", Email: "y@example.com", Role: domain.StaffRole("wizard"),
	}, testutil.TestActor())
	assert.ErrorIs(t, err, app.ErrInvalidStaffRole)
}

// --- Set password with token ---

func TestStaffSetPasswordWithToken_Success(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	staffSvc := newStaffService()
	authSvc := newAuthService()
	ctx := context.Background()

	staff, err := staffSvc.Invite(ctx, tx, app.InviteStaffParams{
		Name: "Wanda", Email: "wanda@example.com", Role: domain.StaffRoleCatalog,
	}, testutil.TestActor())
	require.NoError(t, err)

	// Token lifecycle lives in AuthService (the credential-lifecycle boundary).
	rawToken, err := authSvc.CreateStaffInviteToken(ctx, tx, staff.ID)
	require.NoError(t, err)

	updated, err := authSvc.SetStaffPasswordWithToken(ctx, tx, rawToken, "brandnewpass1")
	require.NoError(t, err)
	assert.Equal(t, staff.ID, updated.ID)
	require.NoError(t, bcrypt.CompareHashAndPassword([]byte(updated.PasswordHash), []byte("brandnewpass1")))

	// Token is single-use — a second redemption fails.
	_, err = authSvc.SetStaffPasswordWithToken(ctx, tx, rawToken, "anotherpass12")
	assert.ErrorIs(t, err, app.ErrStaffInviteInvalid)
}

func TestStaffSetPasswordWithToken_TooShort(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	staffSvc := newStaffService()
	authSvc := newAuthService()
	ctx := context.Background()

	staff, err := staffSvc.Invite(ctx, tx, app.InviteStaffParams{
		Name: "Shorty", Email: "shorty@example.com", Role: domain.StaffRoleSupport,
	}, testutil.TestActor())
	require.NoError(t, err)
	rawToken, err := authSvc.CreateStaffInviteToken(ctx, tx, staff.ID)
	require.NoError(t, err)

	_, err = authSvc.SetStaffPasswordWithToken(ctx, tx, rawToken, "short")
	assert.ErrorIs(t, err, app.ErrPasswordTooShort)
}

func TestStaffSetPasswordWithToken_Invalid(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	authSvc := newAuthService()
	ctx := context.Background()

	_, err := authSvc.SetStaffPasswordWithToken(ctx, tx, "not-a-real-token", "longenough123")
	assert.ErrorIs(t, err, app.ErrStaffInviteInvalid)
}

// --- Change role ---

func TestStaffChangeRole_Success(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newStaffService()
	ctx := context.Background()

	target, err := svc.Invite(ctx, tx, app.InviteStaffParams{
		Name: "Movable", Email: "movable@example.com", Role: domain.StaffRoleSupport,
	}, testutil.TestActor())
	require.NoError(t, err)

	err = svc.ChangeRole(ctx, tx, target.ID, domain.StaffRoleFinance, testutil.TestActor())
	require.NoError(t, err)

	after, err := svc.Get(ctx, tx, target.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StaffRoleFinance, after.Role)

	testutil.LastAuditEntryWithAction(t, tx, "staff", target.ID, audit.AuditStaffRoleChanged)
}

func TestStaffChangeRole_Self(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newStaffService()
	ctx := context.Background()

	me, err := svc.Invite(ctx, tx, app.InviteStaffParams{
		Name: "Me", Email: "me@example.com", Role: domain.StaffRoleAdmin,
	}, testutil.TestActor())
	require.NoError(t, err)

	err = svc.ChangeRole(ctx, tx, me.ID, domain.StaffRoleSupport, testutil.TestActorFromStaff(me.ID))
	assert.ErrorIs(t, err, app.ErrCannotModifySelf)
}

func TestStaffChangeRole_LastActiveAdmin(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newStaffService()
	ctx := context.Background()

	adminA, err := svc.Invite(ctx, tx, app.InviteStaffParams{
		Name: "Admin A", Email: "admina@example.com", Role: domain.StaffRoleAdmin,
	}, testutil.TestActor())
	require.NoError(t, err)

	// Only one active admin — demoting is refused.
	err = svc.ChangeRole(ctx, tx, adminA.ID, domain.StaffRoleSupport, testutil.TestActor())
	assert.ErrorIs(t, err, app.ErrLastActiveAdmin)

	// Add a second admin, then demotion is allowed.
	_, err = svc.Invite(ctx, tx, app.InviteStaffParams{
		Name: "Admin B", Email: "adminb@example.com", Role: domain.StaffRoleAdmin,
	}, testutil.TestActor())
	require.NoError(t, err)

	err = svc.ChangeRole(ctx, tx, adminA.ID, domain.StaffRoleSupport, testutil.TestActor())
	require.NoError(t, err)
}

// --- Activate / deactivate ---

func TestStaffSetActive_DeactivateLastActiveAdmin(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newStaffService()
	ctx := context.Background()

	adminA, err := svc.Invite(ctx, tx, app.InviteStaffParams{
		Name: "Solo Admin", Email: "solo@example.com", Role: domain.StaffRoleAdmin,
	}, testutil.TestActor())
	require.NoError(t, err)

	err = svc.SetActive(ctx, tx, adminA.ID, false, testutil.TestActor())
	assert.ErrorIs(t, err, app.ErrLastActiveAdmin)

	_, err = svc.Invite(ctx, tx, app.InviteStaffParams{
		Name: "Backup Admin", Email: "backup@example.com", Role: domain.StaffRoleAdmin,
	}, testutil.TestActor())
	require.NoError(t, err)

	err = svc.SetActive(ctx, tx, adminA.ID, false, testutil.TestActor())
	require.NoError(t, err)

	after, err := svc.Get(ctx, tx, adminA.ID)
	require.NoError(t, err)
	assert.False(t, after.IsActive)

	testutil.LastAuditEntryWithAction(t, tx, "staff", adminA.ID, audit.AuditStaffDeactivated)
}

func TestStaffSetActive_Self(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newStaffService()
	ctx := context.Background()

	me, err := svc.Invite(ctx, tx, app.InviteStaffParams{
		Name: "Self", Email: "self@example.com", Role: domain.StaffRoleAdmin,
	}, testutil.TestActor())
	require.NoError(t, err)

	err = svc.SetActive(ctx, tx, me.ID, false, testutil.TestActorFromStaff(me.ID))
	assert.ErrorIs(t, err, app.ErrCannotModifySelf)
}
