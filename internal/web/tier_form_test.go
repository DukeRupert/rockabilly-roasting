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

	"github.com/dukerupert/hiri/internal/domain"
)

func tierFormRequest(t *testing.T, form url.Values) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/admin/price-lists/x/products/y/tiers", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	require.NoError(t, r.ParseForm())
	return r
}

func TestParseTierForm(t *testing.T) {
	v1 := uuid.New()
	v2 := uuid.New()

	t.Run("collects breaks per variant", func(t *testing.T) {
		got, err := parseTierForm(tierFormRequest(t, url.Values{
			"tier_qty:" + v1.String() + ":0":   {"12"},
			"tier_price:" + v1.String() + ":0": {"10.00"},
			"tier_qty:" + v1.String() + ":1":   {"24"},
			"tier_price:" + v1.String() + ":1": {"9.50"},
			"tier_qty:" + v2.String() + ":0":   {"6"},
			"tier_price:" + v2.String() + ":0": {"44.00"},
		}))
		require.NoError(t, err)
		assert.ElementsMatch(t, []domain.PriceTier{
			{MinQuantity: 12, Amount: 1000},
			{MinQuantity: 24, Amount: 950},
		}, got[v1])
		assert.Equal(t, []domain.PriceTier{{MinQuantity: 6, Amount: 4400}}, got[v2])
	})

	t.Run("a variant with only blank rows still appears, so its ladder clears", func(t *testing.T) {
		// The editor always renders one empty slot. A variant whose breaks were
		// all deleted submits nothing but blanks, and must still be present in
		// the result — otherwise the save would skip it and the old ladder would
		// survive a deletion.
		got, err := parseTierForm(tierFormRequest(t, url.Values{
			"tier_qty:" + v1.String() + ":0":   {""},
			"tier_price:" + v1.String() + ":0": {""},
		}))
		require.NoError(t, err)
		_, present := got[v1]
		assert.True(t, present)
		assert.Empty(t, got[v1])
	})

	t.Run("clearing a quantity removes the break, price left or not", func(t *testing.T) {
		// The editor tells staff to clear a quantity to remove a break, and the
		// save replaces the whole ladder — so a row without a quantity is simply
		// not rewritten. Requiring the price be cleared too would make the
		// documented gesture fail.
		got, err := parseTierForm(tierFormRequest(t, url.Values{
			"tier_qty:" + v1.String() + ":0":   {""},
			"tier_price:" + v1.String() + ":0": {"11.00"},
			"tier_qty:" + v1.String() + ":1":   {"24"},
			"tier_price:" + v1.String() + ":1": {"10.25"},
		}))
		require.NoError(t, err)
		assert.Equal(t, []domain.PriceTier{{MinQuantity: 24, Amount: 1025}}, got[v1])
	})

	t.Run("a quantity with no price is an error, not a silent drop", func(t *testing.T) {
		// Unlike a cleared quantity, this is ambiguous — a break at a quantity
		// with no price means nothing, and dropping it would look to staff
		// exactly like a successful save.
		_, err := parseTierForm(tierFormRequest(t, url.Values{
			"tier_qty:" + v1.String() + ":0":   {"12"},
			"tier_price:" + v1.String() + ":0": {""},
		}))
		assert.Error(t, err)
	})

	t.Run("a break below 2 is rejected", func(t *testing.T) {
		for _, qty := range []string{"1", "0", "-4", "abc"} {
			_, err := parseTierForm(tierFormRequest(t, url.Values{
				"tier_qty:" + v1.String() + ":0":   {qty},
				"tier_price:" + v1.String() + ":0": {"10.00"},
			}))
			assert.Error(t, err, "qty %q", qty)
		}
	})

	t.Run("duplicate thresholds are rejected", func(t *testing.T) {
		// The database would reject this too, but catching it here names the
		// actual problem instead of surfacing a constraint violation.
		_, err := parseTierForm(tierFormRequest(t, url.Values{
			"tier_qty:" + v1.String() + ":0":   {"12"},
			"tier_price:" + v1.String() + ":0": {"10.00"},
			"tier_qty:" + v1.String() + ":1":   {"12"},
			"tier_price:" + v1.String() + ":1": {"9.00"},
		}))
		assert.Error(t, err)
	})

	t.Run("a malformed price is rejected", func(t *testing.T) {
		_, err := parseTierForm(tierFormRequest(t, url.Values{
			"tier_qty:" + v1.String() + ":0":   {"12"},
			"tier_price:" + v1.String() + ":0": {"ten dollars"},
		}))
		assert.Error(t, err)
	})

	t.Run("unrelated form fields are ignored", func(t *testing.T) {
		got, err := parseTierForm(tierFormRequest(t, url.Values{
			"csrf_token":                       {"abc"},
			"tier_qty:not-a-uuid:0":            {"12"},
			"tier_qty:" + v1.String() + ":0":   {"12"},
			"tier_price:" + v1.String() + ":0": {"10.00"},
		}))
		require.NoError(t, err)
		assert.Len(t, got, 1)
	})
}
