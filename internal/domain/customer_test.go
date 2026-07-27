package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dukerupert/hiri/internal/domain"
)

func TestNormalizeEmail(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"already canonical", "info@example.com", "info@example.com"},
		{"leading capital", "Info@example.com", "info@example.com"},
		{"all caps", "MOESTEACAFE@GMAIL.COM", "moesteacafe@gmail.com"},
		{"mixed case domain", "kara@ExAmPle.CoM", "kara@example.com"},
		{"surrounding whitespace", "  info@example.com  ", "info@example.com"},
		{"pasted with trailing newline", "info@example.com\n", "info@example.com"},
		{"whitespace and case together", " Info@Example.com ", "info@example.com"},
		{"empty stays empty", "", ""},
		{"whitespace only collapses to empty", "   ", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, domain.NormalizeEmail(tc.in))
		})
	}
}

// Normalizing must be idempotent -- it runs at several boundaries and an address
// may pass through more than one of them before reaching the database.
func TestNormalizeEmail_Idempotent(t *testing.T) {
	for _, in := range []string{"Info@Example.com", "  MIXED@Case.NET ", "plain@example.com"} {
		once := domain.NormalizeEmail(in)
		assert.Equal(t, once, domain.NormalizeEmail(once), "input %q", in)
	}
}
