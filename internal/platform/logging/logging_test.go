package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// slog writes wall-clock time with the host's offset. The fleet logging
// standard calls for UTC so lines from hosts in different zones sort and
// correlate, and HandlerOptions is the single place that guarantees it —
// every sink in the service shares it.
func TestHandlerOptions_ForcesUTC(t *testing.T) {
	// A zone that is neither UTC nor the host's, so a pass cannot be an accident
	// of where the test runs.
	kathmandu := time.FixedZone("Asia/Kathmandu", 5*3600+45*60)

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, HandlerOptions(slog.LevelInfo)))
	rec := slog.NewRecord(time.Date(2026, 9, 17, 5, 42, 46, 533565587, kathmandu), slog.LevelInfo, "server starting", 0)
	require.NoError(t, logger.Handler().Handle(t.Context(), rec))

	var out map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &out))

	ts, ok := out["time"].(string)
	require.True(t, ok, "time must be a string")
	assert.True(t, len(ts) > 0 && ts[len(ts)-1] == 'Z', "time must be UTC, got %q", ts)

	parsed, err := time.Parse(time.RFC3339Nano, ts)
	require.NoError(t, err, "time must be RFC3339 with nanoseconds")
	assert.Equal(t, 533565587, parsed.Nanosecond(), "nanosecond precision must survive")
	assert.True(t, parsed.Equal(rec.Time), "the instant must be unchanged, only its zone")
}

// level is promoted to a Loki label; mixed casing silently splits it, so
// {level="ERROR"} would miss lines logged as "error".
func TestHandlerOptions_LevelIsUppercase(t *testing.T) {
	for _, level := range []slog.Level{slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError} {
		var buf bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&buf, HandlerOptions(slog.LevelDebug)))
		logger.Log(t.Context(), level, "event")

		var out map[string]any
		require.NoError(t, json.Unmarshal(buf.Bytes(), &out))
		got := out["level"].(string)
		assert.Equal(t, got, string(bytes.ToUpper([]byte(got))), "level must be uppercase")
	}
}

// Nothing else in the envelope may be rewritten by the UTC hook — a field a
// caller names "time" inside a group, in particular, is theirs.
func TestHandlerOptions_LeavesAttrsAlone(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, HandlerOptions(slog.LevelInfo)))
	logger.Info("cache warmed", "entries", 412, "duration_ms", 83.2)

	var out map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &out))
	assert.Equal(t, "cache warmed", out["msg"])
	assert.Equal(t, float64(412), out["entries"])
	assert.Equal(t, 83.2, out["duration_ms"])
}
