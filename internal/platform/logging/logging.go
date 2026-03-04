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
	FieldStatus      = "status"
	FieldDurationMS  = "duration_ms"
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
