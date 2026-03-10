package quickbooks

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

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
