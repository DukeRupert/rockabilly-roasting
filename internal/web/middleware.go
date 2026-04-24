package web

import (
	"log/slog"
	"net/http"
	"time"

	sentrygo "github.com/getsentry/sentry-go"
	"github.com/google/uuid"

	"github.com/dukerupert/hiri/internal/platform/logging"
	"github.com/dukerupert/hiri/internal/platform/metrics"
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

// responseWriter wraps http.ResponseWriter to capture the status code.
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// loggingMiddleware logs each request with duration and status.
// 5xx responses get logged at Error level with full context for diagnostics.
func loggingMiddleware(next http.Handler, logger *slog.Logger, _ *metrics.Registry) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(rw, r)

		duration := time.Since(start)
		ctxLogger := logging.FromContext(r.Context())

		attrs := []any{
			slog.String(logging.FieldMethod, r.Method),
			slog.String(logging.FieldPath, r.URL.Path),
			slog.String(logging.FieldQuery, truncate(r.URL.RawQuery, 500)),
			slog.Int(logging.FieldStatus, rw.statusCode),
			slog.Float64(logging.FieldDurationMS, float64(duration.Milliseconds())),
			slog.String(logging.FieldRemoteIP, ratelimit.ClientIP(r)),
			slog.String(logging.FieldUserAgent, truncate(r.UserAgent(), 200)),
			slog.String(logging.FieldReferer, truncate(r.Referer(), 200)),
		}

		if rw.statusCode >= 500 {
			ctxLogger.Error("request failed", attrs...)
		} else {
			logger.Info("request", attrs...)
		}
	})
}
