package store_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// Migration 092 moves the cards the bank refused for good from each
// subscription's metadata to the customer. A card it missed would be charged
// again by the customer's next renewal, so it runs here against rows in the
// shapes earlier builds wrote: the list, the single-card key before it, both at
// once, and subscriptions that have since ended.
func TestMigration092_CopiesEveryRecordedDeadCardToItsCustomer(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := context.Background()

	first := claimableSubscription(t, tx)
	owner := first.CustomerID
	withMeta := func(customerID uuid.UUID, status, meta string) {
		t.Helper()
		sub := claimableSubscription(t, tx)
		_, err := tx.Exec(ctx, `UPDATE subscriptions
			SET customer_id = $2, status = $3, metadata = $4::jsonb WHERE id = $1`,
			sub.ID, customerID, status, meta)
		require.NoError(t, err)
	}
	withMeta(owner, "past_due", `{"dunning_hard_decline": true, "dunning_decline_code": "lost_card",
		"dunning_dead_payment_methods": ["pm_a", "pm_b"]}`)
	withMeta(owner, "past_due", `{"dunning_dead_payment_method": "pm_c",
		"dunning_dead_payment_methods": ["pm_a"]}`)
	withMeta(owner, "expired", `{"dunning_dead_payment_method": "pm_d"}`)
	withMeta(owner, "active", `{"dunning_dead_payment_methods": "not a list", "dunning_dead_payment_method": ""}`)

	clean := claimableSubscription(t, tx)
	withMeta(clean.CustomerID, "past_due", `{"dunning_attempt": 2}`)

	_, err := tx.Exec(ctx, `DROP TABLE customer_dead_payment_methods`)
	require.NoError(t, err)
	runMigrationUp(t, tx, "092_customer_dead_payment_methods.sql")

	customers := store.NewCustomerStore()
	dead, err := customers.DeadPaymentMethods(ctx, tx, owner)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"pm_a", "pm_b", "pm_c", "pm_d"}, dead,
		"every card, from either key and any status, once")

	none, err := customers.DeadPaymentMethods(ctx, tx, clean.CustomerID)
	require.NoError(t, err)
	assert.Empty(t, none, "a customer with no dead card gets none")

	var code string
	require.NoError(t, tx.QueryRow(ctx, `SELECT decline_code FROM customer_dead_payment_methods
		WHERE customer_id = $1 AND payment_method_id = 'pm_b'`, owner).Scan(&code))
	assert.Equal(t, "lost_card", code, "the issuer's reason comes with it")
}

// runMigrationUp executes the Up half of a goose migration in tx. The suite
// migrates an empty database, so a backfill is tested by running it again
// against rows made in the state it was written for.
func runMigrationUp(t *testing.T, tx pgx.Tx, name string) {
	t.Helper()
	raw, err := os.ReadFile("../../db/migrations/" + name)
	require.NoError(t, err)
	up, _, found := strings.Cut(string(raw), "-- +goose Down")
	require.True(t, found, "migration has a Down section")
	_, err = tx.Exec(context.Background(), up)
	require.NoError(t, err)
}
