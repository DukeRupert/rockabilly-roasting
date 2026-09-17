# Rate limiting — findings & TODO

**Status:** the production defect (item 1) is **still unfixed** — it is an
operator action on the prod box, not a code change. Items 2 and 3 are done.
**Found:** 2026-07-27, while tracing a customer login failure through the app logs.
**Updated:** 2026-09-17, alongside the fleet logging-standard branch.

Per-IP auth rate limiting is not working in production. Every request resolves to
the same client IP, so all per-IP limits share a single global bucket. Items
ordered by impact; check off as completed.

---

## High impact

- [ ] **1. `TRUSTED_PROXIES` points at the wrong Docker subnet, collapsing every
  per-IP limit into one global bucket.**

  `ClientIP` (`internal/platform/ratelimit/limiter.go:130`) only honors
  `X-Forwarded-For` / `X-Real-IP` when the direct peer is a configured trusted
  proxy — correct design, it stops clients spoofing their own IP. But production
  is configured with:

  ```
  TRUSTED_PROXIES=172.18.0.0/16,127.0.0.0/8
  ```

  while the compose network actually runs on `172.19.0.0/16`, gateway
  `172.19.0.1`:

  ```
  $ docker network inspect rockabilly-roasting_rr-network \
      --format '{{range .IPAM.Config}}{{.Subnet}} gw={{.Gateway}}{{end}}'
  172.19.0.0/16 gw=172.19.0.1
  ```

  `172.19.0.1` is not inside `172.18.0.0/16`, so `isTrustedProxy` returns false,
  the forwarded headers are discarded, and `ClientIP` falls through to
  `RemoteAddr` — the Caddy container's gateway address — for **every request from
  every client**.

  This is directly visible in the logs, which log the same function
  (`internal/web/middleware.go:83` logs `ratelimit.ClientIP(r)`): every line in
  production reads `"remote_ip":"172.19.0.1"`, across entirely unrelated user
  agents.

  Almost certainly config drift rather than a typo — the subnet moves when the
  compose network is recreated, and `TRUSTED_PROXIES` was not updated with it.

  **Consequences:**

  - *Availability.* `AuthIPLimit` is 10 attempts / 15 min, `StaffIPLimit` is 5,
    `MagicLinkIPLimit` is 5 — all now global rather than per-IP. Ten failed
    logins from any customer anywhere locks the login form for **every**
    customer for 15 minutes; five for staff. That is a trivial denial of
    service, and at current traffic it may already fire occasionally on its own.
  - *Forensics.* `sessions.ip_address` and audit IPs record `172.19.0.1` for
    everyone, so neither can attribute an action to a source.

  The per-identifier limits (`AuthIdentifierLimit` = 5, keyed on a hash of the
  email) are unaffected — they never involved the IP. So credential stuffing
  against one account is still bounded; it is the per-IP layer specifically that
  is inert.

  **Fix:** set `TRUSTED_PROXIES=172.19.0.0/16,127.0.0.0/8` in
  `/opt/rockabilly-roasting/.env` and restart the app. Verify by tailing the
  logs and confirming `remote_ip` shows real client addresses.

  **Re-confirm the subnet before applying this** — `172.19.0.0/16` was observed
  on 2026-07-27 and moves whenever the compose network is recreated. Since item
  2, the app itself names the correct address: on the first forwarded request
  from an untrusted peer it logs, once, `msg":"untrusted proxy forwarding"` at
  ERROR with the `peer` field set to the address `TRUSTED_PROXIES` must contain.

  Note that the app-side changes on the logging-standard branch do **not** fix
  this. A configured-but-wrong value takes precedence over any default, so the
  bucket stays global until this value is corrected on the box.

  Do not simply widen the range to all of `172.16.0.0/12` to make the problem go
  away — a trusted-proxy list that covers more than the actual proxy is how IP
  spoofing gets reintroduced.

- [x] **2. Nothing detects the misconfiguration recurring.** *(done 2026-09-17 —
  `reportUntrustedForwarder` in `platform/ratelimit/limiter.go`: one ERROR line
  per process when a peer forwards but is not trusted, naming the peer address.
  Chosen over pinning the subnet because the prod compose file is not in this
  repo. Pinning it is still worth doing and would remove the drift rather than
  reporting it.)* The subnet will move
  again the next time the compose network is recreated, and the symptom is
  silent: limits keep "working", just globally. Options, cheapest first:

  - Log a warning at startup when `SetTrustedProxies` is configured but the
    first N requests all resolve to a single non-forwarded IP.
  - Pin the subnet explicitly in `docker-compose.yml` under `networks:` so it
    cannot drift from the env value.
  - Assert on it in a deploy smoke check.

  Pinning the subnet is the most direct — it removes the drift rather than
  detecting it.

## Worth considering

- [x] **3. There is no test covering the trusted-proxy path.** *(was already
  false when written — `clientip_test.go` has covered both directions since
  before this doc. Extended 2026-09-17 with the forged-prefix, port-stripping,
  IPv6 and untrusted-forwarder-report cases.)* `ClientIP` has the
  subtle branch here and this defect would have been caught by a table test
  asserting that an untrusted peer's `X-Forwarded-For` is ignored *and* that a
  trusted peer's is honored. Cheap to add, no database needed.

- [ ] **4. Rate-limit rejections are not observable.** There is no metric or
  distinct log line when a limiter rejects, so a global lockout of the kind
  described in item 1 would surface only as customer complaints. A counter
  labelled by limiter name would make it visible on the existing Prometheus
  setup.
