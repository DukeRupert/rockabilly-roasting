package web

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/platform/logging"
	"github.com/dukerupert/hiri/internal/platform/routing"
	"github.com/dukerupert/hiri/internal/ui/components/toast"
)

// IsHTMX returns true if the request was made by htmx.
func IsHTMX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

// JSON writes a JSON response with the given status code.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

// Error maps application errors to HTTP responses.
// For htmx requests, it renders an OOB toast so the current page stays intact.
// For non-htmx requests, it returns JSON as before.
func Error(w http.ResponseWriter, r *http.Request, err error) {
	status, msg := mapError(err)
	logger := logging.FromContext(r.Context())
	if status >= 500 {
		logger.Error("internal error", "error", err)
	}
	if IsHTMX(r) {
		// The toast is a single root element carrying hx-swap-oob, and htmx
		// lifts every top-level OOB element out of the response before the
		// normal swap runs. What is left is an empty fragment — so a form that
		// targets itself with hx-swap="outerHTML" would delete itself off the
		// page on any rejection, taking the operator's typed values with it.
		// HX-Reswap: none cancels only the main swap; OOB toasts still land.
		w.Header().Set("HX-Reswap", "none")
		w.WriteHeader(http.StatusOK)
		toast.Toast(toast.VariantError, msg).Render(r.Context(), w) //nolint:errcheck
		return
	}
	JSON(w, status, map[string]string{"error": msg})
}

// mapError converts sentinel errors to HTTP status codes and messages.
func mapError(err error) (int, string) {
	switch {
	case errors.Is(err, app.ErrOrderNotFound),
		errors.Is(err, app.ErrCustomerNotFound),
		errors.Is(err, app.ErrProductNotFound),
		errors.Is(err, app.ErrVariantNotFound),
		errors.Is(err, app.ErrSubscriptionNotFound),
		errors.Is(err, app.ErrFulfillmentNotFound),
		errors.Is(err, app.ErrDiscountNotFound),
		errors.Is(err, app.ErrCouponNotFound),
		errors.Is(err, app.ErrCartNotFound),
		errors.Is(err, app.ErrStaffNotFound),
		errors.Is(err, app.ErrShipmentNotFound),
		errors.Is(err, app.ErrTaxonNotFound),
		errors.Is(err, app.ErrPriceNotFound),
		errors.Is(err, app.ErrLineItemNotFound),
		errors.Is(err, app.ErrInvoiceNotFound),
		errors.Is(err, app.ErrAnnouncementNotFound),
		// Access denial is surfaced as 404 so we never confirm a restricted
		// product/SKU exists to a viewer who may not see it.
		errors.Is(err, app.ErrProductNotAccessible):
		return http.StatusNotFound, "not found"

	// Announcement composition failures name what to fix — the message is shown
	// to the staff member who typed it, and "bad request" tells them nothing.
	case errors.Is(err, app.ErrEmptyAnnouncement),
		errors.Is(err, app.ErrInvalidAudience),
		errors.Is(err, app.ErrScheduleInPast):
		return http.StatusBadRequest, err.Error()

	case errors.Is(err, app.ErrAnnouncementNotCancellable):
		return http.StatusConflict, err.Error()

	case errors.Is(err, app.ErrRouteNotFound):
		return http.StatusNotFound, "that delivery route no longer exists"

	// Route planning failures get specific messages rather than a generic
	// conflict: each one names a different thing for staff to go fix, and the
	// difference between "no orders" and "the shop address is wrong" is the
	// whole message.
	case errors.Is(err, app.ErrNoDeliveryStops):
		return http.StatusConflict, "no local delivery orders are waiting to be routed"
	case errors.Is(err, app.ErrRouteAlreadyActive):
		return http.StatusConflict, "a route for this delivery day is already out with a driver — complete it before planning a new one"
	case errors.Is(err, app.ErrRouteNotActivatable):
		return http.StatusConflict, "this route has already been activated"
	case errors.Is(err, app.ErrRouteEmpty):
		return http.StatusConflict, "a route needs at least one stop"
	case errors.Is(err, app.ErrOriginNotConfigured):
		return http.StatusConflict, "set the roastery address in shipping settings before planning a route"
	case errors.Is(err, app.ErrOriginNotGeocodable):
		return http.StatusConflict, "the roastery address could not be placed on the map — check it in shipping settings"
	case errors.Is(err, app.ErrEndNotGeocodable):
		return http.StatusConflict, "the end-of-run address could not be placed on the map — check the spelling, or leave it blank to finish at the roastery"
	case errors.Is(err, app.ErrGeocoderNotConfigured):
		return http.StatusServiceUnavailable, "address lookup is not configured, so routes cannot be planned"
	case errors.Is(err, app.ErrGeocoderUnavailable):
		return http.StatusServiceUnavailable, "address lookup is temporarily unavailable — try again shortly"
	case errors.Is(err, routing.ErrUnavailable), errors.Is(err, routing.ErrNotConfigured):
		return http.StatusServiceUnavailable, "the routing service is unavailable, so stop order cannot be optimized"
	case errors.Is(err, routing.ErrTooManyStops):
		return http.StatusConflict, "too many stops for one route — split the run"

	case errors.Is(err, app.ErrInvalidCredentials):
		return http.StatusUnauthorized, "invalid credentials"

	case errors.Is(err, app.ErrPermissionDenied):
		return http.StatusForbidden, "permission denied"

	case errors.Is(err, app.ErrDuplicateVariantOptions):
		return http.StatusConflict, err.Error()

	case errors.Is(err, app.ErrEmailAlreadyExists),
		errors.Is(err, app.ErrSKUAlreadyExists),
		errors.Is(err, app.ErrCouponAlreadyUsed):
		return http.StatusConflict, "already exists"

	case errors.Is(err, app.ErrOrderNotRefundable),
		errors.Is(err, app.ErrOrderNotPayable),
		errors.Is(err, app.ErrOrderNotCancellable),
		errors.Is(err, app.ErrOrderAlreadyFulfilled),
		errors.Is(err, app.ErrOrderFulfillmentNotRevertible),
		errors.Is(err, app.ErrOrderShipmentNotRevertible),
		errors.Is(err, app.ErrOrderNotEditable),
		errors.Is(err, app.ErrLineItemNotInOrder),
		errors.Is(err, app.ErrVariantNotOnSameProduct),
		errors.Is(err, app.ErrVariantPriceMismatch),
		errors.Is(err, app.ErrCartExpired),
		errors.Is(err, app.ErrCartEmpty),
		errors.Is(err, app.ErrEmailNotVerified),
		errors.Is(err, app.ErrInsufficientStock),
		errors.Is(err, app.ErrSubscriptionNotActive),
		errors.Is(err, app.ErrSubscriptionNotPausable),
		errors.Is(err, app.ErrSubscriptionNotSkippable),
		errors.Is(err, app.ErrInvalidSkipRequest),
		errors.Is(err, app.ErrSkipIntervalsOutOfRange),
		errors.Is(err, app.ErrSkipDateOutOfRange),
		errors.Is(err, app.ErrSkipDateBeforeNextOrder),
		errors.Is(err, app.ErrNoSkipToUndo),
		errors.Is(err, app.ErrSkipUndoTooLate),
		errors.Is(err, app.ErrDiscountExpired),
		errors.Is(err, app.ErrDiscountNotActive),
		errors.Is(err, app.ErrMinimumOrderNotMet),
		errors.Is(err, app.ErrSessionExpired),
		errors.Is(err, app.ErrTokenExpired),
		errors.Is(err, app.ErrTokenAlreadyUsed),
		errors.Is(err, app.ErrStaffInactive),
		errors.Is(err, app.ErrPaymentFailed),
		errors.Is(err, app.ErrInvalidPrice),
		errors.Is(err, app.ErrInvalidQuantity),
		errors.Is(err, app.ErrInvalidWholesaleMOQ),
		errors.Is(err, app.ErrWholesaleNotApproved),
		errors.Is(err, app.ErrWholesaleNotPending),
		errors.Is(err, app.ErrMOQViolation),
		errors.Is(err, app.ErrOrderNotInvoiceable),
		errors.Is(err, app.ErrInvoiceNotPayable),
		errors.Is(err, app.ErrInvoiceNotSendable),
		errors.Is(err, app.ErrInvoiceNotVoidable),
		errors.Is(err, app.ErrLastAddress),
		errors.Is(err, app.ErrAddressIncomplete),
		errors.Is(err, app.ErrAttributeValueNotAllowed),
		errors.Is(err, app.ErrAttributeAllowedValuesRequired),
		errors.Is(err, app.ErrVariantInUse),
		errors.Is(err, app.ErrVariantArchived),
		errors.Is(err, app.ErrVariantNotInChannel):
		return http.StatusUnprocessableEntity, err.Error()

	default:
		return http.StatusInternalServerError, "internal server error"
	}
}
