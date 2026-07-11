package quickbooks

import (
	"errors"
	"fmt"
)

// Sentinel errors for QB integration.
var (
	ErrNotConnected     = errors.New("quickbooks: not connected")
	ErrTokenExpired     = errors.New("quickbooks: refresh token expired, reconnect required")
	ErrBadRequest       = errors.New("quickbooks: bad request (data problem)")
	ErrRateLimited      = errors.New("quickbooks: rate limited")
	ErrServerError      = errors.New("quickbooks: server error")
	ErrInvalidSignature = errors.New("quickbooks: invalid webhook signature")
	ErrNotFound         = errors.New("quickbooks: resource not found")
)

// APIError represents an error response from the QBO API.
type APIError struct {
	StatusCode int
	Message    string
	Detail     string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("quickbooks API %d: %s — %s", e.StatusCode, e.Message, e.Detail)
}

// IsRetryable returns true if the error is transient and the operation should be retried.
func IsRetryable(err error) bool {
	if errors.Is(err, ErrNotFound) {
		return false // a missing/deleted resource won't reappear on retry
	}
	if errors.Is(err, ErrBadRequest) {
		return false // data or config problem, won't fix itself
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return true // network errors are retryable
	}
	switch apiErr.StatusCode {
	case 400:
		return false // data problem, won't fix itself
	case 401:
		return true // token refresh needed, then retry
	case 429:
		return true // rate limited
	case 500, 502, 503:
		return true // server error
	default:
		return false
	}
}
