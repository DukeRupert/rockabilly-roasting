package ratelimit

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// Predefined limit configurations.
var (
	// Auth endpoints — tight sliding windows.
	AuthIPLimit         = 10  // attempts per window per IP (across all auth endpoints)
	AuthIdentifierLimit = 5   // attempts per window per identifier
	AuthWindow          = 15 * time.Minute

	StaffIPLimit         = 5 // tighter for staff
	StaffIdentifierLimit = 3
	StaffWindow          = 15 * time.Minute

	// Magic link requests.
	MagicLinkIPLimit = 5
	MagicLinkWindow  = 15 * time.Minute

	// Coupon attempts.
	CouponSessionLimit = 10
	CouponIPLimit      = 30
	CouponWindow       = time.Hour

	// Checkout attempts.
	CheckoutSessionLimit = 5
	CheckoutWindow       = 10 * time.Minute

	// Global per-IP.
	GlobalIPLimit = 300
	GlobalWindow  = time.Minute
)

// Limiter provides rate limiting backed by a Store.
type Limiter struct {
	store Store
}

// NewLimiter creates a new Limiter.
func NewLimiter(store Store) *Limiter {
	return &Limiter{store: store}
}

// Allow checks whether the given key is allowed under the specified limits.
func (l *Limiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, int, time.Time, error) {
	return l.store.Allow(ctx, key, limit, window)
}

// Reset clears the counter for key.
func (l *Limiter) Reset(ctx context.Context, key string) error {
	return l.store.Reset(ctx, key)
}

// ClientIP extracts the client IP from the request.
// It checks X-Forwarded-For and X-Real-IP first (for reverse proxies),
// then falls back to RemoteAddr.
func ClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Take the first IP (before any comma).
		for i := 0; i < len(xff); i++ {
			if xff[i] == ',' {
				return strings.TrimSpace(xff[:i])
			}
		}
		return strings.TrimSpace(xff)
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// HashIdentifier returns a hex-encoded SHA-256 hash of an identifier (email).
// This avoids storing plaintext emails in the rate limit store.
func HashIdentifier(identifier string) string {
	h := sha256.Sum256([]byte(identifier))
	return fmt.Sprintf("%x", h[:16]) // 128-bit prefix is sufficient for keying
}

// Key builders for consistent rate limit key construction.

func AuthIPKey(ip string) string                 { return "auth:ip:" + ip }
func AuthIdentifierKey(hash string) string       { return "auth:id:" + hash }
func MagicLinkIPKey(ip string) string            { return "magic:ip:" + ip }
func CouponSessionKey(sessionID string) string   { return "coupon:sess:" + sessionID }
func CouponIPKey(ip string) string               { return "coupon:ip:" + ip }
func CheckoutSessionKey(sessionID string) string { return "checkout:sess:" + sessionID }
func GlobalIPKey(ip string) string               { return "global:ip:" + ip }
