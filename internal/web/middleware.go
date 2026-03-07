package web

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/dukerupert/hiri/internal/platform/logging"
	"github.com/dukerupert/hiri/internal/platform/metrics"
)

// requestIDMiddleware injects a unique request ID into each request context.
func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := uuid.New().String()
		ctx := r.Context()
		logger := logging.FromContext(ctx).With(slog.String(logging.FieldRequestID, requestID))
		ctx = logging.WithContext(ctx, logger)
		w.Header().Set("X-Request-ID", requestID)
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

		if rw.statusCode >= 500 {
			ctxLogger.Error("request failed",
				slog.String(logging.FieldMethod, r.Method),
				slog.String(logging.FieldPath, r.URL.Path),
				slog.Int(logging.FieldStatus, rw.statusCode),
				slog.Float64(logging.FieldDurationMS, float64(duration.Milliseconds())),
			)
		} else {
			logger.Info("request",
				slog.String(logging.FieldMethod, r.Method),
				slog.String(logging.FieldPath, r.URL.Path),
				slog.Int(logging.FieldStatus, rw.statusCode),
				slog.Float64(logging.FieldDurationMS, float64(duration.Milliseconds())),
			)
		}
	})
}
