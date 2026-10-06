package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/auth"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// Whole-box skip and pause from the account page. These commit — see
// setup_test.go — so every fixture is a fresh customer.

type accountBoxFixture struct {
	customer *domain.Customer
	addr     *domain.Address
	at       time.Time
	subs     []*domain.Subscription
}

func newAccountBoxFixture(t *testing.T) accountBoxFixture {
	t.Helper()
	ctx := context.Background()
	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx) //nolint:errcheck

	customer := testutil.CreateCustomer(t, tx)
	addr := testutil.CreateAddress(t, tx, customer.ID)
	at := time.Now().UTC().AddDate(0, 0, 3).Truncate(time.Microsecond)
	f := accountBoxFixture{customer: customer, addr: addr, at: at}
	for i := 0; i < 2; i++ {
		f.subs = append(f.subs, orderPageSubscriptionDueAt(t, tx, customer.ID, addr.ID, at))
	}
	require.NoError(t, tx.Commit(ctx))
	return f
}

// orderPageSubscriptionDueAt writes an active weekly subscription due at at.
func orderPageSubscriptionDueAt(t *testing.T, tx pgx.Tx, customerID, addressID uuid.UUID, at time.Time) *domain.Subscription {
	t.Helper()
	ctx := context.Background()
	subs := store.NewSubscriptionStore(nil)
	product := testutil.CreateProduct(t, tx)
	variant := testutil.CreateVariant(t, tx, product.ID)
	plan, err := subs.CreatePlan(ctx, tx, store.CreatePlanParams{
		Name: "Weekly", Interval: domain.SubscriptionIntervalEvery7Days, IntervalCount: 1, IsActive: true,
	})
	require.NoError(t, err)
	sub, err := subs.Create(ctx, tx, store.CreateSubscriptionParams{
		CustomerID: customerID, PlanID: plan.ID, VariantID: variant.ID, Quantity: 1,
		Status: domain.SubscriptionStatusActive, ShippingAddressID: addressID,
		CurrentPeriodStart: at.AddDate(0, 0, -7), CurrentPeriodEnd: at, NextOrderAt: at,
	})
	require.NoError(t, err)
	return sub
}

func newAccountBoxDeps(t *testing.T) *Deps {
	t.Helper()
	d := newSubscribeDeps(t)
	d.SubscriptionService = app.NewSubscriptionService(
		store.NewSubscriptionStore(nil), store.NewOrderStore(nil),
		audit.NewAuditWriter(), metrics.NewRegistry(),
	).WithCatalog(store.NewCatalogStore(), store.NewPricingStore())
	withRiver(t, d)
	return d
}

func postBoxForm(t *testing.T, d *Deps, handler http.HandlerFunc, path string, form url.Values, customer *domain.Customer) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r = r.WithContext(auth.WithCustomer(r.Context(), customer))
	w := httptest.NewRecorder()
	handler(w, r)
	return w
}

func (f accountBoxFixture) form(resumeOn string) url.Values {
	v := url.Values{
		"address_id":    {f.addr.ID.String()},
		"next_order_at": {f.at.Format(time.RFC3339Nano)},
	}
	if resumeOn != "" {
		v.Set("resume_on", resumeOn)
	}
	return v
}

func nextOrderOf(t *testing.T, id uuid.UUID) *domain.Subscription {
	t.Helper()
	ctx := context.Background()
	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx) //nolint:errcheck
	sub, err := store.NewSubscriptionStore(nil).GetByIDAsStaff(ctx, tx, id)
	require.NoError(t, err)
	return sub
}

func skippedEmailJobs(t *testing.T, customerID uuid.UUID) int {
	t.Helper()
	var n int
	require.NoError(t, testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM river_job
		WHERE kind = $1 AND args->>'customer_id' = $2`,
		"email:subscription_skipped", customerID.String()).Scan(&n))
	return n
}

func TestAccountSubscriptionBoxSkip(t *testing.T) {
	d := newAccountBoxDeps(t)

	t.Run("skips every subscription in the box, one email each", func(t *testing.T) {
		f := newAccountBoxFixture(t)
		resume := f.at.In(d.MerchantTZ).AddDate(0, 0, 10).Format("2006-01-02")

		w := postBoxForm(t, d, d.handleAccountSubscriptionBoxSkip, "/account/subscriptions/box/skip", f.form(resume), f.customer)
		require.Equal(t, http.StatusSeeOther, w.Code, w.Body.String())
		assert.Equal(t, "/account/subscriptions", w.Header().Get("Location"))

		a, b := nextOrderOf(t, f.subs[0].ID), nextOrderOf(t, f.subs[1].ID)
		assert.True(t, a.NextOrderAt.Equal(b.NextOrderAt), "still one box")
		assert.Equal(t, resume, a.NextOrderAt.In(d.MerchantTZ).Format("2006-01-02"))
		assert.Equal(t, 2, skippedEmailJobs(t, f.customer.ID), "the existing per-subscription email, once per member")

		t.Run("and the page shows both under the new day", func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/account/subscriptions", nil)
			r = r.WithContext(auth.WithCustomer(r.Context(), f.customer))
			pw := httptest.NewRecorder()
			d.handleAccountSubscriptions(pw, r)
			require.Equal(t, http.StatusOK, pw.Code)
			body := pw.Body.String()
			assert.Equal(t, 1, strings.Count(body, `data-subscription-box=`), "one box")
			for _, s := range f.subs {
				assert.Contains(t, body, s.ID.String())
			}
		})
	})

	t.Run("a forged address skips nothing", func(t *testing.T) {
		f := newAccountBoxFixture(t)
		stranger := newAccountBoxFixture(t)
		form := f.form(f.at.AddDate(0, 0, 10).Format("2006-01-02"))
		form.Set("address_id", stranger.addr.ID.String())

		w := postBoxForm(t, d, d.handleAccountSubscriptionBoxSkip, "/account/subscriptions/box/skip", form, f.customer)
		assert.Equal(t, http.StatusNotFound, w.Code, "the same answer a per-row skip gives for someone else's subscription")
		for _, fx := range []accountBoxFixture{f, stranger} {
			for _, s := range fx.subs {
				assert.True(t, nextOrderOf(t, s.ID).NextOrderAt.Equal(fx.at), "subscription %s untouched", s.ID)
			}
		}
		assert.Zero(t, skippedEmailJobs(t, stranger.customer.ID))
	})
}

func TestAccountSubscriptionBoxPause(t *testing.T) {
	d := newAccountBoxDeps(t)
	f := newAccountBoxFixture(t)

	w := postBoxForm(t, d, d.handleAccountSubscriptionBoxPause, "/account/subscriptions/box/pause", f.form(""), f.customer)
	require.Equal(t, http.StatusSeeOther, w.Code, w.Body.String())
	for _, s := range f.subs {
		assert.Equal(t, domain.SubscriptionStatusPaused, nextOrderOf(t, s.ID).Status)
	}
}

// The box routes sit on the account mux beside the per-row routes, behind the
// signed-in customer's session. Read from router.go because the mux cannot be
// asked which group a pattern was registered in.
func TestAccountSubscriptionBoxRoutesAreOnTheAccountMux(t *testing.T) {
	src, err := os.ReadFile("router.go")
	require.NoError(t, err)
	for _, handler := range []string{"handleAccountSubscriptionBoxSkip", "handleAccountSubscriptionBoxPause"} {
		re := regexp.MustCompile(`(?m)^.*` + handler + `.*$`)
		line := re.FindString(string(src))
		require.NotEmpty(t, line, "%s is registered", handler)
		assert.Contains(t, line, "accountMux.", "%s is behind the account session", handler)
	}
}
