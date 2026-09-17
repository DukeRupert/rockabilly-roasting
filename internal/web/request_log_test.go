package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureRequestLog runs one request through the request-ID and request-logging
// middleware and returns the single JSON line they produced.
func captureRequestLog(t *testing.T, r *http.Request, h http.HandlerFunc) map[string]any {
	t.Helper()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	prev := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(prev) })

	handler := requestIDMiddleware(loggingMiddleware(h))
	handler.ServeHTTP(httptest.NewRecorder(), r)

	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	require.Len(t, lines, 1, "a request must produce exactly one log line")

	var out map[string]any
	require.NoError(t, json.Unmarshal(lines[0], &out), "log line must be valid JSON")
	return out
}

// The fleet logging standard fixes the shape of this line: Loki queries and
// alerts are written against these exact field names and types.
func TestRequestLog_Envelope(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/products?page=2", nil)
	r.Header.Set("User-Agent", "Mozilla/5.0")
	r.Header.Set("Referer", "https://example.com/")

	got := captureRequestLog(t, r, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	assert.Equal(t, "INFO", got["level"], "level must be uppercase — Loki promotes it to a label and mixed casing splits it")
	assert.Equal(t, "request", got["msg"])
	assert.Equal(t, "GET", got["method"])
	assert.Equal(t, "/products", got["path"], "path must not carry the query string")
	assert.Equal(t, "page=2", got["query"])
	assert.Equal(t, "Mozilla/5.0", got["user_agent"])
	assert.Equal(t, "https://example.com/", got["referer"])
	assert.NotEmpty(t, got["request_id"], "every request line must carry request_id")

	_, err := time.Parse(time.RFC3339Nano, got["time"].(string))
	assert.NoError(t, err, "time must be RFC3339 with nanoseconds")

	// encoding/json decodes every number as float64; the assertion that matters
	// is that status serialises as a bare number, not a quoted string.
	assert.IsType(t, float64(0), got["status"])
	assert.Equal(t, float64(200), got["status"])
	assert.IsType(t, float64(0), got["duration_ms"])
}

// Sub-millisecond requests are the common case on a warm page. Rounding the
// duration to whole milliseconds reported every one of them as 0.
func TestRequestLog_DurationIsFractional(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	got := captureRequestLog(t, r, func(w http.ResponseWriter, r *http.Request) {})

	// Truncating to whole milliseconds reported this handler — which returns in
	// microseconds — as exactly 0.
	assert.Greater(t, got["duration_ms"], float64(0), "a sub-millisecond request must not report 0ms")
}

// 4xx is normal traffic. Only a service failure gets the failure event name.
func TestRequestLog_ClientErrorStaysInfo(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/nope", nil)
	got := captureRequestLog(t, r, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	assert.Equal(t, "INFO", got["level"])
	assert.Equal(t, "request", got["msg"])
	assert.Equal(t, float64(404), got["status"])
}

func TestRequestLog_ServerErrorCarriesError(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/api/subscribe", nil)
	got := captureRequestLog(t, r, func(w http.ResponseWriter, r *http.Request) {
		recordRequestError(r.Context(), errors.New("insert customer: connection refused"), http.StatusInternalServerError)
		w.WriteHeader(http.StatusInternalServerError)
	})

	assert.Equal(t, "ERROR", got["level"])
	assert.Equal(t, "request failed", got["msg"], "failure is its own event name, not a status to regex for")
	assert.Equal(t, "insert customer: connection refused", got["error"])
	assert.NotEmpty(t, got["request_id"])
}

// An htmx request that hits a server error is answered 200 with an error toast,
// because a 500 body would be swapped into the page. That is the right response
// and the wrong thing to log as normal traffic.
func TestRequestLog_HTMXErrorLogsAsFailure(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/admin/orders", nil)
	got := captureRequestLog(t, r, func(w http.ResponseWriter, r *http.Request) {
		recordRequestError(r.Context(), errors.New("query orders: connection refused"), http.StatusInternalServerError)
		w.WriteHeader(http.StatusOK)
	})

	assert.Equal(t, "ERROR", got["level"])
	assert.Equal(t, "request failed", got["msg"])
	assert.Equal(t, float64(200), got["status"], "status is what went on the wire")
	assert.Equal(t, "query orders: connection refused", got["error"])
}

// The Shippo webhook's auth secret rides in the URL path.
func TestRequestLog_RedactsShippoWebhookSecret(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/webhooks/shippo/s3cr3t?token=s3cr3t", nil)
	got := captureRequestLog(t, r, func(w http.ResponseWriter, r *http.Request) {})

	assert.Equal(t, "/webhooks/shippo/[redacted]", got["path"])
	assert.Equal(t, "", got["query"])
	assert.NotContains(t, got["path"], "s3cr3t")
}

// The health check is polled every few seconds by the container healthcheck and
// the uptime monitor; at INFO it would be the highest-volume event in Loki.
func TestRequestLog_HealthCheckIsDebug(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, healthPath, nil)
	got := captureRequestLog(t, r, func(w http.ResponseWriter, r *http.Request) {})
	assert.Equal(t, "DEBUG", got["level"])
}
