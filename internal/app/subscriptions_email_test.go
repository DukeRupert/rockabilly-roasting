package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/emailtemplates"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/email"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// One signup, one confirmation, listing every item it started. The sender
// reads in its own transaction, so these fixtures are committed, keyed on
// fresh ids, and deleted on cleanup: other tests in this package list every
// product in the database and would count them.

func newConfirmEmailService(t *testing.T) (*app.SubscriptionService, *email.TestSender) {
	t.Helper()
	renderer, err := emailtemplates.New(time.UTC)
	require.NoError(t, err)
	sender := email.NewTestSender()
	svc := app.NewSubscriptionService(
		store.NewSubscriptionStore(nil), store.NewOrderStore(nil),
		audit.NewAuditWriter(), metrics.NewRegistry(),
	).WithEmail(app.EmailEnv{
		Mailer: sender, Renderer: renderer, FromAddr: "shop@example.com",
		BaseURL: "https://example.com", StoreName: "Test Store",
	}, store.NewCustomerStore(), store.NewCatalogStore())
	return svc, sender
}

// committedSubscription creates, and commits, a customer's subscription to a
// product with the given title on a plan of the given interval.
func committedSubscription(t *testing.T, customer *domain.Customer, addr *domain.Address, title string, interval domain.SubscriptionInterval) *domain.Subscription {
	t.Helper()
	ctx := context.Background()
	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx) //nolint:errcheck

	product := testutil.CreateProduct(t, tx, testutil.WithProductTitle(title))
	variant := testutil.CreateVariant(t, tx, product.ID)
	plan := planEvery(t, tx, interval)
	sub, err := newSubscriptionService().CreateSubscription(ctx, tx, app.CreateSubscriptionParams{
		CustomerID: customer.ID, PlanID: plan.ID, VariantID: variant.ID,
		Quantity: 1, ShippingAddressID: addr.ID,
	}, testutil.TestActor())
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))
	t.Cleanup(func() {
		for _, q := range []struct {
			sql string
			id  uuid.UUID
		}{
			{`DELETE FROM subscriptions WHERE id = $1`, sub.ID},
			{`DELETE FROM subscription_plans WHERE id = $1`, plan.ID},
			{`DELETE FROM variants WHERE id = $1`, variant.ID},
			{`DELETE FROM products WHERE id = $1`, product.ID},
		} {
			_, err := testPool.Exec(context.Background(), q.sql, q.id)
			assert.NoError(t, err, q.sql)
		}
	})
	return sub
}

func committedCustomer(t *testing.T) (*domain.Customer, *domain.Address) {
	t.Helper()
	ctx := context.Background()
	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx) //nolint:errcheck
	customer := testutil.CreateCustomer(t, tx, testutil.WithEmail(uuid.NewString()+"@example.com"))
	addr := testutil.CreateAddress(t, tx, customer.ID)
	require.NoError(t, tx.Commit(ctx))
	// Registered before the subscriptions' cleanups, so it runs after them.
	t.Cleanup(func() {
		ctx := context.Background()
		_, err := testPool.Exec(ctx, `DELETE FROM addresses WHERE customer_id = $1`, customer.ID)
		assert.NoError(t, err)
		_, err = testPool.Exec(ctx, `DELETE FROM customers WHERE id = $1`, customer.ID)
		assert.NoError(t, err)
	})
	return customer, addr
}

func TestSendConfirmationEmail_OneEmailForTheWholeSignup(t *testing.T) {
	ctx := context.Background()
	svc, sender := newConfirmEmailService(t)
	customer, addr := committedCustomer(t)
	a := committedSubscription(t, customer, addr, "Bonneville Blend "+uuid.NewString()[:8], domain.SubscriptionIntervalEvery30Days)
	b := committedSubscription(t, customer, addr, "Crazy Mountain Decaf "+uuid.NewString()[:8], domain.SubscriptionIntervalEvery30Days)

	require.NoError(t, svc.SendConfirmationEmail(ctx, testPool, []uuid.UUID{a.ID, b.ID}, customer.ID))

	require.Len(t, sender.Sent, 1)
	msg := sender.Sent[0]
	assert.Equal(t, customer.Email, msg.To)
	for _, body := range []string{msg.HTML, msg.Text} {
		assert.Contains(t, body, productTitleOf(t, a))
		assert.Contains(t, body, productTitleOf(t, b))
	}
}

func TestSendConfirmationEmail_RefusesAnotherCustomersSubscription(t *testing.T) {
	// The job names the customer and the subscriptions separately. Nothing
	// ties them together unless the sender checks, and without the check a
	// forged or crossed job mails one customer's box to another.
	ctx := context.Background()
	svc, sender := newConfirmEmailService(t)
	customer, addr := committedCustomer(t)
	stranger, strangerAddr := committedCustomer(t)
	mine := committedSubscription(t, customer, addr, "Mine "+uuid.NewString()[:8], domain.SubscriptionIntervalEvery30Days)
	theirs := committedSubscription(t, stranger, strangerAddr, "Theirs "+uuid.NewString()[:8], domain.SubscriptionIntervalEvery30Days)

	err := svc.SendConfirmationEmail(ctx, testPool, []uuid.UUID{mine.ID, theirs.ID}, customer.ID)
	assert.ErrorIs(t, err, app.ErrSubscriptionNotFound)
	assert.Empty(t, sender.Sent, "nothing is sent, not even the half that was theirs")
}

func TestSendConfirmationEmail_NamesTheEarliestChargeAndSaysWhenDaysDiffer(t *testing.T) {
	ctx := context.Background()
	svc, sender := newConfirmEmailService(t)
	customer, addr := committedCustomer(t)
	monthly := committedSubscription(t, customer, addr, "Monthly Thing "+uuid.NewString()[:8], domain.SubscriptionIntervalEvery30Days)
	weekly := committedSubscription(t, customer, addr, "Weekly Thing "+uuid.NewString()[:8], domain.SubscriptionIntervalEvery7Days)

	require.NoError(t, svc.SendConfirmationEmail(ctx, testPool, []uuid.UUID{monthly.ID, weekly.ID}, customer.ID))
	require.Len(t, sender.Sent, 1)
	text := sender.Sent[0].Text

	fmtDay := func(t time.Time) string { return t.In(time.UTC).Format("January 2, 2006") }
	assert.Contains(t, text, "Next charge: "+fmtDay(weekly.NextOrderAt), "the earliest of the two")
	assert.Contains(t, text, "on their own schedules")
	assert.Contains(t, text, fmtDay(monthly.NextOrderAt))
}

func productTitleOf(t *testing.T, sub *domain.Subscription) string {
	t.Helper()
	ctx := context.Background()
	tx := testutil.NewTestTx(t, testPool)
	catalog := store.NewCatalogStore()
	variant, err := catalog.GetVariantByID(ctx, tx, sub.VariantID)
	require.NoError(t, err)
	product, err := catalog.GetProductByID(ctx, tx, variant.ProductID)
	require.NoError(t, err)
	return product.Title
}
