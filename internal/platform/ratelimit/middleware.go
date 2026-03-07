package ratelimit

import (
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// tooManyRequests writes a 429 response with Retry-After header.
// For htmx requests it sets HX-Retarget so the error renders inline.
func tooManyRequests(w http.ResponseWriter, r *http.Request, resetAt time.Time) {
	retryAfter := int(time.Until(resetAt).Seconds())
	if retryAfter < 1 {
		retryAfter = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(retryAfter))

	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Retarget", "#rate-limit-error")
		w.Header().Set("HX-Reswap", "innerHTML")
	}

	http.Error(w, "Too many requests. Please try again later.", http.StatusTooManyRequests)
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
func AuthLimit(limiter *Limiter, ipLimit int, idLimit int, window time.Duration, identifierFn func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := ClientIP(r)

			// Check per-IP limit.
			ipKey := AuthIPKey(ip)
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
					idKey := AuthIdentifierKey(HashIdentifier(identifier))
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
