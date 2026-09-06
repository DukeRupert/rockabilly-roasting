package auth

import (
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
)

// Key purposes. Each label is the `info` string of an HKDF expansion, so two
// purposes derived from the same APP_SECRET are independent: recovering one
// tells an attacker nothing about the others, and a token signed for one
// purpose cannot verify under another.
//
// These labels are permanent. Changing one changes the key it derives, which
// invalidates every link already signed with it and — for the QuickBooks
// purpose — makes the stored OAuth tokens undecryptable.
const (
	// PurposeUnsubscribe signs the opt-out links in marketing email.
	PurposeUnsubscribe = "hiri/unsubscribe"
	// PurposeOrderAction signs the one-click links in transactional email.
	PurposeOrderAction = "hiri/order-action"
	// PurposeQBTokenEncryption encrypts the QuickBooks OAuth tokens at rest.
	PurposeQBTokenEncryption = "hiri/qb-token-encryption"
)

// Secrets derives per-purpose keys from one application secret.
//
// It exists so a deployment needs a single high-entropy value in its
// environment rather than one variable per feature that happens to need a key.
// The per-feature variables still win when set — a running deployment must not
// have its keys change underneath it, which would break links already sitting
// in customers' inboxes and lock the QuickBooks tokens away — so this is the
// fallback, never the override.
//
// A zero Secrets (no APP_SECRET) derives nothing and reports Enabled() false.
// That is a supported state: every feature that reaches for a key here already
// degrades to a safe, louder behaviour when it has none.
type Secrets struct {
	master []byte
}

// NewSecrets returns a deriver over appSecret. An empty or whitespace-only
// secret yields a disabled Secrets rather than an error, because "no
// APP_SECRET set" is a normal configuration, not a failure.
func NewSecrets(appSecret string) *Secrets {
	trimmed := strings.TrimSpace(appSecret)
	if trimmed == "" {
		return &Secrets{}
	}
	return &Secrets{master: []byte(trimmed)}
}

// Enabled reports whether an application secret was configured.
func (s *Secrets) Enabled() bool { return s != nil && len(s.master) > 0 }

// Key derives length bytes for purpose. It returns nil when no application
// secret is configured — callers check Enabled() (or the nil result) and fall
// back to their own degraded behaviour.
func (s *Secrets) Key(purpose string, length int) ([]byte, error) {
	if !s.Enabled() {
		return nil, nil
	}
	// Salt is nil rather than a constant: the master secret is already
	// high-entropy keying material, which is the case RFC 5869 says a salt is
	// optional for, and a hardcoded salt is not a secret anyway.
	key, err := hkdf.Key(sha256.New, s.master, nil, purpose, length)
	if err != nil {
		return nil, fmt.Errorf("derive key for %s: %w", purpose, err)
	}
	return key, nil
}

// Secret derives a 32-byte key for purpose and returns it base64-encoded, for
// the signers that take their key as a string. Empty when no application
// secret is configured.
func (s *Secrets) Secret(purpose string) (string, error) {
	key, err := s.Key(purpose, 32)
	if err != nil || key == nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(key), nil
}
