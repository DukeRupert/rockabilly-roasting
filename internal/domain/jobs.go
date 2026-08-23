package domain

import "time"

// A dead job is a background job River gave up on: it burned through every
// attempt and moved to the "discarded" state, where nothing will ever pick it
// up again. Every customer email, shipping label, invoice, and subscription
// renewal in this system runs through a job, so a discarded job is a piece of
// work that silently did not happen.
//
// Its symptom is an absence: a broken renewal worker makes the store look
// *quieter* — fewer orders, shorter queues — which reads as a slow day rather
// than an outage. That makes it an engineering alert rather than shop work, so
// it reaches people through Sentry (jobs.ErrorHandler) and the admin-only
// /admin/jobs page, not through the staff dashboard.

// DeadJobKindBuyLabel is excluded from job-health counts because failed label
// purchases have their own, more actionable dashboard group that names the
// affected orders. Counting them twice would inflate the urgent badge and
// train staff to distrust it.
const DeadJobKindBuyLabel = "buy_label"

// DeadJob is one background job that exhausted its retries.
type DeadJob struct {
	// ID is River's own int64 job id, not a UUID — it is the handle every
	// River client call takes.
	ID          int64
	Kind        string // snake_case job kind, e.g. "email_order_confirm"
	Queue       string
	Attempt     int    // attempts actually made
	MaxAttempts int    // the ceiling it hit
	LastError   string // message from the final attempt; empty if River recorded none
	Args        string // raw JSON args, for identifying which record was affected
	CreatedAt   time.Time
	FinalizedAt time.Time // when River gave up
}

// DeadJobKindCount is a per-kind rollup. Dead jobs cluster hard by cause — one
// expired API token discards every job of a kind at once — so the kind
// breakdown is usually the whole diagnosis.
type DeadJobKindCount struct {
	Kind   string
	Count  int
	Oldest time.Time
	Newest time.Time
}
