package store_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// List is hand-written SQL (a dynamic WHERE), so a new discount column has to
// be added to its SELECT and Scan by hand. The admin list reads all three
// settings from it; a column left out would show every discount as the default.
func TestDiscountStore_ListReadsTheSettings(t *testing.T) {
	ctx := context.Background()
	tx := testutil.NewTestTx(t, testPool)
	want := testutil.CreateDiscount(t, tx,
		testutil.WithUsageLimit(domain.CouponUsageOncePerCustomer),
		testutil.WithEligibility(domain.DiscountEligibilityNewSubscribers),
		testutil.WithAppliesTo(domain.DiscountAppliesToSubscriptionSignup))

	all, err := store.NewDiscountStore().List(ctx, tx, store.DiscountFilter{Limit: 200})
	require.NoError(t, err)
	var got *domain.Discount
	for i := range all {
		if all[i].ID == want.ID {
			got = &all[i]
		}
	}
	require.NotNil(t, got, "the discount just created is listed")
	assert.Equal(t, domain.CouponUsageOncePerCustomer, got.UsageLimit)
	assert.Equal(t, domain.DiscountEligibilityNewSubscribers, got.Eligibility)
	assert.Equal(t, domain.DiscountAppliesToSubscriptionSignup, got.AppliesTo)
}
