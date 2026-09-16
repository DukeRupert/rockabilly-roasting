package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Authenticated pages must not be cached. This had no guard at all, and the
// mutation that proved it mattered was not deleting the header but inverting
// it: changing the value to "public, max-age=600" marked admin pages and
// customer order history publicly cacheable for ten minutes, and the whole
// suite stayed green.
//
// Both middlewares set the header before the cookie check returns, so the
// unauthenticated redirect carries it too — which is deliberate, a login
// redirect is not worth caching either. That is what makes this testable
// without standing up a session.
func TestAuthenticatedResponsesAreNotCacheable(t *testing.T) {
	d := &Deps{}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	for _, tc := range []struct {
		name    string
		handler http.Handler
		target  string
	}{
		{"staff", d.requireStaffSession(next), "/admin/settings"},
		{"customer", d.requireCustomerSession(next), "/account/orders"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tc.target, nil)
			rec := httptest.NewRecorder()
			tc.handler.ServeHTTP(rec, req)

			cc := rec.Header().Get("Cache-Control")
			assert.Contains(t, cc, "no-store", "order history and shop settings must never be stored by a cache")
			assert.Contains(t, cc, "no-cache")
			assert.NotContains(t, cc, "public", "the inverse of the requirement")
			assert.NotContains(t, cc, "max-age=", "any positive lifetime defeats the point")
		})
	}
}
