package ratelimit

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// tooManyRequests writes a 429 response with Retry-After header.
//
// For htmx requests the status stays 429 so logs and metrics read true, but
// htmx swaps nothing on a 4xx — the form just sits there, which reads as a
// frozen page and invites the visitor to keep tapping into a closed bucket.
// So the message rides in an HX-Trigger event instead, which htmx dispatches
// before it decides whether to swap; both layouts turn it into a toast.
// HX-Reswap: none keeps the main swap off regardless.
func tooManyRequests(w http.ResponseWriter, r *http.Request, resetAt time.Time) {
	retryAfter := int(time.Until(resetAt).Seconds())
	if retryAfter < 1 {
		retryAfter = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
	msg := limitedMessage(retryAfter)

	if r.Header.Get("HX-Request") == "true" {
		trigger, _ := json.Marshal(map[string]map[string]string{
			RateLimitedEvent: {"message": msg},
		})
		w.Header().Set("HX-Trigger", string(trigger))
		w.Header().Set("HX-Reswap", "none")
	}

	http.Error(w, msg, http.StatusTooManyRequests)
}

// RateLimitedEvent is the HX-Trigger event name the layouts listen for.
const RateLimitedEvent = "rateLimited"

// limitedMessage tells the visitor how long to wait, rounded up to the
// minute: "try again later" gives them nothing to go on, so they retry at
// once and stay locked out.
func limitedMessage(retryAfter int) string {
	minutes := (retryAfter + 59) / 60
	if minutes <= 1 {
		return "Too many attempts. Please wait a minute and try again."
	}
	return fmt.Sprintf("Too many attempts. Please wait about %d minutes and try again.", minutes)
}

// GlobalLimit returns middleware that applies a per-IP sliding window
// rate limit to all requests. Suitable for general scraping protection.
func GlobalLimit(limiter *Limiter, limit int, window time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := ClientIP(r)
			key := GlobalIPKey(ip)

			allowed, _, resetAt, err := limiter.Allow(r.Context(), key, limit, window)
			if err != nil {
				// On store error, allow the request through — fail open.
				next.ServeHTTP(w, r)
				return
			}
			if !allowed {
				tooManyRequests(w, r, resetAt)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// AuthLimit returns middleware that applies per-IP rate limiting to
// authentication endpoints. It also supports an optional per-identifier
// limit when identifierFn is non-nil — identifierFn extracts the login
// identifier (e.g. email) from the request form.
//
// ipScope names the per-IP counter set and idScope the per-identifier one
// (AuthScopeWholesale and friends). Each sign-in surface gets its own IP
// counters, because their limits differ: when they shared one, four wrong
// wholesale passwords plus a forgot-password request filled the retail
// allowance of 5, and the buyer who had just reset their password was locked
// out of every sign-in form. The identifier scope is separate so that forms
// checking the same password can still share one per-account guess budget
// (AuthScopeLoginGuess).
func AuthLimit(limiter *Limiter, ipScope, idScope string, ipLimit int, idLimit int, window time.Duration, identifierFn func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := ClientIP(r)

			// Check per-IP limit.
			ipKey := AuthIPKey(ipScope, ip)
			allowed, _, resetAt, err := limiter.Allow(r.Context(), ipKey, ipLimit, window)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}
			if !allowed {
				tooManyRequests(w, r, resetAt)
				return
			}

			// Check per-identifier limit if an identifier is present.
			if identifierFn != nil {
				if identifier := identifierFn(r); identifier != "" {
					idKey := AuthIdentifierKey(idScope, HashIdentifier(identifier))
					allowed, _, resetAt, err = limiter.Allow(r.Context(), idKey, idLimit, window)
					if err != nil {
						next.ServeHTTP(w, r)
						return
					}
					if !allowed {
						tooManyRequests(w, r, resetAt)
						return
					}
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}

// EndpointLimit returns middleware that applies a sliding window limit
// using a caller-supplied key function. Useful for coupon/checkout limits
// keyed by session or IP.
func EndpointLimit(limiter *Limiter, limit int, window time.Duration, keyFn func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := keyFn(r)
			if key == "" {
				next.ServeHTTP(w, r)
				return
			}

			allowed, _, resetAt, err := limiter.Allow(r.Context(), key, limit, window)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}
			if !allowed {
				tooManyRequests(w, r, resetAt)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RateLimitHeaders sets standard rate limit response headers.
func RateLimitHeaders(w http.ResponseWriter, limit, remaining int, resetAt time.Time) {
	w.Header().Set("X-RateLimit-Limit", strconv.Itoa(limit))
	w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
	w.Header().Set("X-RateLimit-Reset", fmt.Sprintf("%d", resetAt.Unix()))
}
