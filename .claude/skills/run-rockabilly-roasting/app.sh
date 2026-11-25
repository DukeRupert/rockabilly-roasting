#!/usr/bin/env bash
# Driver for running the Rockabilly (hiri) server locally and poking it.
#
#   app.sh up            start dev Postgres, migrate, build, launch server (background)
#   app.sh smoke [slug]  curl a cart flow: add product to cart, show /cart summary + checkout cart JSON
#   app.sh shot <path> [out.png]   headless-chromium screenshot of a public page
#   app.sh psql [args]   psql against the dev DB
#   app.sh logs          tail the server log
#   app.sh down          stop server and dev Postgres
#
# Run from the repo root. Everything binds to loopback.
#
# SAFETY: .env holds LIVE Postmark/Shippo/QuickBooks credentials, and the dev DB
# has queued River email jobs that fire on boot. `up` blanks those env vars;
# godotenv never overrides a variable that is already set (even to ""), so the
# blanks win. Never launch cmd/server from this repo without them.
set -euo pipefail

RUN_DIR=${RR_RUN_DIR:-/tmp/rr-run}
PG_NAME=rr-pg-dev
PG_PORT=${RR_PG_PORT:-5437}
APP_ADDR=127.0.0.1:${RR_PORT:-8080}
BASE=http://${APP_ADDR}
export DATABASE_URL="postgres://rr:firefly@127.0.0.1:${PG_PORT}/rr?sslmode=disable"
mkdir -p "$RUN_DIR"

die() { echo "app.sh: $*" >&2; exit 1; }

pg_up() {
  if ! docker ps --format '{{.Names}}' | grep -qx "$PG_NAME"; then
    # Reuses the compose project's data volume; compose itself can't start
    # because its 5433 mapping collides with hiri-core's Postgres.
    docker run -d --rm --name "$PG_NAME" \
      -e POSTGRES_USER=rr -e POSTGRES_PASSWORD=firefly -e POSTGRES_DB=rr \
      -p "127.0.0.1:${PG_PORT}:5432" \
      -v rockabilly-roasting_pgdata:/var/lib/postgresql/data postgres:17 >/dev/null
  fi
  timeout 30 bash -c "until pg_isready -h 127.0.0.1 -p $PG_PORT -q; do sleep 0.5; done" \
    || die "postgres did not come up on $PG_PORT"
}

server_pid() { [ -f "$RUN_DIR/server.pid" ] && cat "$RUN_DIR/server.pid"; }

cmd_up() {
  pg_up
  mage db:migrate 2>&1 | tail -1
  go build -o "$RUN_DIR/rr-server" ./cmd/server
  if pid=$(server_pid) && kill -0 "$pid" 2>/dev/null; then kill "$pid"; sleep 1; fi
  POSTMARK_SERVER_TOKEN='' SHIPPO_API_KEY='' \
  QB_CLIENT_ID='' QB_CLIENT_SECRET='' QB_WEBHOOK_VERIFIER_TOKEN='' \
  STAFF_NOTIFICATION_EMAIL='' DISABLE_RENEWAL_SCHEDULER=1 \
  ADDR="$APP_ADDR" BASE_URL="$BASE" \
    setsid nohup "$RUN_DIR/rr-server" >"$RUN_DIR/server.log" 2>&1 &
  echo $! >"$RUN_DIR/server.pid"
  timeout 30 bash -c "until curl -sf -o /dev/null $BASE/catalog; do sleep 0.5; done" \
    || { tail -20 "$RUN_DIR/server.log"; die "server did not answer on $BASE"; }
  echo "up: $BASE  (log $RUN_DIR/server.log, db 127.0.0.1:$PG_PORT)"
}

cmd_smoke() {
  local slug=${1:-guatemala} jar="$RUN_DIR/cookies.txt" vid
  rm -f "$jar"
  vid=$(curl -sf -c "$jar" -b "$jar" "$BASE/catalog/$slug" \
        | grep -oE 'name="variant_id"[^>]*value="[0-9a-f-]{36}"' | grep -oE '[0-9a-f-]{36}' | head -1)
  [ -n "$vid" ] || die "no variant_id on /catalog/$slug"
  curl -sf -c "$jar" -b "$jar" -o /dev/null -X POST \
       -d "variant_id=$vid" -d "quantity=1" "$BASE/cart/add" || die "add to cart failed"
  echo "== /cart summary"
  curl -sf -b "$jar" "$BASE/cart" | sed 's/<[^>]*>/ /g' | tr -s ' \n' ' ' \
    | grep -oE 'Subtotal.{0,160}' | head -1
  echo
  echo "== /api/checkout/cart"
  curl -sf -b "$jar" "$BASE/api/checkout/cart" \
    | python3 -c 'import json,sys; d=json.load(sys.stdin); print({k:d.get(k) for k in ("subtotal","coupon","free_shipping_remaining")}, len(d["items"]), "item(s)")'
}

cmd_shot() {
  local path=${1:?usage: app.sh shot /path [out.png]} out=${2:-$RUN_DIR/shot.png}
  chromium --headless=new --disable-gpu --no-sandbox --hide-scrollbars \
    --window-size=1420,1000 --screenshot="$out" "$BASE$path" 2>/dev/null
  echo "$out"
}

cmd_down() {
  if pid=$(server_pid) && kill -0 "$pid" 2>/dev/null; then kill "$pid"; fi
  rm -f "$RUN_DIR/server.pid"
  docker stop "$PG_NAME" >/dev/null 2>&1 || true
  echo down
}

case ${1:-} in
  up) cmd_up ;;
  smoke) shift; cmd_smoke "$@" ;;
  shot) shift; cmd_shot "$@" ;;
  psql) shift; psql "$DATABASE_URL" "$@" ;;
  logs) tail -f "$RUN_DIR/server.log" ;;
  down) cmd_down ;;
  *) sed -n '2,12p' "$0"; exit 1 ;;
esac
