package web

import (
	"context"
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/auth"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// The /subscribe page assembling a box: several lines in one URL, priced
// through the same PriceLines the payment intent uses, and the picker's
// catalog endpoint. See docs/subscriptions-module.md, "The box".

func getSubscribePage(t *testing.T, d *Deps, query string, customer *domain.Customer) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/subscribe?"+query, nil)
	if customer != nil {
		r = r.WithContext(auth.WithCustomer(r.Context(), customer))
	}
	w := httptest.NewRecorder()
	d.handleSubscribePage(w, r)
	return w
}

func lineQuery(planID, variantID uuid.UUID, qty int) string {
	return "line=" + url.QueryEscape(planID.String()+":"+variantID.String()+":"+strconv.Itoa(qty))
}

func TestSubscribePage_RendersMultipleLinesWithADataLinesAttribute(t *testing.T) {
	f := newMultiLineFixture(t)
	d := newSubscribeDeps(t)

	q := lineQuery(f.weekly.ID, f.first, 1) + "&" + lineQuery(f.monthly.ID, f.second, 2)
	w := getSubscribePage(t, d, q, nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	body := w.Body.String()

	// 1620 (10% off 1800) + 2×950 (5% off 1000) = 3520.
	assert.Contains(t, body, "$16.20")
	assert.Contains(t, body, "$9.50", "the second line's own unit price")
	assert.Contains(t, body, "$35.20", "the box subtotal")
	assert.Contains(t, body, `id="subscribe-app"`)
	assert.Contains(t, body, `data-lines="`)
	assert.Contains(t, body, f.first.String())
	assert.Contains(t, body, f.second.String())
}

// The page and the payment intent quote each line from the same PriceLines
// call, from the base price less the line's plan discount, so a customer never
// reads one price and pays another. If either stops doing that, the two
// numbers drift and this is the test that catches it. Checked for a guest, who
// has no customer row until the intent creates one, and for a signed-in
// customer reading the page.
func TestSubscribePage_PricesMatchThePaymentIntentForTheSameCustomer(t *testing.T) {
	f := newMultiLineFixture(t)
	d, _ := newSubscribePaymentDeps(t)
	signedIn := committedPageCustomer(t)

	for name, customer := range map[string]*domain.Customer{"guest": nil, "signed in": signedIn} {
		t.Run(name, func(t *testing.T) {
			q := lineQuery(f.weekly.ID, f.first, 1) + "&" + lineQuery(f.monthly.ID, f.second, 2)
			w := getSubscribePage(t, d, q, customer)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			page := pageLinePrices(t, w.Body.String())

			iw := postSubscribePaymentIntent(t, d, multiLineBody(f.twoLines()))
			require.Equal(t, http.StatusOK, iw.Code, iw.Body.String())
			intent := map[string]int{}
			for _, l := range decodeSubscribeIntentResponse(t, iw).Lines {
				intent[l.VariantID] = l.UnitPrice
			}

			require.Len(t, page, 2)
			assert.Equal(t, intent, page, "each line's price on the page is the price the intent charges")
			assert.Equal(t, 1620, page[f.first.String()], "10% off 1800")
			assert.Equal(t, 950, page[f.second.String()], "5% off 1000")
		})
	}
}

// pageLinePrices reads each line's unit price, by variant, out of the data-lines
// JSON the page hands the Svelte app.
func pageLinePrices(t *testing.T, body string) map[string]int {
	t.Helper()
	m := regexp.MustCompile(`data-lines="([^"]*)"`).FindStringSubmatch(body)
	require.NotNil(t, m, "the mount div carries data-lines")
	var lines []struct {
		VariantID string `json:"variant_id"`
		UnitPrice int    `json:"unit_price"`
	}
	require.NoError(t, json.Unmarshal([]byte(html.UnescapeString(m[1])), &lines))
	out := map[string]int{}
	for _, l := range lines {
		out[l.VariantID] = l.UnitPrice
	}
	return out
}

func committedPageCustomer(t *testing.T) *domain.Customer {
	t.Helper()
	ctx := context.Background()
	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx) //nolint:errcheck
	customer := testutil.CreateCustomer(t, tx)
	require.NoError(t, tx.Commit(ctx))
	return customer
}

func TestSubscribePage_LegacyQueryStillRendersOneLine(t *testing.T) {
	f := newMultiLineFixture(t)
	d := newSubscribeDeps(t)

	w := getSubscribePage(t, d, "plan_id="+f.weekly.ID.String()+"&variant_id="+f.first.String()+"&quantity=2", nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `id="subscribe-app"`)
	assert.Contains(t, w.Body.String(), f.first.String())
}

func TestSubscribePage_LineErrors(t *testing.T) {
	f := newMultiLineFixture(t)
	d := newSubscribeDeps(t)
	ctx := context.Background()

	t.Run("an inactive plan is 422 not 500", func(t *testing.T) {
		tx, err := testPool.Begin(ctx)
		require.NoError(t, err)
		inactive, err := store.NewSubscriptionStore(nil).CreatePlan(ctx, tx, store.CreatePlanParams{
			Name: "Retired", Interval: domain.SubscriptionIntervalEvery30Days, IntervalCount: 1, IsActive: false,
		})
		require.NoError(t, err)
		require.NoError(t, tx.Commit(ctx))

		w := getSubscribePage(t, d, lineQuery(inactive.ID, f.first, 1), nil)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code, w.Body.String())
		assert.Contains(t, w.Body.String(), "no longer available")
	})

	t.Run("an archived variant is 422 not 500", func(t *testing.T) {
		tx, err := testPool.Begin(ctx)
		require.NoError(t, err)
		_, err = d.CatalogService.ArchiveVariant(ctx, tx, f.second, testutil.TestActor())
		require.NoError(t, err)
		require.NoError(t, tx.Commit(ctx))
		t.Cleanup(func() {
			tx, err := testPool.Begin(ctx)
			require.NoError(t, err)
			_, err = d.CatalogService.UnarchiveVariant(ctx, tx, f.second, testutil.TestActor())
			require.NoError(t, err)
			require.NoError(t, tx.Commit(ctx))
		})

		w := getSubscribePage(t, d, lineQuery(f.monthly.ID, f.second, 1), nil)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code, w.Body.String())
	})
}

func TestSubscribePage_SeparateShipmentsNotice(t *testing.T) {
	f := newMultiLineFixture(t)
	d := newSubscribeDeps(t)

	t.Run("shown when lines are on more than one plan", func(t *testing.T) {
		q := lineQuery(f.weekly.ID, f.first, 1) + "&" + lineQuery(f.monthly.ID, f.second, 1)
		w := getSubscribePage(t, d, q, nil)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.Contains(t, w.Body.String(), "ships together this first time")
	})

	t.Run("not shown when every line shares a plan", func(t *testing.T) {
		q := lineQuery(f.weekly.ID, f.first, 1) + "&" + lineQuery(f.weekly.ID, f.second, 1)
		w := getSubscribePage(t, d, q, nil)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.NotContains(t, w.Body.String(), "ships together this first time")
	})
}

func getSubscribeCatalog(t *testing.T, d *Deps) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/subscribe/catalog", nil)
	w := httptest.NewRecorder()
	d.handleSubscribeCatalog(w, r)
	return w
}

func TestSubscribeCatalog_ListsSubscribableVariantsAndActivePlans(t *testing.T) {
	f := newMultiLineFixture(t)
	d := newSubscribeDeps(t)
	ctx := context.Background()

	// A wholesale-only size of a subscribable product: a retail subscriber
	// cannot be sold it, so the picker never offers it.
	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	wholesaleOnly := testutil.CreateVariant(t, tx, f.firstProd)
	testutil.SetBasePriceForVariant(t, tx, wholesaleOnly.ID, 9000, "USD")
	_, err = tx.Exec(ctx, `UPDATE variants SET retail_available = false WHERE id = $1`, wholesaleOnly.ID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `UPDATE products SET subscribable = true WHERE id IN ($1, $2)`,
		f.firstProd, f.secondPr)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))

	w := getSubscribeCatalog(t, d)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var resp subscribeCatalogResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	byVariant := map[string]subscribeCatalogVariant{}
	for _, v := range resp.Variants {
		byVariant[v.VariantID] = v
	}
	_, hasFirst := byVariant[f.first.String()]
	_, hasSecond := byVariant[f.second.String()]
	assert.True(t, hasFirst)
	assert.True(t, hasSecond)
	_, hasWholesaleOnly := byVariant[wholesaleOnly.ID.String()]
	assert.False(t, hasWholesaleOnly, "a variant not sold on the retail channel is not offered")

	var planNames []string
	for _, p := range resp.Plans {
		planNames = append(planNames, p.Name)
	}
	assert.Contains(t, planNames, "Weekly")
	assert.Contains(t, planNames, "Monthly")
}

func TestParseSubscribeLines(t *testing.T) {
	weekly, monthly, variant1, variant2 := uuid.New(), uuid.New(), uuid.New(), uuid.New()

	t.Run("several line params", func(t *testing.T) {
		q := url.Values{"line": []string{
			weekly.String() + ":" + variant1.String() + ":1",
			monthly.String() + ":" + variant2.String() + ":2",
		}}
		lines, err := parseSubscribeLines(q)
		require.NoError(t, err)
		require.Len(t, lines, 2)
	})

	t.Run("legacy trio alone", func(t *testing.T) {
		q := url.Values{"plan_id": {weekly.String()}, "variant_id": {variant1.String()}, "quantity": {"3"}}
		lines, err := parseSubscribeLines(q)
		require.NoError(t, err)
		require.Len(t, lines, 1)
		assert.Equal(t, 3, lines[0].Quantity)
	})

	t.Run("the legacy trio beside line= entries", func(t *testing.T) {
		q := url.Values{
			"plan_id": {weekly.String()}, "variant_id": {variant1.String()}, "quantity": {"1"},
			"line": {monthly.String() + ":" + variant2.String() + ":1"},
		}
		lines, err := parseSubscribeLines(q)
		require.NoError(t, err)
		require.Len(t, lines, 2)
	})

	t.Run("nothing at all is refused", func(t *testing.T) {
		_, err := parseSubscribeLines(url.Values{})
		assert.Error(t, err)
	})

	t.Run("a malformed line is refused", func(t *testing.T) {
		_, err := parseSubscribeLines(url.Values{"line": {"not-enough-parts"}})
		assert.Error(t, err)
	})
}
