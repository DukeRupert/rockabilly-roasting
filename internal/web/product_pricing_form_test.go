package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// postForm builds a request whose PostForm is already parsed, the state the
// pricing parsers expect.
func postForm(t *testing.T, values url.Values) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/admin/catalog/x/pricing", strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	require.NoError(t, r.ParseForm())
	return r
}

// The product pricing grid submits every cell on the page, so the parser's whole
// job is deciding which ones actually changed. Writing an unchanged cell would
// churn rows on every save.
func TestParseProductPricingFormOnlyChangedCells(t *testing.T) {
	v1, v2 := uuid.New(), uuid.New()
	listID := uuid.New()

	r := postForm(t, url.Values{
		// Unchanged base — must not produce an op.
		"base:" + v1.String():      {"18.00"},
		"base_prev:" + v1.String(): {"18.00"},
		// Changed base.
		"base:" + v2.String():      {"19.50"},
		"base_prev:" + v2.String(): {"18.00"},
		// Changed list override.
		"list:" + listID.String() + ":" + v1.String():      {"15.25"},
		"list_prev:" + listID.String() + ":" + v1.String(): {""},
		// Cleared list override — a delete, not a set.
		"list:" + listID.String() + ":" + v2.String():      {""},
		"list_prev:" + listID.String() + ":" + v2.String(): {"14.00"},
	})

	ops, err := parseProductPricingForm(r)
	require.NoError(t, err)
	require.Len(t, ops, 3)

	byKind := map[priceOpKind][]priceOp{}
	for _, op := range ops {
		byKind[op.kind] = append(byKind[op.kind], op)
	}

	require.Len(t, byKind[opBaseSet], 1)
	assert.Equal(t, v2, byKind[opBaseSet][0].variantID)
	assert.Equal(t, 1950, byKind[opBaseSet][0].cents)

	require.Len(t, byKind[opGroupSet], 1)
	assert.Equal(t, v1, byKind[opGroupSet][0].variantID)
	assert.Equal(t, listID, byKind[opGroupSet][0].groupID)
	assert.Equal(t, 1525, byKind[opGroupSet][0].cents)

	require.Len(t, byKind[opGroupDelete], 1)
	assert.Equal(t, v2, byKind[opGroupDelete][0].variantID)
	assert.Equal(t, listID, byKind[opGroupDelete][0].groupID)
}

// Clearing a base price is rejected rather than applied: every list price falls
// back to base, so an empty base leaves those customers with no price at all.
func TestParseProductPricingFormRejectsClearedBase(t *testing.T) {
	variantID := uuid.New()

	r := postForm(t, url.Values{
		"base:" + variantID.String():      {""},
		"base_prev:" + variantID.String(): {"18.00"},
	})

	_, err := parseProductPricingForm(r)
	assert.ErrorIs(t, err, errBasePriceRequired)
}

// A base cell that was empty and stays empty is a variant with no price yet, not
// an attempt to clear one.
func TestParseProductPricingFormAllowsStillUnsetBase(t *testing.T) {
	variantID := uuid.New()

	r := postForm(t, url.Values{
		"base:" + variantID.String():      {""},
		"base_prev:" + variantID.String(): {""},
	})

	ops, err := parseProductPricingForm(r)
	require.NoError(t, err)
	assert.Empty(t, ops)
}

func TestParseProductPricingFormRejectsMalformedPrice(t *testing.T) {
	variantID := uuid.New()

	r := postForm(t, url.Values{
		"base:" + variantID.String():      {"twelve"},
		"base_prev:" + variantID.String(): {"18.00"},
	})

	_, err := parseProductPricingForm(r)
	assert.Error(t, err)
}
