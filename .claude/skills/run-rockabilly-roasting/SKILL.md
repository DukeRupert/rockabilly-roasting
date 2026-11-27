---
name: run-rockabilly-roasting
description: Start, run, smoke-test, screenshot, or drive the Rockabilly Roasting (hiri) Go server locally — storefront, cart, checkout — without sending real email. Use when asked to run the app, launch the dev server, check a change in the browser, curl the cart/checkout API, or stop the local stack.
---

Single Go binary (`cmd/server`) backed by Postgres. Drive it with
`.claude/skills/run-rockabilly-roasting/app.sh`: `up` starts everything
safely, `smoke` walks a cart flow with `curl`, `shot` takes a headless
screenshot, `down` stops it. All paths are relative to the repo root.

> **Never start `cmd/server` here without the driver (or its env blanks).**
> `.env` holds a **live** Postmark token, and the dev DB has queued River
> `email:*` jobs (order confirmations, subscription notices to real customer
> addresses) that the server runs on boot. On 2026-11-25 a plain launch
> sent 11 of them to Postmark. They only bounced because the addresses
> happened to be inactive.

## Prerequisites

Already on the dev box: Docker, `go`, `mage`, `psql`/`pg_isready`,
`chromium` (`/usr/bin/chromium`), `python3`. The Svelte checkout bundle is
committed in `internal/ui/assets/checkout/`; rebuild it only if you changed
`ui/checkout/src`:

```bash
(cd ui/checkout && npm run build)
```

## Run (agent path)

```bash
.claude/skills/run-rockabilly-roasting/app.sh up      # ~4s warm: postgres :5437, migrate, build, server on 127.0.0.1:8080
.claude/skills/run-rockabilly-roasting/app.sh smoke   # add /catalog/guatemala to a fresh cart, print /cart summary + /api/checkout/cart
.claude/skills/run-rockabilly-roasting/app.sh shot /catalog/guatemala   # -> /tmp/rr-run/shot.png (look at it)
.claude/skills/run-rockabilly-roasting/app.sh down
```

Verified `smoke` output:

```
== /cart summary
Subtotal $18.00 Shipping At checkout You&#39;re $32.00 away from free shipping. ...
== /api/checkout/cart
{'subtotal': 1800, 'coupon': None, 'free_shipping_remaining': 3200} 1 item(s)
```

| command | what it does |
|---|---|
| `up` | starts container `rr-pg-dev` (volume `rockabilly-roasting_pgdata`, 127.0.0.1:5437), `mage db:migrate`, builds to `/tmp/rr-run/rr-server`, launches it with outbound creds blanked, then polls `/catalog` |
| `smoke [slug]` | cookie-jar cart flow: scrapes `variant_id` from `/catalog/<slug>`, `POST /cart/add`, prints the cart summary text and checkout-cart JSON |
| `shot <path> [out]` | headless chromium screenshot at 1420x1000 (**no cookies**, see Gotchas) |
| `psql [args]` | psql on the dev DB, e.g. `app.sh psql -Atc "select ..."` |
| `logs` | `tail -f /tmp/rr-run/server.log` (JSON lines) |
| `down` | kills the server, stops `rr-pg-dev` (`--rm`; the volume persists) |

Override with `RR_PORT`, `RR_PG_PORT`, `RR_RUN_DIR`.

### Driving cart/checkout in a real browser

Use Claude in Chrome (the `mcp__claude-in-chrome__*` tools) against
`http://localhost:8080`. The browser has its own cookies, so build the cart
there: open `/catalog/guatemala`, click **Add to cart**, then go to
`/cart` and `/checkout`. Checkout is a Svelte app. Typing into fields with
`form_input` works, and so does the coupon box. For a local-fulfillment
address, use test data with ZIP `99336`. The payment step then shows the
"Free local delivery" / "Ship it to me" radios. Don't click **Pay**.

Coupons: none exist in the dev DB. Create a throwaway one, then delete it afterwards:

```bash
.claude/skills/run-rockabilly-roasting/app.sh psql -qAtc "with d as (insert into discounts(name,type,value) values ('Nudge test 20%','percentage',20) returning id) insert into coupon_codes(discount_id,code) select id,'NUDGETEST' from d returning code;"
.claude/skills/run-rockabilly-roasting/app.sh psql -c "delete from discounts where name='Nudge test 20%'"
```

## Run (human path)

`mage dev` (templ + CSS + build + run) uses `.env` as-is: DB on 5433 (wrong
container on this box) and live Postmark. Don't use it for agent checks.

## Test

```bash
go test ./internal/web/ ./internal/domain/   # ok in ~6s
(cd ui/checkout && npm test)                  # 9 pass
```

## Gotchas

- **Port 5433 is hiri-core's Postgres**, not this project's. `.env`'s
  `DATABASE_URL` then fails with `password authentication failed for user "rr"`.
  `docker compose up` here can't bind 5433 either. The driver mounts the
  compose volume on 5437 instead.
- **Blanking env vars works because godotenv never overrides a set variable,
  even an empty one.** The driver blanks `POSTMARK_SERVER_TOKEN`,
  `SHIPPO_API_KEY`, the `QB_*` client vars and `STAFF_NOTIFICATION_EMAIL`. With
  an empty token, Postmark should reject the calls. That's inferred from the
  `email.NewPostmarkSender` wiring; I haven't yet watched a retry fail
  under it. Inspect the queue with
  `app.sh psql -Atc "select kind,state,attempt,scheduled_at from river_job where kind like 'email:%' and state<>'completed'"`.
- **Startup also runs `shipped_order_auto_deliver`**, which marks old shipped
  orders delivered in the dev DB. That's harmless, but expect the data to change.
- **`BASE_URL` in `.env` is the production domain.** The driver overrides it
  to `http://127.0.0.1:8080` so generated links stay local.
- **`shot` has no session.** Headless chromium starts cookieless, so `/cart`
  renders "Nothing in the bag yet." Use `smoke` (curl jar) for cart state and
  Claude in Chrome for visual checks of cart/checkout.
- **The cart page hides applied coupons**, but its free-shipping hint counts
  them. A $54 cart with a 20% coupon reads "$6.80 away from free shipping after
  your coupon."
- **Blanking `QB_*` only works while the DB has no saved QuickBooks app.** The
  admin-saved config in `qb_app_config` / `qb_credentials` wins over env. Both
  were empty on 2026-11-25. Recheck before trusting the blanks:
  `app.sh psql -Atc "select count(*) from qb_app_config; select count(*) from qb_credentials"`.
- **Counting payment intents:** every Stripe PI is one
  `"path":"/api/checkout/payment-intent"` line in `/tmp/rr-run/server.log`, so
  `rg -c` on it before and after an action shows whether checkout made duplicates.
- **Don't `pkill -f rr-server`** from an agent shell. The pattern matches the
  shell's own command line and kills it (exit 144). The driver kills by PID
  from `/tmp/rr-run/server.pid`.
- **Migrations may be pending** in the dev volume. `up` applies them (goose
  via `mage db:migrate` with the driver's `DATABASE_URL`).

## Troubleshooting

- **`psql: ... port 5433 failed: FATAL: password authentication failed for user "rr"`**:
  you're on hiri-core's DB. Use the driver; it exports the 5437 `DATABASE_URL`.
- **`postmark send: You tried to send to recipient(s) that have been marked as inactive`** in the log:
  the server was started without the blanks and is sending real email. Stop it
  now (`app.sh down`) and relaunch with `app.sh up`.
