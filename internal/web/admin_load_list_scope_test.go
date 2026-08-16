package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
)

// The load list defaults to both channels — one van makes one run — so the
// absent, empty, and unrecognized cases all have to resolve to "no filter"
// rather than quietly falling back to retail. A scope that silently narrows
// would leave half the run off the sheet and off the route planned from it.
func TestLoadListScope(t *testing.T) {
	retail := domain.OrderChannelRetail
	wholesale := domain.OrderChannelWholesale

	tests := []struct {
		name  string
		query string
		want  *domain.OrderChannel
	}{
		{name: "absent means both", query: "", want: nil},
		{name: "empty value means both", query: "?channel=", want: nil},
		{name: "explicit all means both", query: "?channel=all", want: nil},
		{name: "garbage falls back to both", query: "?channel=nonsense", want: nil},
		{name: "retail narrows", query: "?channel=retail", want: &retail},
		{name: "wholesale narrows", query: "?channel=wholesale", want: &wholesale},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/admin/fulfillment/load-list"+tc.query, nil)
			got := loadListScope(r)
			if tc.want == nil {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, *tc.want, *got)
		})
	}
}

// The scope has to survive the round trip into the totals fragment and the
// print sheet: whatever the page resolved is re-serialized into their URLs, so
// a lossy param form would silently re-widen a narrowed sheet.
func TestLoadListScopeParamRoundTrip(t *testing.T) {
	for _, param := range []string{"", "retail", "wholesale"} {
		r := httptest.NewRequest(http.MethodGet, "/admin/fulfillment/load-list?channel="+param, nil)
		assert.Equal(t, param, loadListScopeParam(loadListScope(r)))
	}
}

func TestLoadListChannelLabel(t *testing.T) {
	retail := domain.OrderChannelRetail
	wholesale := domain.OrderChannelWholesale

	assert.Equal(t, "All channels", loadListChannelLabel(nil))
	assert.Equal(t, "Retail", loadListChannelLabel(&retail))
	assert.Equal(t, "Wholesale", loadListChannelLabel(&wholesale))
}
