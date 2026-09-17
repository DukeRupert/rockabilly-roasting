package web

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	sentrygo "github.com/getsentry/sentry-go"
	"github.com/google/uuid"

	"github.com/dukerupert/hiri/internal/platform/logging"
	"github.com/dukerupert/hiri/internal/platform/ratelimit"
)

// truncate clips a string to n runes with a trailing ellipsis, so a pathological
// User-Agent or Referer can't blow up a log line.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// requestIDMiddleware injects a unique request ID into each request context.
func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := uuid.New().String()
		ctx := r.Context()
		logger := logging.FromContext(ctx).With(slog.String(logging.FieldRequestID, requestID))
		ctx = logging.WithContext(ctx, logger)
		w.Header().Set("X-Request-ID", requestID)

		// Tag the Sentry scope for this request. No-op if Sentry isn't initialized.
		if hub := sentrygo.GetHubFromContext(ctx); hub != nil {
			hub.Scope().SetTag("request_id", requestID)
			hub.Scope().SetTag("method", r.Method)
			hub.Scope().SetTag("path", r.URL.Path)
		}

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// errorRecorderKey carries the per-request error slot. It is a struct{} key so
// nothing outside this package can collide with it.
type errorRecorderKey struct{}

// errorRecorder holds the first server-side error raised while handling a
// request, so loggingMiddleware can put it on that request's single log line
// rather than leaving it to be correlated from a separate record.
//
// It also carries the status the error *maps* to, which is not always the
// status on the wire: an htmx request that hits a dead database is answered 200
// with an error toast, because a 500 body would be swapped into the page. That
// is the right response and the wrong thing to log as normal traffic, so the
// recorded status is what decides the log level.
type errorRecorder struct {
	mu     sync.Mutex
	err    error
	status int
}

func (e *errorRecorder) record(err error, status int) {
	if err == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.err == nil {
		e.err, e.status = err, status
	}
}

func (e *errorRecorder) load() (error, int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.err, e.status
}

// recordRequestError attaches an error to the in-flight request so it lands on
// the request log line. Safe to call when no recorder is installed.
func recordRequestError(ctx context.Context, err error, status int) {
	if rec, ok := ctx.Value(errorRecorderKey{}).(*errorRecorder); ok {
		rec.record(err, status)
	}
}

// responseWriter wraps http.ResponseWriter to capture the status code.
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// loggingMiddleware emits exactly one line per request, per the fleet logging
// standard: msg "request" at INFO for served traffic, msg "request failed" at
// ERROR with an error field when the service itself failed.
//
// It runs inside requestIDMiddleware so the line inherits request_id — every
// other line emitted during the request carries the same id, which is the only
// way to gather them back together in Loki.
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		rec := &errorRecorder{}
		r = r.WithContext(context.WithValue(r.Context(), errorRecorderKey{}, rec))

		next.ServeHTTP(rw, r)

		duration := time.Since(start)
		logger := logging.FromContext(r.Context())

		// The Shippo webhook carries its auth secret in the URL path (Shippo
		// can't send headers or sign payloads), so redact path + query here to
		// keep the secret out of logs and any log aggregation.
		loggedPath, loggedQuery := r.URL.Path, truncate(r.URL.RawQuery, 500)
		if strings.HasPrefix(r.URL.Path, "/webhooks/shippo/") {
			loggedPath, loggedQuery = "/webhooks/shippo/[redacted]", ""
		}

		attrs := []any{
			slog.String(logging.FieldMethod, r.Method),
			slog.String(logging.FieldPath, loggedPath),
			slog.String(logging.FieldQuery, loggedQuery),
			slog.Int(logging.FieldStatus, rw.statusCode),
			// Sub-millisecond requests are common on a cached page; truncating
			// to whole milliseconds reported every one of them as 0.
			slog.Float64(logging.FieldDurationMS, float64(duration.Nanoseconds())/1e6),
			slog.String(logging.FieldRemoteIP, ratelimit.ClientIP(r)),
			slog.String(logging.FieldUserAgent, truncate(r.UserAgent(), 200)),
			slog.String(logging.FieldReferer, truncate(r.Referer(), 200)),
		}

		err, mappedStatus := rec.load()
		failed := rw.statusCode >= 500 || mappedStatus >= 500

		// The health check is polled continuously by the container healthcheck
		// and the uptime monitor. Logged at INFO it would be the single
		// highest-volume event in Loki and would say nothing. A *failing* one
		// says a great deal, though — and on an idle site it may be the only
		// request there is, so it must not be swallowed with the rest.
		if r.URL.Path == healthPath && !failed {
			logger.Debug("request", attrs...)
			return
		}

		if !failed {
			// 4xx is normal traffic, not a service failure.
			logger.Info("request", attrs...)
			return
		}
		if err != nil {
			attrs = append(attrs, slog.String(logging.FieldError, err.Error()))
		}
		logger.Error("request failed", attrs...)
	})
}
