package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/auth"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// Customers changing a subscription and adding a coffee to a box from the
// account page. These commit (see setup_test.go), so every fixture is a fresh
// customer on a fresh, subscribable coffee.

type manageWebFixture struct {
	customer         *domain.Customer
	addr             *domain.Address
	wholeBean, decaf *domain.Variant
	plan             *domain.SubscriptionPlan
	sub              *domain.Subscription
}

func newManageWebFixture(t *testing.T) manageWebFixture {
	t.Helper()
	ctx := context.Background()
	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx) //nolint:errcheck

	f := manageWebFixture{}
	f.customer = testutil.CreateCustomer(t, tx)
	f.addr = testutil.CreateAddress(t, tx, f.customer.ID)
	coffee := testutil.CreateProduct(t, tx, testutil.WithProductTitle("Switchblade "+uuid.NewString()[:8]))
	decaf := testutil.CreateProduct(t, tx, testutil.WithProductTitle("Decaf "+uuid.NewString()[:8]))
	_, err = tx.Exec(ctx, `UPDATE products SET subscribable = true WHERE id = ANY($1)`, []uuid.UUID{coffee.ID, decaf.ID})
	require.NoError(t, err)
	f.wholeBean = testutil.CreateVariant(t, tx, coffee.ID)
	f.decaf = testutil.CreateVariant(t, tx, decaf.ID)
	testutil.SetBasePriceForVariant(t, tx, f.wholeBean.ID, 1800, "USD")
	testutil.SetBasePriceForVariant(t, tx, f.decaf.ID, 1900, "USD")

	subs := store.NewSubscriptionStore(nil)
	f.plan, err = subs.CreatePlan(ctx, tx, store.CreatePlanParams{
		Name: "Monthly", Interval: domain.SubscriptionIntervalEvery30Days, IntervalCount: 1, IsActive: true,
	})
	require.NoError(t, err)
	at := time.Now().UTC().AddDate(0, 0, 3).Truncate(time.Microsecond)
	f.sub, err = subs.Create(ctx, tx, store.CreateSubscriptionParams{
		CustomerID: f.customer.ID, PlanID: f.plan.ID, VariantID: f.wholeBean.ID, Quantity: 1,
		Status: domain.SubscriptionStatusActive, ShippingAddressID: f.addr.ID,
		CurrentPeriodStart: at.AddDate(0, 0, -27), CurrentPeriodEnd: at, NextOrderAt: at,
	})
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))
	return f
}

func (f manageWebFixture) editForm(quantity string) url.Values {
	return url.Values{
		"quantity":   {quantity},
		"variant_id": {f.wholeBean.ID.String()},
		"plan_id":    {f.plan.ID.String()},
	}
}

func (f manageWebFixture) addForm(variantID uuid.UUID) url.Values {
	return url.Values{
		"address_id":    {f.addr.ID.String()},
		"next_order_at": {f.sub.NextOrderAt.Format(time.RFC3339Nano)},
		"variant_id":    {variantID.String()},
		"plan_id":       {f.plan.ID.String()},
		"quantity":      {"2"},
	}
}

func postEdit(t *testing.T, d *Deps, subID uuid.UUID, form url.Values, customer *domain.Customer) *httptest.ResponseRecorder {
	t.Helper()
	path := "/account/subscriptions/" + subID.String() + "/edit"
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetPathValue("id", subID.String())
	r = r.WithContext(auth.WithCustomer(r.Context(), customer))
	w := httptest.NewRecorder()
	d.handleAccountSubscriptionEdit(w, r)
	return w
}

func changedEmailJobs(t *testing.T, customerID uuid.UUID) []map[string]any {
	t.Helper()
	rows, err := testPool.Query(context.Background(), `
		SELECT args FROM river_job WHERE kind = 'email:subscription_changed' AND args->>'customer_id' = $1`,
		customerID.String())
	require.NoError(t, err)
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var args map[string]any
		require.NoError(t, rows.Scan(&args))
		out = append(out, args)
	}
	require.NoError(t, rows.Err())
	return out
}

func TestAccountSubscriptionEdit(t *testing.T) {
	d := newAccountBoxDeps(t)

	t.Run("saves the change and queues one email naming it", func(t *testing.T) {
		f := newManageWebFixture(t)
		w := postEdit(t, d, f.sub.ID, f.editForm("3"), f.customer)
		require.Equal(t, http.StatusSeeOther, w.Code, w.Body.String())
		assert.Equal(t, "/account/subscriptions", w.Header().Get("Location"))

		got := nextOrderOf(t, f.sub.ID)
		assert.Equal(t, 3, got.Quantity)
		assert.True(t, got.NextOrderAt.Equal(f.sub.NextOrderAt), "the booked shipment is untouched")

		jobs := changedEmailJobs(t, f.customer.ID)
		require.Len(t, jobs, 1)
		assert.EqualValues(t, 1, jobs[0]["old_quantity"])
		assert.EqualValues(t, 3, jobs[0]["new_quantity"])
		assert.Nil(t, jobs[0]["new_plan_id"], "only what changed")
	})

	t.Run("saving the form untouched changes nothing and sends nothing", func(t *testing.T) {
		f := newManageWebFixture(t)
		w := postEdit(t, d, f.sub.ID, f.editForm("1"), f.customer)
		require.Equal(t, http.StatusSeeOther, w.Code, w.Body.String())
		assert.Empty(t, changedEmailJobs(t, f.customer.ID))
	})

	t.Run("another customer's subscription is not found", func(t *testing.T) {
		f := newManageWebFixture(t)
		stranger := newManageWebFixture(t).customer
		w := postEdit(t, d, f.sub.ID, f.editForm("3"), stranger)
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Equal(t, 1, nextOrderOf(t, f.sub.ID).Quantity)
	})

	t.Run("while a renewal holds it, the customer is told to try again shortly", func(t *testing.T) {
		f := newManageWebFixture(t)
		_, err := testPool.Exec(context.Background(), `UPDATE subscriptions SET renewal_claimed_at = now() WHERE id = $1`, f.sub.ID)
		require.NoError(t, err)

		w := postEdit(t, d, f.sub.ID, f.editForm("3"), f.customer)
		assert.Equal(t, http.StatusConflict, w.Code)
		assert.Contains(t, w.Body.String(), "packing this shipment right now")
		assert.Equal(t, 1, nextOrderOf(t, f.sub.ID).Quantity)
	})

	t.Run("a quantity that is not a number is refused", func(t *testing.T) {
		f := newManageWebFixture(t)
		w := postEdit(t, d, f.sub.ID, f.editForm("lots"), f.customer)
		assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	t.Run("the query string is not read", func(t *testing.T) {
		f := newManageWebFixture(t)
		path := "/account/subscriptions/" + f.sub.ID.String() + "/edit?quantity=9"
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(""))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.SetPathValue("id", f.sub.ID.String())
		r = r.WithContext(auth.WithCustomer(r.Context(), f.customer))
		w := httptest.NewRecorder()
		d.handleAccountSubscriptionEdit(w, r)
		require.Equal(t, http.StatusSeeOther, w.Code, w.Body.String())
		assert.Equal(t, 1, nextOrderOf(t, f.sub.ID).Quantity)
	})
}

func TestAccountSubscriptionBoxAdd(t *testing.T) {
	d := newAccountBoxDeps(t)

	t.Run("adds the coffee to the box and queues one email", func(t *testing.T) {
		f := newManageWebFixture(t)
		w := postBoxForm(t, d, d.handleAccountSubscriptionBoxAdd, "/account/subscriptions/box/add", f.addForm(f.decaf.ID), f.customer)
		require.Equal(t, http.StatusSeeOther, w.Code, w.Body.String())

		ctx := context.Background()
		tx, err := testPool.Begin(ctx)
		require.NoError(t, err)
		defer tx.Rollback(ctx) //nolint:errcheck
		subs, err := store.NewSubscriptionStore(nil).ListByCustomer(ctx, tx, f.customer.ID)
		require.NoError(t, err)
		require.Len(t, subs, 2)
		boxes := domain.GroupIntoBoxes(subs, time.UTC)
		require.Len(t, boxes, 1, "it joined the box")

		jobs := changedEmailJobs(t, f.customer.ID)
		require.Len(t, jobs, 1)
		assert.Equal(t, true, jobs[0]["added"])
	})

	t.Run("a box with a past-due member is refused", func(t *testing.T) {
		f := newManageWebFixture(t)
		_, err := testPool.Exec(context.Background(), `UPDATE subscriptions SET status = 'past_due' WHERE id = $1`, f.sub.ID)
		require.NoError(t, err)

		w := postBoxForm(t, d, d.handleAccountSubscriptionBoxAdd, "/account/subscriptions/box/add", f.addForm(f.decaf.ID), f.customer)
		assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
		assert.Contains(t, w.Body.String(), "unpaid charge")
		assert.Empty(t, changedEmailJobs(t, f.customer.ID))
	})

	t.Run("the same coffee on the same plan says to change its quantity", func(t *testing.T) {
		f := newManageWebFixture(t)
		w := postBoxForm(t, d, d.handleAccountSubscriptionBoxAdd, "/account/subscriptions/box/add", f.addForm(f.wholeBean.ID), f.customer)
		assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
		assert.Contains(t, w.Body.String(), "change its quantity instead")
	})
}

func TestAccountSubscriptionsPage_OffersChangeAndAdd(t *testing.T) {
	d := newAccountBoxDeps(t)
	f := newManageWebFixture(t)

	r := httptest.NewRequest(http.MethodGet, "/account/subscriptions", nil)
	r = r.WithContext(auth.WithCustomer(r.Context(), f.customer))
	w := httptest.NewRecorder()
	d.handleAccountSubscriptions(w, r)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	body := w.Body.String()
	assert.Contains(t, body, `action="/account/subscriptions/`+f.sub.ID.String()+`/edit"`)
	assert.Contains(t, body, `action="/account/subscriptions/box/add"`)
	assert.Contains(t, body, f.decaf.ID.String(), "the decaf is offered to add")
}
