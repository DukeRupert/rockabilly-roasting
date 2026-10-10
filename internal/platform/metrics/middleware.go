package metrics

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// unmatchedRoute labels every request no mux matched — scanner probes, and
// requests rejected by middleware before routing (global rate limit, CSRF).
const unmatchedRoute = "unmatched"

// statusRecorder captures the HTTP status code written by the handler.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

type routeKey struct{}

// routeSlot carries the matched route pattern back out to HTTPMiddleware. It
// has to travel by pointer: middleware between here and the mux hands the next
// handler a copy of the request (r.WithContext), so the r.Pattern the mux
// writes is never visible on the request HTTPMiddleware holds.
type routeSlot struct{ pattern string }

// HTTPMiddleware returns middleware that instruments all HTTP requests with
// Prometheus metrics: request count, duration histogram, and in-flight gauge.
//
// The path_pattern label is the ServeMux pattern the request matched, as
// recorded by RecordRoute — never the raw URL path. Labelling by raw path gave
// every scanner probe (/wp-admin.php, /.env, …) its own permanent series; eight
// days of uptime grew /metrics to 84k series and 10 MB, until Prometheus scrapes
// started timing out.
func HTTPMiddleware(reg *Registry) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reg.HTTPRequestsInFlight.Inc()
			defer reg.HTTPRequestsInFlight.Dec()

			slot := &routeSlot{}
			r = r.WithContext(context.WithValue(r.Context(), routeKey{}, slot))
			rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			start := time.Now()

			next.ServeHTTP(rw, r)

			pattern := routeLabel(slot.pattern)
			status := strconv.Itoa(rw.status)
			duration := time.Since(start).Seconds()

			reg.HTTPRequestsTotal.WithLabelValues(r.Method, pattern, status).Inc()
			reg.HTTPRequestDuration.WithLabelValues(r.Method, pattern, status).Observe(duration)
		})
	}
}

// RecordRoute wraps a ServeMux so the pattern it matched becomes the request's
// path_pattern label. Wrap every mux, including ones mounted inside another: the
// innermost match is the most specific ("/admin/orders/{id}" rather than the
// "/admin/" that mounted it), and since the innermost mux returns first, it is
// the one that fills the slot.
func RecordRoute(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(w, r)
		// ServeMux.ServeHTTP sets r.Pattern on the request it was given.
		if slot, ok := r.Context().Value(routeKey{}).(*routeSlot); ok && slot.pattern == "" {
			slot.pattern = r.Pattern
		}
	})
}

// routeLabel turns a ServeMux pattern ("GET /catalog/{slug}") into the label
// value ("/catalog/{slug}"). The method is already its own label.
func routeLabel(pattern string) string {
	if pattern == "" {
		return unmatchedRoute
	}
	if _, path, ok := strings.Cut(pattern, " "); ok {
		return path
	}
	return pattern
}
