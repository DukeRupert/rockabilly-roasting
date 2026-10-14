package web

import (
	"context"
	"log/slog"
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
	"github.com/dukerupert/hiri/internal/platform/payments"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// A box of several subscriptions fails one charge, and its dunning mail carries
// one update-card link, minted for the first item. The link checked that this
// item was still past due. A customer who paused or cancelled that one item
// clicked through to "already sorted" while the rest of the box still waited on
// a card — and the card is the customer's, not the item's: the Stripe form the
// link opens changes the default every renewal charges. The link now opens
// while anything of the customer's is past due.
//
// These commit. See setup_test.go.

// portalProvider records the Stripe customer each card-form session was opened
// for. Nothing else on the provider is reached by these handlers.
type portalProvider struct {
	payments.Provider
	opened []string
}

func (p *portalProvider) CreatePaymentMethodUpdateSession(_ context.Context, customerID, _ string) (string, error) {
	p.opened = append(p.opened, customerID)
	return "https://billing.example/session", nil
}

type updateCardBox struct {
	stripeCustomerID        string
	first, second           uuid.UUID
	firstTitle, secondTitle string
}

// newUpdateCardBox is one customer with two past-due subscriptions.
func newUpdateCardBox(t *testing.T) updateCardBox {
	t.Helper()
	ctx := context.Background()
	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx) //nolint:errcheck

	customer := testutil.CreateCustomer(t, tx)
	stripeID := "cus_" + uuid.NewString()
	_, err = tx.Exec(ctx, `UPDATE customers SET stripe_customer_id = $2 WHERE id = $1`, customer.ID, stripeID)
	require.NoError(t, err)
	addr := testutil.CreateAddress(t, tx, customer.ID)
	subs := store.NewSubscriptionStore(nil)
	plan, err := subs.CreatePlan(ctx, tx, store.CreatePlanParams{
		Name: "Monthly", Interval: domain.SubscriptionIntervalEvery30Days, IntervalCount: 1, IsActive: true,
	})
	require.NoError(t, err)

	box := updateCardBox{stripeCustomerID: stripeID}
	for i := range 2 {
		// Distinct titles: the page names one item, and the tests say which.
		product := testutil.CreateProduct(t, tx, testutil.WithProductTitle("Item "+uuid.NewString()))
		variant := testutil.CreateVariant(t, tx, product.ID)
		hourAgo := time.Now().Add(-time.Hour)
		sub, err := subs.Create(ctx, tx, store.CreateSubscriptionParams{
			CustomerID:         customer.ID,
			PlanID:             plan.ID,
			VariantID:          variant.ID,
			Quantity:           1,
			Status:             domain.SubscriptionStatusPastDue,
			ShippingAddressID:  addr.ID,
			CurrentPeriodStart: hourAgo.AddDate(0, 0, -30),
			CurrentPeriodEnd:   hourAgo,
			NextOrderAt:        hourAgo,
		})
		require.NoError(t, err)
		if i == 0 {
			box.first, box.firstTitle = sub.ID, product.Title
		} else {
			box.second, box.secondTitle = sub.ID, product.Title
		}
	}
	require.NoError(t, tx.Commit(ctx))
	return box
}

func setStatus(t *testing.T, id uuid.UUID, status domain.SubscriptionStatus) {
	t.Helper()
	_, err := testPool.Exec(context.Background(),
		`UPDATE subscriptions SET status = $2 WHERE id = $1`, id, status)
	require.NoError(t, err)
}

func newUpdateCardDeps(t *testing.T) (*Deps, *portalProvider) {
	t.Helper()
	d := newSubscribeDeps(t)
	d.Logger = slog.New(slog.DiscardHandler)
	d.OrderActionSigner = auth.NewOrderActionSigner("test-secret")
	p := &portalProvider{}
	d.PaymentProvider = p
	return d, p
}

func updateCardGet(d *Deps, token string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	d.handleUpdateCardPage(w, httptest.NewRequest(http.MethodGet,
		"/subscriptions/update-card?t="+url.QueryEscape(token), nil))
	return w
}

func updateCardPost(d *Deps, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/subscriptions/update-card",
		strings.NewReader(url.Values{"t": {token}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	d.handleUpdateCard(w, r)
	return w
}

func TestUpdateCard_ALinkForAnItemNoLongerPastDueOpensForTheRestOfTheBox(t *testing.T) {
	for _, status := range []domain.SubscriptionStatus{domain.SubscriptionStatusPaused, domain.SubscriptionStatusCancelled} {
		t.Run(string(status), func(t *testing.T) {
			d, provider := newUpdateCardDeps(t)
			box := newUpdateCardBox(t)
			setStatus(t, box.first, status)
			token := d.OrderActionSigner.Sign(auth.OrderActionUpdateCard, box.first, time.Now())

			page := updateCardGet(d, token)
			require.Equal(t, http.StatusOK, page.Code, "the box still needs a card")
			assert.Contains(t, page.Body.String(), box.secondTitle,
				"the page speaks for the item still waiting, not the one sorted")
			assert.NotContains(t, page.Body.String(), box.firstTitle)

			form := updateCardPost(d, token)
			require.Equal(t, http.StatusSeeOther, form.Code)
			assert.Equal(t, []string{box.stripeCustomerID}, provider.opened)
		})
	}
}

// The item the link names, still past due itself, is the one the page names.
func TestUpdateCard_ALinkForAnItemStillPastDueNamesThatItem(t *testing.T) {
	d, _ := newUpdateCardDeps(t)
	box := newUpdateCardBox(t)
	token := d.OrderActionSigner.Sign(auth.OrderActionUpdateCard, box.first, time.Now())

	page := updateCardGet(d, token)
	require.Equal(t, http.StatusOK, page.Code)
	assert.Contains(t, page.Body.String(), box.firstTitle)
	assert.NotContains(t, page.Body.String(), box.secondTitle)
}

func TestUpdateCard_NothingPastDueIsAlreadySorted(t *testing.T) {
	d, provider := newUpdateCardDeps(t)
	box := newUpdateCardBox(t)
	setStatus(t, box.first, domain.SubscriptionStatusActive)
	setStatus(t, box.second, domain.SubscriptionStatusCancelled)
	token := d.OrderActionSigner.Sign(auth.OrderActionUpdateCard, box.first, time.Now())

	assert.Equal(t, http.StatusConflict, updateCardGet(d, token).Code)
	assert.Equal(t, http.StatusConflict, updateCardPost(d, token).Code)
	assert.Empty(t, provider.opened)
}

// Another customer being past due opens nothing: the link reaches only the
// customer whose subscription it names.
func TestUpdateCard_SomeoneElsePastDueOpensNothing(t *testing.T) {
	d, provider := newUpdateCardDeps(t)
	mine, theirs := newUpdateCardBox(t), newUpdateCardBox(t)
	setStatus(t, mine.first, domain.SubscriptionStatusActive)
	setStatus(t, mine.second, domain.SubscriptionStatusActive)
	require.NotEqual(t, mine.stripeCustomerID, theirs.stripeCustomerID)
	token := d.OrderActionSigner.Sign(auth.OrderActionUpdateCard, mine.first, time.Now())

	assert.Equal(t, http.StatusConflict, updateCardGet(d, token).Code)
	assert.Equal(t, http.StatusConflict, updateCardPost(d, token).Code)
	assert.Empty(t, provider.opened)
}
