package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	// ErrInvalidOrderActionToken is returned when a token is malformed, carries
	// an unexpected purpose, or fails signature verification. All three report
	// identically so a probe cannot learn which order IDs exist.
	ErrInvalidOrderActionToken = errors.New("invalid order action token")

	// ErrOrderActionTokenExpired is returned for a correctly signed token past
	// its expiry. Distinguished from the invalid case on purpose: the signature
	// proves the link was genuinely ours, so the customer can safely be told it
	// simply aged out rather than being shown a generic failure.
	ErrOrderActionTokenExpired = errors.New("order action token expired")
)

// OrderActionPurpose scopes a token to exactly one operation. It travels inside
// the signed payload, so a token minted for one action cannot be edited into
// another.
type OrderActionPurpose string

// OrderActionSwitchToPickup authorizes moving a local-delivery order to pickup.
const OrderActionSwitchToPickup OrderActionPurpose = "pickup"

// OrderActionUndoSkip authorizes putting a skipped subscription back on its
// previous schedule. The signed payload is a resource UUID and the purpose is
// what says which kind of resource it names — this one carries a subscription
// ID rather than an order ID, and a token minted for one purpose can never be
// replayed against the other.
const OrderActionUndoSkip OrderActionPurpose = "undo_skip"

// OrderActionSigner mints and verifies the tokens behind one-click order links
// in transactional email.
//
// These are stateless HMACs for the same reason unsubscribe links are (see
// UnsubscribeSigner) — no row per order per send — but with one deliberate
// difference: they carry an expiry. An unsubscribe link should work for as long
// as the mail exists; a link that *mutates an order* should not still be live in
// a forwarded inbox months later.
//
// The order ID travels in the clear and the HMAC is what makes the link
// unforgeable. This authorizes one narrow, reversible change to one order and
// must never be treated as proof of identity: it does not sign the holder in,
// and it must not gate anything that reveals customer data beyond the order
// number it already names.
type OrderActionSigner struct {
	secret []byte
	ttl    time.Duration
}

// DefaultOrderActionTTL is how long an emailed order-action link stays valid.
// Two weeks comfortably outlives the few days between placing an order and its
// delivery run, while keeping the link from being useful indefinitely. The
// real guard is the order's own state — a fulfilled order refuses the switch
// regardless of how fresh the token is.
const DefaultOrderActionTTL = 14 * 24 * time.Hour

// NewOrderActionSigner returns a signer for the given secret. An empty secret
// yields a disabled signer: callers check Enabled() and omit the link rather
// than emailing one that can never be verified.
func NewOrderActionSigner(secret string) *OrderActionSigner {
	if strings.TrimSpace(secret) == "" {
		return &OrderActionSigner{}
	}
	return &OrderActionSigner{secret: []byte(secret), ttl: DefaultOrderActionTTL}
}

// Enabled reports whether a secret is configured.
func (s *OrderActionSigner) Enabled() bool { return s != nil && len(s.secret) > 0 }

// Sign returns an opaque token authorizing purpose on resourceID (the order or
// subscription the purpose names), expiring DefaultOrderActionTTL after
// issuedAt. Returns "" when the signer is disabled.
func (s *OrderActionSigner) Sign(purpose OrderActionPurpose, resourceID uuid.UUID, issuedAt time.Time) string {
	if !s.Enabled() {
		return ""
	}
	expiry := issuedAt.Add(s.ttl).Unix()
	payload := string(purpose) + ":" + resourceID.String() + ":" + strconv.FormatInt(expiry, 10)
	return payload + "." + s.mac(payload)
}

// Verify checks a token's signature, purpose, and expiry, returning the resource
// it authorizes. now is passed in so expiry is testable and so a single request
// judges every token against one clock.
func (s *OrderActionSigner) Verify(token string, purpose OrderActionPurpose, now time.Time) (uuid.UUID, error) {
	if !s.Enabled() {
		return uuid.Nil, ErrInvalidOrderActionToken
	}
	// SplitN, not Split: an attacker-supplied extra "." must not shift which
	// segment is treated as the signature.
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return uuid.Nil, ErrInvalidOrderActionToken
	}
	payload, sig := parts[0], parts[1]

	// Verify before parsing anything, so malformed payloads and bad signatures
	// take the same path and cost the same.
	if !hmac.Equal([]byte(sig), []byte(s.mac(payload))) {
		return uuid.Nil, ErrInvalidOrderActionToken
	}

	fields := strings.Split(payload, ":")
	if len(fields) != 3 {
		return uuid.Nil, ErrInvalidOrderActionToken
	}
	if OrderActionPurpose(fields[0]) != purpose {
		return uuid.Nil, ErrInvalidOrderActionToken
	}
	resourceID, err := uuid.Parse(fields[1])
	if err != nil {
		return uuid.Nil, ErrInvalidOrderActionToken
	}
	expiry, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil {
		return uuid.Nil, ErrInvalidOrderActionToken
	}
	if now.After(time.Unix(expiry, 0)) {
		return uuid.Nil, ErrOrderActionTokenExpired
	}
	return resourceID, nil
}

func (s *OrderActionSigner) mac(payload string) string {
	m := hmac.New(sha256.New, s.secret)
	m.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}
