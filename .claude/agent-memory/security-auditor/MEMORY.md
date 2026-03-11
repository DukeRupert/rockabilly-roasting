# Security Auditor Memory — Rockabilly Roasting / Hiri Platform

## Audit History
- **2026-03-10**: Full initial security audit completed across all 10 OWASP categories.

## Confirmed Secure Patterns
- **Stripe webhook**: Signature verification via `webhook.ConstructEventWithOptions`, idempotency via `WebhookStore.Create` (unique on event ID), body limited to 64KB. Passes.
- **QB OAuth tokens**: Encrypted at rest with AES-256-GCM (`crypto/rand` nonce), advisory lock prevents concurrent refresh races. Passes.
- **Magic link tokens**: `crypto/rand` 32-byte tokens, SHA-256 hashed before DB storage, single-use via `Redeem` (row deletion), 15-minute TTL. Passes.
- **Password hashing**: bcrypt with `bcrypt.DefaultCost`. Passes.
- **Session management**: DB-backed sessions with `Revoke` on logout, expiry checked server-side. Passes.
- **SQL queries**: All pgx queries use `$1`/`$2` parameterized placeholders (confirmed by absence of `fmt.Sprintf.*SELECT`). Passes.
- **Monetary values (internal)**: All prices stored/calculated as integer cents. Tax uses `math.Round(float64 * rate)` — acceptable pattern with integer result.
- **No `math/rand`**: Confirmed absent from codebase.
- **No `net/http/pprof`**: Confirmed absent.
- **No hardcoded Stripe secrets**: Confirmed absent from source.
- **Error responses**: `mapError()` in `respond.go` strips internal error details, exposes only safe messages.
- **IDOR on orders**: `handleAccountOrderShow` manually checks `order.CustomerID == customer.ID`. Passes.
- **IDOR on addresses**: `UpdateAddress`, `DeleteAddress`, `SetDefaultAddress` pass `customerID` to scoped store methods. Passes.
- **QB webhook signature**: `VerifySignature` returns false if token is empty. Route is registered for all deployments; if QB is enabled, verifier token must be set.
- **XSS**: Project uses `templ` (auto-escaping); no `template.HTML()` casts found.

## Known Findings (from 2026-03-10 audit)

### Critical
- **QB injection (SOQL)**: `internal/platform/quickbooks/customers.go:83` — `fmt.Sprintf` builds QB query string with field name concatenated unsanitized (the `field` parameter is internal, but the value uses manual escape which is inadequate). QB's query language injection risk.
- **Payment amount not verified in confirm**: `internal/web/checkout.go` (handleCheckoutConfirm) and `internal/web/subscribe.go` (handleSubscribeConfirm) verify `pi.Status == Succeeded` but do NOT compare `pi.AmountCents` against server-recalculated order total. Attacker can create a PI for $0.01 and use it to confirm a full-price order.

### High
- **Open redirect on wholesale login**: `internal/web/customer_auth.go:368-372` — `redirect` query param used without path validation (unlike the `next` param which has validation). Allows redirect to any URL after login.
- **IP spoofing on rate limiter**: `internal/platform/ratelimit/limiter.go:66` trusts `X-Forwarded-For` header unconditionally without checking if request originates from a trusted proxy. Attackers can bypass rate limits by spoofing this header.
- **Session cookies missing `Secure` flag behind proxies**: `internal/web/auth.go:133` and `customer_auth.go:253,364` set `Secure: r.TLS != nil`, which is false when TLS is terminated at a reverse proxy. Cookies sent insecurely over plain HTTP from proxy to server.
- **Prometheus `/metrics` endpoint public**: `internal/web/router.go:72` — mounted on main mux with no auth. Exposes internal counters, pool stats, queue depth to unauthenticated users.
- **PII in logs**: `internal/jobs/magic_link_send.go:89` logs `customer.Email` directly. Several other job files log emails in success messages.

### Medium
- **`.env` not in `.gitignore`**: `.env` file exists and is untracked (`??`), but `.gitignore` does not contain a `.env` pattern. Risk of accidental commit.
- **QB webhook verifier not required at startup**: `cmd/server/main.go:99` loads `QB_WEBHOOK_VERIFIER_TOKEN` without failing if empty. `VerifySignature` returns false if empty (safe), but there is no startup check to enforce configuration when QB is active.
- **Cart item manipulation without cart ownership check**: `handleCartUpdateQuantity` and `handleCartRemoveItem` (cart.go) accept any `item_id` UUID without verifying it belongs to the cookie's cart. Same for wholesale cart handlers. An attacker who knows another cart's item UUID can manipulate it.
- **Session IP stored from `r.RemoteAddr` not real client IP**: `internal/web/auth.go:107`, `customer_auth.go:228,325` use `r.RemoteAddr` (proxy IP) for IP logging, inconsistent with `ClientIP()` used for rate limiting.
- **`docker-compose.yml` has a weak local dev password**: `localdevpassword` — low risk if only used locally, but worth noting.
- **SameSite=Lax on session cookies**: All session cookies use `SameSiteLaxMode`. `SameSiteStrictMode` would be stronger for session cookies since there's no cross-site navigation requirement.
- **`rand.Read` error ignored in OAuth state generation**: `internal/web/admin_settings.go:173` — `rand.Read(b) //nolint:errcheck`. Suppressed error; if rand fails, `b` remains zeroed and state is predictable.

### Low / Informational
- **float64 for QB invoice amounts**: QB API integration uses `float64` for invoice line amounts (QB's API is dollar-denominated). Internal DB values are int cents; conversion happens at the boundary. Not a precision risk for display but worth documentation.
- **Tax rate stored as `float64`**: `domain.TaxConfig.Rate` — float64 used for the rate, result converted to int cents via `math.Round`. Acceptable but noted.
- **Order number is timestamp-based**: `fmt.Sprintf("ORD-%d", time.Now().UnixMilli())` in `app/checkout.go:175` — not sequential, but predictable/enumerable. Not a security issue for UUIDs used as resource IDs.

## Architecture Notes
- Customer-scoped data: store methods have `Get(ctx, tx, id, customerID)` and `GetByID(ctx, tx, id)` variants. Handlers must use the scoped variant for customer-facing routes.
- Admin routes: all under `/admin/` via `requireStaffSession` middleware. QB OAuth callback is correctly inside `adminMux`.
- Rate limiting: In-memory sliding window store; resets on successful auth. Global limit 300/min per IP. Auth limits: 10 IP/15min, 5 identifier/15min for customers; 5 IP/15min, 3 identifier/15min for staff.
- No JWT; session tokens are random hex strings hashed with SHA-256 before DB storage.
