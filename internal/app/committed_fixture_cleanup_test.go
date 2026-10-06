package app_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

// cleanupCommittedCustomer deletes, when the test ends, a committed customer
// and everything a subscription or renewal test hangs off it: orders and their
// lines, subscriptions and their links, addresses. productIDs and planIDs are
// the catalog rows the test committed for it.
//
// Tests that drive RenewalService or another pool-taking method cannot use
// testutil.NewTestTx, so their fixtures are committed. Left behind, they are
// read by every later test that lists the whole catalog or every due
// subscription — the wholesale quick-order tests are the first to notice.
func cleanupCommittedCustomer(t *testing.T, customerID uuid.UUID, productIDs, planIDs []uuid.UUID) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		for _, q := range []struct {
			sql string
			arg any
		}{
			{`DELETE FROM subscription_orders WHERE subscription_id IN (SELECT id FROM subscriptions WHERE customer_id = $1)`, customerID},
			{`DELETE FROM subscription_orders WHERE order_id IN (SELECT id FROM orders WHERE customer_id = $1)`, customerID},
			{`UPDATE orders SET subscription_id = NULL WHERE customer_id = $1`, customerID},
			{`DELETE FROM line_items WHERE order_id IN (SELECT id FROM orders WHERE customer_id = $1)`, customerID},
			{`DELETE FROM orders WHERE customer_id = $1`, customerID},
			{`DELETE FROM subscriptions WHERE customer_id = $1`, customerID},
			{`DELETE FROM addresses WHERE customer_id = $1`, customerID},
			{`DELETE FROM customers WHERE id = $1`, customerID},
			{`DELETE FROM subscription_plans WHERE id = ANY($1)`, planIDs},
			{`DELETE FROM variants WHERE product_id = ANY($1)`, productIDs},
			{`DELETE FROM products WHERE id = ANY($1)`, productIDs},
		} {
			_, err := testPool.Exec(ctx, q.sql, q.arg)
			assert.NoError(t, err, q.sql)
		}
	})
}
