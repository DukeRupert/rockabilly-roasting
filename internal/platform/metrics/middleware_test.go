package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The shape of the real router: an inner mux mounted on an outer one, behind
// middleware that hands the next handler a copy of the request. That copy is
// why the label cannot be read off r.Pattern in HTTPMiddleware itself.
func newTestHandler(reg *Registry) http.Handler {
	inner := http.NewServeMux()
	inner.HandleFunc("GET /admin/orders/{id}", func(w http.ResponseWriter, r *http.Request) {})

	copyRequest := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(r.Context()))
		})
	}

	outer := http.NewServeMux()
	outer.HandleFunc("GET /catalog/{slug}", func(w http.ResponseWriter, r *http.Request) {})
	outer.Handle("GET /admin/", copyRequest(RecordRoute(inner)))

	return HTTPMiddleware(reg)(copyRequest(RecordRoute(outer)))
}

func serve(t *testing.T, h http.Handler, method, path string) {
	t.Helper()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(method, path, nil))
}

func TestHTTPMiddleware_LabelsByMatchedRoute(t *testing.T) {
	reg := NewRegistry()
	h := newTestHandler(reg)

	serve(t, h, "GET", "/catalog/2-stroke")
	serve(t, h, "GET", "/catalog/chop-top")
	serve(t, h, "GET", "/admin/orders/7b2756c7-95c8-4e37-8643-1b7f622fdeed")

	assert.Equal(t, 2.0, testutil.ToFloat64(reg.HTTPRequestsTotal.WithLabelValues("GET", "/catalog/{slug}", "200")))
	// The inner mux's pattern wins over the "/admin/" that mounted it.
	assert.Equal(t, 1.0, testutil.ToFloat64(reg.HTTPRequestsTotal.WithLabelValues("GET", "/admin/orders/{id}", "200")))
}

// Scanner probes must not mint a series per URL — that grew /metrics to 10 MB.
func TestHTTPMiddleware_UnmatchedPathsShareOneLabel(t *testing.T) {
	reg := NewRegistry()
	h := newTestHandler(reg)

	for _, p := range []string{"/wp-login.php", "/.env", "/wordpress/wp-content/x", "/fogrflbur1k9da2sz59i"} {
		serve(t, h, "GET", p)
	}

	require.Equal(t, 1, testutil.CollectAndCount(reg.HTTPRequestsTotal))
	assert.Equal(t, 4.0, testutil.ToFloat64(reg.HTTPRequestsTotal.WithLabelValues("GET", "unmatched", "404")))
}

func TestRouteLabel(t *testing.T) {
	assert.Equal(t, "/catalog/{slug}", routeLabel("GET /catalog/{slug}"))
	assert.Equal(t, "/static/", routeLabel("/static/"))
	assert.Equal(t, "unmatched", routeLabel(""))
}
