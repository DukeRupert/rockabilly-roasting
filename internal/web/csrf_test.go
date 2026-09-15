package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The CSRF middleware has to hold two lines at once: refuse a cross-site POST
// from a browser, and let the server-to-server webhooks through. Getting the
// second wrong would silently stop Stripe payments, Shippo labels and
// QuickBooks invoice updates from being processed, with nothing failing
// locally — so each case is pinned here rather than discovered in production.
func TestCrossOriginProtection(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := crossOriginProtection(ok)

	call := func(method, target string, headers map[string]string) int {
		req := httptest.NewRequest(method, target, nil)
		req.Host = "rockabillyroasting.com"
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	t.Run("cross-site POST from a browser is refused", func(t *testing.T) {
		assert.NotEqual(t, http.StatusOK,
			call("POST", "/admin/settings", map[string]string{"Sec-Fetch-Site": "cross-site"}))
		assert.NotEqual(t, http.StatusOK,
			call("POST", "/admin/settings", map[string]string{"Origin": "https://evil.example"}))
	})

	t.Run("same-origin POST is allowed", func(t *testing.T) {
		assert.Equal(t, http.StatusOK,
			call("POST", "/admin/settings", map[string]string{"Sec-Fetch-Site": "same-origin"}))
		assert.Equal(t, http.StatusOK,
			call("POST", "/admin/settings", map[string]string{"Origin": "https://rockabillyroasting.com"}))
	})

	t.Run("webhooks with no browser headers are allowed", func(t *testing.T) {
		// Stripe, Shippo and Intuit post server-to-server: no Origin, no
		// Sec-Fetch-Site. Such a request cannot have been made by a browser on
		// another site's behalf, so it is not a CSRF vector — and refusing it
		// would break payment, label and invoice processing.
		for _, path := range []string{"/webhooks/stripe", "/webhooks/quickbooks", "/webhooks/shippo"} {
			assert.Equal(t, http.StatusOK, call("POST", path, nil), path)
		}
	})

	t.Run("safe methods are never checked", func(t *testing.T) {
		// The QuickBooks OAuth callback is a cross-site GET by design — it is
		// the return from Intuit's consent screen. It carries its own signed
		// state cookie; this middleware must not stand in its way.
		assert.Equal(t, http.StatusOK,
			call("GET", "/admin/settings/integrations/quickbooks/callback?code=x&realmId=1",
				map[string]string{"Sec-Fetch-Site": "cross-site"}))
	})
}
