package metrics

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var uuidRegex = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// statusRecorder captures the HTTP status code written by the handler.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// HTTPMiddleware returns middleware that instruments all HTTP requests with
// Prometheus metrics: request count, duration histogram, and in-flight gauge.
//
// Path patterns are normalized to replace UUIDs with {id} to prevent
// cardinality explosion. Static asset paths are collapsed to /static/{file}.
func HTTPMiddleware(reg *Registry) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reg.HTTPRequestsInFlight.Inc()
			defer reg.HTTPRequestsInFlight.Dec()

			rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			start := time.Now()

			next.ServeHTTP(rw, r)

			pattern := normalizePathPattern(r.URL.Path)
			status := strconv.Itoa(rw.status)
			duration := time.Since(start).Seconds()

			reg.HTTPRequestsTotal.WithLabelValues(r.Method, pattern, status).Inc()
			reg.HTTPRequestDuration.WithLabelValues(r.Method, pattern, status).Observe(duration)
		})
	}
}

// normalizePathPattern replaces UUID path segments with {id} and collapses
// static asset paths to prevent label cardinality explosion.
func normalizePathPattern(path string) string {
	// Collapse all static asset paths.
	if strings.HasPrefix(path, "/static/") {
		return "/static/{file}"
	}

	parts := strings.Split(path, "/")
	for i, part := range parts {
		if uuidRegex.MatchString(part) {
			parts[i] = "{id}"
		}
	}
	return strings.Join(parts, "/")
}
