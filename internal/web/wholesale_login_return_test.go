package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// A logged-out click on a wholesale deep link (e.g. the reminder email's
// "Reorder This") must come back to that page after signing in, not dump the
// customer on the portal.
func TestWholesaleLoginWithReturn(t *testing.T) {
	tests := []struct {
		name   string
		method string
		target string
		want   string
	}{
		{
			name:   "GET deep link round-trips",
			method: http.MethodGet,
			target: "/wholesale/reorder",
			want:   "/wholesale/login?redirect=%2Fwholesale%2Freorder",
		},
		{
			name:   "query string is preserved",
			method: http.MethodGet,
			target: "/wholesale/checkout?reordered=3",
			want:   "/wholesale/login?redirect=%2Fwholesale%2Fcheckout%3Freordered%3D3",
		},
		{
			// Replaying a POST after login would re-submit something the
			// customer never re-confirmed.
			name:   "POST does not round-trip",
			method: http.MethodPost,
			target: "/wholesale/cart/update",
			want:   "/wholesale/login",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(tt.method, tt.target, nil)
			require.Equal(t, tt.want, wholesaleLoginWithReturn(r))
		})
	}
}

// safeNextOr guards the redirect against being turned into an off-site bounce.
func TestSafeNextOrRejectsOffsiteRedirects(t *testing.T) {
	tests := []struct {
		name string
		next string
		want string
	}{
		{"absolute http URL", "http://evil.test/phish", "/fallback"},
		{"absolute https URL", "https://evil.test/phish", "/fallback"},
		{"protocol-relative", "//evil.test/phish", "/fallback"},
		{"empty", "", "/fallback"},
		{"bare word", "evil.test", "/fallback"},
		{"local path allowed", "/wholesale/reorder", "/wholesale/reorder"},
		{"local path with query allowed", "/wholesale/checkout?x=1", "/wholesale/checkout?x=1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, safeNextOr(tt.next, "/fallback"))
		})
	}
}
