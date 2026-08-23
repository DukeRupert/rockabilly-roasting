package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureHandler returns a handler writing JSON records to buf.
func captureLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func lastRecord(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	require.NotEmpty(t, lines)
	var rec map[string]any
	require.NoError(t, json.Unmarshal(lines[len(lines)-1], &rec))
	return rec
}

// The log level is the alert switch: platform/sentry escalates Error records to
// Sentry events and files everything below as a breadcrumb. A job that still
// has retries left must not page anyone — most of them succeed on the next
// attempt — but the one that burns its last attempt must.
func TestErrorHandler_OnlyFinalAttemptAlerts(t *testing.T) {
	var buf bytes.Buffer
	h := NewErrorHandler(captureLogger(&buf))
	job := &rivertype.JobRow{ID: 7, Kind: "email_order_confirm", Queue: "default", MaxAttempts: 3}

	job.Attempt = 1
	require.Nil(t, h.HandleError(context.Background(), job, errors.New("postmark timeout")))
	rec := lastRecord(t, &buf)
	assert.Equal(t, "WARN", rec["level"])

	job.Attempt = 3
	require.Nil(t, h.HandleError(context.Background(), job, errors.New("postmark timeout")))
	rec = lastRecord(t, &buf)
	assert.Equal(t, "ERROR", rec["level"])
	assert.Equal(t, "email_order_confirm", rec["kind"])
	assert.Equal(t, "postmark timeout", rec["error"])
}

// A panic is a bug in the worker rather than a flaky dependency, so it reports
// on the first attempt without waiting for the retries to drain.
func TestErrorHandler_PanicAlertsOnFirstAttempt(t *testing.T) {
	var buf bytes.Buffer
	h := NewErrorHandler(captureLogger(&buf))
	job := &rivertype.JobRow{ID: 9, Kind: "qb_create_invoice", Queue: "default", Attempt: 1, MaxAttempts: 5}

	require.Nil(t, h.HandlePanic(context.Background(), job, "nil map write", "goroutine 1 [running]"))
	rec := lastRecord(t, &buf)
	assert.Equal(t, "ERROR", rec["level"])
	assert.Equal(t, "nil map write", rec["panic"])
	assert.Equal(t, "goroutine 1 [running]", rec["trace"])
}
