package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// newSubscriptionServiceWithCatalog builds a SubscriptionService wired with the
// catalog and pricing stores that ChangeVariant requires.
func newSubscriptionServiceWithCatalog() *app.SubscriptionService {
	return app.NewSubscriptionService(
		store.NewSubscriptionStore(nil),
		store.NewOrderStore(nil),
		audit.NewAuditWriter(),
		metrics.NewRegistry(),
	).WithCatalog(store.NewCatalogStore(), store.NewPricingStore())
}

// createTestSubscription inserts an active subscription on the given variant. The
// period window is fixed so tests can assert next_order_at is untouched by a
// variant change.
func createTestSubscription(t *testing.T, tx pgx.Tx, customerID, planID, variantID, addrID uuid.UUID) *domain.Subscription {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	sub, err := store.NewSubscriptionStore(nil).Create(context.Background(), tx, store.CreateSubscriptionParams{
		CustomerID:         customerID,
		PlanID:             planID,
		VariantID:          variantID,
		Quantity:           1,
		Status:             domain.SubscriptionStatusActive,
		ShippingAddressID:  addrID,
		CurrentPeriodStart: now,
		CurrentPeriodEnd:   now.Add(30 * 24 * time.Hour),
		NextOrderAt:        now.Add(30 * 24 * time.Hour),
	})
	require.NoError(t, err)
	return sub
}

func TestSubscriptionService_ChangeVariant(t *testing.T) {
	pool := testPool
	ctx := context.Background()
	actor := testutil.TestActor()
	subs := newSubscriptionServiceWithCatalog()

	// setup creates a customer, address, plan, a product with a 12oz variant
	// ($16.20) and a 3lb variant ($45.00), and an active subscription on the 3lb
	// variant. It returns the subscription plus both variant IDs.
	setup := func(t *testing.T, tx pgx.Tx) (sub *domain.Subscription, threeLb, twelveOz *domain.Variant) {
		t.Helper()
		customer := testutil.CreateCustomer(t, tx)
		addr := testutil.CreateAddress(t, tx, customer.ID)
		product := testutil.CreateProduct(t, tx)
		twelveOz = testutil.CreateVariant(t, tx, product.ID, testutil.WithSKU("COFFEE-12OZ"))
		threeLb = testutil.CreateVariant(t, tx, product.ID, testutil.WithSKU("COFFEE-3LB"))
		testutil.SetBasePriceForVariant(t, tx, twelveOz.ID, 1620, "USD")
		testutil.SetBasePriceForVariant(t, tx, threeLb.ID, 4500, "USD")

		plan, err := store.NewSubscriptionStore(nil).CreatePlan(ctx, tx, store.CreatePlanParams{
			Name:          "Monthly",
			Interval:      domain.SubscriptionIntervalEvery30Days,
			IntervalCount: 1,
			DiscountPct:   10,
			IsActive:      true,
		})
		require.NoError(t, err)
		sub = createTestSubscription(t, tx, customer.ID, plan.ID, threeLb.ID, addr.ID)
		return sub, threeLb, twelveOz
	}

	t.Run("same-price swap still succeeds without the flag", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		sub, _, twelveOz := setup(t, tx)
		// A second 12oz-priced variant (e.g. a grind option) on the same product.
		sibling := testutil.CreateVariant(t, tx, twelveOz.ProductID, testutil.WithSKU("COFFEE-3LB-GROUND"))
		testutil.SetBasePriceForVariant(t, tx, sibling.ID, 4500, "USD") // same as current 3lb

		updated, err := subs.ChangeVariant(ctx, tx, sub.ID, sibling.ID, false, actor)
		require.NoError(t, err)
		assert.Equal(t, sibling.ID, updated.VariantID)
	})

	t.Run("different-price swap is rejected when flag is false", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		sub, _, twelveOz := setup(t, tx)

		_, err := subs.ChangeVariant(ctx, tx, sub.ID, twelveOz.ID, false, actor)
		assert.ErrorIs(t, err, app.ErrVariantPriceMismatch)
	})

	t.Run("different-price swap succeeds with the flag and leaves the schedule untouched", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		sub, _, twelveOz := setup(t, tx)

		updated, err := subs.ChangeVariant(ctx, tx, sub.ID, twelveOz.ID, true, actor)
		require.NoError(t, err)
		assert.Equal(t, twelveOz.ID, updated.VariantID)
		// No out-of-cycle order: the renewal schedule is untouched.
		assert.Equal(t, sub.NextOrderAt, updated.NextOrderAt)
		assert.Equal(t, sub.CurrentPeriodStart, updated.CurrentPeriodStart)
		assert.Equal(t, sub.CurrentPeriodEnd, updated.CurrentPeriodEnd)

		// Audit entry records the old and new price.
		entries, err := store.NewAuditStore().ListByResource(ctx, tx, "subscription", sub.ID)
		require.NoError(t, err)
		require.NotEmpty(t, entries)
		var found bool
		for _, e := range entries {
			if e.Action != audit.AuditSubscriptionVariantChanged {
				continue
			}
			found = true
			assert.EqualValues(t, 4500, e.Metadata["old_price"])
			assert.EqualValues(t, 1620, e.Metadata["new_price"])
		}
		assert.True(t, found, "expected a subscription_variant_changed audit entry")
	})

	t.Run("archived target is rejected even with the flag", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		sub, _, twelveOz := setup(t, tx)
		_, err := store.NewCatalogStore().ArchiveVariant(ctx, tx, twelveOz.ID)
		require.NoError(t, err)

		_, err = subs.ChangeVariant(ctx, tx, sub.ID, twelveOz.ID, true, actor)
		assert.ErrorIs(t, err, app.ErrVariantArchived)
	})

	t.Run("cross-product target is rejected even with the flag", func(t *testing.T) {
		tx := testutil.NewTestTx(t, pool)
		sub, _, _ := setup(t, tx)
		otherProduct := testutil.CreateProduct(t, tx, testutil.WithProductSlug("other-coffee"))
		otherVariant := testutil.CreateVariant(t, tx, otherProduct.ID, testutil.WithSKU("OTHER-12OZ"))
		testutil.SetBasePriceForVariant(t, tx, otherVariant.ID, 1620, "USD")

		_, err := subs.ChangeVariant(ctx, tx, sub.ID, otherVariant.ID, true, actor)
		assert.ErrorIs(t, err, app.ErrVariantNotOnSameProduct)
	})
}
