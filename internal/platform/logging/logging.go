package logging

import (
	"context"
	"log/slog"
	"os"
)

type contextKey string

const loggerKey contextKey = "logger"

// Standard log field names.
const (
	FieldRequestID   = "request_id"
	FieldActorID     = "actor_id"
	FieldActorType   = "actor_type"
	FieldMethod      = "method"
	FieldPath        = "path"
	FieldQuery       = "query"
	FieldStatus      = "status"
	FieldDurationMS  = "duration_ms"
	FieldRemoteIP    = "remote_ip"
	FieldUserAgent   = "user_agent"
	FieldReferer     = "referer"
	FieldService     = "service"
	FieldEnv         = "env"
	FieldEvent       = "event"
	FieldResourceType = "resource_type"
	FieldResourceID  = "resource_id"
	FieldOrderID     = "order_id"
	FieldCustomerID  = "customer_id"
	FieldAmount      = "amount"
	FieldCurrency    = "currency"
)

// New creates a new JSON logger for production use.
func New(level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	}))
}

// NewWithHandlers returns a logger that fans each record out to every handler.
// Use to compose the JSON stdout handler with extra sinks (e.g. Sentry).
func NewWithHandlers(handlers ...slog.Handler) *slog.Logger {
	return slog.New(Fanout(handlers...))
}

// Fanout returns a slog.Handler that dispatches to all of the given handlers.
// Handler errors are ignored so one sink going down cannot starve another.
func Fanout(handlers ...slog.Handler) slog.Handler {
	return &fanoutHandler{handlers: handlers}
}

type fanoutHandler struct {
	handlers []slog.Handler
}

func (f *fanoutHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range f.handlers {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (f *fanoutHandler) Handle(ctx context.Context, r slog.Record) error {
	for _, h := range f.handlers {
		if h.Enabled(ctx, r.Level) {
			_ = h.Handle(ctx, r.Clone())
		}
	}
	return nil
}

func (f *fanoutHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make([]slog.Handler, len(f.handlers))
	for i, h := range f.handlers {
		out[i] = h.WithAttrs(attrs)
	}
	return &fanoutHandler{handlers: out}
}

func (f *fanoutHandler) WithGroup(name string) slog.Handler {
	out := make([]slog.Handler, len(f.handlers))
	for i, h := range f.handlers {
		out[i] = h.WithGroup(name)
	}
	return &fanoutHandler{handlers: out}
}

// WithContext stores a logger in the context.
func WithContext(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey, logger)
}

// FromContext retrieves the logger from the context, or returns the default logger.
func FromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerKey).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}
