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
	"github.com/dukerupert/hiri/internal/store/sqlcgen"
	"github.com/dukerupert/hiri/internal/testutil"
)

func newCatalogService() *app.CatalogService {
	return app.NewCatalogService(store.NewCatalogStore(), store.NewCustomerStore(), store.NewCustomerGroupStore(), audit.NewAuditWriter(), metrics.NewRegistry())
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

func TestCatalogService_ArchiveVariant(t *testing.T) {
	pool := testPool
	tx := testutil.NewTestTx(t, pool)
	svc := newCatalogService()
	ctx := context.Background()
	actor := testutil.TestActor()

	product := testutil.CreateProduct(t, tx)
	v := testutil.CreateVariant(t, tx, product.ID, testutil.WithSKU("ARCH-1"))

	archived, err := svc.ArchiveVariant(ctx, tx, v.ID, actor)
	require.NoError(t, err)
	require.NotNil(t, archived.ArchivedAt)

	// Audit record written.
	testutil.LastAuditEntryWithAction(t, tx, "variant", v.ID, audit.AuditVariantArchived)

	// Active list excludes the archived variant.
	active, err := svc.ListActiveVariants(ctx, tx, product.ID)
	require.NoError(t, err)
	for _, av := range active {
		assert.NotEqual(t, v.ID, av.ID, "archived variant must not appear in ListActiveVariants")
	}

	// Full list still includes it.
	all, err := svc.ListVariants(ctx, tx, product.ID)
	require.NoError(t, err)
	found := false
	for _, av := range all {
		if av.ID == v.ID {
			found = true
			require.NotNil(t, av.ArchivedAt)
		}
	}
	assert.True(t, found, "archived variant must still appear in ListVariants")

	// Unarchive clears the flag.
	unarchived, err := svc.UnarchiveVariant(ctx, tx, v.ID, actor)
	require.NoError(t, err)
	assert.Nil(t, unarchived.ArchivedAt)

	testutil.LastAuditEntryWithAction(t, tx, "variant", v.ID, audit.AuditVariantUnarchived)
}

func TestCatalogService_DeleteVariant_WithLineItems_ReturnsErrVariantInUse(t *testing.T) {
	pool := testPool
	tx := testutil.NewTestTx(t, pool)
	svc := newCatalogService()
	ctx := context.Background()
	actor := testutil.TestActor()

	product := testutil.CreateProduct(t, tx)
	variant := testutil.CreateVariant(t, tx, product.ID, testutil.WithSKU("INUSE-1"))
	customer := testutil.CreateCustomer(t, tx)
	addr := testutil.CreateAddress(t, tx, customer.ID)
	order := testutil.CreateOrder(t, tx, customer.ID, addr.ID, addr.ID)

	_, err := sqlcgen.New(tx).CreateLineItem(ctx, sqlcgen.CreateLineItemParams{
		ID:        uuid.New(),
		OrderID:   order.ID,
		VariantID: variant.ID,
		Quantity:  1,
		UnitPrice: 1000,
		Subtotal:  1000,
		Total:     1000,
		Metadata:  json.RawMessage(`{}`),
	})
	require.NoError(t, err)

	err = svc.DeleteVariant(ctx, tx, variant.ID, actor)
	assert.ErrorIs(t, err, app.ErrVariantInUse,
		"DeleteVariant must surface a domain error, not a generic 500, when an order references the variant")
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
