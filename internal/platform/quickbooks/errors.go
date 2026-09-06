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

	// ErrNotConfigured is returned by every Client method when no Intuit app
	// is configured — neither a saved row nor the environment fallback. It is
	// the state a fresh deployment boots in, so it is an ordinary answer
	// rather than a fault: the settings page turns it into a form to fill in,
	// and a job that hits it fails with a message naming the setting.
	ErrNotConfigured = errors.New("quickbooks: no Intuit app configured")

	// ErrAppConfigUnreadable is returned when a saved app configuration will
	// not decrypt, which means the encryption key changed. Distinguished from
	// "not configured" because the two need opposite responses: one is a form
	// to fill in, the other is a key to restore, and re-entering credentials
	// would not recover the connection either way.
	ErrAppConfigUnreadable = errors.New("quickbooks: stored app configuration could not be decrypted")

	// ErrNoEncryptionKey is returned when a QuickBooks secret has to be
	// encrypted or decrypted and no key is configured. Named rather than left
	// to surface as "crypto/aes: invalid key size 0", because the fix is a
	// variable on the box and the message has to say which one.
	ErrNoEncryptionKey = errors.New("quickbooks: no encryption key configured")

	// ErrInvalidAppConfig is returned when a submitted app configuration is
	// incomplete or names an environment that does not exist.
	ErrInvalidAppConfig = errors.New("quickbooks: invalid app configuration")

	// ErrConnected is returned when a change would strand the live connection
	// — the stored tokens were issued by the app being replaced, so they stop
	// meaning anything the moment it changes. Disconnect first.
	ErrConnected = errors.New("quickbooks: disconnect before changing the app configuration")
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
	if errors.Is(err, ErrNotConfigured) {
		return false // needs a human in the admin, not another attempt
	}
	if errors.Is(err, ErrAppConfigUnreadable) {
		return false // needs a key restored on the box; retrying cannot find one
	}
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
