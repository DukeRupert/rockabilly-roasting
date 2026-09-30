package app_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/testutil"
)

// The audit entry for a placed order has to name who placed it, not just that
// one was placed.
//
// Every assertion on this entry used to read .Action, which an entry filed
// under the wrong actor satisfies. Actor is what the log is for: three of the
// five staff roles can reach an order path, the renewal worker reaches the same
// service as domain.SystemActor, and "who did this" is the first question asked
// of a disputed order.
func TestPlaceOrder_AuditEntryNamesTheActor(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	ctx := context.Background()
	svc := newCheckoutService()
	actor := testutil.TestActor()

	customer := testutil.CreateCustomer(t, tx)
	addr := testutil.CreateAddress(t, tx, customer.ID)
	product := testutil.CreateProduct(t, tx)
	variant := testutil.CreateVariant(t, tx, product.ID)
	testutil.SetBasePriceForVariant(t, tx, variant.ID, 1500, "USD")

	order, err := svc.PlaceOrder(ctx, tx, app.PlaceOrderParams{
		CustomerID:        customer.ID,
		ShippingAddressID: addr.ID,
		BillingAddressID:  addr.ID,
		CurrencyCode:      "USD",
		Items:             []app.CartItem{{VariantID: variant.ID, Quantity: 2, UnitPrice: 1500}},
		ShippingCents:     500,
		TaxCents:          240,
	}, actor)
	require.NoError(t, err)

	entry := testutil.LastAuditEntry(t, tx, "order", order.ID)
	assert.Equal(t, audit.AuditOrderCreated, entry.Action)
	assert.Equal(t, string(domain.AuditActorTypeStaff), entry.ActorType,
		"a staff-placed order must not be filed as the system's")
	assert.Equal(t, actor.Name, entry.ActorName)

	// And the snapshot is the order as priced, not a stub. A log that records
	// the totals is the only after-the-fact answer to "what were they charged"
	// once an admin has edited the order.
	var snap struct {
		Total         int
		Subtotal      int
		DiscountTotal int
	}
	require.NoError(t, json.Unmarshal(entry.AfterSnapshot, &snap))
	assert.Equal(t, order.Total, snap.Total)
	assert.Equal(t, order.Subtotal, snap.Subtotal)
	assert.Equal(t, order.DiscountTotal, snap.DiscountTotal)
}
