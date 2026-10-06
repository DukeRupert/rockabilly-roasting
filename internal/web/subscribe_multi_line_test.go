package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/payments"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// Several items in one signup: one PaymentIntent, one order, shipping once.
//
// The complaint this answers: a customer wanting three items on subscription
// checked out three times and paid shipping three times. These drive the
// endpoint with a two-line body and hold the card, the order and the lines to
// one another. Lines are matched by variant, never by position — the order's
// lines come back sorted by a random UUID.

type multiLineFixture struct {
	weekly, monthly     *domain.SubscriptionPlan // 10% and 5% off
	first, second       uuid.UUID                // variants at 1800 and 1000
	firstProd, secondPr uuid.UUID
}

func newMultiLineFixture(t *testing.T) multiLineFixture {
	t.Helper()
	ctx := context.Background()
	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx) //nolint:errcheck

	p1 := testutil.CreateProduct(t, tx)
	v1 := testutil.CreateVariant(t, tx, p1.ID)
	testutil.SetBasePriceForVariant(t, tx, v1.ID, 1800, "USD")
	p2 := testutil.CreateProduct(t, tx)
	v2 := testutil.CreateVariant(t, tx, p2.ID)
	testutil.SetBasePriceForVariant(t, tx, v2.ID, 1000, "USD")

	plans := store.NewSubscriptionStore(nil)
	weekly, err := plans.CreatePlan(ctx, tx, store.CreatePlanParams{
		Name: "Weekly", Interval: domain.SubscriptionIntervalEvery7Days, IntervalCount: 1,
		IsActive: true, DiscountPct: 10,
	})
	require.NoError(t, err)
	monthly, err := plans.CreatePlan(ctx, tx, store.CreatePlanParams{
		Name: "Monthly", Interval: domain.SubscriptionIntervalEvery30Days, IntervalCount: 1,
		IsActive: true, DiscountPct: 5,
	})
	require.NoError(t, err)

	require.NoError(t, tx.Commit(ctx))
	return multiLineFixture{
		weekly: weekly, monthly: monthly,
		first: v1.ID, second: v2.ID, firstProd: p1.ID, secondPr: p2.ID,
	}
}

// basePrice is the catalog price of each fixture variant.
func (f multiLineFixture) basePrice(variantID uuid.UUID) int {
	if variantID == f.first {
		return 1800
	}
	return 1000
}

// twoLines is the fixture's mixed box: one weekly at 10% off 1800, and two
// monthly at 5% off 1000. 1620 + 2×950 = 3520.
func (f multiLineFixture) twoLines() []map[string]any {
	return []map[string]any{
		{"plan_id": f.weekly.ID.String(), "variant_id": f.first.String(), "quantity": 1},
		{"plan_id": f.monthly.ID.String(), "variant_id": f.second.String(), "quantity": 2},
	}
}

// multiLineBody is a signup body carrying lines, for a fresh guest email.
func multiLineBody(lines []map[string]any, opts ...func(map[string]any)) string {
	body := map[string]any{
		"lines":       lines,
		"email":       "multi-" + uuid.NewString() + "@example.test",
		"first_name":  "Ada",
		"last_name":   "Byron",
		"line1":       "1 Main St",
		"city":        "Helena",
		"state":       "MT",
		"postal_code": "59601",
		"country":     "US",
	}
	for _, opt := range opts {
		opt(body)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func shipToWashington(m map[string]any) {
	m["state"] = "WA"
	m["city"] = "Seattle"
	m["postal_code"] = "98101"
}

func decodeSubscribeIntentResponse(t *testing.T, w *httptest.ResponseRecorder) subscribePaymentIntentResponse {
	t.Helper()
	var resp subscribePaymentIntentResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp
}

// withFlatRateTax switches the instance to a flat rate for one test and puts
// the previous config back. The flat-rate calculator only taxes Washington
// addresses, so a test using it ships there. Safe for the reason
// withFlatShippingRate is: nothing in this package runs in parallel.
func withFlatRateTax(t *testing.T, rate float64) {
	t.Helper()
	ctx := context.Background()
	settings := store.NewSettingsStore()
	set := func(p store.UpdateTaxConfigParams) {
		tx, err := testPool.Begin(ctx)
		require.NoError(t, err)
		_, err = settings.UpdateTaxConfig(ctx, tx, p)
		require.NoError(t, err)
		require.NoError(t, tx.Commit(ctx))
	}
	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	before, err := settings.GetTaxConfig(ctx, tx)
	require.NoError(t, err)
	require.NoError(t, tx.Rollback(ctx))
	t.Cleanup(func() {
		set(store.UpdateTaxConfigParams{Mode: before.Mode, Rate: before.Rate, Label: before.Label})
	})
	set(store.UpdateTaxConfigParams{Mode: domain.TaxModeFlatRate, Rate: rate, Label: "Sales tax"})
}

func setTaxExempt(t *testing.T, productID uuid.UUID) {
	t.Helper()
	_, err := testPool.Exec(context.Background(), `UPDATE products SET tax_exempt = true WHERE id = $1`, productID)
	require.NoError(t, err)
}

func orderForIntent(t *testing.T, d *Deps, intentID string) (*domain.Order, []domain.LineItem) {
	t.Helper()
	ctx := context.Background()
	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx) //nolint:errcheck
	order, err := d.OrderService.GetOrderByStripePaymentIntentIDAsStaff(ctx, tx, intentID)
	require.NoError(t, err)
	lines, err := d.OrderService.ListLineItems(ctx, tx, order.ID)
	require.NoError(t, err)
	return order, lines
}

func lineByVariant(t *testing.T, lines []domain.LineItem, variantID uuid.UUID) domain.LineItem {
	t.Helper()
	for _, l := range lines {
		if l.VariantID == variantID {
			return l
		}
	}
	t.Fatalf("no line for variant %s", variantID)
	return domain.LineItem{}
}

// The test named for the complaint.
func TestSubscribePaymentIntent_TwoItemsShipForOneShippingCharge(t *testing.T) {
	f := newMultiLineFixture(t)
	d, fake := newSubscribePaymentDeps(t)
	withFlatShippingRate(t, d, 650)

	w := postSubscribePaymentIntent(t, d, multiLineBody(f.twoLines()))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	resp := decodeSubscribeIntentResponse(t, w)
	assert.Equal(t, 1620+2*950, resp.Subtotal, "each line at its own plan's discount")
	assert.Equal(t, 650, resp.ShippingTotal, "shipping once, not once per item")
	assert.Equal(t, resp.Subtotal+resp.ShippingTotal+resp.TaxTotal, resp.Amount)

	require.Equal(t, 1, fake.intentCount(), "one PaymentIntent for the whole signup")
	intent, _ := fake.lastIntent()
	assert.Equal(t, int64(resp.Amount), intent.AmountCents)

	require.Len(t, resp.Lines, 2)
	byVariant := map[string]subscribeLineResponse{}
	for _, l := range resp.Lines {
		byVariant[l.VariantID] = l
	}
	assert.Equal(t, 1620, byVariant[f.first.String()].UnitPrice)
	assert.Equal(t, 1620, byVariant[f.first.String()].Subtotal)
	assert.Equal(t, 950, byVariant[f.second.String()].UnitPrice)
	assert.Equal(t, 2, byVariant[f.second.String()].Quantity)
	assert.Equal(t, 1900, byVariant[f.second.String()].Subtotal)
}

func TestSubscribePaymentIntent_TheChargeEqualsTheRecord(t *testing.T) {
	f := newMultiLineFixture(t)
	d, fake := newSubscribePaymentDeps(t)
	withFlatShippingRate(t, d, 650)
	withFlatRateTax(t, 0.10)

	w := postSubscribePaymentIntent(t, d, multiLineBody(f.twoLines(), shipToWashington))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	resp := decodeSubscribeIntentResponse(t, w)
	require.NotZero(t, resp.TaxTotal, "tax is on, or this test proves nothing about it")

	intent, ok := fake.lastIntent()
	require.True(t, ok)
	order, _ := orderForIntent(t, d, piIDFromClientSecret(resp.ClientSecret))
	assert.Equal(t, int64(order.Total), intent.AmountCents, "the card and the order agree")
	assert.Equal(t, resp.Subtotal, order.Subtotal)
	assert.Equal(t, resp.ShippingTotal, order.ShippingTotal)
	assert.Equal(t, resp.TaxTotal, order.TaxTotal)
}

func TestSubscribePaymentIntent_EachLineRecordsItsOwnPlan(t *testing.T) {
	f := newMultiLineFixture(t)
	d, _ := newSubscribePaymentDeps(t)

	w := postSubscribePaymentIntent(t, d, multiLineBody(f.twoLines()))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	order, lines := orderForIntent(t, d, piIDFromClientSecret(decodeSubscribeIntentResponse(t, w).ClientSecret))

	require.Len(t, lines, 2)
	assert.Equal(t, 1620, lineByVariant(t, lines, f.first).UnitPrice)
	assert.Equal(t, 950, lineByVariant(t, lines, f.second).UnitPrice)
	assert.Equal(t, 2, lineByVariant(t, lines, f.second).Quantity)

	_, orderNamesAPlan := order.Metadata["subscription_plan_id"]
	assert.False(t, orderNamesAPlan, "the plan lives on each line now, not on the order")
	assert.Equal(t, true, order.Metadata["subscription_signup"])

	// Each line's discount is its own plan's. A handler that priced one plan
	// and recorded another passes every total above and fails here.
	plans := map[string]*domain.SubscriptionPlan{
		f.weekly.ID.String(): f.weekly, f.monthly.ID.String(): f.monthly,
	}
	for _, l := range lines {
		planID, _ := l.Metadata["subscription_plan_id"].(string)
		plan, ok := plans[planID]
		require.True(t, ok, "line %s names a plan from this signup, got %q", l.VariantID, planID)
		base := f.basePrice(l.VariantID)
		assert.Equal(t, plan.DiscountPct, (base-l.UnitPrice)*100/base,
			"line %s is discounted at its own plan's rate", l.VariantID)
	}
}

func TestSubscribePaymentIntent_TaxesEachLineOnItsOwn(t *testing.T) {
	// One taxable line, one exempt. 10% of the taxable line alone is 162. A
	// handler taxing the order subtotal under either product's flag gets 0 or
	// 352, so the one right number is the assertion.
	f := newMultiLineFixture(t)
	d, _ := newSubscribePaymentDeps(t)
	withFlatRateTax(t, 0.10)
	setTaxExempt(t, f.secondPr)

	w := postSubscribePaymentIntent(t, d, multiLineBody(f.twoLines(), shipToWashington))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, 162, decodeSubscribeIntentResponse(t, w).TaxTotal)
}

func TestSubscribePaymentIntent_RefusesBothBodyShapesAtOnce(t *testing.T) {
	f := newMultiLineFixture(t)
	d, fake := newSubscribePaymentDeps(t)

	// Which of the two the client meant is not something the server can know.
	body := multiLineBody(f.twoLines(), func(m map[string]any) {
		m["plan_id"] = f.weekly.ID.String()
		m["variant_id"] = f.first.String()
		m["quantity"] = 1
	})
	w := postSubscribePaymentIntent(t, d, body)
	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	assert.Zero(t, fake.intentCount())
}

func TestSubscribePaymentIntent_RefusesLinesOutsideTheCaps(t *testing.T) {
	f := newMultiLineFixture(t)
	d, fake := newSubscribePaymentDeps(t)

	eleven := make([]map[string]any, 11)
	for i := range eleven {
		eleven[i] = map[string]any{"plan_id": f.weekly.ID.String(), "variant_id": uuid.NewString(), "quantity": 1}
	}
	cases := map[string][]map[string]any{
		"eleven lines": eleven,
		"quantity 0":   {{"plan_id": f.weekly.ID.String(), "variant_id": f.first.String(), "quantity": 0}},
		"quantity 11":  {{"plan_id": f.weekly.ID.String(), "variant_id": f.first.String(), "quantity": 11}},
		"six and six merge to twelve": {
			{"plan_id": f.weekly.ID.String(), "variant_id": f.first.String(), "quantity": 6},
			{"plan_id": f.weekly.ID.String(), "variant_id": f.first.String(), "quantity": 6},
		},
	}
	for name, lines := range cases {
		t.Run(name, func(t *testing.T) {
			w := postSubscribePaymentIntent(t, d, multiLineBody(lines))
			assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
		})
	}
	assert.Zero(t, fake.intentCount())
}

func TestSubscribePaymentIntent_MergesDuplicateLines(t *testing.T) {
	f := newMultiLineFixture(t)
	d, _ := newSubscribePaymentDeps(t)

	w := postSubscribePaymentIntent(t, d, multiLineBody([]map[string]any{
		{"plan_id": f.weekly.ID.String(), "variant_id": f.first.String(), "quantity": 2},
		{"plan_id": f.weekly.ID.String(), "variant_id": f.first.String(), "quantity": 3},
	}))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	_, lines := orderForIntent(t, d, piIDFromClientSecret(decodeSubscribeIntentResponse(t, w).ClientSecret))
	require.Len(t, lines, 1)
	assert.Equal(t, 5, lines[0].Quantity)
}

// A signup holds every item it is given, but a customer can still sign up
// again while the first order is on the shelf. The open-box waiver covers that:
// the second signup is packed with the first, ships free, and says so on the
// order. It runs once for the whole multi-line order.
func TestSubscribePaymentIntent_ASecondSignupRidesWithAnOpenOrder(t *testing.T) {
	f := newMultiLineFixture(t)
	d, _ := newSubscribePaymentDeps(t)
	withFlatShippingRate(t, d, 700)
	email := "rides-" + uuid.NewString() + "@example.test"
	sameCustomer := func(m map[string]any) { m["email"] = email }

	w := postSubscribePaymentIntent(t, d, multiLineBody(f.twoLines(), sameCustomer))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	first := decodeSubscribeIntentResponse(t, w)
	require.Equal(t, 700, first.ShippingTotal, "nothing on the shelf yet, so the first pays shipping")
	firstOrder, _ := orderForIntent(t, d, piIDFromClientSecret(first.ClientSecret))

	// Paid, and waiting to be packed.
	ctx := context.Background()
	_, err := testPool.Exec(ctx,
		`UPDATE orders SET status = 'confirmed', payment_status = 'captured' WHERE id = $1`, firstOrder.ID)
	require.NoError(t, err)

	w = postSubscribePaymentIntent(t, d, multiLineBody(f.twoLines(), sameCustomer))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	second := decodeSubscribeIntentResponse(t, w)
	assert.Zero(t, second.ShippingTotal)
	assert.Equal(t, "Free — ships with "+firstOrder.Number, second.ShippingLabel)
	assert.Equal(t, second.Subtotal+second.TaxTotal, second.Amount, "no shipping in the charge")

	secondOrder, lines := orderForIntent(t, d, piIDFromClientSecret(second.ClientSecret))
	assert.Len(t, lines, 2, "one order for both lines")
	assert.Equal(t, firstOrder.Number, secondOrder.Metadata["ships_with_order"])
}

// --- Confirm and webhook ---

var riverMigrateOnce sync.Once

// withRiver gives d an insert-only River client over the test database, so a
// handler that enqueues in its transaction can be driven and the job read back.
// River's own tables are not part of the goose migrations; they are migrated
// here once, as cmd/server does at startup.
func withRiver(t *testing.T, d *Deps) {
	t.Helper()
	riverMigrateOnce.Do(func() {
		migrator, err := rivermigrate.New(riverpgxv5.New(testPool), nil)
		require.NoError(t, err)
		_, err = migrator.Migrate(context.Background(), rivermigrate.DirectionUp, nil)
		require.NoError(t, err)
	})
	client, err := river.NewClient(riverpgxv5.New(testPool), &river.Config{})
	require.NoError(t, err)
	d.RiverClient = client
}

// confirmEmailJobs returns the subscription ids on every confirmation job
// queued for customerID, one slice per job.
func confirmEmailJobs(t *testing.T, customerID uuid.UUID) [][]string {
	t.Helper()
	rows, err := testPool.Query(context.Background(), `
		SELECT args FROM river_job
		WHERE kind = 'email:subscription_confirm' AND args->>'customer_id' = $1`, customerID.String())
	require.NoError(t, err)
	defer rows.Close()
	var out [][]string
	for rows.Next() {
		var args struct {
			SubscriptionIDs []string `json:"subscription_ids"`
		}
		var raw []byte
		require.NoError(t, rows.Scan(&raw))
		require.NoError(t, json.Unmarshal(raw, &args))
		out = append(out, args.SubscriptionIDs)
	}
	require.NoError(t, rows.Err())
	return out
}

func postSubscribeConfirm(t *testing.T, d *Deps, intentID string) subscribeConfirmResponse {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/subscribe/confirm",
		strings.NewReader(`{"payment_intent_id":"`+intentID+`"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	d.handleSubscribeConfirm(w, r)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var raw map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &raw))
	var resp subscribeConfirmResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	_, hasSingle := raw["subscription_id"]
	if !hasSingle {
		resp.SubscriptionID = ""
	}
	return resp
}

// signUp places a signup through the payment-intent endpoint and marks the
// fake's intents as paid.
func signUp(t *testing.T, d *Deps, fake *fakePaymentProvider, lines []map[string]any) (intentID string, customerID uuid.UUID) {
	t.Helper()
	w := postSubscribePaymentIntent(t, d, multiLineBody(lines))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	intentID = piIDFromClientSecret(decodeSubscribeIntentResponse(t, w).ClientSecret)
	order, _ := orderForIntent(t, d, intentID)
	fake.mu.Lock()
	fake.intentStatus = payments.PaymentIntentStatusSucceeded
	fake.mu.Unlock()
	return intentID, *order.CustomerID
}

func subscriptionsOfOrder(t *testing.T, orderID uuid.UUID) []string {
	t.Helper()
	ctx := context.Background()
	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx) //nolint:errcheck
	subs, err := store.NewSubscriptionStore(nil).ListByOrder(ctx, tx, orderID)
	require.NoError(t, err)
	ids := make([]string, len(subs))
	for i, s := range subs {
		ids[i] = s.ID.String()
	}
	return ids
}

func TestSubscribeConfirm_ATwoLineSignup(t *testing.T) {
	f := newMultiLineFixture(t)
	d, fake := newSubscribePaymentDeps(t)
	withRiver(t, d)

	intentID, customerID := signUp(t, d, fake, f.twoLines())
	resp := postSubscribeConfirm(t, d, intentID)

	order, _ := orderForIntent(t, d, intentID)
	want := subscriptionsOfOrder(t, order.ID)
	require.Len(t, want, 2)
	assert.ElementsMatch(t, want, resp.SubscriptionIDs)
	assert.Empty(t, resp.SubscriptionID, "omitted: there is no one subscription to name")

	jobs := confirmEmailJobs(t, customerID)
	require.Len(t, jobs, 1, "one confirmation for the whole signup")
	assert.ElementsMatch(t, want, jobs[0])

	t.Run("a confirm that lost the race still names both", func(t *testing.T) {
		again := postSubscribeConfirm(t, d, intentID)
		assert.ElementsMatch(t, want, again.SubscriptionIDs)
		assert.Len(t, confirmEmailJobs(t, customerID), 1, "and queues nothing more")
	})
}

func TestSubscribeConfirm_AOneLineSignupKeepsBothFields(t *testing.T) {
	// The Svelte success screen reads subscription_id until the page learns
	// about several.
	f := newMultiLineFixture(t)
	d, fake := newSubscribePaymentDeps(t)
	withRiver(t, d)

	intentID, _ := signUp(t, d, fake, f.twoLines()[:1])
	resp := postSubscribeConfirm(t, d, intentID)
	require.Len(t, resp.SubscriptionIDs, 1)
	assert.Equal(t, resp.SubscriptionIDs[0], resp.SubscriptionID)
}

func TestPaymentIntentSucceededWebhook_ActivatesEveryLine(t *testing.T) {
	f := newMultiLineFixture(t)
	d, fake := newSubscribePaymentDeps(t)
	withRiver(t, d)

	intentID, customerID := signUp(t, d, fake, f.twoLines())
	require.NoError(t, d.handlePaymentIntentSucceeded(context.Background(), &payments.WebhookEvent{
		ID: "evt_" + uuid.NewString(), Type: "payment_intent.succeeded",
		Data: []byte(`{"id":"` + intentID + `"}`),
	}))

	order, _ := orderForIntent(t, d, intentID)
	subs := subscriptionsOfOrder(t, order.ID)
	require.Len(t, subs, 2)

	jobs := confirmEmailJobs(t, customerID)
	require.Len(t, jobs, 1)
	assert.ElementsMatch(t, subs, jobs[0])
}

// One variant on two plans is two lines — the merge key includes the plan, and
// the picker builds exactly this box. Each priced line names its plan, so the
// form can put each price on the line it belongs to; matched by variant alone,
// both prices land on the first line and the second shows none.
func TestSubscribePaymentIntent_EachPricedLineNamesItsPlan(t *testing.T) {
	f := newMultiLineFixture(t)
	d, _ := newSubscribePaymentDeps(t)

	w := postSubscribePaymentIntent(t, d, multiLineBody([]map[string]any{
		{"plan_id": f.weekly.ID.String(), "variant_id": f.first.String(), "quantity": 1},
		{"plan_id": f.monthly.ID.String(), "variant_id": f.first.String(), "quantity": 1},
	}))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	byPlan := map[string]int{}
	for _, l := range decodeSubscribeIntentResponse(t, w).Lines {
		assert.Equal(t, f.first.String(), l.VariantID)
		byPlan[l.PlanID] = l.UnitPrice
	}
	assert.Equal(t, map[string]int{
		f.weekly.ID.String():  1620, // 10% off 1800
		f.monthly.ID.String(): 1710, // 5% off 1800
	}, byPlan)
}
