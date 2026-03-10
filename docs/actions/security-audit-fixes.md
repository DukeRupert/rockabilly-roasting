# Security Audit Fix List

**Audit date:** 2026-03-10
**Auditor:** Claude Code (security-auditor agent)
**Status:** Open

---

## Priority 1 — Critical (financial impact, auth bypass)

### C1: Payment amount not verified at checkout confirm

- **File:** `internal/web/checkout.go:596–607`
- **Risk:** An attacker can create a Stripe PaymentIntent for $0.01 via the public API, then POST that `payment_intent_id` alongside any cart. The server only checks `pi.Status == Succeeded` but never compares `pi.AmountCents` against the server-recalculated order total. This allows placing full-priced orders for near-zero payment.
- **Fix:** After calling `GetPaymentIntent`, compare `pi.AmountCents` to the amount computed inside the DB transaction (accounting for discounts and tax). If they differ by more than 1 cent, reject with HTTP 422.
- [x] Implement fix
- [ ] Add test covering amount mismatch rejection
- [ ] Verify in staging

### C2: Payment amount not verified at subscription confirm

- **File:** `internal/web/subscribe.go:290–299`
- **Risk:** Identical to C1. `handleSubscribeConfirm` only checks `pi.Status`; `pi.AmountCents` is never compared to `finalPrice * quantity`.
- **Fix:** Same approach as C1 — verify amount matches server-computed total before creating the subscription record.
- [x] Implement fix
- [ ] Add test covering amount mismatch rejection
- [ ] Verify in staging

### C3: QuickBooks query language injection

- **File:** `internal/platform/quickbooks/customers.go:83`
- **Risk:** `queryCustomer` builds a QB query via `fmt.Sprintf` with user-controlled values (customer display name, email). The `escapeQBQuery` function uses backslash escaping, but QB's query language does not recognize backslash as an escape character. A crafted display name can produce malformed or injected queries.
- **Fix:** Whitelist the allowed field names in a switch statement with static query strings per field. URL-encode the value parameter rather than attempting manual escaping. Verify QB's actual escaping rules in their API documentation.
- [x] Research QB query parameterization or correct escaping
- [x] Implement fix
- [x] Add test with adversarial input (single quotes, backslashes)

---

## Priority 2 — High (easy to exploit, significant impact)

### H1: Open redirect on wholesale login

- **File:** `internal/web/customer_auth.go:368–372`
- **Risk:** The `redirect` query parameter is accepted after successful wholesale login and passed directly to `http.Redirect` with no validation. An attacker can craft `/wholesale/login?redirect=https://evil.com` to redirect authenticated users to a phishing site.
- **Fix:** Apply the same path validation used on the `next` parameter in `handleAccountMagicRedeem`: only allow values starting with `/` and not starting with `//`. Reject all others, falling back to `/wholesale/portal`.
- [x] Implement fix
- [ ] Add test for external URL rejection
- [ ] Add test for `//evil.com` rejection

### H2: Prometheus `/metrics` publicly accessible

- **File:** `internal/web/router.go:72`
- **Risk:** The metrics endpoint is mounted on the main mux with no auth. Anyone can query it and observe internal counters — webhook event counts, checkout conversion rates, DB pool stats, River queue depth, subscription counts.
- **Fix:** Either move the metrics handler to a separate internal-only listener (e.g., `:9090` bound to `127.0.0.1`) or add Bearer token authentication with a secret from env.
- [x] Decide approach (separate listener vs auth) — chose separate internal listener
- [x] Implement fix
- [ ] Verify metrics no longer accessible from public port

### H3: Session cookies missing `Secure` flag behind reverse proxy

- **Files:** `internal/web/auth.go:133`, `customer_auth.go:253,364`
- **Risk:** `Secure: r.TLS != nil` evaluates to `false` when TLS terminates at a reverse proxy (nginx, Cloudflare), which is the standard production deployment. Both `hiri_session` and `hiri_customer` cookies are then sent without the Secure flag, allowing browsers to transmit session tokens over plain HTTP.
- **Fix:** Hard-code `Secure: true` on all session cookies. For local development, add an env flag (`INSECURE_COOKIES=true`) that opts out.
- [ ] Implement fix
- [ ] Verify cookies have Secure flag in staging

### H4: Rate limiter trusts `X-Forwarded-For` without proxy allowlisting

- **File:** `internal/platform/ratelimit/limiter.go:63–82`
- **Risk:** `ClientIP()` reads `X-Forwarded-For` and `X-Real-IP` unconditionally. An attacker can rotate their effective IP on every request by changing the header, entirely defeating per-IP rate limits on login, magic link, coupon, and checkout endpoints.
- **Fix:** Add a `TrustedProxies []net.IPNet` config (loaded from env). Only read forwarded headers when `r.RemoteAddr` matches a trusted CIDR. Otherwise use `r.RemoteAddr` directly.
- [x] Implement trusted proxy configuration
- [x] Update `ClientIP()` logic
- [x] Add tests for spoofed header rejection

### H5: Customer email addresses logged in plaintext

- **Files:** `internal/jobs/magic_link_send.go:89`, `invoice_send.go:112`, `wholesale_approved.go:109`, `wholesale_suspended.go:84`
- **Risk:** PII (email addresses) persisted in application logs. Under GDPR/CCPA, this is a compliance concern. Log aggregation systems may retain these indefinitely.
- **Fix:** Replace `"email", customer.Email` log fields with `"customer_id", customer.ID`. If delivery debugging is needed, log the `message_id` returned by the mailer.
- [x] Update all job log statements (7 instances across 7 files)
- [x] Grep for any other email logging instances

---

## Priority 3 — Medium (harder to exploit or limited blast radius)

### M1: `.env` not in `.gitignore`

- **File:** `.gitignore`
- **Risk:** `.env` exists at project root with live secrets. A `git add .` will silently commit it.
- **Fix:** `echo -e ".env\n.env.*" >> .gitignore`
- [ ] Add `.env` patterns to `.gitignore`

### M2: Cart item manipulation without ownership check

- **Files:** `internal/web/cart.go:194–208,226–232`, `wholesale.go:487,509`
- **Risk:** `handleCartUpdateQuantity` and `handleCartRemoveItem` accept an `item_id` UUID without verifying the item belongs to the cookie's cart. An attacker who discovers another cart's item UUID can modify or delete items in another user's cart.
- **Fix:** Add ownership verification — either check `item.CartID == cookieCartID` before mutation, or add `cart_id` to the WHERE clause in the store method (atomic ownership check).
- [ ] Add `cart_id` scoping to store delete/update methods
- [ ] Add tests for cross-cart item manipulation

### M3: QB webhook verifier token not required at startup

- **File:** `cmd/server/main.go:99`
- **Risk:** `QB_WEBHOOK_VERIFIER_TOKEN` is read but not validated. A misconfigured deployment silently rejects all valid QB webhooks.
- **Fix:** At startup, if `QB_CLIENT_ID` is set, require `QB_WEBHOOK_VERIFIER_TOKEN` to also be non-empty. Log a fatal error otherwise.
- [ ] Add startup validation

### M4: `rand.Read` error suppressed in OAuth state generation

- **File:** `internal/web/admin_settings.go:173`
- **Risk:** `//nolint:errcheck` suppresses the error from `crypto/rand.Read`. If the OS random source fails, the state token is all zeros, making CSRF protection trivially bypassable.
- **Fix:** Check the error and return HTTP 500 if `rand.Read` fails.
- [ ] Remove `//nolint:errcheck` and handle the error

### M5: Session IP recorded from proxy address, not client

- **Files:** `internal/web/auth.go:107`, `customer_auth.go:228,325`
- **Risk:** `r.RemoteAddr` is the proxy's IP when behind a reverse proxy. Session audit logs record the wrong IP.
- **Fix:** Use the same `ClientIP(r)` function used for rate limiting (after H4 is fixed to properly resolve client IP).
- [ ] Update login handlers to use `ClientIP(r)`

### M6: No request body size limits on form endpoints

- **File:** `cmd/server/main.go:294–300`
- **Risk:** No `MaxHeaderBytes` configured and no `http.MaxBytesReader` on form-encoded request bodies (only webhook routes have limits). Large form submissions to admin or checkout address endpoints are unbounded.
- **Fix:** Add `MaxHeaderBytes` to the server config. Apply `http.MaxBytesReader` middleware to all non-webhook routes with a reasonable limit (e.g., 1MB).
- [ ] Set `MaxHeaderBytes` on server
- [ ] Add body size limit middleware

### M7: Session cookies use `SameSite=Lax` instead of `Strict`

- **Files:** `internal/web/auth.go:132`, `customer_auth.go:252,363`
- **Risk:** Lax allows session cookies on top-level cross-site navigations. Since there is no OAuth or cross-site redirect flow requiring Lax for the session cookie, Strict provides better CSRF protection.
- **Fix:** Change `SameSite: http.SameSiteLaxMode` to `http.SameSiteStrictMode`.
- [ ] Update all session cookie settings

---

## Priority 4 — Low / Informational

### L1: Float64 rounding in QB invoice amounts

- **File:** `internal/platform/quickbooks/invoices.go:23,30,116–118`
- **Note:** `centsToFloat()` divides by 100.0, which can introduce floating-point rounding. QB rounds on display so this is cosmetic, but should be documented and tested for edge cases.
- [ ] Add comment documenting the rounding behavior
- [ ] Add test for edge-case amounts (e.g., $0.10, $33.33)

### L2: Guessable order numbers

- **File:** `internal/app/checkout.go:175`
- **Note:** `ORD-<unix_milli>` reveals order timing and is enumerable. Not a direct vulnerability (orders are fetched by UUID), but could reveal order volume. Consider shorter random alphanumeric codes.
- [ ] Evaluate switching to random order number format

### L3: Hardcoded dev database password in Docker Compose

- **File:** `docker-compose.yml:5–7`
- **Note:** `localdevpassword` is committed. Low risk since it's dev-only, but developers may reuse passwords.
- [ ] Move to `.env` or document as dev-only

### L4: Health check has no database liveness check

- **File:** `internal/web/router.go:66`
- **Note:** `GET /health` returns 200 unconditionally. Load balancer won't detect DB connectivity loss.
- [ ] Add optional DB ping to health check

### L5: Minimum password length of 8 characters

- **File:** `internal/app/auth.go:287`
- **Note:** Meets NIST minimum but is at the floor. Consider checking against known breach lists (Have I Been Pwned API) or raising minimum to 10+.
- [ ] Evaluate stronger password policy

---

## Passed Checks (no action needed)

These areas were audited and found to be correctly implemented:

- Stripe webhook signature verification with body size limit
- Stripe webhook idempotency via event deduplication
- No hardcoded API keys in source
- `crypto/rand` used for all token generation (no `math/rand`)
- Magic link tokens: 32-byte random, SHA-256 hashed, single-use, 15-min TTL
- Bcrypt password hashing at default cost
- Server-side session validation on every request
- Session revocation on logout
- Customer order IDOR protection (ownership check)
- Customer address scoping via `customerID` parameter
- Customer subscription scoping
- QB OAuth CSRF with HMAC-SHA256 state parameter
- QB OAuth tokens encrypted at rest (AES-256-GCM)
- Parameterized SQL throughout (no string-concatenated queries)
- Templ auto-escaping for HTML (no `text/template` or `template.HTML()`)
- Sanitized error responses (internal details logged, not returned)
- HTTP server timeouts configured (read: 10s, write: 30s, idle: 60s)
- No `net/http/pprof` in production
- Dedicated `http.NewServeMux()` (no default mux)
- Rate limiting on all auth endpoints
- No secrets in git history
- All resource IDs are UUIDs (not sequential integers)
- Integer cents for all monetary calculations
