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
// The recipient ID travels in the clear; the HMAC is what makes the link
// unforgeable. That is deliberate — this authorizes exactly one low-stakes
// action (turn my own reminders off) and nothing else, so it must never be
// accepted as proof of identity anywhere else in the app.
type UnsubscribeSigner struct {
	secret []byte
}

// UnsubscribeAudience distinguishes whose preference a token turns off. A
// wholesale account can have several people on one mailing, and an opt-out must
// only ever silence the address that clicked it.
type UnsubscribeAudience string

const (
	// UnsubscribeAudienceCustomer targets the account's own contact address,
	// stored as customers.order_reminders_enabled.
	UnsubscribeAudienceCustomer UnsubscribeAudience = "c"
	// UnsubscribeAudienceCustomerUser targets one invited teammate, stored as
	// customer_users.receives_notifications.
	UnsubscribeAudienceCustomerUser UnsubscribeAudience = "u"
)

// UnsubscribeTarget is the single recipient a token authorizes an opt-out for.
type UnsubscribeTarget struct {
	Audience UnsubscribeAudience
	ID       uuid.UUID
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

// Sign returns an opaque token authorizing an opt-out for exactly one
// recipient. Returns "" when the signer is disabled.
//
// The audience prefix is inside the signed payload, so a token minted for a
// teammate cannot be edited into one that silences the whole account.
func (s *UnsubscribeSigner) Sign(target UnsubscribeTarget) string {
	if !s.Enabled() {
		return ""
	}
	payload := string(target.Audience) + ":" + target.ID.String()
	return payload + "." + s.mac(payload)
}

// Verify checks a token's signature and returns the recipient it authorizes.
//
// Bare "<uuid>.<mac>" tokens — the format minted before opt-outs became
// per-recipient — are still accepted and resolve to the account contact, so
// links already sitting in inboxes keep working.
func (s *UnsubscribeSigner) Verify(token string) (UnsubscribeTarget, error) {
	if !s.Enabled() {
		return UnsubscribeTarget{}, ErrInvalidUnsubscribeToken
	}
	// SplitN, not Split: an attacker-supplied extra "." must not silently
	// shift which segment is treated as the signature.
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return UnsubscribeTarget{}, ErrInvalidUnsubscribeToken
	}
	payload, sig := parts[0], parts[1]

	// Compare before parsing anything, so a malformed payload and a bad
	// signature take the same path and cost the same.
	if !hmac.Equal([]byte(sig), []byte(s.mac(payload))) {
		return UnsubscribeTarget{}, ErrInvalidUnsubscribeToken
	}

	audience := UnsubscribeAudienceCustomer
	rawID := payload
	if prefix, rest, found := strings.Cut(payload, ":"); found {
		switch UnsubscribeAudience(prefix) {
		case UnsubscribeAudienceCustomer, UnsubscribeAudienceCustomerUser:
			audience = UnsubscribeAudience(prefix)
		default:
			return UnsubscribeTarget{}, ErrInvalidUnsubscribeToken
		}
		rawID = rest
	}

	id, err := uuid.Parse(rawID)
	if err != nil {
		return UnsubscribeTarget{}, ErrInvalidUnsubscribeToken
	}
	return UnsubscribeTarget{Audience: audience, ID: id}, nil
}

func (s *UnsubscribeSigner) mac(id string) string {
	m := hmac.New(sha256.New, s.secret)
	m.Write([]byte(id))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}
