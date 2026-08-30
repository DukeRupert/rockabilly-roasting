package admin

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every link in the control strip has to name its ranking.
//
// The regression: Hours carried the empty string, serviceCostsHref omitted the
// parameter for it, and the handler reads an absent sort as "give me the
// default" — so clicking Hours returned the cost ranking with Cost highlighted.
func TestServiceCostsHrefAlwaysNamesTheSort(t *testing.T) {
	for _, sort := range []string{"", "hours", "parts", "visits", "cost"} {
		href := serviceCostsHref(90, sort)

		u, err := url.Parse(href)
		require.NoError(t, err)
		got := u.Query().Get("sort")

		assert.NotEmpty(t, got, "%q produced %q, which reads as no preference", sort, href)
		if sort != "" {
			assert.Equal(t, sort, got)
		} else {
			assert.Equal(t, "hours", got,
				"the strip's own Hours link must name hours — an omitted parameter asks for the default, which is cost")
		}
	}
}

// The strip offers Cost only where there is money to rank on.
func TestServiceSortsOffersCostOnlyWhenCostable(t *testing.T) {
	var unrated []string
	for _, s := range serviceSorts(false) {
		unrated = append(unrated, s.Value)
	}
	assert.NotContains(t, unrated, "cost")
	assert.Contains(t, unrated, "hours")

	var rated []string
	for _, s := range serviceSorts(true) {
		rated = append(rated, s.Value)
	}
	assert.Equal(t, "cost", rated[0], "cost leads once it means something")
}
