package ratelimit

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
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

// directlyExposed records that this deployment intends to see client sockets
// directly, so falling back to RemoteAddr is correct and needs no report.
var directlyExposed atomic.Bool

// SetDirectlyExposed marks this service as internet-facing, silencing the
// untrusted-forwarder report. Call only when no reverse proxy sits in front.
func SetDirectlyExposed() { directlyExposed.Store(true) }

// reportedUntrusted ensures the report below is emitted once per process. The
// condition is a static misconfiguration, not an event: one line names the
// address to fix, and repeating it per request would bury it.
var reportedUntrusted atomic.Bool

// reportUntrustedForwarder fires when a peer sends X-Forwarded-For but is not a
// trusted proxy. That means a reverse proxy is in front of us and we are
// discarding what it tells us, so every request resolves to the same peer
// address and every per-IP rate limit collapses into a single global bucket.
//
// This has happened in production and went unnoticed for months, because the
// limiter keeps "working" — just globally (docs/security/rate-limiting-TODO.md).
// The subnet moves whenever the compose network is recreated, so the fix drifts
// out of date on its own. The report names the peer address, which is exactly
// the value TRUSTED_PROXIES needs to contain.
func reportUntrustedForwarder(peer string) {
	if directlyExposed.Load() || reportedUntrusted.Swap(true) {
		return
	}
	slog.Error("untrusted proxy forwarding",
		"peer", peer,
		"effect", "per-IP rate limits share one bucket and logged client IPs are all this peer",
		"fix", "set TRUSTED_PROXIES to a range containing "+peer)
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

// trustedHops is how many proxies of our own sit between the internet and this
// process. Today that is one: the host-level Caddy. If a CDN (Cloudflare
// proxying) is ever put in front of Caddy, Caddy needs `trusted_proxies` set
// *and* this must become 2 — otherwise the right-most entry is Cloudflare's
// edge address rather than the visitor's.
const trustedHops = 1

// ClientIP extracts the real client IP from the request.
//
// Forwarded headers are only honoured when the direct connection comes from a
// configured trusted proxy CIDR; otherwise RemoteAddr is used, so a request
// that reaches this process directly cannot name its own address.
//
// Within X-Forwarded-For we take the entry our own proxy wrote — counting
// trustedHops in from the right — never the left-most one. The left-most entry
// is whatever the caller claimed, and this value keys the rate limiter and
// lands in audit-adjacent logs: trusting it would let a visitor pin abuse on an
// arbitrary address, or evade a limiter by rotating the header.
//
// Caddy with `trusted_proxies` unset (the current fleet config) *replaces*
// X-Forwarded-For with the immediate peer rather than appending, so there is
// exactly one entry today and left and right agree. That is a property of the
// proxy config, not a guarantee — counting from the right stays correct when it
// changes.
func ClientIP(r *http.Request) string {
	remoteHost := stripPort(r.RemoteAddr)

	// Only read forwarded headers if the direct connection is from a trusted proxy.
	if isTrustedProxy(remoteHost) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			// Count in from the right: the last entry was written by the proxy
			// nearest us, the one before it by the proxy before that.
			i := len(parts) - trustedHops
			if i < 0 {
				i = 0
			}
			if ip := stripPort(strings.TrimSpace(parts[i])); ip != "" {
				return ip
			}
		}
		if xri := strings.TrimSpace(r.Header.Get("X-Real-IP")); xri != "" {
			return stripPort(xri)
		}
	}

	// Falling back to the socket address is correct when nothing is forwarding.
	// If the peer *did* forward, it is a proxy we have not been told to trust,
	// and this value is about to be the same for every visitor.
	if !isTrustedProxy(remoteHost) &&
		(r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("X-Real-IP") != "") {
		reportUntrustedForwarder(remoteHost)
	}

	return remoteHost
}

// stripPort removes a trailing :port from an address, leaving the bare IP.
// Logged and rate-limited addresses must never carry a port — the same visitor
// gets a new source port on every connection.
func stripPort(addr string) string {
	if addr == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	// Not host:port — either a bare IP, or a bare IPv6 address whose colons
	// SplitHostPort chokes on. Both are already what we want.
	return strings.Trim(addr, "[]")
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
