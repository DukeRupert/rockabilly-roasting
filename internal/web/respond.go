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

// expectedFailure reports whether an error is one the application named on
// purpose *and* that a redirect can sensibly carry — a validation rule, a state
// machine refusing a move — as opposed to something that went wrong.
//
// The distinction is what separates "tell the operator, in the words the
// sentinel carries" from "log it, alert on it, and say nothing useful". A
// redirect-with-flash path that skips the question turns a dead database into a
// cheerful 303 and a sentence about maintenance schedules, and Sentry never
// hears about it.
//
// Not-found is excluded, though it is every bit as deliberate as the rest. The
// callers are redirect-back helpers, and the thing they redirect back *to* is
// usually the resource that was not found: flashing "no such plan" onto a 303
// aimed at that plan's page just produces a 404 with the message lost on the
// way. A missing resource wants the 404 rendered directly.
func expectedFailure(err error) bool {
	status, _ := mapError(err)
	return status < http.StatusInternalServerError && status != http.StatusNotFound
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
		errors.Is(err, app.ErrEquipmentNotFound),
		errors.Is(err, app.ErrServiceTicketNotFound),
		errors.Is(err, app.ErrServicePartNotFound),
		errors.Is(err, app.ErrServiceTimeEntryNotFound),
		errors.Is(err, app.ErrPlanNotFound),
		errors.Is(err, app.ErrPlanTaskNotFound),
		errors.Is(err, app.ErrPlanAssignmentNotFound),
		errors.Is(err, app.ErrMaintenanceNotFound),
		errors.Is(err, app.ErrSubscriptionPlanNotFound),
		errors.Is(err, app.ErrPriceListNotFound),
		errors.Is(err, app.ErrAttributeSetNotFound),
		errors.Is(err, app.ErrAttributeKeyNotFound),
		errors.Is(err, app.ErrAddressNotFound),
		errors.Is(err, app.ErrRouteStopNotFound),
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

	// Service ticket validation. Same rule as equipment: say what to fix.
	case errors.Is(err, app.ErrPartNameRequired),
		errors.Is(err, app.ErrInvalidPartQuantity),
		errors.Is(err, app.ErrInvalidPartCost),
		errors.Is(err, app.ErrInvalidPartStatus),
		errors.Is(err, app.ErrInvalidTimeMinutes),
		errors.Is(err, app.ErrInvalidServiceTimeKind),
		// Preventive maintenance. Every one of these carries a sentence
		// written for the staff member who typed the thing; falling through to
		// the default would answer them with "internal server error" and page
		// somebody. ErrLaborRateInvalid is the one that reached a handler
		// calling Error() directly, so it was a 500 in practice rather than in
		// theory.
		errors.Is(err, app.ErrLaborRateInvalid),
		errors.Is(err, app.ErrTravelRateWithoutLabor),
		errors.Is(err, app.ErrLaborRateZero),
		errors.Is(err, app.ErrPlanNameRequired),
		errors.Is(err, app.ErrPlanNameTaken),
		errors.Is(err, app.ErrPlanInUse),
		errors.Is(err, app.ErrPlanInactive),
		errors.Is(err, app.ErrPlanHasNoTasks),
		errors.Is(err, app.ErrPlanTaskNameRequired),
		errors.Is(err, app.ErrPlanTaskHasHistory),
		errors.Is(err, app.ErrPlanIntervalInvalid),
		errors.Is(err, app.ErrPlanLeadInvalid),
		errors.Is(err, app.ErrPlanStartRequired),
		errors.Is(err, app.ErrPlanContractEndsBeforeStart),
		errors.Is(err, app.ErrPlanAlreadyAssigned),
		errors.Is(err, app.ErrPlanAssignmentEnded),
		errors.Is(err, app.ErrMaintenanceAlreadyClosed),
		errors.Is(err, app.ErrMaintenanceDateRequired),
		errors.Is(err, app.ErrMaintenanceDateOutOfRange),
		errors.Is(err, app.ErrMaintenanceDateInFuture),
		errors.Is(err, app.ErrEquipmentRetired):
		return http.StatusBadRequest, err.Error()

	case errors.Is(err, app.ErrServiceTicketTitleRequired),
		errors.Is(err, app.ErrInvalidServiceSeverity),
		errors.Is(err, app.ErrInvalidServiceTicketStatus),
		errors.Is(err, app.ErrInvalidServiceNoteKind),
		errors.Is(err, app.ErrEmptyServiceNote),
		errors.Is(err, app.ErrTicketEquipmentMismatch):
		return http.StatusBadRequest, err.Error()

	// Equipment validation. Each message is the correction to make, not a
	// restatement of the rule.
	case errors.Is(err, app.ErrEquipmentMakeRequired),
		errors.Is(err, app.ErrInvalidEquipmentCategory),
		errors.Is(err, app.ErrInvalidEquipmentOwnership),
		errors.Is(err, app.ErrInvalidEquipmentStatus):
		return http.StatusBadRequest, err.Error()

	// QuickBooks billing configuration. Bad input, not a server fault. The
	// handlers reject most of these before the service is reached, so several
	// are unreachable today; they are mapped because the alternative when one
	// does escape is a 500 for what is plainly a bad request.
	case errors.Is(err, app.ErrInvalidQBBillingMode),
		errors.Is(err, app.ErrQBSalesItemRequired),
		errors.Is(err, app.ErrQBItemNotFound),
		errors.Is(err, app.ErrQBNotConnected),
		errors.Is(err, app.ErrQBNoActiveItems),
		errors.Is(err, app.ErrQBBillingNotLive),
		errors.Is(err, app.ErrQBOrderAlreadyInvoiced),
		errors.Is(err, app.ErrQBOrderNotBillable):
		return http.StatusBadRequest, err.Error()

	// A toggle naming a module this binary does not know about. 404 rather
	// than 400: from the caller's side the module genuinely does not exist.
	case errors.Is(err, app.ErrUnknownModule):
		return http.StatusNotFound, "no such module"

	case errors.Is(err, app.ErrPermissionDenied):
		return http.StatusForbidden, "permission denied"

	// Naming the white-label products blocking an archive is the whole message —
	// staff have no other view of which labels a coffee backs.
	case errors.Is(err, app.ErrProductHasWhiteLabelChildren):
		return http.StatusConflict, err.Error()

	case errors.Is(err, app.ErrNotWhiteLabelProduct):
		return http.StatusBadRequest, "this product has no base coffee to reassign"

	case errors.Is(err, app.ErrWhiteLabelBaseInvalid):
		return http.StatusBadRequest, "pick an active coffee with a wholesale size"

	case errors.Is(err, app.ErrDuplicateVariantOptions):
		return http.StatusConflict, err.Error()

	case errors.Is(err, app.ErrEmailAlreadyExists),
		errors.Is(err, app.ErrSKUAlreadyExists),
		errors.Is(err, app.ErrCouponAlreadyUsed):
		return http.StatusConflict, "already exists"

	// State-machine refusals that are neither "not found" nor a typo in the
	// form: the record exists, the operator has the right to touch it, and the
	// move is simply not available from where it stands. Each carries its own
	// sentence because the difference between them is what the operator does
	// next — buy no second label, use the QuickBooks flow, leave the stop alone.
	case errors.Is(err, app.ErrInvalidOrderStatus):
		return http.StatusConflict, "this order is not in a state where that step can be taken — reload the page to see where it stands"
	case errors.Is(err, app.ErrOrderHasActiveLabel),
		errors.Is(err, app.ErrOrderQBManaged),
		errors.Is(err, app.ErrStopAlreadyDelivered):
		return http.StatusConflict, err.Error()

	// Not the operator's fault and not a bad request: this binary has no River
	// client wired, so the retry button cannot work anywhere in this process.
	case errors.Is(err, app.ErrJobRetryUnavailable):
		return http.StatusServiceUnavailable, err.Error()

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
		errors.Is(err, app.ErrSubscriptionNotCancellable),
		errors.Is(err, app.ErrSubscriptionNotResumable),
		errors.Is(err, app.ErrSubscriptionNotEditable),
		errors.Is(err, app.ErrSubscriptionPlanInactive),
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
