package web

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsNewWholesaleAddr(t *testing.T) {
	assert.True(t, isNewWholesaleAddr(""), "empty means no saved address was selected")
	assert.True(t, isNewWholesaleAddr("new"))
	assert.False(t, isNewWholesaleAddr("d56a0ba3-8253-4e51-9c1b-7b5f8b9dd0fe"))
}

func TestWholesaleNewAddrComplete(t *testing.T) {
	newReq := func(values url.Values) (complete bool) {
		r := httptest.NewRequest("POST", "/wholesale/checkout/confirm", strings.NewReader(values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return wholesaleNewAddrComplete(r, "ship")
	}

	t.Run("all required fields present", func(t *testing.T) {
		assert.True(t, newReq(url.Values{
			"ship_line1":       {"42 Tisdale Ave"},
			"ship_city":        {"Portland"},
			"ship_state":       {"OR"},
			"ship_postal_code": {"97214"},
		}))
	})

	t.Run("missing street is incomplete", func(t *testing.T) {
		assert.False(t, newReq(url.Values{
			"ship_city":        {"Portland"},
			"ship_state":       {"OR"},
			"ship_postal_code": {"97214"},
		}))
	})

	t.Run("whitespace-only field is incomplete", func(t *testing.T) {
		assert.False(t, newReq(url.Values{
			"ship_line1":       {"   "},
			"ship_city":        {"Portland"},
			"ship_state":       {"OR"},
			"ship_postal_code": {"97214"},
		}))
	})

	t.Run("prefix scopes the fields", func(t *testing.T) {
		// ship_* present but we validate the bill_* prefix → incomplete.
		r := httptest.NewRequest("POST", "/", strings.NewReader(url.Values{
			"ship_line1":       {"42 Tisdale Ave"},
			"ship_city":        {"Portland"},
			"ship_state":       {"OR"},
			"ship_postal_code": {"97214"},
		}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		assert.False(t, wholesaleNewAddrComplete(r, "bill"))
	})
}
