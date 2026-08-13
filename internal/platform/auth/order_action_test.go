package auth

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOrderActionSignerRoundTrip(t *testing.T) {
	s := NewOrderActionSigner("test-secret")
	orderID := uuid.New()
	issued := time.Date(2026, time.August, 13, 9, 0, 0, 0, time.UTC)

	token := s.Sign(OrderActionSwitchToPickup, orderID, issued)
	require.NotEmpty(t, token)

	got, err := s.Verify(token, OrderActionSwitchToPickup, issued.Add(time.Hour))
	require.NoError(t, err)
	assert.Equal(t, orderID, got)
}

func TestOrderActionSignerDisabled(t *testing.T) {
	s := NewOrderActionSigner("   ")
	assert.False(t, s.Enabled())
	assert.Empty(t, s.Sign(OrderActionSwitchToPickup, uuid.New(), time.Now()),
		"a disabled signer must mint nothing rather than an unverifiable link")

	_, err := s.Verify("anything.atall", OrderActionSwitchToPickup, time.Now())
	assert.ErrorIs(t, err, ErrInvalidOrderActionToken)
}

func TestOrderActionSignerRejectsTampering(t *testing.T) {
	s := NewOrderActionSigner("test-secret")
	orderID := uuid.New()
	issued := time.Now()
	token := s.Sign(OrderActionSwitchToPickup, orderID, issued)

	t.Run("swapped order id", func(t *testing.T) {
		// Re-point the link at somebody else's order, keeping the signature.
		sig := token[len(token)-43:]
		forged := string(OrderActionSwitchToPickup) + ":" + uuid.New().String() + ":9999999999." + sig
		_, err := s.Verify(forged, OrderActionSwitchToPickup, issued)
		assert.ErrorIs(t, err, ErrInvalidOrderActionToken)
	})

	t.Run("extended expiry", func(t *testing.T) {
		_, err := s.Verify(token+"x", OrderActionSwitchToPickup, issued)
		assert.ErrorIs(t, err, ErrInvalidOrderActionToken)
	})

	t.Run("different secret", func(t *testing.T) {
		other := NewOrderActionSigner("other-secret")
		_, err := other.Verify(token, OrderActionSwitchToPickup, issued)
		assert.ErrorIs(t, err, ErrInvalidOrderActionToken)
	})

	t.Run("malformed", func(t *testing.T) {
		for _, bad := range []string{"", ".", "nodot", "a.b.c"} {
			_, err := s.Verify(bad, OrderActionSwitchToPickup, issued)
			assert.ErrorIs(t, err, ErrInvalidOrderActionToken, "input %q", bad)
		}
	})
}

// The purpose is inside the signed payload precisely so a token cannot be
// repurposed for a different action if one is added later.
func TestOrderActionSignerRejectsWrongPurpose(t *testing.T) {
	s := NewOrderActionSigner("test-secret")
	token := s.Sign(OrderActionSwitchToPickup, uuid.New(), time.Now())

	_, err := s.Verify(token, OrderActionPurpose("cancel"), time.Now())
	assert.ErrorIs(t, err, ErrInvalidOrderActionToken)
}

func TestOrderActionSignerExpiry(t *testing.T) {
	s := NewOrderActionSigner("test-secret")
	issued := time.Date(2026, time.August, 13, 9, 0, 0, 0, time.UTC)
	token := s.Sign(OrderActionSwitchToPickup, uuid.New(), issued)

	t.Run("just inside the window", func(t *testing.T) {
		_, err := s.Verify(token, OrderActionSwitchToPickup, issued.Add(DefaultOrderActionTTL-time.Minute))
		assert.NoError(t, err)
	})

	t.Run("past the window", func(t *testing.T) {
		_, err := s.Verify(token, OrderActionSwitchToPickup, issued.Add(DefaultOrderActionTTL+time.Minute))
		// Expiry is reported distinctly from forgery so the page can tell the
		// customer the link aged out instead of implying it was never valid.
		assert.ErrorIs(t, err, ErrOrderActionTokenExpired)
	})
}
