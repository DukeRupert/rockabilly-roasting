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
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

func newPriceListService() *app.PriceListService {
	return app.NewPriceListService(store.NewPriceListStore(), audit.NewAuditWriter(), metrics.NewRegistry())
}

func TestPriceListService_Create(t *testing.T) {
	pool := testPool
	tx := testutil.NewTestTx(t, pool)
	ctx := context.Background()
	svc := newPriceListService()

	pl, err := svc.Create(ctx, tx, "Wholesale Tier 1", domain.PriceListTypeOverride, domain.PriceListStatusActive, testutil.TestActor())
	require.NoError(t, err)
	assert.Equal(t, "Wholesale Tier 1", pl.Name)
	assert.Equal(t, domain.PriceListTypeOverride, pl.Type)
	assert.Equal(t, domain.PriceListStatusActive, pl.Status)
	assert.NotEqual(t, uuid.Nil, pl.ID)
}

func TestPriceListService_CreateDefaultsTypeAndStatus(t *testing.T) {
	pool := testPool
	tx := testutil.NewTestTx(t, pool)
	ctx := context.Background()
	svc := newPriceListService()

	pl, err := svc.Create(ctx, tx, "Defaults", "", "", testutil.TestActor())
	require.NoError(t, err)
	assert.Equal(t, domain.PriceListTypeOverride, pl.Type)
	assert.Equal(t, domain.PriceListStatusActive, pl.Status)
}

func TestPriceListService_Get(t *testing.T) {
	pool := testPool
	tx := testutil.NewTestTx(t, pool)
	ctx := context.Background()
	svc := newPriceListService()

	created := testutil.CreatePriceList(t, tx, testutil.WithPriceListName("Lookup Test"))

	got, err := svc.Get(ctx, tx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, created.ID, got.ID)
	assert.Equal(t, created.Name, got.Name)

	_, err = svc.Get(ctx, tx, uuid.New())
	assert.ErrorIs(t, err, app.ErrPriceListNotFound)
}

func TestPriceListService_List(t *testing.T) {
	pool := testPool
	tx := testutil.NewTestTx(t, pool)
	ctx := context.Background()
	svc := newPriceListService()

	a := testutil.CreatePriceList(t, tx, testutil.WithPriceListName("Aaa"))
	b := testutil.CreatePriceList(t, tx, testutil.WithPriceListName("Bbb"))

	lists, err := svc.List(ctx, tx)
	require.NoError(t, err)

	ids := make(map[uuid.UUID]bool, len(lists))
	for _, pl := range lists {
		ids[pl.ID] = true
	}
	assert.True(t, ids[a.ID])
	assert.True(t, ids[b.ID])
}

func TestPriceListService_Update(t *testing.T) {
	pool := testPool
	tx := testutil.NewTestTx(t, pool)
	ctx := context.Background()
	svc := newPriceListService()

	created := testutil.CreatePriceList(t, tx, testutil.WithPriceListName("Old Name"))

	updated, err := svc.Update(ctx, tx, created.ID, "New Name", domain.PriceListStatusExpired, testutil.TestActor())
	require.NoError(t, err)
	assert.Equal(t, "New Name", updated.Name)
	assert.Equal(t, domain.PriceListStatusExpired, updated.Status)

	_, err = svc.Update(ctx, tx, uuid.New(), "X", domain.PriceListStatusActive, testutil.TestActor())
	assert.ErrorIs(t, err, app.ErrPriceListNotFound)
}

func TestPriceListService_Delete(t *testing.T) {
	pool := testPool
	tx := testutil.NewTestTx(t, pool)
	ctx := context.Background()
	svc := newPriceListService()

	created := testutil.CreatePriceList(t, tx)

	require.NoError(t, svc.Delete(ctx, tx, created.ID, testutil.TestActor()))

	_, err := svc.Get(ctx, tx, created.ID)
	assert.ErrorIs(t, err, app.ErrPriceListNotFound)
}

func TestPriceListService_DeleteNullsCustomerAssignment(t *testing.T) {
	pool := testPool
	tx := testutil.NewTestTx(t, pool)
	ctx := context.Background()
	svc := newPriceListService()
	custSvc := app.NewCustomerService(store.NewCustomerStore(), audit.NewAuditWriter(), metrics.NewRegistry())

	pl := testutil.CreatePriceList(t, tx)
	customer := testutil.CreateCustomer(t, tx)

	require.NoError(t, custSvc.UpdatePriceList(ctx, tx, customer.ID, &pl.ID, testutil.TestActor()))

	require.NoError(t, svc.Delete(ctx, tx, pl.ID, testutil.TestActor()))

	// FK ON DELETE SET NULL — customer's price_list_id should now be nil.
	got, err := custSvc.GetCustomer(ctx, tx, customer.ID)
	require.NoError(t, err)
	assert.Nil(t, got.PriceListID)
}

func TestPriceListService_CountCustomers(t *testing.T) {
	pool := testPool
	tx := testutil.NewTestTx(t, pool)
	ctx := context.Background()
	svc := newPriceListService()
	custSvc := app.NewCustomerService(store.NewCustomerStore(), audit.NewAuditWriter(), metrics.NewRegistry())

	pl := testutil.CreatePriceList(t, tx)

	n, err := svc.CountCustomers(ctx, tx, pl.ID)
	require.NoError(t, err)
	assert.Equal(t, 0, n)

	c1 := testutil.CreateCustomer(t, tx)
	c2 := testutil.CreateCustomer(t, tx)
	require.NoError(t, custSvc.UpdatePriceList(ctx, tx, c1.ID, &pl.ID, testutil.TestActor()))
	require.NoError(t, custSvc.UpdatePriceList(ctx, tx, c2.ID, &pl.ID, testutil.TestActor()))

	n, err = svc.CountCustomers(ctx, tx, pl.ID)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
}
