package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The shape of the real router: an inner mux mounted on an outer one, a home
// page, a GET catch-all 404, and between HTTPMiddleware and the outer mux,
// panic recovery and middleware that hands the next handler a copy of the
// request. That copy is why the label cannot be read off r.Pattern in
// HTTPMiddleware itself.
func newTestHandler(reg *Registry) http.Handler {
	inner := http.NewServeMux()
	inner.HandleFunc("GET /admin/orders/{id}", func(w http.ResponseWriter, r *http.Request) {})
	inner.HandleFunc("GET /admin/boom", func(w http.ResponseWriter, r *http.Request) { panic("boom") })

	copyRequest := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(r.Context()))
		})
	}
	recovery := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if recover() != nil {
					w.WriteHeader(http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}

	outer := http.NewServeMux()
	outer.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {})
	outer.HandleFunc("GET /catalog/{slug}", func(w http.ResponseWriter, r *http.Request) {})
	outer.HandleFunc("GET /boom", func(w http.ResponseWriter, r *http.Request) { panic("boom") })
	outer.Handle("GET /admin/", copyRequest(RecordRoute(inner)))
	outer.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) })

	return HTTPMiddleware(reg)(recovery(copyRequest(RecordRoute(outer))))
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
// A GET or HEAD probe matches the catch-all "GET /" (the 404 page), which the
// home page's "/{$}" does not share.
func TestHTTPMiddleware_ProbesShareTheCatchAllLabel(t *testing.T) {
	reg := NewRegistry()
	h := newTestHandler(reg)

	serve(t, h, "GET", "/")
	for _, p := range []string{"/wp-login.php", "/.env", "/wordpress/wp-content/x", "/fogrflbur1k9da2sz59i"} {
		serve(t, h, "GET", p)
	}

	require.Equal(t, 2, testutil.CollectAndCount(reg.HTTPRequestsTotal))
	assert.Equal(t, 1.0, testutil.ToFloat64(reg.HTTPRequestsTotal.WithLabelValues("GET", "/{$}", "200")))
	assert.Equal(t, 4.0, testutil.ToFloat64(reg.HTTPRequestsTotal.WithLabelValues("GET", "/", "404")))
}

// A method no route registers gets a 405 from the mux and no pattern.
func TestHTTPMiddleware_WrongMethodIsUnmatched(t *testing.T) {
	reg := NewRegistry()
	h := newTestHandler(reg)

	serve(t, h, "POST", "/wp-login.php")
	serve(t, h, "POST", "/.env")

	require.Equal(t, 1, testutil.CollectAndCount(reg.HTTPRequestsTotal))
	assert.Equal(t, 2.0, testutil.ToFloat64(reg.HTTPRequestsTotal.WithLabelValues("POST", "unmatched", "405")))
}

// Recovery sits between HTTPMiddleware and the mux, so a panic unwinds through
// RecordRoute. The route is still the one the mux matched.
func TestHTTPMiddleware_PanicKeepsItsRoute(t *testing.T) {
	reg := NewRegistry()
	h := newTestHandler(reg)

	serve(t, h, "GET", "/boom")
	serve(t, h, "GET", "/admin/boom")

	assert.Equal(t, 1.0, testutil.ToFloat64(reg.HTTPRequestsTotal.WithLabelValues("GET", "/boom", "500")))
	assert.Equal(t, 1.0, testutil.ToFloat64(reg.HTTPRequestsTotal.WithLabelValues("GET", "/admin/boom", "500")))
}

// net/http accepts any token as a method, and Caddy passes it through. Each
// made-up method must not mint a series of its own.
func TestHTTPMiddleware_UnknownMethodsShareOneLabel(t *testing.T) {
	reg := NewRegistry()
	h := newTestHandler(reg)

	for _, m := range []string{"PROPFIND", "XQZ1", "XQZ2"} {
		serve(t, h, m, "/")
	}

	require.Equal(t, 1, testutil.CollectAndCount(reg.HTTPRequestsTotal))
	assert.Equal(t, 3.0, testutil.ToFloat64(reg.HTTPRequestsTotal.WithLabelValues("other", "unmatched", "405")))
}

func TestRouteLabel(t *testing.T) {
	assert.Equal(t, "/catalog/{slug}", routeLabel("GET /catalog/{slug}"))
	assert.Equal(t, "/static/", routeLabel("/static/"))
	assert.Equal(t, "unmatched", routeLabel(""))
}

func TestMethodLabel(t *testing.T) {
	for _, m := range []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "CONNECT", "TRACE"} {
		assert.Equal(t, m, methodLabel(m))
	}
	assert.Equal(t, "other", methodLabel("get"), "methods are case-sensitive")
	assert.Equal(t, "other", methodLabel("PROPFIND"))
}
