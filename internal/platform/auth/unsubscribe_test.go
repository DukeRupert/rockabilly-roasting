package auth

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func customerTarget(id uuid.UUID) UnsubscribeTarget {
	return UnsubscribeTarget{Audience: UnsubscribeAudienceCustomer, ID: id}
}

func TestUnsubscribeSignerRoundTrip(t *testing.T) {
	s := NewUnsubscribeSigner("test-secret")

	for _, audience := range []UnsubscribeAudience{UnsubscribeAudienceCustomer, UnsubscribeAudienceCustomerUser} {
		t.Run(string(audience), func(t *testing.T) {
			target := UnsubscribeTarget{Audience: audience, ID: uuid.New()}
			got, err := s.Verify(s.Sign(target))
			require.NoError(t, err)
			require.Equal(t, target, got)
		})
	}
}

// Links minted before opt-outs became per-recipient are bare "<uuid>.<mac>"
// and may still be sitting in inboxes. They must keep working, resolving to
// the account contact.
func TestUnsubscribeSignerAcceptsLegacyBareToken(t *testing.T) {
	s := NewUnsubscribeSigner("test-secret")
	id := uuid.New()

	legacy := id.String() + "." + s.mac(id.String())

	got, err := s.Verify(legacy)
	require.NoError(t, err)
	require.Equal(t, customerTarget(id), got)
}

// The audience prefix is inside the signed payload, so a teammate's token
// cannot be edited into one that silences the whole account.
func TestUnsubscribeSignerAudienceIsSigned(t *testing.T) {
	s := NewUnsubscribeSigner("test-secret")
	id := uuid.New()

	memberToken := s.Sign(UnsubscribeTarget{Audience: UnsubscribeAudienceCustomerUser, ID: id})
	sig := strings.SplitN(memberToken, ".", 2)[1]

	// Swap "u:" for "c:" but keep the signature.
	forged := "c:" + id.String() + "." + sig

	_, err := s.Verify(forged)
	require.ErrorIs(t, err, ErrInvalidUnsubscribeToken)
}

func TestUnsubscribeSignerRejectsUnknownAudience(t *testing.T) {
	s := NewUnsubscribeSigner("test-secret")
	payload := "x:" + uuid.New().String()

	// Correctly signed, but names an audience the app does not serve.
	_, err := s.Verify(payload + "." + s.mac(payload))
	require.ErrorIs(t, err, ErrInvalidUnsubscribeToken)
}

func TestUnsubscribeSignerRejectsTampering(t *testing.T) {
	s := NewUnsubscribeSigner("test-secret")
	id := uuid.New()
	valid := s.Sign(customerTarget(id))
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
		{"different customer, same signature", "c:" + other.String() + "." + strings.SplitN(valid, ".", 2)[1]},
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
	token := NewUnsubscribeSigner("secret-one").Sign(customerTarget(uuid.New()))

	_, err := NewUnsubscribeSigner("secret-two").Verify(token)
	require.ErrorIs(t, err, ErrInvalidUnsubscribeToken)
}

// With no secret configured the signer must refuse to mint or accept anything,
// rather than emitting links that silently fail (or worse, verify trivially).
func TestUnsubscribeSignerDisabled(t *testing.T) {
	s := NewUnsubscribeSigner("   ")
	require.False(t, s.Enabled())
	require.Equal(t, "", s.Sign(customerTarget(uuid.New())))

	_, err := s.Verify("anything")
	require.ErrorIs(t, err, ErrInvalidUnsubscribeToken)

	// A token that is valid elsewhere must still be refused here.
	valid := NewUnsubscribeSigner("real").Sign(customerTarget(uuid.New()))
	_, err = s.Verify(valid)
	require.ErrorIs(t, err, ErrInvalidUnsubscribeToken)
}
