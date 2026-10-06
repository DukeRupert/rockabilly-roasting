package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The box summary at the top of /subscribe is rendered by the server. When the
// picker adds or removes a line, the Svelte app fetches this fragment for the
// new URL and swaps it in, so the summary, its per-delivery total and the
// separate-shipments notice never describe a box the customer has since
// changed. The fragment is the page's own summary component, priced by the
// same PriceLines call — there is one template and one price computation.

func getSubscribeBoxSummary(t *testing.T, d *Deps, query string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/subscribe/box?"+query, nil)
	w := httptest.NewRecorder()
	d.handleSubscribeBoxSummary(w, r)
	return w
}

func TestSubscribeBoxSummary_IsThePagesSummaryForTheNewBox(t *testing.T) {
	f := newMultiLineFixture(t)
	d := newSubscribeDeps(t)

	// One variant on two plans, the case the payment page once mispriced.
	q := lineQuery(f.weekly.ID, f.first, 1) + "&" + lineQuery(f.monthly.ID, f.first, 1)
	w := getSubscribeBoxSummary(t, d, q)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	body := w.Body.String()

	assert.Contains(t, body, `id="subscribe-box-summary"`)
	assert.Contains(t, body, "$16.20", "weekly: 10% off 1800")
	assert.Contains(t, body, "$17.10", "monthly: 5% off 1800")
	assert.Contains(t, body, "$33.30", "the per-delivery total for both")
	assert.Contains(t, body, "ships together this first time", "two plans: the separate-shipments notice")

	// The app reads the new prices from here, matched by plan and variant.
	prices := pageLinePricesIn(t, body, "subscribe-box-summary")
	assert.Len(t, prices, 1, "one variant")

	// A fragment: no layout, and no second Svelte mount.
	assert.NotContains(t, body, "<html")
	assert.NotContains(t, body, `id="subscribe-app"`)
}

func TestSubscribeBoxSummary_TheNoticeFollowsTheBox(t *testing.T) {
	f := newMultiLineFixture(t)
	d := newSubscribeDeps(t)

	w := getSubscribeBoxSummary(t, d, lineQuery(f.weekly.ID, f.first, 1)+"&"+lineQuery(f.weekly.ID, f.second, 2))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.NotContains(t, w.Body.String(), "ships together this first time", "one plan: no separate shipments")
	assert.Contains(t, w.Body.String(), "$34.20", "1620 + 2 × 900")
}

func TestSubscribeBoxSummary_ThePageRendersTheSameComponent(t *testing.T) {
	f := newMultiLineFixture(t)
	d := newSubscribeDeps(t)

	w := getSubscribePage(t, d, lineQuery(f.weekly.ID, f.first, 1), nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, 1, strings.Count(w.Body.String(), `id="subscribe-box-summary"`),
		"the element the app swaps is the one the page drew")
}

func TestSubscribeBoxSummary_RefusesALineThePageWouldRefuse(t *testing.T) {
	f := newMultiLineFixture(t)
	d := newSubscribeDeps(t)
	ctx := context.Background()
	_, err := testPool.Exec(ctx, `UPDATE subscription_plans SET is_active = false WHERE id = $1`, f.monthly.ID)
	require.NoError(t, err)

	w := getSubscribeBoxSummary(t, d, lineQuery(f.monthly.ID, f.first, 1))
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Contains(t, w.Body.String(), "no longer available")
}

// pageLinePricesIn reads the data-lines JSON off the element with the given id.
func pageLinePricesIn(t *testing.T, body, id string) map[string]int {
	t.Helper()
	m := regexp.MustCompile(`id="` + id + `"[^>]*`).FindString(body)
	require.NotEmpty(t, m, "element %s", id)
	return pageLinePrices(t, m)
}
