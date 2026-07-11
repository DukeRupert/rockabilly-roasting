package quickbooks

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestIsRetryable pins the retry classification the QB job workers depend on:
// anything wrapping ErrBadRequest or ErrNotFound must be permanent (JobCancel),
// including errors produced by classifyError, whose 400/404 paths must keep
// the sentinel in the chain.
func TestIsRetryable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"config error wrapping ErrBadRequest", fmt.Errorf("%w: QB sales item not configured", ErrBadRequest), false},
		{"classified 400", classifyError(400, []byte(`{"Fault":{}}`)), false},
		{"classified 404", classifyError(404, []byte(``)), false},
		{"classified 401", classifyError(401, []byte(``)), true},
		{"classified 429", classifyError(429, []byte(``)), true},
		{"classified 500", classifyError(500, []byte(``)), true},
		{"plain network error", errors.New("dial tcp: connection refused"), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsRetryable(tt.err))
		})
	}
}

// The joined 400 must satisfy both errors.Is (sentinel checks) and errors.As
// (status-code inspection) — the two ways callers unwrap QB errors.
func TestClassifyError400KeepsChain(t *testing.T) {
	err := classifyError(400, []byte(`bad item ref`))
	assert.True(t, errors.Is(err, ErrBadRequest))
	var apiErr *APIError
	assert.True(t, errors.As(err, &apiErr))
	assert.Equal(t, 400, apiErr.StatusCode)
}
