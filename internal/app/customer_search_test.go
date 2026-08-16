package app_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// makeWholesale promotes a fixture customer to a wholesale account. The
// fixture helper has no option for this and the search/filter tests need one.
func makeWholesale(t *testing.T, tx pgx.Tx, id any, status, company string) {
	t.Helper()
	_, err := tx.Exec(context.Background(),
		`UPDATE customers SET account_type = 'wholesale', wholesale_status = $2, company_name = $3 WHERE id = $1`,
		id, status, company)
	require.NoError(t, err)
}

func setVerified(t *testing.T, tx pgx.Tx, id any, verified bool) {
	t.Helper()
	_, err := tx.Exec(context.Background(),
		`UPDATE customers SET email_verified = $2 WHERE id = $1`, id, verified)
	require.NoError(t, err)
}

// seedSearchCustomers creates a small, deliberately-named cast. Every name and
// email carries the same unique token so the tests can scope their assertions
// to their own rows regardless of anything else in the database.
func seedSearchCustomers(t *testing.T, tx pgx.Tx, token string) map[string]*domain.Customer {
	t.Helper()
	out := map[string]*domain.Customer{}

	out["ash"] = testutil.CreateCustomer(t, tx,
		testutil.WithCustomerName("Ash", "Kagan"+token),
		testutil.WithEmail(fmt.Sprintf("ash.%s@example.com", token)))
	out["billie"] = testutil.CreateCustomer(t, tx,
		testutil.WithCustomerName("Billie", "Alvarez"+token),
		testutil.WithEmail(fmt.Sprintf("billie.%s@example.com", token)))
	out["cass"] = testutil.CreateCustomer(t, tx,
		testutil.WithCustomerName("Cass", "Zimmer"+token),
		testutil.WithEmail(fmt.Sprintf("cass.%s@example.com", token)))

	makeWholesale(t, tx, out["cass"].ID, "approved", "Zimmer Coffee "+token)
	setVerified(t, tx, out["ash"].ID, true)
	setVerified(t, tx, out["billie"].ID, false)
	setVerified(t, tx, out["cass"].ID, false)

	return out
}

func TestCustomerSearch_SortByName(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCustomerService()
	ctx := context.Background()
	token := "srt1"

	seedSearchCustomers(t, tx, token)

	asc, err := svc.ListCustomers(ctx, tx, store.CustomerFilter{
		Search: token,
		Sort:   store.CustomerSortNameAsc,
	})
	require.NoError(t, err)
	require.Len(t, asc, 3)
	assert.Equal(t, "Alvarez"+token, asc[0].LastName)
	assert.Equal(t, "Zimmer"+token, asc[2].LastName)

	desc, err := svc.ListCustomers(ctx, tx, store.CustomerFilter{
		Search: token,
		Sort:   store.CustomerSortNameDesc,
	})
	require.NoError(t, err)
	require.Len(t, desc, 3)
	assert.Equal(t, "Zimmer"+token, desc[0].LastName)
	assert.Equal(t, "Alvarez"+token, desc[2].LastName)
}

func TestCustomerSearch_SortByEmail(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCustomerService()
	ctx := context.Background()
	token := "srt2"

	seedSearchCustomers(t, tx, token)

	asc, err := svc.ListCustomers(ctx, tx, store.CustomerFilter{
		Search: token,
		Sort:   store.CustomerSortEmailAsc,
	})
	require.NoError(t, err)
	require.Len(t, asc, 3)
	assert.Equal(t, fmt.Sprintf("ash.%s@example.com", token), asc[0].Email)
}

// An unrecognised sort value must not blow up the query — the store falls back
// to the default ordering, mirroring how the handler clamps the param.
func TestCustomerSearch_UnknownSortFallsBackToDefault(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCustomerService()
	ctx := context.Background()
	token := "srt3"

	seedSearchCustomers(t, tx, token)

	got, err := svc.ListCustomers(ctx, tx, store.CustomerFilter{
		Search: token,
		Sort:   store.CustomerSort("created_at DESC; DROP TABLE customers"),
	})
	require.NoError(t, err)
	assert.Len(t, got, 3)
}

func TestCustomerSearch_MatchesCompanyName(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCustomerService()
	ctx := context.Background()
	token := "cmp1"

	people := seedSearchCustomers(t, tx, token)

	// "Zimmer Coffee" is only on the company column, not the name or email.
	got, err := svc.ListCustomers(ctx, tx, store.CustomerFilter{
		Search: "Zimmer Coffee " + token,
	})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, people["cass"].ID, got[0].ID)
}

func TestCustomerSearch_FilterByTypeAndVerified(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCustomerService()
	ctx := context.Background()
	token := "flt1"

	people := seedSearchCustomers(t, tx, token)
	wholesale := domain.AccountTypeWholesale
	verified := true

	byType, err := svc.ListCustomers(ctx, tx, store.CustomerFilter{
		Search:      token,
		AccountType: &wholesale,
	})
	require.NoError(t, err)
	require.Len(t, byType, 1)
	assert.Equal(t, people["cass"].ID, byType[0].ID)

	byVerified, err := svc.ListCustomers(ctx, tx, store.CustomerFilter{
		Search:        token,
		EmailVerified: &verified,
	})
	require.NoError(t, err)
	require.Len(t, byVerified, 1)
	assert.Equal(t, people["ash"].ID, byVerified[0].ID)
}

// Count must agree with List, or the "X–Y of Z" pagination label lies.
func TestCustomerSearch_CountMatchesList(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCustomerService()
	ctx := context.Background()
	token := "cnt1"

	seedSearchCustomers(t, tx, token)
	unverified := false

	filter := store.CustomerFilter{
		Search:        token,
		EmailVerified: &unverified,
	}

	listed, err := svc.ListCustomers(ctx, tx, filter)
	require.NoError(t, err)
	count, err := svc.CountCustomers(ctx, tx, filter)
	require.NoError(t, err)
	assert.Equal(t, len(listed), count)
	assert.Equal(t, 2, count)
}

func TestCustomerSearch_PaginationOffset(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCustomerService()
	ctx := context.Background()
	token := "pag1"

	seedSearchCustomers(t, tx, token)

	first, err := svc.ListCustomers(ctx, tx, store.CustomerFilter{
		Search: token, Sort: store.CustomerSortNameAsc, Limit: 2, Offset: 0,
	})
	require.NoError(t, err)
	require.Len(t, first, 2)

	second, err := svc.ListCustomers(ctx, tx, store.CustomerFilter{
		Search: token, Sort: store.CustomerSortNameAsc, Limit: 2, Offset: 2,
	})
	require.NoError(t, err)
	require.Len(t, second, 1)
	assert.Equal(t, "Zimmer"+token, second[0].LastName)
}

// The whole point of the feature: a misspelled term that the exact ILIKE search
// misses should still surface the right person.
func TestCustomerSearch_SuggestsFuzzyMatch(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCustomerService()
	ctx := context.Background()
	token := "fzy1"

	people := seedSearchCustomers(t, tx, token)

	// "Kagen" is a plausible misspelling of the seeded "Kagan".
	exact, err := svc.ListCustomers(ctx, tx, store.CustomerFilter{Search: "Kagen" + token})
	require.NoError(t, err)
	require.Empty(t, exact, "exact search should miss the misspelling")

	suggestions, err := svc.SuggestCustomers(ctx, tx, "Kagen"+token, 5)
	require.NoError(t, err)
	require.NotEmpty(t, suggestions, "fuzzy search should recover the misspelling")

	var found bool
	for _, s := range suggestions {
		if s.ID == people["ash"].ID {
			found = true
		}
	}
	assert.True(t, found, "expected Ash Kagan among fuzzy suggestions")
}

// A term with nothing near it should return nothing rather than noise.
func TestCustomerSearch_SuggestReturnsEmptyForNonsense(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCustomerService()
	ctx := context.Background()

	seedSearchCustomers(t, tx, "fzy2")

	got, err := svc.SuggestCustomers(ctx, tx, "qqzzxxwwvvuu", 5)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestCustomerSearch_SuggestEmptyTerm(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc := newCustomerService()
	ctx := context.Background()

	got, err := svc.SuggestCustomers(ctx, tx, "", 5)
	require.NoError(t, err)
	assert.Empty(t, got)
}
