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

func newCatalogService() *app.CatalogService {
	return app.NewCatalogService(store.NewCatalogStore(), audit.NewAuditWriter(), metrics.NewRegistry())
}

func TestCatalogService_CreateProduct(t *testing.T) {
	pool := testPool
	tx := testutil.NewTestTx(t, pool)
	svc := newCatalogService()
	ctx := context.Background()
	actor := testutil.TestActor()
	taxon := testutil.CreateTaxon(t, tx)

	product, err := svc.CreateProduct(ctx, tx, store.CreateProductParams{
		Slug:        "test-coffee",
		Title:       "Test Coffee",
		Description: "A fine test coffee",
		Status:      domain.ProductStatusActive,
		TaxonID:     taxon.ID,
	}, actor)
	require.NoError(t, err)
	assert.Equal(t, "test-coffee", product.Slug)
	assert.Equal(t, "Test Coffee", product.Title)

	entry := testutil.LastAuditEntry(t, tx, "product", product.ID)
	assert.Equal(t, audit.AuditProductCreated, entry.Action)
}

func TestCatalogService_GetProduct(t *testing.T) {
	pool := testPool
	tx := testutil.NewTestTx(t, pool)
	svc := newCatalogService()
	ctx := context.Background()

	product := testutil.CreateProduct(t, tx)

	got, err := svc.GetProduct(ctx, tx, product.ID)
	require.NoError(t, err)
	assert.Equal(t, product.ID, got.ID)

	_, err = svc.GetProduct(ctx, tx, uuid.New())
	assert.ErrorIs(t, err, app.ErrProductNotFound)
}

func TestCatalogService_UpdateProduct(t *testing.T) {
	pool := testPool
	tx := testutil.NewTestTx(t, pool)
	svc := newCatalogService()
	ctx := context.Background()
	actor := testutil.TestActor()

	product := testutil.CreateProduct(t, tx)

	updated, err := svc.UpdateProduct(ctx, tx, product.ID, store.UpdateProductParams{
		Slug:        "updated-slug",
		Title:       "Updated Title",
		Description: "Updated description",
		TaxonID:     product.TaxonID,
	}, actor)
	require.NoError(t, err)
	assert.Equal(t, "updated-slug", updated.Slug)
	assert.Equal(t, "Updated Title", updated.Title)

	entry := testutil.LastAuditEntry(t, tx, "product", product.ID)
	assert.Equal(t, audit.AuditProductUpdated, entry.Action)
}

func TestCatalogService_UpdateProductStatus(t *testing.T) {
	pool := testPool
	ctx := context.Background()
	actor := testutil.TestActor()
	svc := newCatalogService()

	t.Run("archive writes AuditProductArchived", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		product := testutil.CreateProduct(t, tx)

		got, err := svc.UpdateProductStatus(ctx, tx, product.ID, domain.ProductStatusArchived, actor)
		require.NoError(t, err)
		assert.Equal(t, domain.ProductStatusArchived, got.Status)

		entry := testutil.LastAuditEntry(t, tx, "product", product.ID)
		assert.Equal(t, audit.AuditProductArchived, entry.Action)
	})

	t.Run("draft writes AuditProductUpdated", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		product := testutil.CreateProduct(t, tx)

		got, err := svc.UpdateProductStatus(ctx, tx, product.ID, domain.ProductStatusDraft, actor)
		require.NoError(t, err)
		assert.Equal(t, domain.ProductStatusDraft, got.Status)

		entry := testutil.LastAuditEntry(t, tx, "product", product.ID)
		assert.Equal(t, audit.AuditProductUpdated, entry.Action)
	})
}

func TestCatalogService_CreateVariant(t *testing.T) {
	pool := testPool
	tx := testutil.NewTestTx(t, pool)
	svc := newCatalogService()
	ctx := context.Background()

	product := testutil.CreateProduct(t, tx)

	actor := testutil.TestActor()

	v, err := svc.CreateVariant(ctx, tx, store.CreateVariantParams{
		ProductID: product.ID,
		SKU:       "UNIQUE-SKU-1",
		IsDefault: true,
	}, actor)
	require.NoError(t, err)
	assert.Equal(t, "UNIQUE-SKU-1", v.SKU)

	// Duplicate SKU should fail.
	_, err = svc.CreateVariant(ctx, tx, store.CreateVariantParams{
		ProductID: product.ID,
		SKU:       "UNIQUE-SKU-1",
	}, actor)
	assert.ErrorIs(t, err, app.ErrSKUAlreadyExists)
}

func TestCatalogService_UpdateVariant(t *testing.T) {
	pool := testPool
	tx := testutil.NewTestTx(t, pool)
	svc := newCatalogService()
	ctx := context.Background()

	product := testutil.CreateProduct(t, tx)
	v1 := testutil.CreateVariant(t, tx, product.ID, testutil.WithSKU("SKU-A"))
	testutil.CreateVariant(t, tx, product.ID, testutil.WithSKU("SKU-B"))

	actor := testutil.TestActor()

	// Same SKU unchanged is OK.
	updated, err := svc.UpdateVariant(ctx, tx, v1.ID, store.UpdateVariantParams{
		SKU:       "SKU-A",
		IsDefault: true,
	}, actor)
	require.NoError(t, err)
	assert.Equal(t, "SKU-A", updated.SKU)

	// Changing to taken SKU errors.
	_, err = svc.UpdateVariant(ctx, tx, v1.ID, store.UpdateVariantParams{
		SKU: "SKU-B",
	}, actor)
	assert.ErrorIs(t, err, app.ErrSKUAlreadyExists)
}

func TestCatalogService_GetTaxon(t *testing.T) {
	pool := testPool
	tx := testutil.NewTestTx(t, pool)
	svc := newCatalogService()
	ctx := context.Background()

	taxon := testutil.CreateTaxon(t, tx)

	got, err := svc.GetTaxon(ctx, tx, taxon.ID)
	require.NoError(t, err)
	assert.Equal(t, taxon.ID, got.ID)

	_, err = svc.GetTaxon(ctx, tx, uuid.New())
	assert.ErrorIs(t, err, app.ErrTaxonNotFound)
}
