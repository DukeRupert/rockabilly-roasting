package app_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
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

func TestCustomerService_UpdateEmailAsStaff(t *testing.T) {
	pool := testPool
	ctx := context.Background()
	svc := newCustomerService()
	actor := testutil.TestActor()

	t.Run("records who made the change and what the address was", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		customer := testutil.CreateCustomer(t, tx, testutil.WithEmail("old@example.com"))

		updated, err := svc.UpdateEmailAsStaff(ctx, tx, customer.ID, "new@example.com", actor)
		require.NoError(t, err)
		assert.Equal(t, "new@example.com", updated.Email)

		entry := testutil.LastAuditEntryWithAction(t, tx, "customer", customer.ID, audit.AuditCustomerEmailUpdated)
		var after map[string]any
		require.NoError(t, json.Unmarshal(entry.AfterSnapshot, &after))
		assert.Equal(t, "new@example.com", after["email"])

		// Support staff hold customers:write purely for this action, so the
		// trail has to name the person and the address they moved away from.
		assert.Equal(t, string(domain.AuditActorTypeStaff), entry.ActorType)
		assert.Equal(t, actor.Name, entry.ActorName)

		var metadata map[string]any
		require.NoError(t, tx.QueryRow(ctx,
			`SELECT metadata FROM audit_log WHERE id = $1`, entry.ID,
		).Scan(&metadata))
		assert.Equal(t, "old@example.com", metadata["previous_email"])
	})

	t.Run("normalizes before writing", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		customer := testutil.CreateCustomer(t, tx)

		updated, err := svc.UpdateEmailAsStaff(ctx, tx, customer.ID, "  Mixed.Case@Example.COM ", actor)
		require.NoError(t, err)
		assert.Equal(t, "mixed.case@example.com", updated.Email)
	})

	t.Run("clears verification so the new address is not vouched for", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		customer := testutil.CreateCustomer(t, tx, testutil.WithEmail("verified@example.com"))
		require.NoError(t, svc.VerifyEmail(ctx, tx, customer.ID))

		updated, err := svc.UpdateEmailAsStaff(ctx, tx, customer.ID, "moved@example.com", actor)
		require.NoError(t, err)
		assert.False(t, updated.EmailVerified)

		got, err := svc.GetCustomer(ctx, tx, customer.ID)
		require.NoError(t, err)
		assert.False(t, got.EmailVerified)
	})

	t.Run("no-op change leaves verification and the trail alone", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		customer := testutil.CreateCustomer(t, tx, testutil.WithEmail("steady@example.com"))
		require.NoError(t, svc.VerifyEmail(ctx, tx, customer.ID))

		updated, err := svc.UpdateEmailAsStaff(ctx, tx, customer.ID, "Steady@Example.com", actor)
		require.NoError(t, err)
		assert.Equal(t, "steady@example.com", updated.Email)
		assert.True(t, updated.EmailVerified)

		var count int
		require.NoError(t, tx.QueryRow(ctx,
			`SELECT count(*) FROM audit_log WHERE resource_type = 'customer' AND resource_id = $1 AND action = $2`,
			customer.ID, audit.AuditCustomerEmailUpdated,
		).Scan(&count))
		assert.Zero(t, count)
	})

	t.Run("refuses an address another customer holds", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		testutil.CreateCustomer(t, tx, testutil.WithEmail("taken@example.com"))
		other := testutil.CreateCustomer(t, tx)

		_, err := svc.UpdateEmailAsStaff(ctx, tx, other.ID, "TAKEN@example.com", actor)
		assert.ErrorIs(t, err, app.ErrEmailAlreadyExists)
	})

	t.Run("unknown customer", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)

		_, err := svc.UpdateEmailAsStaff(ctx, tx, uuid.New(), "nobody@example.com", actor)
		assert.ErrorIs(t, err, app.ErrCustomerNotFound)
	})
}

func TestCustomerService_UpdatePhone(t *testing.T) {
	pool := testPool
	ctx := context.Background()
	svc := newCustomerService()
	actor := testutil.TestActor()

	t.Run("set phone on customer with no phone", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		customer := testutil.CreateCustomer(t, tx)
		require.Nil(t, customer.Phone)

		phone := "(509) 555-0123"
		updated, err := svc.UpdatePhone(ctx, tx, customer.ID, &phone, actor)
		require.NoError(t, err)
		require.NotNil(t, updated.Phone)
		assert.Equal(t, phone, *updated.Phone)

		entry := testutil.LastAuditEntryWithAction(t, tx, "customer", customer.ID, audit.AuditCustomerPhoneUpdated)
		var after map[string]any
		require.NoError(t, json.Unmarshal(entry.AfterSnapshot, &after))
		assert.Equal(t, phone, after["phone"])
	})

	t.Run("clear phone with nil", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		existing := "(509) 555-9999"
		customer := testutil.CreateCustomer(t, tx, testutil.WithPhone(&existing))
		require.NotNil(t, customer.Phone)

		updated, err := svc.UpdatePhone(ctx, tx, customer.ID, nil, actor)
		require.NoError(t, err)
		assert.Nil(t, updated.Phone)

		entry := testutil.LastAuditEntryWithAction(t, tx, "customer", customer.ID, audit.AuditCustomerPhoneUpdated)
		var after map[string]any
		require.NoError(t, json.Unmarshal(entry.AfterSnapshot, &after))
		assert.Nil(t, after["phone"])
	})

	t.Run("replace existing phone", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		old := "(509) 555-0001"
		customer := testutil.CreateCustomer(t, tx, testutil.WithPhone(&old))

		newPhone := "(509) 555-0002"
		updated, err := svc.UpdatePhone(ctx, tx, customer.ID, &newPhone, actor)
		require.NoError(t, err)
		require.NotNil(t, updated.Phone)
		assert.Equal(t, newPhone, *updated.Phone)
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

func TestCustomerService_UpdatePreferredLocalFulfillmentSelf(t *testing.T) {
	pool := testPool
	ctx := context.Background()
	svc := newCustomerService()
	cstore := store.NewCustomerStore()

	t.Run("shipped is a valid preference and persists", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		customer := testutil.CreateCustomer(t, tx)
		shipped := domain.ShippingMethodShipped

		require.NoError(t, svc.UpdatePreferredLocalFulfillmentSelf(ctx, tx, customer.ID, &shipped))

		got, err := cstore.GetByID(ctx, tx, customer.ID)
		require.NoError(t, err)
		require.NotNil(t, got.PreferredLocalFulfillment)
		assert.Equal(t, domain.ShippingMethodShipped, *got.PreferredLocalFulfillment)
	})

	t.Run("nil clears the preference", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		customer := testutil.CreateCustomer(t, tx)
		delivery := domain.ShippingMethodLocalDelivery
		require.NoError(t, svc.UpdatePreferredLocalFulfillmentSelf(ctx, tx, customer.ID, &delivery))

		require.NoError(t, svc.UpdatePreferredLocalFulfillmentSelf(ctx, tx, customer.ID, nil))

		got, err := cstore.GetByID(ctx, tx, customer.ID)
		require.NoError(t, err)
		assert.Nil(t, got.PreferredLocalFulfillment)
	})

	t.Run("an unknown method is rejected", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		customer := testutil.CreateCustomer(t, tx)
		bogus := domain.ShippingMethod("carrier_pigeon")

		err := svc.UpdatePreferredLocalFulfillmentSelf(ctx, tx, customer.ID, &bogus)
		assert.Error(t, err)
	})
}
