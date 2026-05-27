# Postgres Backup & Restore Runbook

Production Postgres for Rockabilly Roasting runs in the `rr-postgres` Docker container on the Hetzner VPS (`5.161.245.139`). This runbook covers how backups work, how to verify them, and how to restore.

## What's running

- **Backup script:** `/usr/local/sbin/rr-backup.sh` (source of truth: `ops/backup/rr-backup.sh` in this repo)
- **Timer:** `rr-backup.timer` fires daily at 09:00 UTC (+ up to 5 min jitter)
- **Service:** `rr-backup.service` runs as user `deploy`
- **Secrets:** `/opt/rockabilly-roasting/backup.env` (0600 deploy:deploy)
- **Local staging:** `/var/backups/rockabilly-roasting/` (last 3 dumps kept)
- **Remote:** Cloudflare R2 bucket `rockabilly-roasting-db`
  - `daily/rr-YYYYMMDDTHHMMSSZ.dump` — every run, 30-day lifecycle rule
  - `monthly/rr-YYYYMMDDTHHMMSSZ.dump` — 1st of each month, 365-day lifecycle rule
- **Format:** `pg_dump -Fc` (custom format, compressed, supports partial restore)
- **Alerts:** on failure, the script emails `ALERT_EMAIL` via Postmark and exits non-zero

Quick status checks:
```bash
ssh rr-deploy 'sudo systemctl list-timers rr-backup.timer'
ssh rr-deploy 'sudo journalctl -u rr-backup.service --since "24 hours ago" --no-pager'
```

## Verify a backup is healthy

### 1. Confirm today's dump landed in R2

```bash
ssh rr-deploy '
  set -a; source /opt/rockabilly-roasting/backup.env; set +a
  rclone --bind 5.161.245.139 lsl r2backups:rockabilly-roasting-db/daily/ | tail -5
'
```

Size should be non-trivial (hundreds of KB at 2026-04 scale; grows with data).

### 2. Test-restore into a throwaway container

The only way to know a backup is actually good is to restore it. Do this monthly.

```bash
ssh rr-deploy '
  set -e
  DUMP=$(ls -1t /var/backups/rockabilly-roasting/rr-*.dump | head -1)
  echo "restoring $DUMP"

  docker rm -f rr-restore-test 2>/dev/null || true
  docker run --rm -d --name rr-restore-test -e POSTGRES_PASSWORD=test postgres:17-alpine >/dev/null

  for i in $(seq 1 30); do
      docker exec rr-restore-test pg_isready -U postgres >/dev/null 2>&1 && break
      sleep 0.5
  done

  docker cp "$DUMP" rr-restore-test:/tmp/dump
  docker exec rr-restore-test pg_restore --create --no-owner --no-privileges -U postgres -d postgres /tmp/dump 2>&1 | tail

  docker exec rr-restore-test psql -U postgres -d rr -tAc \
    "SELECT (SELECT COUNT(*) FROM customers) AS customers, (SELECT COUNT(*) FROM subscriptions) AS subs, (SELECT COUNT(*) FROM orders) AS orders;"

  docker rm -f rr-restore-test >/dev/null
'
```

Compare against production:
```bash
ssh rr-deploy 'docker exec rr-postgres psql -U rr -d rr -tAc \
  "SELECT (SELECT COUNT(*) FROM customers), (SELECT COUNT(*) FROM subscriptions), (SELECT COUNT(*) FROM orders);"'
```

Counts should match (allowing for rows written between dump time and now).

## Download a specific backup

From the VPS (IP-allowlisted, will just work):
```bash
ssh rr-deploy '
  set -a; source /opt/rockabilly-roasting/backup.env; set +a
  rclone --bind 5.161.245.139 copy r2backups:rockabilly-roasting-db/daily/rr-YYYYMMDDTHHMMSSZ.dump /tmp/
'
```

From your laptop (VPS down / need to restore elsewhere): the scoped token has an IP filter, so use the **Cloudflare dashboard** — R2 → `rockabilly-roasting-db` → object → Download. Or mint a short-lived unfiltered token if scripting.

## Restore scenarios

### A. Partial restore — one table only

Useful when `TRUNCATE` or bad migration nuked a single table but the rest of prod is fine.

```bash
# On the VPS — dump is already in /var/backups/rockabilly-roasting/
DUMP=/var/backups/rockabilly-roasting/rr-YYYYMMDDTHHMMSSZ.dump

# Extract only the 'customers' table into a separate database first,
# then copy rows back manually. DO NOT restore directly into prod with
# --clean — it will drop the table and all references.

# Safer pattern: restore the full dump into a side database and cherry-pick.
docker exec rr-postgres psql -U rr -d postgres -c "CREATE DATABASE rr_restore;"
docker exec -i rr-postgres pg_restore --no-owner --no-privileges -U rr -d rr_restore < "$DUMP"

# Copy rows back into prod. Exact SQL depends on the situation — talk to
# someone before running it, and wrap in a transaction.
# When done:
docker exec rr-postgres psql -U rr -d postgres -c "DROP DATABASE rr_restore;"
```

### B. Full disaster recovery — production DB is lost

**Read the whole section before typing anything.** Order matters.

#### B.1 Stop the app

```bash
ssh rr-deploy 'cd /opt/rockabilly-roasting && sudo docker compose stop app'
```

This prevents the app from writing to Postgres while you restore, and avoids the app handing out stale sessions mid-restore.

#### B.2 Grab the most recent good dump

```bash
ssh rr-deploy '
  set -a; source /opt/rockabilly-roasting/backup.env; set +a
  # Local copies first (fastest, last 3 dumps)
  ls -lt /var/backups/rockabilly-roasting/rr-*.dump | head
  # Or pull from R2:
  rclone --bind 5.161.245.139 ls r2backups:rockabilly-roasting-db/daily/ | tail
  # Then: rclone copy r2backups:rockabilly-roasting-db/daily/rr-...dump /var/backups/rockabilly-roasting/
'
```

#### B.3 Drop and recreate the `rr` database

```bash
ssh rr-deploy 'docker exec rr-postgres psql -U rr -d postgres -c "DROP DATABASE rr;"'
ssh rr-deploy 'docker exec rr-postgres psql -U rr -d postgres -c "CREATE DATABASE rr OWNER rr;"'
```

If this fails with "database is being accessed by other users", the app didn't fully shut down. Re-run `docker compose stop app` and try again.

#### B.4 Restore

```bash
ssh rr-deploy '
  DUMP=/var/backups/rockabilly-roasting/rr-YYYYMMDDTHHMMSSZ.dump
  docker cp "$DUMP" rr-postgres:/tmp/dump
  docker exec rr-postgres pg_restore --no-owner --no-privileges -U rr -d rr /tmp/dump
  docker exec rr-postgres rm /tmp/dump
'
```

`pg_restore` will print warnings about missing roles and extensions — that's normal because of `--no-owner --no-privileges`. As long as it exits 0, the data is in.

#### B.5 Verify

```bash
ssh rr-deploy 'docker exec rr-postgres psql -U rr -d rr -tAc \
  "SELECT (SELECT COUNT(*) FROM customers), (SELECT COUNT(*) FROM subscriptions), (SELECT COUNT(*) FROM orders);"'
```

Expected: roughly the row counts from the moment the dump was taken.

#### B.6 Restart the app

```bash
ssh rr-deploy 'cd /opt/rockabilly-roasting && sudo docker compose up -d app'
ssh rr-deploy 'sudo docker logs --tail 50 rr-app'
```

Watch for migration success and no connection errors. Hit `https://rockabillyroasting.com/healthz` (or whatever the health endpoint is) to confirm.

#### B.7 Reconcile with external systems

The dump is a point-in-time snapshot. Anything that happened **between dump time and disaster time is lost from our DB** but may still be recorded by external systems:

- **Stripe** — subscription state, customer objects, payment method tokens. Check the Stripe dashboard for webhooks received after dump time; you may need to replay them or manually reconcile.
- **EasyPost** — any shipping labels printed in the gap are already at the carrier; the label metadata is gone from our DB until reconciled.
- **Postmark** — sent email history lives in Postmark, not our DB.
- **River job queue** — any jobs enqueued in the gap are gone. Jobs that had started but not completed at dump time will run again when the app starts (jobs must be idempotent, per `CLAUDE.md`).

For a small gap (hours), reconciling manually from Stripe's dashboard is usually sufficient. For a larger gap, prioritize: subscriptions first (revenue), then orders in-flight, then customer record edits.

## What backups do NOT cover

- **Anything outside the `rr` database** — config, email templates, compiled assets live in the Docker image / repo, not in Postgres.
- **In-flight jobs / sessions** — whatever was in memory is gone. The dump has River job state at dump time only.
- **Point-in-time recovery** — pg_dump gives you ~24h granularity. For sub-hour RPO, move to pgbackrest + WAL archiving.
- **The R2 bucket itself** — if Cloudflare lost the bucket, we're toast. Consider adding a secondary off-site copy (Backblaze B2, S3 cross-account) once the business can justify it.
- **Filesystem-level Postgres state** (pg_hba.conf, roles outside the dump, extensions) — we use stock postgres:17-alpine with no custom setup, so this isn't a concern today. If that changes, note it here.

## Troubleshooting

### Timer didn't fire
```bash
ssh rr-deploy 'sudo systemctl status rr-backup.timer'
ssh rr-deploy 'sudo systemctl list-timers rr-backup.timer'
```
If it's inactive, `sudo systemctl enable --now rr-backup.timer`.

### Last run failed
```bash
ssh rr-deploy 'sudo journalctl -u rr-backup.service -n 200 --no-pager'
```

Common failures:
- **403 Forbidden from R2** — token expired, token rotated, IP allowlist wrong (did the VPS IP change?), or bucket permissions changed. Re-check `/opt/rockabilly-roasting/backup.env` and the Cloudflare token.
- **container rr-postgres not running** — Postgres container is down; unrelated app problem.
- **dump suspiciously small** — pg_dump may have hit a permission issue; run manually: `docker exec rr-postgres pg_dump -Fc -U rr -d rr | wc -c`.
- **Postmark call silently fails** — check Postmark activity feed; token may be revoked or the sender domain's DNS changed.

### Manual run
```bash
ssh rr-deploy 'sudo systemctl start rr-backup.service && sudo journalctl -u rr-backup.service -f'
```

### Rotating R2 credentials
1. Cloudflare dashboard → mint new scoped token for `rockabilly-roasting-db`.
2. `ssh rr-deploy 'sudo nano /opt/rockabilly-roasting/backup.env'` — update `RCLONE_CONFIG_R2BACKUPS_ACCESS_KEY_ID` + `RCLONE_CONFIG_R2BACKUPS_SECRET_ACCESS_KEY`.
3. `ssh rr-deploy 'sudo systemctl start rr-backup.service'` to verify the new token works.
4. Revoke the old token in the Cloudflare dashboard.

## Future hardening (not implemented)

- **pgbackrest + WAL archiving** for point-in-time recovery and lower RPO.
- **Off-site secondary copy** (B2 or S3 cross-account) so we're not reliant on a single R2 bucket.
- **Automated monthly restore test** — systemd timer that runs the throwaway-container restore and emails results.
- **Encrypted dumps** (age/gpg client-side) for defense-in-depth against R2 account compromise.
