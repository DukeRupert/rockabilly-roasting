package app_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// A signup order records each line's plan on the line itself, so
// one order can carry a weekly line and a monthly line and activation can tell
// them apart. These tests pin the record and its reader, including the one-line
// fallback for orders placed before the line carried it.

// signupFixture is one customer with an address and two priced variants.
type signupFixture struct {
	customer *domain.Customer
	addr     *domain.Address
	product  *domain.Product
	first    *domain.Variant
	second   *domain.Variant
}

func newSignupFixture(t *testing.T, tx pgx.Tx) signupFixture {
	t.Helper()
	customer := testutil.CreateCustomer(t, tx)
	addr := testutil.CreateAddress(t, tx, customer.ID)
	product := testutil.CreateProduct(t, tx)
	first := testutil.CreateVariant(t, tx, product.ID)
	second := testutil.CreateVariant(t, tx, product.ID)
	testutil.SetBasePriceForVariant(t, tx, first.ID, 1800, "USD")
	testutil.SetBasePriceForVariant(t, tx, second.ID, 1000, "USD")
	return signupFixture{customer: customer, addr: addr, product: product, first: first, second: second}
}

func (f signupFixture) place(t *testing.T, tx pgx.Tx, items []app.CartItem, meta map[string]any) (*domain.Order, []domain.LineItem) {
	t.Helper()
	ctx := context.Background()
	order, err := newCheckoutService().PlaceOrder(ctx, tx, app.PlaceOrderParams{
		CustomerID:        f.customer.ID,
		ShippingAddressID: f.addr.ID,
		BillingAddressID:  f.addr.ID,
		CurrencyCode:      "USD",
		Items:             items,
		Metadata:          meta,
	}, testutil.TestActor())
	require.NoError(t, err)
	lines, err := store.NewOrderStore(nil).ListLineItems(ctx, tx, order.ID)
	require.NoError(t, err)
	return order, lines
}

// lineFor finds an order's line by variant. ListLineItemsByOrder sorts by a
// random UUID, so index order says nothing about the order lines were added.
func lineFor(t *testing.T, lines []domain.LineItem, variantID uuid.UUID) domain.LineItem {
	t.Helper()
	for _, l := range lines {
		if l.VariantID == variantID {
			return l
		}
	}
	t.Fatalf("no line for variant %s", variantID)
	return domain.LineItem{}
}

func TestPlaceOrder_WritesEachSignupLinesPlanOntoTheLine(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	f := newSignupFixture(t, tx)

	planID := uuid.New()
	_, lines := f.place(t, tx, []app.CartItem{
		{VariantID: f.first.ID, Quantity: 1, UnitPrice: 1800, SubscriptionPlanID: &planID},
		{VariantID: f.second.ID, Quantity: 1, UnitPrice: 1000},
	}, nil)
	require.Len(t, lines, 2)

	signup := lineFor(t, lines, f.first.ID)
	assert.Equal(t, planID.String(), signup.Metadata["subscription_plan_id"])

	retail := lineFor(t, lines, f.second.ID)
	assert.Empty(t, retail.Metadata, "a retail line carries nothing")
}

func TestSignupLines(t *testing.T) {
	t.Run("reads each line's plan off the line", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		f := newSignupFixture(t, tx)

		weekly, monthly := uuid.New(), uuid.New()
		order, items := f.place(t, tx, []app.CartItem{
			{VariantID: f.first.ID, Quantity: 2, UnitPrice: 1800, SubscriptionPlanID: &weekly},
			{VariantID: f.second.ID, Quantity: 1, UnitPrice: 1000, SubscriptionPlanID: &monthly},
		}, map[string]any{"subscription_signup": true, "payment_intent_id": "pi_lines"})

		got, err := app.SignupLines(order, items)
		require.NoError(t, err)
		require.Len(t, got, 2)

		byVariant := map[uuid.UUID]app.SignupLine{}
		for _, l := range got {
			byVariant[l.Line.VariantID] = l
		}
		assert.Equal(t, weekly, byVariant[f.first.ID].PlanID)
		assert.Equal(t, 2, byVariant[f.first.ID].Line.Quantity)
		assert.Equal(t, monthly, byVariant[f.second.ID].PlanID)
	})

	t.Run("an order placed before lines carried their plan still resolves", func(t *testing.T) {
		// Written out literally, exactly as the order-level helper wrote it
		// before lines carried their own plan. Not built with the helper: the
		// helper changes, and the orders already in the database do not.
		tx := testutil.NewTestTx(t, testPool)
		f := newSignupFixture(t, tx)

		planID := uuid.New()
		order, items := f.place(t, tx, []app.CartItem{
			{VariantID: f.first.ID, Quantity: 1, UnitPrice: 1800},
		}, map[string]any{
			"subscription_signup":  true,
			"subscription_plan_id": planID.String(),
			"payment_intent_id":    "pi_legacy",
		})
		require.Empty(t, items[0].Metadata, "the legacy line carries nothing of its own")

		got, err := app.SignupLines(order, items)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, f.first.ID, got[0].Line.VariantID)
		assert.Equal(t, planID, got[0].PlanID)
	})

	t.Run("a multi-line order with a line missing its plan is an error, not a fallback", func(t *testing.T) {
		// The order-level plan names one plan. Handing it to a second line would
		// be a guess about what the customer signed up for.
		tx := testutil.NewTestTx(t, testPool)
		f := newSignupFixture(t, tx)

		planID := uuid.New()
		order, items := f.place(t, tx, []app.CartItem{
			{VariantID: f.first.ID, Quantity: 1, UnitPrice: 1800, SubscriptionPlanID: &planID},
			{VariantID: f.second.ID, Quantity: 1, UnitPrice: 1000},
		}, map[string]any{
			"subscription_signup":  true,
			"subscription_plan_id": planID.String(),
			"payment_intent_id":    "pi_mixed",
		})

		// Both orders of the lines, not whichever the database returned: a
		// reader that looked only at the first line would pass half the time.
		planless := lineFor(t, items, f.second.ID)
		planned := lineFor(t, items, f.first.ID)
		for _, ordered := range [][]domain.LineItem{{planless, planned}, {planned, planless}} {
			_, err := app.SignupLines(order, ordered)
			assert.Error(t, err)
		}
	})

	t.Run("no lines is an error", func(t *testing.T) {
		order := &domain.Order{ID: uuid.New(), Metadata: map[string]any{"subscription_signup": true}}
		_, err := app.SignupLines(order, nil)
		assert.Error(t, err)
	})
}

func TestIsSubscriptionSignupOrder(t *testing.T) {
	legacy := map[string]any{
		"subscription_signup":  true,
		"subscription_plan_id": uuid.NewString(),
		"payment_intent_id":    "pi_legacy",
	}
	assert.True(t, app.IsSubscriptionSignupOrder(legacy))

	lines := map[string]any{"subscription_signup": true, "payment_intent_id": "pi_lines"}
	assert.True(t, app.IsSubscriptionSignupOrder(lines), "the flag alone, with no order-level plan")

	assert.False(t, app.IsSubscriptionSignupOrder(map[string]any{"cart_id": uuid.NewString()}))
	assert.False(t, app.IsSubscriptionSignupOrder(nil))
}
