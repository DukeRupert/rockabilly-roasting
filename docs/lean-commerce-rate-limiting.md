# Lean Commerce — Rate Limiting

Scope: **authentication endpoints only** (login, register, password reset). These are the highest-risk surfaces — credential stuffing, brute force, and account takeover all start here.

Deployment: **single VPS, single instance**. In-memory state is sufficient and has no external dependency. The design is structured so that swapping the store to Redis/Valkey requires changing one type, not the rate limiting logic.

---

## Why auth endpoints specifically

Different endpoints have different risk profiles. Browsing products and viewing order history are low-risk — abuse is annoying but not dangerous. Auth endpoints are different:

- **Login** — brute force and credential stuffing. An attacker with a leaked password list will hammer this.
- **Register** — account spam, disposable email abuse, bot signups that hold inventory.
- **Password reset** — account takeover. Also: email bombing if the same address can trigger unlimited resets.

Each has a distinct attack pattern and warrants its own limit configuration.

---

## Algorithm: token bucket

The token bucket algorithm is the right choice for auth rate limiting. Each actor (identified by IP or email) gets a bucket with a capacity. Requests consume tokens; tokens replenish at a fixed rate. When the bucket is empty, requests are rejected.

This is preferable to a fixed window (e.g. "10 requests per minute") because it handles bursts naturally — a legitimate user who hasn't logged in for a week can make a few rapid attempts without being blocked, but sustained hammering drains the bucket quickly.

```
Bucket parameters:
  capacity  — maximum tokens (burst allowance)
  rate      — tokens added per second (sustained allowance)
  tokens    — current token count

On each request:
  refill tokens based on time elapsed since last request
  if tokens >= 1: consume 1 token, allow request
  if tokens < 1:  reject with 429, set Retry-After header
```

---

## Rate limit keys

Each limit is keyed by the most specific identifier available. This prevents one actor from being blocked by another's abuse (shared IP at a coffee shop) while still catching targeted attacks.

| Endpoint | Primary key | Secondary key | Rationale |
|---|---|---|---|
| POST /auth/customer/login | `ip:<addr>` | `email:<hash>` | Per-IP catches credential stuffing; per-email catches targeted brute force on a known account |
| POST /auth/staff/login | `ip:<addr>` | `email:<hash>` | Staff endpoint warrants tighter limits on both dimensions |
| POST /auth/customer/register | `ip:<addr>` | — | Email not known until after parsing; IP is the right gate |
| POST /auth/customer/reset-password | `email:<hash>` | `ip:<addr>` | Per-email prevents bombing one address; per-IP prevents automated harvesting |

Email is **hashed** (SHA-256, no salt needed here — this is a cache key, not stored credential material) before use as a map key. This avoids storing plaintext emails in memory and keeps key length uniform.

---

## Limit configurations

Tighter than you might expect. Legitimate users do not attempt login 10 times in 30 seconds.

```go
type LimitConfig struct {
    Capacity float64       // max tokens (burst)
    Rate     float64       // tokens per second
    Window   time.Duration // for cleanup — how long before idle bucket is evicted
}

var limitConfigs = map[string]LimitConfig{
    // Customer login: 5 attempts burst, then ~1 per 10s sustained
    "customer.login.ip":    {Capacity: 5,  Rate: 0.1,  Window: 15 * time.Minute},
    "customer.login.email": {Capacity: 5,  Rate: 0.1,  Window: 15 * time.Minute},

    // Staff login: tighter — staff accounts are high-value targets
    "staff.login.ip":    {Capacity: 3,  Rate: 0.05, Window: 15 * time.Minute},
    "staff.login.email": {Capacity: 3,  Rate: 0.05, Window: 15 * time.Minute},

    // Registration: 10 accounts per IP per hour is generous; bots need far more
    "customer.register.ip": {Capacity: 10, Rate: 0.003, Window: time.Hour},

    // Password reset: 3 reset emails per address per hour
    "customer.reset.email": {Capacity: 3,  Rate: 0.001, Window: time.Hour},
    "customer.reset.ip":    {Capacity: 10, Rate: 0.003, Window: time.Hour},
}
```

---

## The store interface

The store interface is the seam between rate limiting logic and the backing implementation. The logic never touches a map or a Redis client directly — it calls through this interface. Swapping implementations is a one-line change at startup.

```go
// Store manages token bucket state.
// Implementations must be safe for concurrent use.
type Store interface {
    // Allow checks whether the key has available tokens.
    // Returns (allowed, remaining tokens, time until next token).
    Allow(key string, cfg LimitConfig) (bool, float64, time.Duration)
}
```

---

## In-memory implementation

```go
type bucket struct {
    tokens    float64
    lastRefil time.Time
    mu        sync.Mutex
}

func (b *bucket) allow(cfg LimitConfig) (bool, float64, time.Duration) {
    b.mu.Lock()
    defer b.mu.Unlock()

    now := time.Now()
    elapsed := now.Sub(b.lastRefil).Seconds()
    b.lastRefil = now

    // Refill tokens based on elapsed time, capped at capacity
    b.tokens = min(cfg.Capacity, b.tokens+elapsed*cfg.Rate)

    if b.tokens >= 1 {
        b.tokens--
        return true, b.tokens, 0
    }

    // Time until next token is available
    waitSeconds := (1 - b.tokens) / cfg.Rate
    return false, 0, time.Duration(waitSeconds * float64(time.Second))
}

type InMemoryStore struct {
    mu      sync.RWMutex
    buckets map[string]*bucket
}

func NewInMemoryStore() *InMemoryStore {
    s := &InMemoryStore{
        buckets: make(map[string]*bucket),
    }
    go s.cleanup()
    return s
}

func (s *InMemoryStore) Allow(key string, cfg LimitConfig) (bool, float64, time.Duration) {
    s.mu.RLock()
    b, ok := s.buckets[key]
    s.mu.RUnlock()

    if !ok {
        s.mu.Lock()
        // Double-check after acquiring write lock
        if b, ok = s.buckets[key]; !ok {
            b = &bucket{tokens: cfg.Capacity, lastRefil: time.Now()}
            s.buckets[key] = b
        }
        s.mu.Unlock()
    }

    return b.allow(cfg)
}

// cleanup evicts idle buckets to prevent unbounded memory growth.
// Runs every 5 minutes; evicts buckets not seen within their Window.
func (s *InMemoryStore) cleanup() {
    ticker := time.NewTicker(5 * time.Minute)
    for range ticker.C {
        s.mu.Lock()
        now := time.Now()
        for key, b := range s.buckets {
            b.mu.Lock()
            // Evict if bucket hasn't been accessed within its window
            // (approximated by checking lastRefil)
            if now.Sub(b.lastRefil) > 15*time.Minute {
                delete(s.buckets, key)
            }
            b.mu.Unlock()
        }
        s.mu.Unlock()
    }
}
```

---

## Redis migration path

When the platform moves to multiple instances, replace `InMemoryStore` with a Redis implementation of the same `Store` interface. The rate limiting middleware, limit configurations, and response headers all stay identical.

The standard Redis pattern for token bucket rate limiting uses a Lua script — executed atomically on the Redis server — to read, update, and return the bucket state in a single round trip.

```go
// RedisStore implements Store using Redis + Lua for atomic bucket operations.
// Drop-in replacement for InMemoryStore.
type RedisStore struct {
    client *redis.Client
    script *redis.Script
}

// The Lua script runs atomically on Redis, avoiding race conditions
// between the read and write of bucket state.
const luaScript = `
local key      = KEYS[1]
local capacity = tonumber(ARGV[1])
local rate     = tonumber(ARGV[2])
local now      = tonumber(ARGV[3])

local data = redis.call('HMGET', key, 'tokens', 'last_refill')
local tokens     = tonumber(data[1]) or capacity
local last_refill = tonumber(data[2]) or now

local elapsed = now - last_refill
tokens = math.min(capacity, tokens + elapsed * rate)

local allowed = 0
local wait_ms = 0
if tokens >= 1 then
    tokens = tokens - 1
    allowed = 1
else
    wait_ms = math.ceil((1 - tokens) / rate * 1000)
end

redis.call('HMSET', key, 'tokens', tokens, 'last_refill', now)
redis.call('PEXPIRE', key, 900000) -- 15 min TTL

return {allowed, math.floor(tokens), wait_ms}
`

func (s *RedisStore) Allow(key string, cfg LimitConfig) (bool, float64, time.Duration) {
    now := float64(time.Now().UnixMilli()) / 1000.0
    result, err := s.script.Run(
        context.Background(), s.client,
        []string{key},
        cfg.Capacity, cfg.Rate, now,
    ).Int64Slice()

    if err != nil {
        // On Redis failure, fail open — don't block legitimate users
        // because the rate limit store is unavailable.
        // Log the error and alert; don't silently swallow it.
        return true, 0, 0
    }

    allowed := result[0] == 1
    remaining := float64(result[1])
    retryAfter := time.Duration(result[2]) * time.Millisecond
    return allowed, remaining, retryAfter
}
```

The fail-open decision on Redis error is deliberate and worth calling out explicitly: if the rate limit store is unavailable, the correct behavior is to allow requests through rather than block all authentication. A Redis outage should not lock every user out of the platform. Log the error, alert on it, but don't compound the outage with a self-inflicted lockout.

---

## Middleware

The rate limiting middleware runs before session validation — you don't need to know who someone is to apply IP-based limits, and applying limits before auth work saves CPU on blocked requests.

```go
type Limiter struct {
    store Store
}

func NewLimiter(store Store) *Limiter {
    return &Limiter{store: store}
}

// Limit returns middleware that applies the named limit configuration.
// keyFn extracts the rate limit key from the request.
func (l *Limiter) Limit(configName string, keyFn func(*http.Request) string) func(http.Handler) http.Handler {
    cfg, ok := limitConfigs[configName]
    if !ok {
        panic(fmt.Sprintf("rate limiter: unknown config %q", configName))
    }

    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            key := keyFn(r)
            allowed, remaining, retryAfter := l.store.Allow(key, cfg)

            // Always set headers so clients can back off gracefully
            w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%.0f", cfg.Capacity))
            w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%.0f", remaining))

            if !allowed {
                w.Header().Set("Retry-After", fmt.Sprintf("%.0f", retryAfter.Seconds()))
                w.Header().Set("X-RateLimit-Reset", fmt.Sprintf("%d",
                    time.Now().Add(retryAfter).Unix()))

                // Log rate limit hits — useful for detecting active attacks
                LoggerFromContext(r.Context()).Warn("rate_limit.exceeded",
                    "config",      configName,
                    "key",         key,
                    "retry_after", retryAfter.Seconds(),
                )

                http.Error(w, "too many requests", http.StatusTooManyRequests)
                return
            }

            next.ServeHTTP(w, r)
        })
    }
}
```

---

## Applying multiple limits per endpoint

Login applies two limits — per-IP and per-email — both must pass. The email key is only available after parsing the request body, so it's extracted lazily via the key function.

```go
// Route registration
mux.Handle("POST /auth/customer/login",
    limiter.Limit("customer.login.ip",
        func(r *http.Request) string {
            return "ip:" + realIP(r)
        },
    )(
        limiter.Limit("customer.login.email",
            func(r *http.Request) string {
                // Body is parsed once and stored in context by a body-parsing middleware
                // that runs before rate limiting. Key is hashed email.
                email := emailFromContext(r.Context())
                return "email:" + sha256hex(email)
            },
        )(
            handleCustomerLogin(deps),
        ),
    ),
)
```

One practical detail: the email key function requires the request body to already be parsed. The body-parsing middleware must run before the email-keyed rate limiter. IP-keyed limits have no such dependency and can run first, which means they gate the request before any body parsing occurs — saving work on blocked requests.

Order of middleware on auth endpoints:

```
Request
  │
  ├─▶ IP rate limit        (no body needed — fast gate)
  ├─▶ Body parsing         (parse JSON once, store in context)
  ├─▶ Email rate limit     (reads email from context)
  ├─▶ Session middleware   (not applicable on login — skipped)
  └─▶ Handler
```

---

## IP extraction

`r.RemoteAddr` is not the real client IP when the application is behind a reverse proxy (nginx, Caddy). The real IP is in `X-Forwarded-For` or `X-Real-IP`. This must be extracted carefully — these headers can be spoofed by clients if the proxy doesn't strip and rewrite them.

```go
// realIP extracts the client IP, trusting the proxy header only
// if the immediate connection is from a trusted proxy address.
func realIP(r *http.Request) string {
    // Only trust X-Real-IP / X-Forwarded-For if request came from
    // a known proxy (localhost or internal network).
    remoteIP, _, _ := net.SplitHostPort(r.RemoteAddr)
    if isTrustedProxy(remoteIP) {
        if ip := r.Header.Get("X-Real-IP"); ip != "" {
            return ip
        }
        if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
            // XFF may be a comma-separated list; take the first (leftmost) IP
            return strings.TrimSpace(strings.SplitN(xff, ",", 2)[0])
        }
    }
    return remoteIP
}

func isTrustedProxy(ip string) bool {
    trusted := []string{"127.0.0.1", "::1", "10.0.0.0/8"}
    for _, cidr := range trusted {
        if strings.Contains(cidr, "/") {
            _, network, _ := net.ParseCIDR(cidr)
            if network.Contains(net.ParseIP(ip)) { return true }
        } else if ip == cidr {
            return true
        }
    }
    return false
}
```

If `X-Forwarded-For` is blindly trusted without checking the source, any client can set `X-Forwarded-For: 1.2.3.4` and bypass the IP-based rate limit entirely by rotating the spoofed header value. The trusted proxy check closes this.

---

## Metrics integration

Rate limit hits are worth tracking in Prometheus — a spike in `rate_limit_hits_total` for `customer.login.ip` at 3am is an active credential stuffing attack.

```go
// Add to Registry
RateLimitHitsTotal *prometheus.CounterVec
// Labels: config (limit name), key_type (ip | email)

// Increment in middleware when a request is blocked
reg.RateLimitHitsTotal.WithLabelValues(configName, keyType).Inc()
```

Alert on sustained rate limit hits:

```yaml
- alert: CredentialStuffingDetected
  expr: rate(rate_limit_hits_total{config=~".*login.*"}[5m]) > 10
  for: 2m
  annotations:
    summary: "Elevated login rate limit hits — possible credential stuffing"
```

---

## What this design does not cover

**Application-layer bot detection** — IP + email rate limiting stops naive bots and distributed attacks where each IP makes many requests. It does not stop sophisticated distributed attacks where each IP makes exactly one request. That requires behavioral analysis or CAPTCHA, which is an escalation path if attacks materialize.

**Webhook endpoints** — not rate limited here because they're protected by signature verification, which is a stronger gate than rate limiting for that surface.

**General API browsing** — deliberately out of scope for now. If scraping becomes a problem, the same `Store` interface and `Limiter` middleware apply — add a new `LimitConfig` entry and wrap the relevant routes.
