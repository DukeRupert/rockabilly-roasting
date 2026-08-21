package app_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// subSearchWorld is the cast the admin subscription list tests filter over:
// three customers, two coffees, two plans, and one subscription per
// interesting combination. Every name, email, and title carries the caller's
// token so assertions stay scoped to their own rows.
type subSearchWorld struct {
	store *store.SubscriptionStore
	// Plans and products, so tests can filter by one of them.
	monthlyPlan, weeklyPlan  uuid.UUID
	houseProduct, ritProduct uuid.UUID
	houseVariant, ritVariant uuid.UUID
	// Customers, named so the sort assertions read plainly.
	alvarez, kagan, zimmer uuid.UUID
}

func seedSubSearchWorld(t *testing.T, tx pgx.Tx, token string) subSearchWorld {
	t.Helper()
	ctx := context.Background()
	s := store.NewSubscriptionStore(nil)

	newCustomer := func(first, last string) uuid.UUID {
		c := testutil.CreateCustomer(t, tx,
			testutil.WithCustomerName(first, last+token),
			testutil.WithEmail(fmt.Sprintf("%s.%s@example.com", first, token)))
		return c.ID
	}

	w := subSearchWorld{store: s}
	w.alvarez = newCustomer("billie", "Alvarez")
	w.kagan = newCustomer("ash", "Kagan")
	w.zimmer = newCustomer("cass", "Zimmer")

	house := testutil.CreateProduct(t, tx, testutil.WithProductTitle("House Blend "+token))
	rit := testutil.CreateProduct(t, tx, testutil.WithProductTitle("Rite of Spring "+token))
	w.houseProduct, w.ritProduct = house.ID, rit.ID
	w.houseVariant = testutil.CreateVariant(t, tx, house.ID, testutil.WithSKU("SUBQ-HOUSE-"+token)).ID
	w.ritVariant = testutil.CreateVariant(t, tx, rit.ID, testutil.WithSKU("SUBQ-RITE-"+token)).ID

	monthly, err := s.CreatePlan(ctx, tx, store.CreatePlanParams{
		Name: "Monthly " + token, Interval: domain.SubscriptionIntervalEvery30Days,
		IntervalCount: 1, IsActive: true,
	})
	require.NoError(t, err)
	weekly, err := s.CreatePlan(ctx, tx, store.CreatePlanParams{
		Name: "Weekly " + token, Interval: domain.SubscriptionIntervalEvery7Days,
		IntervalCount: 1, IsActive: true,
	})
	require.NoError(t, err)
	w.monthlyPlan, w.weeklyPlan = monthly.ID, weekly.ID
	return w
}

// sub inserts one subscription into the world and returns its id.
func (w subSearchWorld) sub(t *testing.T, tx pgx.Tx, customerID, planID, variantID uuid.UUID, status domain.SubscriptionStatus, nextOrderAt time.Time) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	addr := testutil.CreateAddress(t, tx, customerID)
	sub, err := w.store.Create(ctx, tx, store.CreateSubscriptionParams{
		CustomerID:         customerID,
		PlanID:             planID,
		VariantID:          variantID,
		Quantity:           1,
		Status:             status,
		ShippingAddressID:  addr.ID,
		CurrentPeriodStart: nextOrderAt.AddDate(0, 0, -30),
		CurrentPeriodEnd:   nextOrderAt,
		NextOrderAt:        nextOrderAt,
	})
	require.NoError(t, err)
	return sub.ID
}

// scoped narrows a filter to one test's own rows via the shared token, so the
// assertions hold no matter what else the database contains.
func scoped(token string, f store.SubscriptionFilter) store.SubscriptionFilter {
	f.CustomerQuery = token
	return f
}

func TestSubscriptionSearch_SortByCustomerName(t *testing.T) {
	ctx := context.Background()
	tx := testutil.NewTestTx(t, testPool)
	token := "subsrt1"
	w := seedSubSearchWorld(t, tx, token)
	day := time.Now().AddDate(0, 0, 5)

	w.sub(t, tx, w.zimmer, w.monthlyPlan, w.houseVariant, domain.SubscriptionStatusActive, day)
	w.sub(t, tx, w.alvarez, w.monthlyPlan, w.houseVariant, domain.SubscriptionStatusActive, day)
	w.sub(t, tx, w.kagan, w.monthlyPlan, w.houseVariant, domain.SubscriptionStatusActive, day)

	// Ordering follows the name as displayed — "First Last" — so ash sorts
	// ahead of billie, not Alvarez ahead of Kagan.
	asc, err := w.store.List(ctx, tx, scoped(token, store.SubscriptionFilter{Sort: store.SubscriptionSortCustomerAsc}))
	require.NoError(t, err)
	require.Len(t, asc, 3)
	assert.Equal(t, w.kagan, asc[0].CustomerID, "ash Kagan sorts first by displayed name")
	assert.Equal(t, w.alvarez, asc[1].CustomerID)
	assert.Equal(t, w.zimmer, asc[2].CustomerID)

	desc, err := w.store.List(ctx, tx, scoped(token, store.SubscriptionFilter{Sort: store.SubscriptionSortCustomerDesc}))
	require.NoError(t, err)
	require.Len(t, desc, 3)
	assert.Equal(t, w.zimmer, desc[0].CustomerID)
	assert.Equal(t, w.kagan, desc[2].CustomerID)
}

// An unrecognised sort value must not blow up the query — the store falls back
// to the default ordering, mirroring how the handler clamps the param.
func TestSubscriptionSearch_UnknownSortFallsBackToDefault(t *testing.T) {
	ctx := context.Background()
	tx := testutil.NewTestTx(t, testPool)
	token := "subsrt2"
	w := seedSubSearchWorld(t, tx, token)
	day := time.Now().AddDate(0, 0, 5)
	w.sub(t, tx, w.kagan, w.monthlyPlan, w.houseVariant, domain.SubscriptionStatusActive, day)

	subs, err := w.store.List(ctx, tx, scoped(token, store.SubscriptionFilter{
		Sort: store.SubscriptionSort("created_at DESC; DROP TABLE subscriptions"),
	}))
	require.NoError(t, err)
	assert.Len(t, subs, 1)
}

func TestSubscriptionSearch_FilterByPlanAndProduct(t *testing.T) {
	ctx := context.Background()
	tx := testutil.NewTestTx(t, testPool)
	token := "subflt1"
	w := seedSubSearchWorld(t, tx, token)
	day := time.Now().AddDate(0, 0, 5)

	w.sub(t, tx, w.kagan, w.monthlyPlan, w.houseVariant, domain.SubscriptionStatusActive, day)
	w.sub(t, tx, w.alvarez, w.weeklyPlan, w.houseVariant, domain.SubscriptionStatusActive, day)
	w.sub(t, tx, w.zimmer, w.weeklyPlan, w.ritVariant, domain.SubscriptionStatusActive, day)

	byPlan, err := w.store.List(ctx, tx, scoped(token, store.SubscriptionFilter{PlanID: &w.weeklyPlan}))
	require.NoError(t, err)
	assert.Len(t, byPlan, 2)

	byProduct, err := w.store.List(ctx, tx, scoped(token, store.SubscriptionFilter{ProductID: &w.ritProduct}))
	require.NoError(t, err)
	require.Len(t, byProduct, 1)
	assert.Equal(t, w.zimmer, byProduct[0].CustomerID)

	// The two dimensions compose: weekly ∩ House Blend is one row, not three.
	both, err := w.store.List(ctx, tx, scoped(token, store.SubscriptionFilter{
		PlanID: &w.weeklyPlan, ProductID: &w.houseProduct,
	}))
	require.NoError(t, err)
	require.Len(t, both, 1)
	assert.Equal(t, w.alvarez, both[0].CustomerID)
}

func TestSubscriptionSearch_FilterByNextOrderWindow(t *testing.T) {
	ctx := context.Background()
	tx := testutil.NewTestTx(t, testPool)
	token := "subflt2"
	w := seedSubSearchWorld(t, tx, token)
	now := time.Now()

	overdue := w.sub(t, tx, w.kagan, w.monthlyPlan, w.houseVariant, domain.SubscriptionStatusActive, now.AddDate(0, 0, -3))
	soon := w.sub(t, tx, w.alvarez, w.monthlyPlan, w.houseVariant, domain.SubscriptionStatusActive, now.AddDate(0, 0, 3))
	w.sub(t, tx, w.zimmer, w.monthlyPlan, w.houseVariant, domain.SubscriptionStatusActive, now.AddDate(0, 0, 60))

	before := now
	past, err := w.store.List(ctx, tx, scoped(token, store.SubscriptionFilter{NextOrderTo: &before}))
	require.NoError(t, err)
	require.Len(t, past, 1)
	assert.Equal(t, overdue, past[0].ID)

	weekEnd := now.AddDate(0, 0, 7)
	upcoming, err := w.store.List(ctx, tx, scoped(token, store.SubscriptionFilter{
		NextOrderFrom: &before, NextOrderTo: &weekEnd,
	}))
	require.NoError(t, err)
	require.Len(t, upcoming, 1)
	assert.Equal(t, soon, upcoming[0].ID)
}

// The list and the "of N" total have to agree under every combination, or the
// pagination footer starts contradicting the rows above it.
func TestSubscriptionSearch_CountMatchesListUnderCombinedFilters(t *testing.T) {
	ctx := context.Background()
	tx := testutil.NewTestTx(t, testPool)
	token := "subcnt1"
	w := seedSubSearchWorld(t, tx, token)
	day := time.Now().AddDate(0, 0, 5)

	w.sub(t, tx, w.kagan, w.monthlyPlan, w.houseVariant, domain.SubscriptionStatusActive, day)
	w.sub(t, tx, w.alvarez, w.monthlyPlan, w.houseVariant, domain.SubscriptionStatusPaused, day)
	w.sub(t, tx, w.zimmer, w.weeklyPlan, w.ritVariant, domain.SubscriptionStatusActive, day)

	active := domain.SubscriptionStatusActive
	f := scoped(token, store.SubscriptionFilter{
		Status:    &active,
		PlanID:    &w.monthlyPlan,
		ProductID: &w.houseProduct,
	})

	subs, err := w.store.List(ctx, tx, f)
	require.NoError(t, err)
	count, err := w.store.Count(ctx, tx, f)
	require.NoError(t, err)
	assert.Equal(t, len(subs), count)
	assert.Equal(t, 1, count)

	// Count ignores paging, so page 2 of a 1-row set still reports the total.
	paged := f
	paged.Limit, paged.Offset = 1, 5
	pagedCount, err := w.store.Count(ctx, tx, paged)
	require.NoError(t, err)
	assert.Equal(t, 1, pagedCount)
}

// The status pills vary only the status dimension: each number must be what
// clicking that pill would show under the filters already applied.
func TestSubscriptionSearch_StatusCountsIgnoreStatusButHonourOtherFilters(t *testing.T) {
	ctx := context.Background()
	tx := testutil.NewTestTx(t, testPool)
	token := "subcnt2"
	w := seedSubSearchWorld(t, tx, token)
	day := time.Now().AddDate(0, 0, 5)

	w.sub(t, tx, w.kagan, w.monthlyPlan, w.houseVariant, domain.SubscriptionStatusActive, day)
	w.sub(t, tx, w.alvarez, w.monthlyPlan, w.houseVariant, domain.SubscriptionStatusPaused, day)
	w.sub(t, tx, w.zimmer, w.monthlyPlan, w.houseVariant, domain.SubscriptionStatusCancelled, day)
	w.sub(t, tx, w.zimmer, w.weeklyPlan, w.ritVariant, domain.SubscriptionStatusActive, day)

	cancelled := domain.SubscriptionStatusCancelled
	counts, err := w.store.CountsByStatus(ctx, tx, scoped(token, store.SubscriptionFilter{
		Status: &cancelled, // must be ignored
		PlanID: &w.monthlyPlan,
	}))
	require.NoError(t, err)
	assert.Equal(t, 1, counts[domain.SubscriptionStatusActive], "the weekly active row is excluded by the plan filter")
	assert.Equal(t, 1, counts[domain.SubscriptionStatusPaused])
	assert.Equal(t, 1, counts[domain.SubscriptionStatusCancelled])
	assert.Zero(t, counts[domain.SubscriptionStatusExpired], "a status with no rows is simply absent")
}

func TestSubscriptionSearch_MatchesCompanyName(t *testing.T) {
	ctx := context.Background()
	tx := testutil.NewTestTx(t, testPool)
	token := "subco1"
	w := seedSubSearchWorld(t, tx, token)
	makeWholesale(t, tx, w.zimmer, "approved", "Zimmer Coffee "+token)
	day := time.Now().AddDate(0, 0, 5)
	w.sub(t, tx, w.zimmer, w.monthlyPlan, w.houseVariant, domain.SubscriptionStatusActive, day)

	subs, err := w.store.List(ctx, tx, store.SubscriptionFilter{CustomerQuery: "Zimmer Coffee " + token})
	require.NoError(t, err)
	require.Len(t, subs, 1)
	assert.Equal(t, w.zimmer, subs[0].CustomerID)
}

func TestSubscriptionSearch_ListSubscribedProducts(t *testing.T) {
	ctx := context.Background()
	tx := testutil.NewTestTx(t, testPool)
	token := "subprod1"
	w := seedSubSearchWorld(t, tx, token)
	day := time.Now().AddDate(0, 0, 5)

	w.sub(t, tx, w.kagan, w.monthlyPlan, w.houseVariant, domain.SubscriptionStatusActive, day)
	w.sub(t, tx, w.alvarez, w.monthlyPlan, w.houseVariant, domain.SubscriptionStatusActive, day)
	w.sub(t, tx, w.zimmer, w.weeklyPlan, w.ritVariant, domain.SubscriptionStatusActive, day)

	products, err := w.store.ListSubscribedProducts(ctx, tx)
	require.NoError(t, err)

	byID := map[uuid.UUID]store.SubscribedProduct{}
	for _, p := range products {
		byID[p.ID] = p
	}
	assert.Equal(t, 2, byID[w.houseProduct].Count)
	assert.Equal(t, 1, byID[w.ritProduct].Count)
	assert.Equal(t, "House Blend "+token, byID[w.houseProduct].Title)
}
