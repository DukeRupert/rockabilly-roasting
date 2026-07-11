package quickbooks

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPickEmailMatch(t *testing.T) {
	missoula := QBCustomer{ID: "10", DisplayName: "Blue Heron Cafe - Missoula", Email: "owner@blueheron.com"}
	bozeman := QBCustomer{ID: "11", DisplayName: "Blue Heron Cafe - Bozeman", Email: "owner@blueheron.com"}

	t.Run("no matches", func(t *testing.T) {
		assert.Nil(t, pickEmailMatch(nil, "Blue Heron Cafe - Bozeman"))
	})

	t.Run("single match is trusted regardless of name", func(t *testing.T) {
		got := pickEmailMatch([]QBCustomer{missoula}, "Some Other Name")
		assert.Equal(t, &missoula, got)
	})

	t.Run("shared email resolves by display name", func(t *testing.T) {
		got := pickEmailMatch([]QBCustomer{missoula, bozeman}, "Blue Heron Cafe - Bozeman")
		assert.Equal(t, &bozeman, got)
	})

	t.Run("display name comparison is case-insensitive", func(t *testing.T) {
		got := pickEmailMatch([]QBCustomer{missoula, bozeman}, "blue heron cafe - bozeman")
		assert.Equal(t, &bozeman, got)
	})

	t.Run("ambiguous email with no name match defers to display-name lookup", func(t *testing.T) {
		assert.Nil(t, pickEmailMatch([]QBCustomer{missoula, bozeman}, "Roadside Diner"))
	})

	t.Run("ambiguous email with empty display name defers", func(t *testing.T) {
		assert.Nil(t, pickEmailMatch([]QBCustomer{missoula, bozeman}, ""))
	})
}

func TestEscapeQBQuery(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"no special chars", "Acme Corp", "Acme Corp"},
		{"single quote", "O'Brien", "O''Brien"},
		{"multiple single quotes", "it's a 'test'", "it''s a ''test''"},
		{"backslash unchanged", `back\slash`, `back\slash`},
		{"empty string", "", ""},
		{"only quotes", "'''", "''''''"},
		{"injection attempt", "' OR 1=1 --", "'' OR 1=1 --"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := escapeQBQuery(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}
