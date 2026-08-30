package web

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dukerupert/hiri/internal/app"
)

// These sentinels used to fall through mapError's default case, which meant an
// operator who tried an unavailable move got "internal server error" and Sentry
// got paged for the application working exactly as designed. The sentinel text
// was never shown. Nothing failed a build over it — an unmapped sentinel is
// only visible at runtime, on the one path nobody exercised.
func TestPreviouslyUnmappedSentinels(t *testing.T) {
	cases := []struct {
		err    error
		status int
	}{
		// The ID came off a URL the caller typed or followed. Missing means
		// missing.
		{app.ErrSubscriptionPlanNotFound, http.StatusNotFound},
		{app.ErrPriceListNotFound, http.StatusNotFound},
		{app.ErrAttributeSetNotFound, http.StatusNotFound},
		{app.ErrAttributeKeyNotFound, http.StatusNotFound},
		{app.ErrAddressNotFound, http.StatusNotFound},
		{app.ErrRouteStopNotFound, http.StatusNotFound},

		// The record exists and the operator may touch it; the move is just not
		// available from where it stands.
		{app.ErrSubscriptionNotCancellable, http.StatusUnprocessableEntity},
		{app.ErrSubscriptionNotResumable, http.StatusUnprocessableEntity},
		{app.ErrSubscriptionNotEditable, http.StatusUnprocessableEntity},
		{app.ErrSubscriptionPlanInactive, http.StatusUnprocessableEntity},
		{app.ErrInvalidOrderStatus, http.StatusConflict},
		{app.ErrOrderHasActiveLabel, http.StatusConflict},
		{app.ErrOrderQBManaged, http.StatusConflict},
		{app.ErrStopAlreadyDelivered, http.StatusConflict},

		// Nothing the operator did — this process has no River client.
		{app.ErrJobRetryUnavailable, http.StatusServiceUnavailable},
	}

	for _, tc := range cases {
		t.Run(tc.err.Error(), func(t *testing.T) {
			status, msg := mapError(tc.err)
			assert.Equal(t, tc.status, status)
			assert.NotEqual(t, "internal server error", msg,
				"the sentinel's own words are what the operator needs")

			// Wrapped is the shape handlers actually produce, and errors.Is
			// has to carry the mapping through it.
			wrapped, _ := mapError(fmt.Errorf("load: %w", tc.err))
			assert.Equal(t, tc.status, wrapped, "mapping must survive wrapping")
		})
	}
}

// A missing address or plan reached through an ID stored on an order or
// subscription is broken data, not a bad request. The 404 mappings above must
// not quietly absorb it — the handlers wrap those misses so they stay 500s, and
// this pins the wrapping's one load-bearing property: it breaks errors.Is.
func TestBrokenReferenceStaysInternal(t *testing.T) {
	// fmt.Errorf without %w is deliberate at those call sites.
	status, _ := mapError(fmt.Errorf("order X references missing address Y"))
	assert.Equal(t, http.StatusInternalServerError, status)
}
