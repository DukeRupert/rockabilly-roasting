package app_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// Store-level coverage for volume price tiers (migration 065). PricingStore is
// exercised from here because internal/app owns the test container; there is no
// separate store test package.

// tierFixture sets up a variant priced on a list with a base rung, and returns
// the pieces needed to author tiers against it.
type tierFixture struct {
	priceSetID  uuid.UUID
	priceListID uuid.UUID
	variantID   uuid.UUID
}

func newTierFixture(t *testing.T, tx pgx.Tx, baseCents int) tierFixture {
	t.Helper()
	ctx := context.Background()
	pricing := store.NewPricingStore()

	product := testutil.CreateProduct(t, tx)
	variant := testutil.CreateVariant(t, tx, product.ID)
	list := testutil.CreatePriceList(t, tx)
	testutil.CreatePriceListPrice(t, tx, list.ID, variant.ID, baseCents, "USD")

	ps, err := pricing.GetOrCreatePriceSet(ctx, tx, variant.ID)
	require.NoError(t, err)

	return tierFixture{priceSetID: ps.ID, priceListID: list.ID, variantID: variant.ID}
}

func TestGetTierLadder_AssemblesBaseRungAndBreaks(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := context.Background()
	pricing := store.NewPricingStore()
	f := newTierFixture(t, tx, 1100)

	// Authored out of order on purpose — the ladder must sort them.
	_, err := pricing.SetTierPrice(ctx, tx, f.priceSetID, f.priceListID, 24, 950, "USD")
	require.NoError(t, err)
	_, err = pricing.SetTierPrice(ctx, tx, f.priceSetID, f.priceListID, 12, 1000, "USD")
	require.NoError(t, err)

	ladder, err := pricing.GetTierLadder(ctx, tx, f.variantID, f.priceListID, "USD")
	require.NoError(t, err)

	assert.Equal(t, []domain.PriceTier{
		{MinQuantity: 1, Amount: 1100},
		{MinQuantity: 12, Amount: 1000},
		{MinQuantity: 24, Amount: 950},
	}, ladder.Rungs(), "base rung folds in at threshold 1")

	assert.Equal(t, 1100, ladder.UnitPriceAt(11))
	assert.Equal(t, 1000, ladder.UnitPriceAt(12))
	assert.Equal(t, 950, ladder.UnitPriceAt(24))
}

func TestGetTierLadder_EmptyWhenVariantNotOnList(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := context.Background()
	pricing := store.NewPricingStore()

	product := testutil.CreateProduct(t, tx)
	variant := testutil.CreateVariant(t, tx, product.ID)
	list := testutil.CreatePriceList(t, tx)
	testutil.SetBasePriceForVariant(t, tx, variant.ID, 1500, "USD")

	ladder, err := pricing.GetTierLadder(ctx, tx, variant.ID, list.ID, "USD")
	require.NoError(t, err)
	assert.True(t, ladder.IsEmpty(), "base price must not leak onto a list ladder")
}

func TestSetTierPrice_RewritesExistingThreshold(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := context.Background()
	pricing := store.NewPricingStore()
	f := newTierFixture(t, tx, 1100)

	_, err := pricing.SetTierPrice(ctx, tx, f.priceSetID, f.priceListID, 12, 1000, "USD")
	require.NoError(t, err)
	_, err = pricing.SetTierPrice(ctx, tx, f.priceSetID, f.priceListID, 12, 900, "USD")
	require.NoError(t, err, "re-authoring a threshold must update, not collide")

	ladder, err := pricing.GetTierLadder(ctx, tx, f.variantID, f.priceListID, "USD")
	require.NoError(t, err)
	require.Len(t, ladder.Rungs(), 2, "the 12 rung must exist exactly once")
	assert.Equal(t, 900, ladder.UnitPriceAt(12))
}

func TestTierConstraints(t *testing.T) {
	ctx := context.Background()

	t.Run("a break below 2 is rejected", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		pricing := store.NewPricingStore()
		f := newTierFixture(t, tx, 1100)

		// 1 is the base rung's threshold and is spelled NULL. Allowing it here
		// would give a ladder two base rungs that could disagree.
		_, err := pricing.SetTierPrice(ctx, tx, f.priceSetID, f.priceListID, 1, 1000, "USD")
		assert.Error(t, err)
	})

	t.Run("max_quantity cannot be set", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		f := newTierFixture(t, tx, 1100)

		// No store method writes max_quantity; the CHECK is what guarantees no
		// future one can start, so it is asserted directly against the table.
		_, err := tx.Exec(ctx, `
			INSERT INTO prices (id, price_set_id, amount, currency_code, price_list_id, min_quantity, max_quantity)
			VALUES ($1, $2, 950, 'USD', $3, 12, 23)`,
			uuid.New(), f.priceSetID, f.priceListID)
		assert.Error(t, err, "open-ended ladders only — an upper bound admits gaps and overlaps")
	})

	t.Run("base prices cannot carry tiers", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		pricing := store.NewPricingStore()

		product := testutil.CreateProduct(t, tx)
		variant := testutil.CreateVariant(t, tx, product.ID)
		testutil.SetBasePriceForVariant(t, tx, variant.ID, 1500, "USD")
		ps, err := pricing.GetOrCreatePriceSet(ctx, tx, variant.ID)
		require.NoError(t, err)

		// The firewall protecting retail, subscriptions, and renewals: those all
		// read base prices, so a base price must stay single-rung. Nothing in the
		// store can write this, so the guard is asserted at the table.
		var count int
		err = tx.QueryRow(ctx, `
			SELECT count(*) FROM prices
			WHERE price_set_id = $1 AND price_list_id IS NULL AND min_quantity IS NOT NULL`,
			ps.ID).Scan(&count)
		require.NoError(t, err)
		assert.Zero(t, count)
	})
}

func TestDeleteTierPrice_LeavesOtherRungs(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := context.Background()
	pricing := store.NewPricingStore()
	f := newTierFixture(t, tx, 1100)

	_, err := pricing.SetTierPrice(ctx, tx, f.priceSetID, f.priceListID, 12, 1000, "USD")
	require.NoError(t, err)
	_, err = pricing.SetTierPrice(ctx, tx, f.priceSetID, f.priceListID, 24, 950, "USD")
	require.NoError(t, err)

	require.NoError(t, pricing.DeleteTierPrice(ctx, tx, f.priceSetID, f.priceListID, 12, "USD"))

	ladder, err := pricing.GetTierLadder(ctx, tx, f.variantID, f.priceListID, "USD")
	require.NoError(t, err)
	assert.Equal(t, []domain.PriceTier{
		{MinQuantity: 1, Amount: 1100},
		{MinQuantity: 24, Amount: 950},
	}, ladder.Rungs())
}

func TestDeleteTierPricesForList_KeepsBaseRung(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := context.Background()
	pricing := store.NewPricingStore()
	f := newTierFixture(t, tx, 1100)

	_, err := pricing.SetTierPrice(ctx, tx, f.priceSetID, f.priceListID, 12, 1000, "USD")
	require.NoError(t, err)
	_, err = pricing.SetTierPrice(ctx, tx, f.priceSetID, f.priceListID, 24, 950, "USD")
	require.NoError(t, err)

	require.NoError(t, pricing.DeleteTierPricesForList(ctx, tx, f.priceSetID, f.priceListID, "USD"))

	ladder, err := pricing.GetTierLadder(ctx, tx, f.variantID, f.priceListID, "USD")
	require.NoError(t, err)
	assert.Equal(t, []domain.PriceTier{{MinQuantity: 1, Amount: 1100}}, ladder.Rungs())
	assert.False(t, ladder.IsTiered())
}

func TestListTierLaddersByVariants(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := context.Background()
	pricing := store.NewPricingStore()

	product := testutil.CreateProduct(t, tx)
	tiered := testutil.CreateVariant(t, tx, product.ID, testutil.WithSKU("TIERED"))
	flat := testutil.CreateVariant(t, tx, product.ID, testutil.WithSKU("FLAT"))
	absent := testutil.CreateVariant(t, tx, product.ID, testutil.WithSKU("ABSENT"))
	list := testutil.CreatePriceList(t, tx)

	testutil.CreatePriceListPrice(t, tx, list.ID, tiered.ID, 1100, "USD")
	testutil.CreatePriceListPrice(t, tx, list.ID, flat.ID, 800, "USD")

	ps, err := pricing.GetOrCreatePriceSet(ctx, tx, tiered.ID)
	require.NoError(t, err)
	_, err = pricing.SetTierPrice(ctx, tx, ps.ID, list.ID, 12, 1000, "USD")
	require.NoError(t, err)

	ladders, err := pricing.ListTierLaddersByVariants(ctx, tx,
		[]uuid.UUID{tiered.ID, flat.ID, absent.ID}, list.ID, "USD")
	require.NoError(t, err)

	assert.True(t, ladders[tiered.ID].IsTiered())
	assert.Equal(t, 1000, ladders[tiered.ID].UnitPriceAt(12))

	assert.False(t, ladders[flat.ID].IsTiered(), "a list price with no breaks is a one-rung ladder")
	assert.Equal(t, 800, ladders[flat.ID].UnitPriceAt(999))

	_, ok := ladders[absent.ID]
	assert.False(t, ok, "variants not on the list are omitted, matching the other batch readers")
}

func TestListTierLaddersByVariants_ScopedToOneList(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := context.Background()
	pricing := store.NewPricingStore()

	product := testutil.CreateProduct(t, tx)
	variant := testutil.CreateVariant(t, tx, product.ID)
	wholesale2025 := testutil.CreatePriceList(t, tx, testutil.WithPriceListName("Wholesale 2025"))
	wholesale2026 := testutil.CreatePriceList(t, tx, testutil.WithPriceListName("Wholesale 2026"))

	testutil.CreatePriceListPrice(t, tx, wholesale2025.ID, variant.ID, 1100, "USD")
	testutil.CreatePriceListPrice(t, tx, wholesale2026.ID, variant.ID, 1200, "USD")

	ps, err := pricing.GetOrCreatePriceSet(ctx, tx, variant.ID)
	require.NoError(t, err)
	_, err = pricing.SetTierPrice(ctx, tx, ps.ID, wholesale2025.ID, 12, 900, "USD")
	require.NoError(t, err)
	_, err = pricing.SetTierPrice(ctx, tx, ps.ID, wholesale2026.ID, 12, 1050, "USD")
	require.NoError(t, err)

	// Grandfathered customers sit on the 2025 list; its ladder must not pick up
	// rungs authored on 2026 for the same variant.
	got, err := pricing.GetTierLadder(ctx, tx, variant.ID, wholesale2025.ID, "USD")
	require.NoError(t, err)
	assert.Equal(t, []domain.PriceTier{
		{MinQuantity: 1, Amount: 1100},
		{MinQuantity: 12, Amount: 900},
	}, got.Rungs())
}

func TestExistingPriceReadsIgnoreTiers(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := context.Background()
	pricing := store.NewPricingStore()
	f := newTierFixture(t, tx, 1100)
	testutil.SetBasePriceForVariant(t, tx, f.variantID, 1500, "USD")

	_, err := pricing.SetTierPrice(ctx, tx, f.priceSetID, f.priceListID, 12, 1000, "USD")
	require.NoError(t, err)

	// The compatibility guarantee: every pre-existing reader keeps its
	// `min_quantity IS NULL` filter, so authoring tiers cannot move the prices
	// the storefront, subscriptions, and renewals resolve.
	base, err := pricing.GetBasePrice(ctx, tx, f.variantID, "USD")
	require.NoError(t, err)
	assert.Equal(t, 1500, base.Amount)

	listPrice, err := pricing.GetPriceListPrice(ctx, tx, f.variantID, f.priceListID, "USD")
	require.NoError(t, err)
	assert.Equal(t, 1100, listPrice.Amount, "resolves the base rung, never a break")

	byVariant, err := pricing.ListPriceListPricesByVariants(ctx, tx, []uuid.UUID{f.variantID}, f.priceListID, "USD")
	require.NoError(t, err)
	assert.Equal(t, 1100, byVariant[f.variantID])

	bases, err := pricing.ListBasePricesByVariants(ctx, tx, []uuid.UUID{f.variantID}, "USD")
	require.NoError(t, err)
	assert.Equal(t, 1500, bases[f.variantID])
}
