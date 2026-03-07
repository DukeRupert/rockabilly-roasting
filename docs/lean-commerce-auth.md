# Lean Commerce — Authentication & Authorization

Two actor types: **Customers** (retail/wholesale buyers) and **Staff** (merchant operators). No system/service accounts in scope — background jobs run under the application's own database credentials and don't need a session.

Session strategy: **database-backed opaque tokens** throughout. Instant revocation, full audit visibility, no cryptographic complexity.

---

## Part 1: Authentication

### Three actor flows, all separate by design

Customers and staff authenticate through different endpoints, produce different session types, and are stored in separate session rows. A customer token cannot be presented to a staff endpoint and vice versa — the session lookup always scopes by `actor_type`. This means a compromised customer account cannot escalate to staff access even if the token somehow crossed surfaces.

Retail and wholesale customers also have separate login routes — they use different auth mechanisms (magic link vs. password) and land in different portals after login.

```
/account/login           → retail customer magic link request
/account/magic           → retail customer magic link redemption (?token=...)
/account/logout

/wholesale/apply         → wholesale account application (public)
/wholesale/login         → wholesale customer password login
/wholesale/logout

/auth/staff/login        → staff password login
/auth/staff/logout
```

There is no shared `/auth/login` endpoint. The split is intentional — it makes the routing unambiguous and allows different auth mechanisms, rate limits, lockout policies, and audit rules per actor type.

---

### Schema

#### Customers

```sql
CREATE TABLE customers (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email             text NOT NULL,
    email_verified    bool NOT NULL DEFAULT false,
    account_type      text NOT NULL DEFAULT 'retail'
                          CHECK (account_type IN ('retail', 'wholesale')),
    password_hash     text,            -- null for retail (magic link only);
                                       -- required for wholesale
    two_fa_enabled    bool NOT NULL DEFAULT false,
    two_fa_method     text CHECK (two_fa_method IN ('magic_link', 'totp')),
    customer_group_id uuid REFERENCES customer_groups(id),
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT uq_customer_email UNIQUE (email)
);
```

A few design notes on this table:

`account_type` distinguishes retail from wholesale customers. Retail customers authenticate via magic link and have no password. Wholesale customers authenticate with a password and are subject to the wholesale approval flow. This is the primary discriminator used by auth middleware and the session system.

`password_hash` is nullable by design — it is always null for retail customers and always set for wholesale customers. The login handler checks `account_type` before attempting password verification, so a retail customer cannot accidentally authenticate via password even if somehow presented with a password field.

`two_fa_enabled` and `two_fa_method` support wholesale 2FA via magic link as a future addition. When `two_fa_enabled = true` on a wholesale customer, the login handler sends a magic link after password verification and waits for token redemption before creating a session. No schema changes are needed to enable this — the `magic_link_tokens` table serves both retail passwordless auth and wholesale 2FA.

There is no `is_guest` column. Retail customers are persistent records created at checkout with no password — they are not "guests" in the disposable sense. A customer's record survives indefinitely and is accessible via magic link if they want to view order history or manage subscriptions.

`customer_group_id` is the pricing gate. Retail vs. wholesale vs. VIP is determined here, and pricing queries filter against it. A customer belongs to exactly one group at a time (simpler than a join table for this use case — promote/demote is a single update).

#### Staff

```sql
CREATE TYPE staff_role AS ENUM (
    'admin',
    'fulfillment',
    'finance',
    'catalog',
    'support'
);

CREATE TABLE staff (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email         text NOT NULL,
    name          text NOT NULL,
    password_hash text NOT NULL,
    role          staff_role NOT NULL DEFAULT 'support',
    is_active     bool NOT NULL DEFAULT true,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT uq_staff_email UNIQUE (email)
);
```

`is_active` is the deactivation flag — setting it false immediately invalidates all active sessions for that staff member (the session lookup checks this on every request). Deactivated staff rows are never deleted; they're retained for audit trail.

`role` is a single enum column, not a join table. Coarse roles don't need the flexibility of a join table and a single column is trivial to index and query. If the role model needs to become more granular later, adding a `permissions jsonb` column is the migration path — not a full schema redesign.

#### Sessions (shared table, actor_type discriminates)

```sql
CREATE TYPE session_actor_type AS ENUM ('customer', 'staff');

CREATE TABLE sessions (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_type   session_actor_type NOT NULL,
    actor_id     uuid NOT NULL,
    token_hash   text NOT NULL,         -- SHA-256 of the raw token sent to client
    ip_address   text,
    user_agent   text,
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT uq_session_token UNIQUE (token_hash)
);

CREATE INDEX idx_sessions_actor ON sessions (actor_type, actor_id);
CREATE INDEX idx_sessions_expires ON sessions (expires_at);
```

A single `sessions` table for both actor types keeps session management (expiry cleanup, "log out all devices") in one place. The `actor_type + actor_id` pair resolves to either `customers.id` or `staff.id` — there's no foreign key because a single FK can't reference two tables. This is intentional: the application enforces referential integrity by always looking up the actor after the session is validated.

**Token handling:**

The raw token is a cryptographically random string (32 bytes → 64 hex characters, or base64url). It is generated by the application, sent to the client exactly once (in the response body or a `Set-Cookie` header), and never stored in plaintext. `token_hash` stores `SHA-256(raw_token)`. On every request, the application hashes the presented token and looks up `token_hash` — if the database is exfiltrated, the raw tokens are not exposed.

**Expiry:**

| Actor | Session lifetime | Rationale |
|---|---|---|
| Retail customer (magic link session) | 30 days | Infrequent logins; long session reduces friction |
| Wholesale customer (remember me) | 30 days | Matches user expectation |
| Wholesale customer (no remember me) | 24 hours | Short enough to limit exposure |
| Staff | 8 hours | Workday-scoped; re-auth each morning |
| Magic link token | 15 minutes | Single use; separate from session lifetime |

A background job (River, scheduled) prunes expired sessions nightly. The index on `expires_at` makes this efficient.

---

### Authentication flow — retail customer (magic link)

Retail customers have no password. Authentication is initiated by requesting a magic link.

```
1. POST /account/login  { email }

2. Look up customer by email
   → Whether found or not: respond with "if you have an account, check your email"
     Never reveal whether the email exists in the system.

3. If customer found and account_type = 'retail':
   Generate raw token: crypto/rand, 32 bytes, hex-encoded
   Compute token_hash: sha256(raw_token)
   INSERT INTO magic_link_tokens (customer_id, token_hash, expires_at = now() + 15min)
   Send email with link: https://example.com/account/magic?token=<raw_token>

4. GET /account/magic?token=<raw_token>
   Compute token_hash = sha256(raw_token)
   UPDATE magic_link_tokens
       SET used_at = now()
       WHERE token_hash = $1
         AND used_at IS NULL
         AND expires_at > now()
       RETURNING *
   → No row returned: render "this link has expired or has already been used"
   → Row returned: create session for customer_id, redirect to ?next param (validated as local path) or /account
```

The atomic `UPDATE ... WHERE used_at IS NULL RETURNING *` prevents double-use under concurrent requests — only one request can set `used_at`.

**Magic link tokens schema:**

```sql
CREATE TABLE magic_link_tokens (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_id uuid NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    token_hash  text NOT NULL UNIQUE,  -- SHA-256 of raw token; raw token never stored
    expires_at  timestamptz NOT NULL,
    used_at     timestamptz,           -- null = unused; set atomically on first use
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_magic_link_tokens_customer ON magic_link_tokens (customer_id);
```

The nightly session cleanup job also prunes expired magic link tokens:
`DELETE FROM magic_link_tokens WHERE expires_at < now()`.

---

### Authentication flow — wholesale customer (password)

```
1. POST /wholesale/login  { email, password, remember_me }

2. Look up customer by email WHERE account_type = 'wholesale'
   → Not found or wrong account_type: return generic "invalid credentials"

3. Check customer.wholesale_status = 'approved'
   → 'pending':   render pending approval message
   → 'suspended': render suspended account message

4. bcrypt.CompareHashAndPassword(customer.password_hash, password)
   → Mismatch: increment failed attempt counter; return generic "invalid credentials"

5. If customer.two_fa_enabled:
   Generate magic link token (same flow as retail, 15min expiry)
   Send to customer email
   Render 2FA prompt: "Check your email for a verification link"
   → Customer clicks link → GET /wholesale/magic?token=...
   → Redeem token (same atomic UPDATE pattern)
   → Create session, redirect to /wholesale/portal

6. Generate raw session token: crypto/rand, 32 bytes, hex-encoded
   Compute token_hash: sha256(raw_token)
   Set expires_at based on remember_me flag

7. INSERT INTO sessions (actor_type='customer', actor_id=customer.id, token_hash, ...)

8. Set-Cookie: session=<raw_token>; HttpOnly; Secure; SameSite=Lax
   Redirect to /wholesale/portal
```

Note that `magic_link_tokens` serves double duty: retail passwordless auth now, wholesale 2FA later. No schema changes are needed to enable wholesale 2FA — just set `two_fa_enabled = true` on the customer record.

---

### Authentication flow (staff login — unchanged)

---

### Session validation middleware (every authenticated request)

```
1. Extract token from Authorization: Bearer <token> header, or from cookie

2. token_hash = sha256(raw_token)

3. SELECT s.*, c.* (or staff.*)
   FROM sessions s
   JOIN customers c ON s.actor_id = c.id    -- (or staff for staff routes)
   WHERE s.token_hash = $1
     AND s.actor_type = 'customer'           -- (or 'staff')
     AND s.expires_at > now()

4. If no row: return 401

5. If customer: check c.is_active (future field) or email_verified
   If staff: check staff.is_active = true
   → If inactive: return 401, do NOT delete session (preserve audit trail)

6. UPDATE sessions SET last_seen_at = now() WHERE id = $session_id
   (fire-and-forget; don't block the request on this)

7. Attach actor to request context
   → ctx = context.WithValue(ctx, actorKey, Actor{Type: "customer", ID: customer.ID, ...})

8. Continue to handler
```

The `last_seen_at` update can be skipped if it was updated within the last 5 minutes — avoids a write on every request for active sessions without losing meaningful data.

---

### Guest checkout

Guest customers follow a slightly different path:

```
1. POST /checkout/start  (no authentication required)
   → Creates customer { is_guest: true, email: null }
   → Creates session with 72hr expiry
   → Returns token (stored client-side for cart continuity)

2. During checkout, guest provides email + optional password
   → If password provided: this becomes a permanent account (is_guest → false, email_verified pending)
   → If no password: account remains guest, order confirmation sent to email

3. On subsequent login with same email:
   → If guest account exists with that email: merge cart, addresses, orders into authenticated account
   → Guest session + guest customer row are retired (soft delete or is_guest stays true, sessions deleted)
```

---

### Password reset

```
reset_tokens
  id          uuid PRIMARY KEY
  actor_type  session_actor_type NOT NULL
  actor_id    uuid NOT NULL
  token_hash  text NOT NULL UNIQUE
  expires_at  timestamptz NOT NULL        -- 1 hour
  used_at     timestamptz                 -- null = not yet used
  created_at  timestamptz NOT NULL DEFAULT now()
```

Reset tokens are single-use (`used_at` set on consumption) and short-lived (1 hour). The same hash-on-store pattern applies — the raw token goes in the email link, the hash is stored. Consuming a reset token does not log the user in — it allows setting a new password, after which they log in normally. This prevents reset tokens from becoming long-lived session substitutes.

---

### Email verification

```
email_verifications
  id          uuid PRIMARY KEY
  customer_id uuid NOT NULL REFERENCES customers(id)
  token_hash  text NOT NULL UNIQUE
  expires_at  timestamptz NOT NULL        -- 24 hours
  verified_at timestamptz                 -- null = not yet verified
  created_at  timestamptz NOT NULL DEFAULT now()
```

Verification is required before a customer can place orders. A customer can request a new verification email if the previous token expired. Verified tokens are retained (with `verified_at` set) rather than deleted, for audit purposes.

---

## Part 2: Authorization

Authorization answers "can this authenticated actor do this thing?" It runs after the session middleware has established identity.

### Customers: authorization by ownership, not roles

Customers have no roles. Their authorization model is resource ownership enforced by query scoping. Every query that returns customer-sensitive data must be scoped to the authenticated customer's ID:

```sql
-- Correct
SELECT * FROM orders WHERE id = $1 AND customer_id = $2

-- Wrong — returns order regardless of who's asking
SELECT * FROM orders WHERE id = $1
```

This is enforced at the repository layer, not the handler layer. Repository functions for customer-facing data always accept `customerID` as a required parameter and include it in every query. A handler that omits it is a compile-time error (the function signature requires it), not a runtime access check.

There are no permission checks for customers — either the row belongs to them (query returns a result) or it doesn't (query returns nothing, handler returns 404). Returning 403 instead of 404 leaks information about the existence of the resource; 404 is correct here.

### Staff: coarse role-based access control

Staff permissions are determined entirely by their `role` column. Roles map to sets of allowed actions. The mapping is defined once in code — not in the database — because it changes only when the business decides to restructure staff responsibilities, which is a deployment event.

#### Role capability matrix

| Capability | admin | fulfillment | finance | catalog | support |
|---|:---:|:---:|:---:|:---:|:---:|
| View orders | ✓ | ✓ | ✓ | | ✓ |
| Update fulfillment status | ✓ | ✓ | | | |
| Issue refunds | ✓ | | ✓ | | |
| View financial reports | ✓ | | ✓ | | |
| Create/edit products | ✓ | | | ✓ | |
| Manage pricing | ✓ | | ✓ | ✓ | |
| View customers | ✓ | | | | ✓ |
| Edit customers | ✓ | | | | |
| Manage staff accounts | ✓ | | | | |
| Create draft orders | ✓ | | | | ✓ |
| Manage inventory | ✓ | ✓ | | ✓ | |

#### Implementation: permission constants + middleware

Permissions are string constants (or typed consts) defined in one place:

```go
const (
    PermViewOrders         = "orders:view"
    PermUpdateFulfillment  = "orders:fulfill"
    PermIssueRefunds       = "orders:refund"
    PermViewReports        = "reports:view"
    PermManageProducts     = "products:write"
    PermManagePricing      = "pricing:write"
    PermViewCustomers      = "customers:view"
    PermEditCustomers      = "customers:write"
    PermManageStaff        = "staff:write"
    PermCreateDraftOrders  = "orders:draft"
    PermManageInventory    = "inventory:write"
)

// rolePermissions maps each role to its allowed permissions.
// This is the single source of truth. Change here, change everywhere.
var rolePermissions = map[staff_role][]string{
    RoleAdmin: {
        PermViewOrders, PermUpdateFulfillment, PermIssueRefunds,
        PermViewReports, PermManageProducts, PermManagePricing,
        PermViewCustomers, PermEditCustomers, PermManageStaff,
        PermCreateDraftOrders, PermManageInventory,
    },
    RoleFulfillment: {
        PermViewOrders, PermUpdateFulfillment, PermManageInventory,
    },
    RoleFinance: {
        PermViewOrders, PermIssueRefunds, PermViewReports, PermManagePricing,
    },
    RoleCatalog: {
        PermManageProducts, PermManagePricing, PermManageInventory,
    },
    RoleSupport: {
        PermViewOrders, PermViewCustomers, PermCreateDraftOrders,
    },
}

// HasPermission checks if a role includes the given permission.
func HasPermission(role StaffRole, perm string) bool {
    for _, p := range rolePermissions[role] {
        if p == perm { return true }
    }
    return false
}
```

Authorization is applied in middleware, not in handlers or services:

```go
// RequirePermission returns HTTP middleware that checks the staff actor's role.
func RequirePermission(perm string) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            actor := StaffFromContext(r.Context())
            if actor == nil {
                http.Error(w, "unauthorized", http.StatusUnauthorized)
                return
            }
            if !HasPermission(actor.Role, perm) {
                http.Error(w, "forbidden", http.StatusForbidden)
                return
            }
            next.ServeHTTP(w, r)
        })
    }
}
```

Routes are wrapped at registration time:

```go
// Staff routes
mux.Handle("POST /admin/orders/{id}/refund",
    staffSessionMiddleware(
        RequirePermission(PermIssueRefunds)(
            handleIssueRefund(deps),
        ),
    ),
)
```

The handler itself contains no authorization logic. It receives a request, trusts that the middleware stack has verified the actor, and does its work. This makes handlers easier to test (no auth setup needed in unit tests) and makes the authorization surface auditable by reading route registrations.

---

### What lives where: the layering rule

```
HTTP request
    │
    ├─▶ Rate limiting middleware     (no actor context yet)
    │
    ├─▶ Session middleware           (establishes actor in context)
    │       └─ Looks up session, attaches actor to ctx
    │
    ├─▶ RequirePermission middleware (staff routes only)
    │       └─ Reads actor from ctx, checks role
    │
    └─▶ Handler
            └─ Reads actor from ctx for ownership scoping (customer routes)
               Calls service/repository functions
               Repository enforces customer_id scoping in queries
```

Domain services and repositories know nothing about HTTP, sessions, or roles. They receive explicit parameters (including `customerID` where needed) and operate on them. This keeps domain logic testable without any auth infrastructure.

---

### Context keys

Actor identity is passed through the request context using unexported key types to prevent collisions:

```go
type contextKey string

const (
    customerContextKey contextKey = "customer"
    staffContextKey    contextKey = "staff"
)

func CustomerFromContext(ctx context.Context) *Customer {
    v, _ := ctx.Value(customerContextKey).(*Customer)
    return v  // nil if not a customer request
}

func StaffFromContext(ctx context.Context) *Staff {
    v, _ := ctx.Value(staffContextKey).(*Staff)
    return v  // nil if not a staff request
}
```

Handlers check for nil explicitly when the route can be reached by both authenticated and unauthenticated actors (e.g. product browsing). For routes that require authentication, the session middleware has already returned 401 before the handler runs — the handler can assume a non-nil actor.

**Why `SameSite=Lax` and not `Strict`?** Strict breaks navigation from external links (e.g. a customer clicking through from an email). Lax blocks cross-site POST requests (CSRF attack vector) while allowing GET navigation. For a commerce site this is the right balance.

---

### WooCommerce customer migration

Existing Rockabilly Roasting customers are imported from WooCommerce. Magic link auth means no password hashes are needed — import the customer record with email only. Customers who want account access visit `/account/login`, enter their WooCommerce email, receive a magic link, and are in. Customers who never log in are unaffected.

**Per customer, import:**

- Email → `customers.email`
- Name → name fields
- Default shipping address → `addresses` table
- Order history → `orders` table (status = fulfilled, payment_status = captured)
- Active subscriptions → `subscriptions` table

All imported customers get `account_type = 'retail'`, `password_hash = null`. No forced re-registration, no password reset emails, no migration friction.

---

## Part 3: Customer Account Portal

Retail customers who have authenticated via magic link have access to a self-service account portal. The portal is gated by the `requireRetailCustomer` middleware and served under `/account/*`.

### Middleware: `requireRetailCustomer`

This middleware runs on all `/account/*` routes (except login/magic/logout which are public). It enforces three checks:

1. **Session exists** — validates the session cookie, looks up the session + customer. Redirects to `/account/login?next=<current_path>` if unauthenticated. The `next` parameter is preserved through the magic link email round-trip: login form passes it as a hidden field → `MagicLinkSendArgs` carries it → email worker appends it to the magic link URL → redemption handler reads it back and validates it's a local path (starts with `/`, not `//`) before redirecting.
2. **Customer is retail** — wholesale customers are redirected to `/wholesale/portal`. The two account types have separate portals by design.
3. **Customer attached to context** — on success, the customer is attached via `auth.WithCustomer(ctx, customer)` so handlers can read it with `auth.CustomerFromContext(ctx)`.

This is separate from `requireCustomerSession` (which is used for wholesale routes and account logout) because it adds the retail-vs-wholesale routing logic.

### Account routes

All routes are registered on an `accountMux` sub-mux, mounted behind `requireRetailCustomer`:

```
GET  /account/{$}                          → redirect to /account/settings
GET  /account/settings                     → profile form (name, email)
POST /account/settings                     → update profile

GET  /account/orders                       → order history list
GET  /account/orders/{id}                  → order detail (line items, shipping, tracking)

GET  /account/subscriptions                → subscription list with plan details
POST /account/subscriptions/{id}/pause     → pause subscription
POST /account/subscriptions/{id}/resume    → resume subscription
POST /account/subscriptions/{id}/cancel    → cancel subscription

GET  /account/addresses                    → address book
POST /account/addresses                    → create address
POST /account/addresses/{id}               → update address
POST /account/addresses/{id}/delete        → delete address (blocked if last address)
POST /account/addresses/{id}/default       → set default address
```

### Ownership enforcement

All account handlers read the authenticated customer from context and pass `customer.ID` to every service/store call. This enforces ownership at the query level per the project's authorization model:

- **Orders:** `GetOrder` fetches the order, then the handler checks `order.CustomerID == customer.ID`. Returns 404 (not 403) if the order belongs to another customer.
- **Subscriptions:** `GetSubscriptionByCustomer(ctx, tx, id, customerID)` scopes the query to the customer. Pause/resume/cancel verify ownership before acting.
- **Addresses:** All address operations pass `customerID` to the store, which includes it in the WHERE clause.

### Template structure

Account pages live in `internal/ui/storefront/account.templ` (single file, not split by section). Each page has a `Content` variant (partial) and a `Page` variant (full page with layout), following the htmx partial rendering pattern. Handlers check `IsHTMX(r)` and render the appropriate variant.

The account layout provides:
- **Desktop:** sidebar navigation (settings, orders, subscriptions, addresses) + content area
- **Mobile:** horizontal scrolling tab bar above the content area

Interactive elements (inline pause/cancel confirmation on subscriptions, show/hide address edit forms) use Alpine.js with `x-data`/`x-show`/`x-cloak`. Alpine.js is loaded via CDN in the storefront layout.

---

**"Log out all devices"** — `DELETE FROM sessions WHERE actor_id = $1 AND actor_type = $2`. One query, all sessions gone. This is the main reason for database-backed sessions over JWTs.

**Staff deactivation** — Setting `staff.is_active = false` immediately invalidates all sessions on the next request. No sessions need to be deleted; the session middleware checks `is_active` on every lookup.

**Concurrent session limit** — Not implemented by default, but trivially addable: before issuing a new session, check `SELECT COUNT(*) FROM sessions WHERE actor_id = $1 AND expires_at > now()`. If over the limit (e.g. 5 for customers), delete the oldest one first.

**Customer group changes** — If a customer is moved from Retail to Wholesale, their existing session remains valid but `customer_group_id` on their customer row has changed. Pricing queries read `customer_group_id` at query time, not at session time — so the new pricing takes effect immediately on the next request, without requiring re-login.

**Admin impersonation** — Admins occasionally need to view the platform as a specific customer (debugging orders, checking pricing). This is implemented by storing an `impersonated_by` field on the session, not by issuing a full customer session. The audit log records all actions taken during an impersonation session. This is a feature to design explicitly when needed, not to bolt on later.
