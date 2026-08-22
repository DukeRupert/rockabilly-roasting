package admin

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The website comes from the public wholesale application form and is rendered
// through templ.SafeURL, which bypasses templ's own sanitizer. This allow-list
// is the only thing between an applicant and a script URL on a staff page, so
// it gets a test rather than a comment.
func TestSafeWebsiteURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"whitespace only", "   ", ""},
		{"bare domain gets https", "example.com", "https://example.com"},
		{"bare domain with path", "example.com/wholesale", "https://example.com/wholesale"},
		{"www prefix", "www.example.com", "https://www.example.com"},
		{"https passes through", "https://example.com", "https://example.com"},
		{"http passes through", "http://example.com", "http://example.com"},
		{"scheme case is ignored", "HTTPS://example.com", "HTTPS://example.com"},
		{"surrounding whitespace trimmed", "  example.com  ", "https://example.com"},
		{"host:port is not a scheme", "example.com:8080/x", "https://example.com:8080/x"},

		// The ones that matter.
		{"javascript scheme rejected", "javascript:alert(1)", ""},
		{"javascript with slashes rejected", "javascript://%0aalert(1)", ""},
		{"data scheme rejected", "data:text/html,<script>alert(1)</script>", ""},
		{"vbscript scheme rejected", "vbscript:msgbox(1)", ""},
		{"file scheme rejected", "file:///etc/passwd", ""},
		{"ftp scheme rejected", "ftp://example.com", ""},
		{"uppercase javascript rejected", "JavaScript:alert(1)", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, safeWebsiteURL(tc.in))
		})
	}
}
