package web

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
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

// ErrInvalidOrderStatus is the one sentinel whose useful sentence lives in the
// wrap rather than the sentinel. Producers name the blocker there by
// convention, and both the batch UI and mapError read it back out.
func TestInvalidOrderStatusKeepsTheProducersPhrase(t *testing.T) {
	wrapped := fmt.Errorf("order is not ready for pickup: %w", app.ErrInvalidOrderStatus)

	status, msg := mapError(wrapped)
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "order is not ready for pickup", msg,
		"the prefix is the only place the actual blocker is named")

	// Bare, or wrapped by something that is not a phrase, falls back to copy
	// that at least tells the operator what to do next.
	_, bare := mapError(app.ErrInvalidOrderStatus)
	assert.Contains(t, bare, "reload the page")
}

// brokenReference exists to defeat errors.Is, and this is the test that says
// so. A stored ID that no longer resolves is broken data: it has to stay a 500
// and page, not become the 404 that ErrAddressNotFound and
// ErrSubscriptionPlanNotFound now map to.
//
// Switching brokenReference to %w — the reflex when wrapping an error — would
// pass a build, a vet and every other test in this package. It would fail here.
func TestBrokenReferenceDoesNotCarryTheSentinel(t *testing.T) {
	order, address := uuid.New(), uuid.New()

	err := brokenReference("order", order, "address", address)

	assert.False(t, errors.Is(err, app.ErrAddressNotFound),
		"brokenReference must not wrap the sentinel with %%w — errors.Is reaching it would turn broken data into a 404")

	status, _ := mapError(err)
	assert.Equal(t, http.StatusInternalServerError, status,
		"a dangling reference has to page, not 404")

	// Both IDs belong in the message: without them the Sentry event names no
	// row to go look at.
	assert.Contains(t, err.Error(), order.String())
	assert.Contains(t, err.Error(), address.String())
}

// The counterpart: the same sentinel, reached the ordinary way, still maps to
// 404. Together these pin both halves of the split — the URL-supplied ID gets
// its 404, the stored one does not.
func TestSentinelStillMapsWhenWrappedNormally(t *testing.T) {
	status, _ := mapError(fmt.Errorf("load address: %w", app.ErrAddressNotFound))
	assert.Equal(t, http.StatusNotFound, status)
}
