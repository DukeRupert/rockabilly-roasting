package app

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The bulk fulfillment methods own their own transactions via store.Tx, so the
// testutil NewTestTx-with-rollback pattern doesn't fit — those internal commits
// would escape the outer rollback. The codebase already follows this trade-off:
// RenewBatch and the email-send pool methods have no integration coverage
// either. End-to-end behavior is verified in the dev-server pass per the
// architect handoff. What's covered here is the pure logic: empty-input
// short-circuit, ID-order preservation, and the error-to-reason translation
// that the UI banner depends on.

func TestRunBulkOrderVerb_EmptyInput(t *testing.T) {
	s := &OrderService{}
	out, err := s.runBulkOrderVerb(context.Background(), nil, nil, func(context.Context, pgx.Tx, uuid.UUID) error {
		t.Fatal("verb should not be invoked for empty input")
		return nil
	})
	require.NoError(t, err)
	assert.Empty(t, out.Succeeded)
	assert.Empty(t, out.Failed)
}

func TestFailureReasonFor(t *testing.T) {
	t.Run("nil error returns empty", func(t *testing.T) {
		assert.Equal(t, "", failureReasonFor(nil))
	})

	t.Run("ErrOrderNotFound", func(t *testing.T) {
		assert.Equal(t, "order not found", failureReasonFor(ErrOrderNotFound))
	})

	t.Run("ErrOrderNotFound wrapped", func(t *testing.T) {
		err := fmt.Errorf("get order for picked-up: %w", ErrOrderNotFound)
		assert.Equal(t, "order not found", failureReasonFor(err))
	})

	t.Run("ErrInvalidOrderStatus wrapped with context", func(t *testing.T) {
		err := fmt.Errorf("order is not ready for pickup: %w", ErrInvalidOrderStatus)
		assert.Equal(t, "order is not ready for pickup", failureReasonFor(err))
	})

	t.Run("ErrInvalidOrderStatus bare", func(t *testing.T) {
		// Defensive — the service always wraps with a context phrase, but the
		// helper shouldn't crash if it ever doesn't.
		assert.Equal(t, "invalid status", failureReasonFor(ErrInvalidOrderStatus))
	})

	t.Run("unknown error falls back to err.Error()", func(t *testing.T) {
		err := errors.New("postgres: connection reset")
		assert.Equal(t, "postgres: connection reset", failureReasonFor(err))
	})

	t.Run("local delivery wrong-method phrasing survives translation", func(t *testing.T) {
		// Mirrors the exact wrap used by MarkOutForDelivery so we know the UI
		// will see something the customer-service team can recognise.
		err := fmt.Errorf("order is not a local delivery order: %w", ErrInvalidOrderStatus)
		assert.Equal(t, "order is not a local delivery order", failureReasonFor(err))
	})
}
