package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// setOrderShape forces the columns the list filters and sorts on. The order
// fixture doesn't expose total or placed_at as options.
func setOrderShape(t *testing.T, tx pgx.Tx, id any, total int, placedAt time.Time) {
	t.Helper()
	_, err := tx.Exec(context.Background(),
		`UPDATE orders SET total = $2, placed_at = $3 WHERE id = $1`, id, total, placedAt)
	require.NoError(t, err)
}

// seedSortableOrders creates three retail orders with distinct totals and
// placed dates, all for one customer so a search term can scope to them.
func seedSortableOrders(t *testing.T, tx pgx.Tx) (*domain.Customer, []*domain.Order) {
	t.Helper()
	customer := testutil.CreateCustomer(t, tx,
		testutil.WithCustomerName("Sortable", "Tester"),
		testutil.WithEmail("sortable.tester@example.com"))
	ship := testutil.CreateAddress(t, tx, customer.ID)
	bill := testutil.CreateAddress(t, tx, customer.ID)

	base := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	o1 := testutil.CreateOrder(t, tx, customer.ID, ship.ID, bill.ID)
	o2 := testutil.CreateOrder(t, tx, customer.ID, ship.ID, bill.ID)
	o3 := testutil.CreateOrder(t, tx, customer.ID, ship.ID, bill.ID)

	setOrderShape(t, tx, o1.ID, 1000, base)                   // cheapest, oldest
	setOrderShape(t, tx, o2.ID, 5000, base.AddDate(0, 0, 5))  // middle
	setOrderShape(t, tx, o3.ID, 2500, base.AddDate(0, 0, 10)) // newest

	return customer, []*domain.Order{o1, o2, o3}
}

func TestOrderSearch_SortByTotal(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newOrderService()
	ctx := context.Background()

	customer, _ := seedSortableOrders(t, tx)

	desc, err := svc.ListOrders(ctx, tx, store.OrderFilter{
		CustomerID: &customer.ID,
		Sort:       store.OrderSortTotalDesc,
	})
	require.NoError(t, err)
	require.Len(t, desc, 3)
	assert.Equal(t, 5000, desc[0].Total)
	assert.Equal(t, 1000, desc[2].Total)

	asc, err := svc.ListOrders(ctx, tx, store.OrderFilter{
		CustomerID: &customer.ID,
		Sort:       store.OrderSortTotalAsc,
	})
	require.NoError(t, err)
	require.Len(t, asc, 3)
	assert.Equal(t, 1000, asc[0].Total)
	assert.Equal(t, 5000, asc[2].Total)
}

func TestOrderSearch_SortByPlaced(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newOrderService()
	ctx := context.Background()

	customer, _ := seedSortableOrders(t, tx)

	asc, err := svc.ListOrders(ctx, tx, store.OrderFilter{
		CustomerID: &customer.ID,
		Sort:       store.OrderSortPlacedAsc,
	})
	require.NoError(t, err)
	require.Len(t, asc, 3)
	assert.True(t, asc[0].PlacedAt.Before(asc[2].PlacedAt))

	// The zero value must keep the historical default: newest first.
	def, err := svc.ListOrders(ctx, tx, store.OrderFilter{CustomerID: &customer.ID})
	require.NoError(t, err)
	require.Len(t, def, 3)
	assert.True(t, def[0].PlacedAt.After(def[2].PlacedAt))
}

// An unrecognised sort must not reach the SQL.
func TestOrderSearch_UnknownSortFallsBackToDefault(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newOrderService()
	ctx := context.Background()

	customer, _ := seedSortableOrders(t, tx)

	got, err := svc.ListOrders(ctx, tx, store.OrderFilter{
		CustomerID: &customer.ID,
		Sort:       store.OrderSort("total DESC; DROP TABLE orders"),
	})
	require.NoError(t, err)
	assert.Len(t, got, 3)
}

func TestOrderSearch_FilterByTotalRange(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newOrderService()
	ctx := context.Background()

	customer, _ := seedSortableOrders(t, tx)
	min, max := 2000, 4000

	filter := store.OrderFilter{
		CustomerID: &customer.ID,
		TotalMin:   &min,
		TotalMax:   &max,
	}

	got, err := svc.ListOrders(ctx, tx, filter)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, 2500, got[0].Total)

	// Count must agree with List or the "X–Y of Z" label lies.
	count, err := svc.CountOrders(ctx, tx, filter)
	require.NoError(t, err)
	assert.Equal(t, len(got), count)
}

func TestOrderSearch_FilterByPlacedRange(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newOrderService()
	ctx := context.Background()

	customer, _ := seedSortableOrders(t, tx)

	from := time.Date(2026, 3, 14, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 3, 16, 23, 59, 59, 0, time.UTC)

	filter := store.OrderFilter{
		CustomerID: &customer.ID,
		PlacedFrom: &from,
		PlacedTo:   &to,
	}

	got, err := svc.ListOrders(ctx, tx, filter)
	require.NoError(t, err)
	require.Len(t, got, 1, "only the Mar 15 order falls in range")
	assert.Equal(t, 5000, got[0].Total)

	count, err := svc.CountOrders(ctx, tx, filter)
	require.NoError(t, err)
	assert.Equal(t, len(got), count)
}

// Search must reach the customer's company name — wholesale orders were
// previously unfindable by the company that placed them.
func TestOrderSearch_MatchesCompanyName(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newOrderService()
	ctx := context.Background()

	customer := testutil.CreateCustomer(t, tx,
		testutil.WithCustomerName("Buyer", "Person"),
		testutil.WithEmail("buyer.person@example.com"))
	makeWholesale(t, tx, customer.ID, "approved", "Ironside Roasters LLC")
	ship := testutil.CreateAddress(t, tx, customer.ID)
	bill := testutil.CreateAddress(t, tx, customer.ID)
	testutil.CreateOrder(t, tx, customer.ID, ship.ID, bill.ID)

	got, err := svc.ListOrders(ctx, tx, store.OrderFilter{Search: "Ironside Roasters"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.NotNil(t, got[0].CustomerID)
	assert.Equal(t, customer.ID, *got[0].CustomerID)

	// And the count query must agree, or the tab totals contradict the rows.
	count, err := svc.CountOrders(ctx, tx, store.OrderFilter{Search: "Ironside Roasters"})
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

// The refactor moved three queries onto one WHERE builder; this pins the
// property that made it worth doing.
func TestOrderSearch_ListAndCountAgreeUnderCombinedFilters(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newOrderService()
	ctx := context.Background()

	customer, _ := seedSortableOrders(t, tx)
	min := 2000

	filter := store.OrderFilter{
		CustomerID: &customer.ID,
		TotalMin:   &min,
		Sort:       store.OrderSortTotalDesc,
	}

	got, err := svc.ListOrders(ctx, tx, filter)
	require.NoError(t, err)
	count, err := svc.CountOrders(ctx, tx, filter)
	require.NoError(t, err)

	assert.Equal(t, len(got), count)
	assert.Equal(t, 2, count)
}
