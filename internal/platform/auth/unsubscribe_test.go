package auth

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestUnsubscribeSignerRoundTrip(t *testing.T) {
	s := NewUnsubscribeSigner("test-secret")
	id := uuid.New()

	got, err := s.Verify(s.Sign(id))
	require.NoError(t, err)
	require.Equal(t, id, got)
}

func TestUnsubscribeSignerRejectsTampering(t *testing.T) {
	s := NewUnsubscribeSigner("test-secret")
	id := uuid.New()
	valid := s.Sign(id)
	other := uuid.New()

	tests := []struct {
		name  string
		token string
	}{
		{"empty", ""},
		{"no separator", strings.ReplaceAll(valid, ".", "")},
		{"signature only", strings.SplitN(valid, ".", 2)[1]},
		{"id only", id.String()},
		{"unsigned id", id.String() + "."},
		// The whole point: swapping in someone else's ID must not verify,
		// or the link becomes an unsubscribe-anyone button.
		{"different customer, same signature", other.String() + "." + strings.SplitN(valid, ".", 2)[1]},
		{"truncated signature", valid[:len(valid)-4]},
		{"extra segment", valid + ".extra"},
		{"garbage", "not-a-token"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := s.Verify(tt.token)
			require.ErrorIs(t, err, ErrInvalidUnsubscribeToken)
		})
	}
}

// A token minted under one secret must not verify under another — rotating
// the secret has to actually invalidate outstanding links.
func TestUnsubscribeSignerIsSecretBound(t *testing.T) {
	id := uuid.New()
	token := NewUnsubscribeSigner("secret-one").Sign(id)

	_, err := NewUnsubscribeSigner("secret-two").Verify(token)
	require.ErrorIs(t, err, ErrInvalidUnsubscribeToken)
}

// With no secret configured the signer must refuse to mint or accept anything,
// rather than emitting links that silently fail (or worse, verify trivially).
func TestUnsubscribeSignerDisabled(t *testing.T) {
	s := NewUnsubscribeSigner("   ")
	require.False(t, s.Enabled())
	require.Equal(t, "", s.Sign(uuid.New()))

	_, err := s.Verify("anything")
	require.ErrorIs(t, err, ErrInvalidUnsubscribeToken)

	// A token that is valid elsewhere must still be refused here.
	valid := NewUnsubscribeSigner("real").Sign(uuid.New())
	_, err = s.Verify(valid)
	require.ErrorIs(t, err, ErrInvalidUnsubscribeToken)
}
