package jobs_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/emailtemplates"
	"github.com/dukerupert/hiri/internal/jobs"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/email"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// A job queued before a signup could carry several items names one
// subscription; one queued after names them all. Both have to send.

type confirmFixture struct {
	customer *domain.Customer
	subs     []*domain.Subscription
	titles   []string
}

func newConfirmFixture(t *testing.T, n int) confirmFixture {
	t.Helper()
	ctx := context.Background()
	pool := testPool(t)
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx) //nolint:errcheck

	subsStore := store.NewSubscriptionStore(nil)
	svc := app.NewSubscriptionService(subsStore, store.NewOrderStore(nil), audit.NewAuditWriter(), metrics.NewRegistry())
	customer := testutil.CreateCustomer(t, tx, testutil.WithEmail(uuid.NewString()+"@example.com"))
	addr := testutil.CreateAddress(t, tx, customer.ID)
	plan, err := subsStore.CreatePlan(ctx, tx, store.CreatePlanParams{
		Name: "Monthly", Interval: domain.SubscriptionIntervalEvery30Days, IntervalCount: 1, IsActive: true,
	})
	require.NoError(t, err)

	f := confirmFixture{customer: customer}
	for i := 0; i < n; i++ {
		title := "Item " + uuid.NewString()[:8]
		product := testutil.CreateProduct(t, tx, testutil.WithProductTitle(title))
		variant := testutil.CreateVariant(t, tx, product.ID)
		sub, err := svc.CreateSubscription(ctx, tx, app.CreateSubscriptionParams{
			CustomerID: customer.ID, PlanID: plan.ID, VariantID: variant.ID,
			Quantity: 1, ShippingAddressID: addr.ID,
		}, testutil.TestActor())
		require.NoError(t, err)
		f.subs = append(f.subs, sub)
		f.titles = append(f.titles, title)
	}
	require.NoError(t, tx.Commit(ctx))
	return f
}

func newConfirmWorker(t *testing.T) (*jobs.SubscriptionConfirmEmailWorker, *email.TestSender) {
	t.Helper()
	renderer, err := emailtemplates.New(time.UTC)
	require.NoError(t, err)
	sender := email.NewTestSender()
	svc := app.NewSubscriptionService(
		store.NewSubscriptionStore(nil), store.NewOrderStore(nil), audit.NewAuditWriter(), metrics.NewRegistry(),
	).WithEmail(app.EmailEnv{
		Mailer: sender, Renderer: renderer, FromAddr: "shop@example.com",
		BaseURL: "https://example.com", StoreName: "Test Store",
	}, store.NewCustomerStore(), store.NewCatalogStore())
	return jobs.NewSubscriptionConfirmEmailWorker(svc, testPool(t)), sender
}

func TestSubscriptionConfirmEmailWorker(t *testing.T) {
	ctx := context.Background()

	t.Run("a job from before the change names one subscription", func(t *testing.T) {
		f := newConfirmFixture(t, 1)
		w, sender := newConfirmWorker(t)
		require.NoError(t, w.Work(ctx, &river.Job[jobs.SubscriptionConfirmEmailArgs]{
			Args: jobs.SubscriptionConfirmEmailArgs{SubscriptionID: f.subs[0].ID, CustomerID: f.customer.ID},
		}))
		require.Len(t, sender.Sent, 1)
		assert.Contains(t, sender.Sent[0].Text, f.titles[0])
	})

	t.Run("a job naming several sends one email for all", func(t *testing.T) {
		f := newConfirmFixture(t, 2)
		w, sender := newConfirmWorker(t)
		require.NoError(t, w.Work(ctx, &river.Job[jobs.SubscriptionConfirmEmailArgs]{
			Args: jobs.SubscriptionConfirmEmailArgs{
				SubscriptionIDs: []uuid.UUID{f.subs[0].ID, f.subs[1].ID}, CustomerID: f.customer.ID,
			},
		}))
		require.Len(t, sender.Sent, 1)
		assert.Contains(t, sender.Sent[0].Text, f.titles[0])
		assert.Contains(t, sender.Sent[0].Text, f.titles[1])
	})

	t.Run("a job carrying both reads the list and sends once", func(t *testing.T) {
		f := newConfirmFixture(t, 3)
		w, sender := newConfirmWorker(t)
		require.NoError(t, w.Work(ctx, &river.Job[jobs.SubscriptionConfirmEmailArgs]{
			Args: jobs.SubscriptionConfirmEmailArgs{
				SubscriptionID:  f.subs[2].ID,
				SubscriptionIDs: []uuid.UUID{f.subs[0].ID, f.subs[1].ID},
				CustomerID:      f.customer.ID,
			},
		}))
		require.Len(t, sender.Sent, 1)
		assert.Contains(t, sender.Sent[0].Text, f.titles[0])
		assert.Contains(t, sender.Sent[0].Text, f.titles[1])
		assert.NotContains(t, sender.Sent[0].Text, f.titles[2])
	})
}
