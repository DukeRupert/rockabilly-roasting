package ratelimit

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Predefined limit configurations.
var (
	// Auth endpoints — tight sliding windows.
	AuthIPLimit         = 10 // attempts per window per IP (across all auth endpoints)
	AuthIdentifierLimit = 5  // attempts per window per identifier
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

	// Subscribe payment-intent attempts. This endpoint creates Stripe
	// customers and PaymentIntents unauthenticated, so it's a card-testing
	// target — but a legitimate signup recreates the PI on each address edit,
	// so the cap needs headroom above a single careful run.
	SubscribeIPLimit = 30
	SubscribeWindow  = 10 * time.Minute

	// Contact form.
	ContactIPLimit = 3
	ContactWindow  = time.Hour

	// Wholesale application form.
	WholesaleApplyIPLimit = 3
	WholesaleApplyWindow  = time.Hour

	// Newsletter signup. The footer form appears on every storefront page, so
	// a real visitor might resubmit after a typo — but nobody legitimately
	// subscribes more than a handful of addresses from one IP in an hour.
	NewsletterIPLimit = 5
	NewsletterWindow  = time.Hour

	// White-label label submissions. Its own bucket, deliberately roomier than
	// the apply form: the invite link is reusable and the success page invites
	// "Add another label", so one sitting legitimately means several posts —
	// plus retries. Still IP-capped since the endpoint is only token-gated.
	WhiteLabelIPLimit = 10
	WhiteLabelWindow  = time.Hour

	// Equipment fault reports from the wholesale portal. Roomier than the apply
	// form because a real bad morning legitimately produces several — two
	// machines down and a follow-up on each is four — but capped so a stuck
	// form cannot page the crew all day.
	ServiceReportLimit  = 10
	ServiceReportWindow = time.Hour

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

// trustedProxies holds the CIDRs of trusted reverse proxies.
// Only connections from these addresses will have X-Forwarded-For / X-Real-IP
// headers honored. Set once at startup via SetTrustedProxies.
var (
	trustedMu      sync.RWMutex
	trustedProxies []*net.IPNet
)

// SetTrustedProxies configures which source IPs are allowed to set
// forwarded headers. Pass CIDR strings like "10.0.0.0/8" or "172.16.0.1/32".
// Must be called before serving requests (typically in main).
func SetTrustedProxies(cidrs []string) error {
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			return fmt.Errorf("parse trusted proxy CIDR %q: %w", cidr, err)
		}
		nets = append(nets, ipNet)
	}
	trustedMu.Lock()
	trustedProxies = nets
	trustedMu.Unlock()
	return nil
}

// isTrustedProxy checks whether ip is within a configured trusted proxy CIDR.
func isTrustedProxy(ip string) bool {
	trustedMu.RLock()
	proxies := trustedProxies
	trustedMu.RUnlock()

	if len(proxies) == 0 {
		return false
	}

	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	for _, cidr := range proxies {
		if cidr.Contains(parsed) {
			return true
		}
	}
	return false
}

// ClientIP extracts the client IP from the request.
// Forwarded headers (X-Forwarded-For, X-Real-IP) are only trusted when
// RemoteAddr matches a configured trusted proxy CIDR. Otherwise RemoteAddr
// is used directly, preventing attackers from spoofing their IP.
func ClientIP(r *http.Request) string {
	remoteHost, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		remoteHost = r.RemoteAddr
	}

	// Only read forwarded headers if the direct connection is from a trusted proxy.
	if isTrustedProxy(remoteHost) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			// Take the first IP (the original client).
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
	}

	return remoteHost
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
func SubscribeIPKey(ip string) string            { return "subscribe:ip:" + ip }
func CheckoutSessionKey(sessionID string) string { return "checkout:sess:" + sessionID }
func ContactIPKey(ip string) string              { return "contact:ip:" + ip }
func WholesaleApplyIPKey(ip string) string       { return "wholesale-apply:ip:" + ip }
func NewsletterIPKey(ip string) string           { return "newsletter:ip:" + ip }
func WhiteLabelIPKey(ip string) string           { return "white-label:ip:" + ip }
func GlobalIPKey(ip string) string               { return "global:ip:" + ip }

// ServiceReportKey buckets equipment fault reports by wholesale account, not by
// IP: a cafe is one account behind one router, and the barista reporting the
// grinder must not be blocked by the manager who reported the espresso machine.
func ServiceReportKey(customerID string) string { return "service-report:cust:" + customerID }
