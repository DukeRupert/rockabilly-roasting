#!/usr/bin/env bash
# Daily pg_dump of the rr Postgres container, uploaded to Cloudflare R2.
# Invoked by rr-backup.timer. Secrets are loaded from backup.env.

set -euo pipefail

BACKUP_ENV_FILE="${BACKUP_ENV_FILE:-/opt/rockabilly-roasting/backup.env}"
if [[ ! -r "$BACKUP_ENV_FILE" ]]; then
    echo "cannot read $BACKUP_ENV_FILE" >&2
    exit 1
fi
set -a
# shellcheck disable=SC1090
source "$BACKUP_ENV_FILE"
set +a

: "${POSTGRES_CONTAINER:=rr-postgres}"
: "${POSTGRES_USER:=rr}"
: "${POSTGRES_DB:=rr}"
: "${BACKUP_STAGING_DIR:=/var/backups/rockabilly-roasting}"
: "${R2_REMOTE:=r2backups}"
: "${R2_BUCKET:?R2_BUCKET must be set in backup.env}"
: "${POSTMARK_SERVER_TOKEN:?POSTMARK_SERVER_TOKEN required}"
: "${EMAIL_FROM:?EMAIL_FROM required}"
: "${ALERT_EMAIL:?ALERT_EMAIL required}"

ts=$(date -u +%Y%m%dT%H%M%SZ)
filename="rr-${ts}.dump"
local_path="${BACKUP_STAGING_DIR}/${filename}"
host=$(hostname -s)

log() { printf '[%s] %s\n' "$(date -u +%FT%TZ)" "$*"; }

notify_failure() {
    local exit_code=$?
    local line=${1:-?}
    local msg="rr-backup FAILED on ${host} (line ${line}, exit ${exit_code})"
    logger -t rr-backup "$msg"
    if command -v curl >/dev/null && command -v jq >/dev/null; then
        curl -sS -m 15 -X POST "https://api.postmarkapp.com/email" \
            -H "Accept: application/json" \
            -H "Content-Type: application/json" \
            -H "X-Postmark-Server-Token: $POSTMARK_SERVER_TOKEN" \
            -d "$(jq -cn \
                --arg from "$EMAIL_FROM" \
                --arg to "$ALERT_EMAIL" \
                --arg subj "[rr-backup] FAILED on ${host}" \
                --arg body "${msg}

Check: journalctl -u rr-backup.service -n 200 --no-pager" \
                '{From:$from, To:$to, Subject:$subj, TextBody:$body, MessageStream:"outbound"}')" \
            >/dev/null || true
    fi
    exit "$exit_code"
}
trap 'notify_failure $LINENO' ERR

for bin in docker rclone curl jq; do
    command -v "$bin" >/dev/null || { echo "missing dependency: $bin" >&2; exit 1; }
done

if ! docker inspect -f '{{.State.Running}}' "$POSTGRES_CONTAINER" 2>/dev/null | grep -q true; then
    echo "container $POSTGRES_CONTAINER not running" >&2
    exit 1
fi

mkdir -p "$BACKUP_STAGING_DIR"
log "starting dump → ${filename}"

docker exec -i "$POSTGRES_CONTAINER" \
    pg_dump -Fc -U "$POSTGRES_USER" -d "$POSTGRES_DB" \
    > "$local_path"

size=$(stat -c%s "$local_path")
if [[ "$size" -lt 10000 ]]; then
    echo "dump suspiciously small: ${size} bytes" >&2
    exit 1
fi
log "dump complete, ${size} bytes"

: "${RCLONE_BIND:=5.161.245.139}"
RCLONE_OPTS=(--bind "$RCLONE_BIND" --s3-no-check-bucket)

rclone copyto "${RCLONE_OPTS[@]}" "$local_path" \
    "${R2_REMOTE}:${R2_BUCKET}/daily/${filename}"
log "uploaded to ${R2_BUCKET}/daily/${filename}"

remote_size=$(rclone size --bind "$RCLONE_BIND" --json "${R2_REMOTE}:${R2_BUCKET}/daily/${filename}" | jq -r .bytes)
if [[ "$remote_size" != "$size" ]]; then
    echo "remote size ${remote_size} != local ${size}" >&2
    exit 1
fi
log "remote size verified"

# On the 1st of the month, also copy into monthly/ for long-term retention.
if [[ "$(date -u +%d)" == "01" ]]; then
    rclone copyto "${RCLONE_OPTS[@]}" \
        "${R2_REMOTE}:${R2_BUCKET}/daily/${filename}" \
        "${R2_REMOTE}:${R2_BUCKET}/monthly/${filename}"
    log "copied to monthly/${filename}"
fi

# Keep the 3 most recent dumps on the local FS for fast restore; prune older.
ls -1t "$BACKUP_STAGING_DIR"/rr-*.dump 2>/dev/null | tail -n +4 | xargs -r rm -f

log "done"
