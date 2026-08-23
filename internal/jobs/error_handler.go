package jobs

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// ErrorHandler reports job failures to the logger, which fans Error-level
// records out to Sentry (see platform/sentry).
//
// Failures are alerts, not staff work. A discarded email or invoice job is an
// engineering fault — nobody in the shop can retry a QuickBooks sync or fix a
// panicking worker — so it belongs in the on-call feed, not in the admin's
// Urgent band alongside orders on hold. /admin/jobs stays available for whoever
// is diagnosing the alert.
//
// One class of failure never reaches this handler at all: River skips the
// error handler for a job that returns river.JobCancel, and logs the cancel at
// Debug (see jobexecutor's `if e.ErrorHandler != nil && !cancelJob`). This repo
// cancels for permanent failures in a dozen places — a QuickBooks call that
// will never succeed, a subscription that is no longer renewable — so those
// workers do their own reporting at the cancel site: logWorkerFailure for a
// fault, an explicit Warn for the terminal-but-expected cases whose reasoning
// lives at the call site. Do not assume a cancelled job is covered by this
// file.
//
// The other half of that split matters here: because this handler pages on the
// final attempt, a worker must NOT log Error for a failure it is about to
// return for retry. See logWorkerFailure.
//
// Only the *final* attempt pages anyone. River retries with backoff, and a
// single transient Stripe timeout that succeeds on attempt two is noise; the
// intermediate attempts land as Warn, which the Sentry handler files as a
// breadcrumb, so the failure history still rides along with the event that
// matters. Panics always report — a panicking worker is a bug on the first
// occurrence.
type ErrorHandler struct {
	logger *slog.Logger
}

// NewErrorHandler returns a River error handler that logs failures. A nil
// logger falls back to slog's default.
func NewErrorHandler(logger *slog.Logger) *ErrorHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &ErrorHandler{logger: logger}
}

// HandleError logs a failed attempt. It never changes River's retry behavior.
func (h *ErrorHandler) HandleError(ctx context.Context, job *rivertype.JobRow, err error) *river.ErrorHandlerResult {
	attrs := []any{
		"job_id", job.ID,
		"kind", job.Kind,
		"queue", job.Queue,
		"attempt", job.Attempt,
		"max_attempts", job.MaxAttempts,
		"error", err.Error(),
	}
	// The kind rides in the message, not only the attrs: the slog handler
	// captures the message as the Sentry title, so a constant string would
	// fingerprint every worker's failures into one issue — resolve it for a
	// fixed QuickBooks token and the next email worker's first failure lands
	// silently in the same closed thread.
	if job.Attempt >= job.MaxAttempts {
		h.logger.ErrorContext(ctx, fmt.Sprintf("background job %s discarded after final attempt", job.Kind), attrs...)
	} else {
		h.logger.WarnContext(ctx, fmt.Sprintf("background job %s failed, will retry", job.Kind), attrs...)
	}
	return nil
}

// HandlePanic logs a panicking worker. Unlike a returned error this always
// reports, on every attempt: a panic is a bug in the worker, not a flaky
// dependency, and the stack trace from the first occurrence is the useful one.
func (h *ErrorHandler) HandlePanic(ctx context.Context, job *rivertype.JobRow, panicVal any, trace string) *river.ErrorHandlerResult {
	h.logger.ErrorContext(ctx, fmt.Sprintf("background job %s panicked", job.Kind),
		"job_id", job.ID,
		"kind", job.Kind,
		"queue", job.Queue,
		"attempt", job.Attempt,
		"max_attempts", job.MaxAttempts,
		"panic", panicVal,
		"trace", trace,
	)
	return nil
}
