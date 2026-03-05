package web

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/platform/logging"
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
		errors.Is(err, app.ErrPriceNotFound):
		return http.StatusNotFound, "not found"

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
		errors.Is(err, app.ErrOrderNotCancellable),
		errors.Is(err, app.ErrOrderAlreadyFulfilled),
		errors.Is(err, app.ErrCartExpired),
		errors.Is(err, app.ErrCartEmpty),
		errors.Is(err, app.ErrEmailNotVerified),
		errors.Is(err, app.ErrInsufficientStock),
		errors.Is(err, app.ErrSubscriptionNotActive),
		errors.Is(err, app.ErrSubscriptionNotPausable),
		errors.Is(err, app.ErrDiscountExpired),
		errors.Is(err, app.ErrDiscountNotActive),
		errors.Is(err, app.ErrMinimumOrderNotMet),
		errors.Is(err, app.ErrSessionExpired),
		errors.Is(err, app.ErrTokenExpired),
		errors.Is(err, app.ErrTokenAlreadyUsed),
		errors.Is(err, app.ErrStaffInactive),
		errors.Is(err, app.ErrPaymentFailed),
		errors.Is(err, app.ErrInvalidPrice):
		return http.StatusUnprocessableEntity, err.Error()

	default:
		return http.StatusInternalServerError, "internal server error"
	}
}
