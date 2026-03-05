package app_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

func newCustomerService() *app.CustomerService {
	return app.NewCustomerService(store.NewCustomerStore(), audit.NewAuditWriter(), metrics.NewRegistry())
}

func TestCustomerService_GetCustomer(t *testing.T) {
	pool := testPool
	tx := testutil.NewTestTx(t, pool)
	svc := newCustomerService()
	ctx := context.Background()

	customer := testutil.CreateCustomer(t, tx)

	got, err := svc.GetCustomer(ctx, tx, customer.ID)
	require.NoError(t, err)
	assert.Equal(t, customer.ID, got.ID)
	assert.Equal(t, customer.Email, got.Email)

	_, err = svc.GetCustomer(ctx, tx, uuid.New())
	assert.ErrorIs(t, err, app.ErrCustomerNotFound)
}

func TestCustomerService_UpdateEmail(t *testing.T) {
	pool := testPool
	ctx := context.Background()
	svc := newCustomerService()

	t.Run("success", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		customer := testutil.CreateCustomer(t, tx)

		updated, err := svc.UpdateEmail(ctx, tx, customer.ID, "new@example.com")
		require.NoError(t, err)
		assert.Equal(t, "new@example.com", updated.Email)
	})

	t.Run("same email OK", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		customer := testutil.CreateCustomer(t, tx, testutil.WithEmail("same@example.com"))

		updated, err := svc.UpdateEmail(ctx, tx, customer.ID, "same@example.com")
		require.NoError(t, err)
		assert.Equal(t, "same@example.com", updated.Email)
	})

	t.Run("taken email errors", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		testutil.CreateCustomer(t, tx, testutil.WithEmail("taken@example.com"))
		other := testutil.CreateCustomer(t, tx)

		_, err := svc.UpdateEmail(ctx, tx, other.ID, "taken@example.com")
		assert.ErrorIs(t, err, app.ErrEmailAlreadyExists)
	})
}

func TestCustomerService_VerifyEmail(t *testing.T) {
	pool := testPool
	tx := testutil.NewTestTx(t, pool)
	svc := newCustomerService()
	ctx := context.Background()

	customer := testutil.CreateCustomer(t, tx)
	assert.False(t, customer.EmailVerified)

	err := svc.VerifyEmail(ctx, tx, customer.ID)
	require.NoError(t, err)

	got, err := svc.GetCustomer(ctx, tx, customer.ID)
	require.NoError(t, err)
	assert.True(t, got.EmailVerified)
}

func TestCustomerService_GrantTaxExemption(t *testing.T) {
	pool := testPool
	tx := testutil.NewTestTx(t, pool)
	svc := newCustomerService()
	ctx := context.Background()
	actor := testutil.TestActor()

	customer := testutil.CreateCustomer(t, tx)

	err := svc.GrantTaxExemption(ctx, tx, customer.ID, "nonprofit", actor)
	require.NoError(t, err)

	got, err := svc.GetCustomer(ctx, tx, customer.ID)
	require.NoError(t, err)
	assert.True(t, got.TaxExempt)

	entry := testutil.LastAuditEntry(t, tx, "customer", customer.ID)
	assert.Equal(t, audit.AuditCustomerTaxExemptionGranted, entry.Action)
}

func TestCustomerService_RevokeTaxExemption(t *testing.T) {
	pool := testPool
	tx := testutil.NewTestTx(t, pool)
	svc := newCustomerService()
	ctx := context.Background()
	actor := testutil.TestActor()

	customer := testutil.CreateCustomer(t, tx)
	// Grant first, then revoke.
	err := svc.GrantTaxExemption(ctx, tx, customer.ID, "nonprofit", actor)
	require.NoError(t, err)

	err = svc.RevokeTaxExemption(ctx, tx, customer.ID, actor)
	require.NoError(t, err)

	got, err := svc.GetCustomer(ctx, tx, customer.ID)
	require.NoError(t, err)
	assert.False(t, got.TaxExempt)

	entry := testutil.LastAuditEntryWithAction(t, tx, "customer", customer.ID, audit.AuditCustomerTaxExemptionRevoked)
	assert.Equal(t, audit.AuditCustomerTaxExemptionRevoked, entry.Action)
}

func TestCustomerService_GetAddress(t *testing.T) {
	pool := testPool
	tx := testutil.NewTestTx(t, pool)
	svc := newCustomerService()
	ctx := context.Background()

	customer := testutil.CreateCustomer(t, tx)
	addr := testutil.CreateAddress(t, tx, customer.ID)

	got, err := svc.GetAddress(ctx, tx, addr.ID, customer.ID)
	require.NoError(t, err)
	assert.Equal(t, addr.ID, got.ID)

	// Wrong customer returns not found.
	_, err = svc.GetAddress(ctx, tx, addr.ID, uuid.New())
	assert.ErrorIs(t, err, app.ErrAddressNotFound)
}
