package web

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/testutil"
)

// A subscription is priced from the base price less its plan's discount,
// because that is what RenewalService charges for every box after the first,
// whatever price list the customer is on. This is the multi-line form of
// TestPriceLines_ASubscriptionPricesFromTheBaseLikeItsRenewals, driven through
// the endpoint: a returning customer whom staff put on a list priced below base
// signs up for two lines on two plans. Charged the list price, the first box
// would cost one number and every renewal another, and nothing would compare
// the two.
func TestSubscribePaymentIntent_ACustomerOnAPriceListIsChargedBaseLessEachLinesPlan(t *testing.T) {
	f := newMultiLineFixture(t) // bases 1800 and 1000; weekly 10% off, monthly 5% off
	d, fake := newSubscribePaymentDeps(t)

	ctx := context.Background()
	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx) //nolint:errcheck
	list := testutil.CreatePriceList(t, tx)
	testutil.CreatePriceListPrice(t, tx, list.ID, f.first, 1200, "USD")
	testutil.CreatePriceListPrice(t, tx, list.ID, f.second, 700, "USD")
	email := "listed-" + uuid.NewString() + "@example.test"
	customer := testutil.CreateCustomer(t, tx, testutil.WithEmail(email), testutil.WithPriceList(list.ID))
	require.NoError(t, tx.Commit(ctx))

	w := postSubscribePaymentIntent(t, d, multiLineBody(f.twoLines(), func(m map[string]any) {
		m["email"] = email
	}))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	resp := decodeSubscribeIntentResponse(t, w)

	byPlan := map[string]int{}
	for _, l := range resp.Lines {
		byPlan[l.PlanID] = l.UnitPrice
	}
	assert.Equal(t, map[string]int{
		f.weekly.ID.String():  1620, // 1800 less 10%, not 1200 less 10%
		f.monthly.ID.String(): 950,  // 1000 less 5%, not 700 less 5%
	}, byPlan)
	assert.Equal(t, 1620+2*950, resp.Subtotal)
	assert.Equal(t, resp.Subtotal+resp.ShippingTotal+resp.TaxTotal, resp.Amount)

	req, ok := fake.lastIntent()
	require.True(t, ok, "an intent was created")
	assert.Equal(t, int64(resp.Amount), req.AmountCents, "the card is charged what the page is told")

	// The order is the existing customer's, and records the same prices.
	order, lines := orderForIntent(t, d, piIDFromClientSecret(resp.ClientSecret))
	require.NotNil(t, order.CustomerID)
	assert.Equal(t, customer.ID, *order.CustomerID, "the returning customer, so their list was in play")
	assert.Equal(t, 1620, lineByVariant(t, lines, f.first).UnitPrice)
	assert.Equal(t, 950, lineByVariant(t, lines, f.second).UnitPrice)
	assert.Equal(t, resp.Subtotal, order.Subtotal)
}
