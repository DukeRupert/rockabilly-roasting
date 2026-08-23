package jobs

import (
	"context"
	"log/slog"
)

// logWorkerFailure records a worker's own view of a failure, at the level that
// matches what happens to the job next.
//
// The level is the alert switch — platform/sentry turns an Error record into a
// Sentry event and files everything below it as a breadcrumb — so it has to
// answer one question: is anyone going to hear about this if I stay quiet?
//
//   - terminal: Error. The worker is about to cancel, and River does not run
//     jobs.ErrorHandler for a cancelled job, so this log is the only report
//     that will ever exist for it.
//   - retryable: Warn. The job goes back on the queue, and jobs.ErrorHandler
//     pages on the final attempt with the same facts.
//
// Every worker used to log Error unconditionally here, which meant attempt 1 of
// 5 against a QuickBooks timeout raised a Sentry event for work that succeeded
// thirty seconds later — and once each worker's message named its own kind,
// that became nine noisy issues instead of one. A failure that the system is
// about to retry is context, not news.
//
// Terminal-but-*expected* cancels are not this function's business: a declined
// card is dunning working, and the renewal workers log those at Warn at their
// own call sites, where the reasoning about what is expected lives.
func logWorkerFailure(ctx context.Context, kind string, terminal bool, attrs ...any) {
	if terminal {
		slog.ErrorContext(ctx, "background job "+kind+" failed permanently", attrs...)
		return
	}
	slog.WarnContext(ctx, "background job "+kind+" failed, will retry", attrs...)
}
