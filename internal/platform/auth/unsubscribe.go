package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"

	"github.com/google/uuid"
)

// ErrInvalidUnsubscribeToken is returned when a token is malformed, or its
// signature does not match. Both cases are reported identically so a probe
// cannot learn which customer IDs exist.
var ErrInvalidUnsubscribeToken = errors.New("invalid unsubscribe token")

// UnsubscribeSigner mints and verifies the tokens embedded in marketing-email
// opt-out links.
//
// Tokens are stateless HMACs rather than rows in magic_link_tokens for two
// reasons. An unsubscribe link has to keep working for as long as the email
// sits in someone's inbox — which is forever, not the hours or days a sign-in
// token gets — so there is no sensible expiry to enforce. And a stored token
// would mean one row per recipient per week, written on every send, purely to
// support a link most people never click.
//
// The customer ID travels in the clear; the HMAC is what makes the link
// unforgeable. That is deliberate — this authorizes exactly one low-stakes
// action (turn my own reminders off) and nothing else, so it must never be
// accepted as proof of identity anywhere else in the app.
type UnsubscribeSigner struct {
	secret []byte
}

// NewUnsubscribeSigner returns a signer for the given secret. An empty secret
// yields a disabled signer: callers check Enabled() and omit opt-out links
// rather than emitting links that cannot be verified.
func NewUnsubscribeSigner(secret string) *UnsubscribeSigner {
	if strings.TrimSpace(secret) == "" {
		return &UnsubscribeSigner{}
	}
	return &UnsubscribeSigner{secret: []byte(secret)}
}

// Enabled reports whether a secret is configured.
func (s *UnsubscribeSigner) Enabled() bool { return s != nil && len(s.secret) > 0 }

// Sign returns an opaque token authorizing an opt-out for customerID.
// Returns "" when the signer is disabled.
func (s *UnsubscribeSigner) Sign(customerID uuid.UUID) string {
	if !s.Enabled() {
		return ""
	}
	id := customerID.String()
	return id + "." + s.mac(id)
}

// Verify checks a token's signature and returns the customer it authorizes.
func (s *UnsubscribeSigner) Verify(token string) (uuid.UUID, error) {
	if !s.Enabled() {
		return uuid.Nil, ErrInvalidUnsubscribeToken
	}
	// SplitN, not Split: an attacker-supplied extra "." must not silently
	// shift which segment is treated as the signature.
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return uuid.Nil, ErrInvalidUnsubscribeToken
	}
	id, sig := parts[0], parts[1]

	// Compare before parsing the UUID, so a malformed ID and a bad signature
	// take the same path and cost the same.
	if !hmac.Equal([]byte(sig), []byte(s.mac(id))) {
		return uuid.Nil, ErrInvalidUnsubscribeToken
	}
	customerID, err := uuid.Parse(id)
	if err != nil {
		return uuid.Nil, ErrInvalidUnsubscribeToken
	}
	return customerID, nil
}

func (s *UnsubscribeSigner) mac(id string) string {
	m := hmac.New(sha256.New, s.secret)
	m.Write([]byte(id))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}
