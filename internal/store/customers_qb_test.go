package store_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// The QuickBooks link was written but never read back: the query selected
// qb_customer_id and the row type carried it, but customerFromRow dropped it,
// so every customer came back looking unlinked. EnsureQBCustomer therefore
// took find-or-create on every run and SyncQBPayment returned early every
// time, so payments were never sent to QuickBooks at all.
func TestCustomerReadsBackItsQuickBooksLink(t *testing.T) {
	ctx := t.Context()
	tx := testutil.NewTestTx(t, testPool)
	customers := store.NewCustomerStore()
	customer := testutil.CreateCustomer(t, tx)

	fresh, err := customers.GetByID(ctx, tx, customer.ID)
	require.NoError(t, err)
	assert.Nil(t, fresh.QBCustomerID, "a customer starts unlinked")
	assert.Nil(t, fresh.QBSyncedAt)

	require.NoError(t, customers.SetQBCustomerID(ctx, tx, customer.ID, "62"))

	linked, err := customers.GetByID(ctx, tx, customer.ID)
	require.NoError(t, err)
	require.NotNil(t, linked.QBCustomerID, "the link must survive a read, or the QB jobs cannot see it")
	assert.Equal(t, "62", *linked.QBCustomerID)
	assert.NotNil(t, linked.QBSyncedAt, "the sync time is what decides whether details are re-pushed")
}
