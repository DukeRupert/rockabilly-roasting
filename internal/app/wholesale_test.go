package app_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

func newWholesaleService() *app.WholesaleService {
	return app.NewWholesaleService(
		store.NewCustomerStore(),
		store.NewCustomerGroupStore(),
		store.NewCatalogStore(),
		store.NewOrderStore(),
		store.NewCartStore(),
		audit.NewAuditWriter(),
		metrics.NewRegistry(),
	)
}

func TestWholesaleService_SubmitApplication(t *testing.T) {
	pool := testPool
	svc := newWholesaleService()
	ctx := context.Background()

	t.Run("successful application", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)

		customer, err := svc.SubmitApplication(ctx, tx, app.ApplyParams{
			Email:       "wholesale@example.com",
			FirstName:   "Jane",
			LastName:    "Doe",
			CompanyName: "Acme Corp",
		})
		require.NoError(t, err)
		assert.Equal(t, domain.AccountTypeWholesale, customer.AccountType)
		assert.NotNil(t, customer.WholesaleStatus)
		assert.Equal(t, domain.WholesaleStatusPending, *customer.WholesaleStatus)
		assert.NotNil(t, customer.CompanyName)
		assert.Equal(t, "Acme Corp", *customer.CompanyName)
	})

	t.Run("duplicate email rejected", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)

		_, err := svc.SubmitApplication(ctx, tx, app.ApplyParams{
			Email:       "dup@example.com",
			FirstName:   "A",
			LastName:    "B",
			CompanyName: "C",
		})
		require.NoError(t, err)

		_, err = svc.SubmitApplication(ctx, tx, app.ApplyParams{
			Email:       "dup@example.com",
			FirstName:   "X",
			LastName:    "Y",
			CompanyName: "Z",
		})
		assert.ErrorIs(t, err, app.ErrEmailAlreadyExists)
	})
}

func TestWholesaleService_ApproveApplication(t *testing.T) {
	pool := testPool
	svc := newWholesaleService()
	ctx := context.Background()

	t.Run("approve pending application", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		staffID := testutil.CreateStaff(t, tx)
		actor := testutil.TestActorFromStaff(staffID)

		customer, err := svc.SubmitApplication(ctx, tx, app.ApplyParams{
			Email:       "approve@example.com",
			FirstName:   "Jane",
			LastName:    "Doe",
			CompanyName: "Acme",
		})
		require.NoError(t, err)

		approved, err := svc.ApproveApplication(ctx, tx, customer.ID, actor)
		require.NoError(t, err)
		assert.Equal(t, domain.WholesaleStatusApproved, *approved.WholesaleStatus)
		assert.NotNil(t, approved.ApprovedAt)

		entry := testutil.LastAuditEntry(t, tx, "customer", customer.ID)
		assert.Equal(t, audit.AuditWholesaleApplicationApproved, entry.Action)
	})

	t.Run("approve non-wholesale customer fails", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		actor := testutil.TestActor()
		customer := testutil.CreateCustomer(t, tx)

		_, err := svc.ApproveApplication(ctx, tx, customer.ID, actor)
		assert.ErrorIs(t, err, app.ErrWholesaleNotPending)
	})
}

func TestWholesaleService_SuspendAccount(t *testing.T) {
	pool := testPool
	svc := newWholesaleService()
	ctx := context.Background()

	t.Run("suspend approved account", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		staffID := testutil.CreateStaff(t, tx)
		actor := testutil.TestActorFromStaff(staffID)

		customer, err := svc.SubmitApplication(ctx, tx, app.ApplyParams{
			Email:       "suspend@example.com",
			FirstName:   "Jane",
			LastName:    "Doe",
			CompanyName: "Corp",
		})
		require.NoError(t, err)

		_, err = svc.ApproveApplication(ctx, tx, customer.ID, actor)
		require.NoError(t, err)

		suspended, err := svc.SuspendAccount(ctx, tx, customer.ID, "policy violation", actor)
		require.NoError(t, err)
		assert.Equal(t, domain.WholesaleStatusSuspended, *suspended.WholesaleStatus)

		entry := testutil.LastAuditEntryWithAction(t, tx, "customer", customer.ID, audit.AuditWholesaleAccountSuspended)
		assert.Equal(t, audit.AuditWholesaleAccountSuspended, entry.Action)
	})

	t.Run("suspend pending account fails", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		actor := testutil.TestActor()

		customer, err := svc.SubmitApplication(ctx, tx, app.ApplyParams{
			Email:       "pending-suspend@example.com",
			FirstName:   "A",
			LastName:    "B",
			CompanyName: "C",
		})
		require.NoError(t, err)

		_, err = svc.SuspendAccount(ctx, tx, customer.ID, "test", actor)
		assert.ErrorIs(t, err, app.ErrWholesaleNotApproved)
	})
}
