package web

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
)

func TestAddressMatches(t *testing.T) {
	ptr := func(s string) *string { return &s }

	base := domain.Address{
		Line1:       "123 Main St",
		Line2:       ptr("Apt 4"),
		City:        "Kennewick",
		State:       "WA",
		PostalCode:  "99336",
		CountryCode: "US",
	}
	params := store.CreateAddressParams{
		Line1:       "123 Main St",
		Line2:       ptr("Apt 4"),
		City:        "Kennewick",
		State:       "WA",
		PostalCode:  "99336",
		CountryCode: "US",
	}

	t.Run("identical matches", func(t *testing.T) {
		assert.True(t, addressMatches(base, params))
	})

	t.Run("case and whitespace insensitive", func(t *testing.T) {
		a := base
		a.Line1 = "  123 main st "
		a.City = "KENNEWICK"
		a.State = "wa"
		assert.True(t, addressMatches(a, params))
	})

	t.Run("nil line2 equals empty line2", func(t *testing.T) {
		a := base
		a.Line2 = nil
		p := params
		p.Line2 = nil
		assert.True(t, addressMatches(a, p))

		// nil vs blank string also match (both normalize to "").
		p.Line2 = ptr("   ")
		assert.True(t, addressMatches(a, p))
	})

	t.Run("different line1 does not match", func(t *testing.T) {
		a := base
		a.Line1 = "124 Main St"
		assert.False(t, addressMatches(a, params))
	})

	t.Run("different apartment does not match", func(t *testing.T) {
		a := base
		a.Line2 = ptr("Apt 5")
		assert.False(t, addressMatches(a, params))
	})

	t.Run("different zip does not match", func(t *testing.T) {
		a := base
		a.PostalCode = "99337"
		assert.False(t, addressMatches(a, params))
	})
}
