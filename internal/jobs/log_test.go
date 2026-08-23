package jobs

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The level is the alert switch: platform/sentry raises an Error record as a
// Sentry event and files anything below it as a breadcrumb. A worker about to
// cancel is the only report that will ever exist for that job, because River
// skips the ErrorHandler for a cancel. A worker about to be retried is not:
// the handler pages on the final attempt, and shouting on attempt 1 of 5 is
// how nine workers between them turned a recovered timeout into an incident.
func TestLogWorkerFailure_LevelFollowsWhatHappensNext(t *testing.T) {
	var buf bytes.Buffer
	restore := swapDefaultLogger(t, &buf)
	defer restore()

	logWorkerFailure(context.Background(), "qb_sync_customer", false, "job_id", int64(7))
	rec := lastRecord(t, &buf)
	assert.Equal(t, "WARN", rec["level"])
	assert.Contains(t, rec["msg"], "qb_sync_customer")
	assert.Contains(t, rec["msg"], "will retry")

	logWorkerFailure(context.Background(), "qb_sync_customer", true, "job_id", int64(7))
	rec = lastRecord(t, &buf)
	assert.Equal(t, "ERROR", rec["level"])
	assert.Contains(t, rec["msg"], "qb_sync_customer")
	assert.Contains(t, rec["msg"], "permanently")

	require.Equal(t, int64(7), int64(rec["job_id"].(float64)))
}
