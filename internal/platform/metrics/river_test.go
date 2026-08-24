package metrics

import (
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
)

// A deliberate cancel is neither outcome TrackJob can record. Counting it as a
// failure put a declined card on the same graph line as a broken worker, and
// counting it as a completion would claim work happened that did not.
func TestTrackJobCancelled_IsItsOwnOutcome(t *testing.T) {
	reg := NewRegistry()
	start := time.Now()

	TrackJobCancelled(reg, "subscription_renewal", start)

	assert.Equal(t, 1.0, testutil.ToFloat64(reg.RiverJobsCancelled.WithLabelValues("subscription_renewal")))
	assert.Equal(t, 0.0, testutil.ToFloat64(reg.RiverJobsFailed.WithLabelValues("subscription_renewal")))
	assert.Equal(t, 0.0, testutil.ToFloat64(reg.RiverJobsCompleted.WithLabelValues("subscription_renewal")))

	// A genuine failure still lands where it always did, so the existing
	// dashboard query keeps meaning what it meant.
	TrackJob(reg, "subscription_renewal", start, errors.New("boom"))
	assert.Equal(t, 1.0, testutil.ToFloat64(reg.RiverJobsFailed.WithLabelValues("subscription_renewal")))
	assert.Equal(t, 1.0, testutil.ToFloat64(reg.RiverJobsCancelled.WithLabelValues("subscription_renewal")))
}

// A nil registry is the no-metrics configuration; helpers must tolerate it.
func TestTrackJobCancelled_NilRegistry(t *testing.T) {
	assert.NotPanics(t, func() { TrackJobCancelled(nil, "batch_renewal", time.Now()) })
}
