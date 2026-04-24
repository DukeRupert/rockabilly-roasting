// Package sentry wires the Sentry SDK into Hiri.
//
// Init is a no-op when DSN is empty, so local development stays quiet.
// The slog handler forwards Error-level records as Sentry events and
// lower levels as breadcrumbs, so services only need to log to get reports.
package sentry

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	sentrygo "github.com/getsentry/sentry-go"
)

// Config configures Sentry initialization.
type Config struct {
	DSN         string
	Environment string
	Release     string
	// TracesSampleRate in [0,1]; 0 disables performance monitoring.
	TracesSampleRate float64
}

// Init initializes the Sentry SDK. Returns true if Sentry was enabled.
// Safe to call with an empty DSN (no-op).
func Init(cfg Config) (bool, error) {
	if cfg.DSN == "" {
		return false, nil
	}
	err := sentrygo.Init(sentrygo.ClientOptions{
		Dsn:              cfg.DSN,
		Environment:      cfg.Environment,
		Release:          cfg.Release,
		TracesSampleRate: cfg.TracesSampleRate,
		EnableTracing:    cfg.TracesSampleRate > 0,
	})
	if err != nil {
		return false, fmt.Errorf("sentry init: %w", err)
	}
	return true, nil
}

// Flush blocks up to timeout waiting for buffered events to be sent.
// Call from main's shutdown path.
func Flush(timeout time.Duration) {
	sentrygo.Flush(timeout)
}

// SlogHandler is a slog.Handler that forwards records to Sentry.
//
//   - Error level → Sentry event (CaptureMessage with Error severity)
//   - Warn level  → breadcrumb at Warning severity
//   - Info/Debug  → breadcrumb at Info/Debug severity
//
// It only fans records to Sentry; it does NOT write to stdout. Compose with
// the JSON handler via logging.Fanout.
type SlogHandler struct {
	level slog.Level
	attrs []slog.Attr
	group string
}

// NewSlogHandler returns a handler that forwards records at >= minLevel.
func NewSlogHandler(minLevel slog.Level) *SlogHandler {
	return &SlogHandler{level: minLevel}
}

func (h *SlogHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

func (h *SlogHandler) Handle(ctx context.Context, r slog.Record) error {
	hub := sentrygo.GetHubFromContext(ctx)
	if hub == nil {
		hub = sentrygo.CurrentHub()
	}

	// Collect attributes into a map, applying WithAttrs + WithGroup.
	data := map[string]interface{}{}
	for _, a := range h.attrs {
		addAttr(data, h.group, a)
	}
	r.Attrs(func(a slog.Attr) bool {
		addAttr(data, h.group, a)
		return true
	})

	switch {
	case r.Level >= slog.LevelError:
		hub.WithScope(func(scope *sentrygo.Scope) {
			if len(data) > 0 {
				scope.SetContext("log", sentrygo.Context(data))
			}
			scope.SetLevel(sentrygo.LevelError)
			hub.CaptureMessage(r.Message)
		})
	case r.Level >= slog.LevelWarn:
		hub.AddBreadcrumb(&sentrygo.Breadcrumb{
			Message:   r.Message,
			Level:     sentrygo.LevelWarning,
			Data:      data,
			Timestamp: r.Time,
		}, nil)
	default:
		sentryLevel := sentrygo.LevelInfo
		if r.Level < slog.LevelInfo {
			sentryLevel = sentrygo.LevelDebug
		}
		hub.AddBreadcrumb(&sentrygo.Breadcrumb{
			Message:   r.Message,
			Level:     sentryLevel,
			Data:      data,
			Timestamp: r.Time,
		}, nil)
	}
	return nil
}

func (h *SlogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clone := *h
	clone.attrs = append(append([]slog.Attr{}, h.attrs...), attrs...)
	return &clone
}

func (h *SlogHandler) WithGroup(name string) slog.Handler {
	clone := *h
	if h.group == "" {
		clone.group = name
	} else {
		clone.group = h.group + "." + name
	}
	return &clone
}

func addAttr(dst map[string]interface{}, group string, a slog.Attr) {
	key := a.Key
	if group != "" {
		key = group + "." + key
	}
	dst[key] = a.Value.Any()
}
