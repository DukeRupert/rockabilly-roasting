#!/usr/bin/env bash
# Install a prepared OSRM dataset on the production host and restart the
# router. Runs on prod; the tarball comes from osrm-build.sh on angmar.
#
# This never preprocesses anything — that is the point. Unpack, swap, restart.
# Peak memory here is a tar extraction, not a graph build.
#
# Usage:
#   sudo ./osrm-install.sh /tmp/osrm-washington-20260814.tar.zst

set -euo pipefail

: "${OSRM_DATA_DIR:=/opt/rockabilly-roasting/osrm-data}"
: "${COMPOSE_DIR:=/opt/rockabilly-roasting}"
: "${OSRM_SERVICE:=osrm}"

tarball="${1:-}"
if [[ -z "$tarball" ]]; then
    echo "usage: $0 <tarball>" >&2
    exit 2
fi
[[ -r "$tarball" ]] || { echo "cannot read $tarball" >&2; exit 1; }

log() { printf '[%s] %s\n' "$(date -u +%FT%TZ)" "$*"; }

# Unpack beside the live data rather than over it. A half-extracted dataset
# under a running router is a routing outage; a staging directory means the
# swap is a rename.
staging="${OSRM_DATA_DIR}.incoming"
previous="${OSRM_DATA_DIR}.previous"

rm -rf "$staging"
mkdir -p "$staging"

log "unpacking $(basename "$tarball")"
case "$tarball" in
    *.tar.zst) tar --zstd -xf "$tarball" -C "$staging" ;;
    *.tar.gz)  tar -xzf "$tarball" -C "$staging" ;;
    *) echo "unrecognized archive type: $tarball" >&2; exit 1 ;;
esac

# Sanity check before touching the live directory: .osrm.fileIndex and
# .osrm.mldgr only exist if partition and customize both ran. Installing a
# half-built dataset would leave the router up but answering nothing.
shopt -s nullglob
mld_files=("$staging"/*.osrm.mldgr)
shopt -u nullglob
if (( ${#mld_files[@]} == 0 )); then
    echo "archive has no .osrm.mldgr — it was not customized; rebuild it" >&2
    rm -rf "$staging"
    exit 1
fi
log "dataset looks complete ($(du -sh "$staging" | cut -f1))"

# Keep exactly one generation back. Quarterly refreshes make a deeper history
# pointless, but a single rollback target has earned its disk more than once.
rm -rf "$previous"
if [[ -d "$OSRM_DATA_DIR" ]]; then
    mv "$OSRM_DATA_DIR" "$previous"
fi
mv "$staging" "$OSRM_DATA_DIR"

log "restarting ${OSRM_SERVICE}"
cd "$COMPOSE_DIR"
docker compose up -d --force-recreate "$OSRM_SERVICE"

# Give the router a moment to mmap the dataset before asking it anything.
sleep 5

log "smoke test: is the router accepting connections"
# bash, not /bin/sh: /dev/tcp is a bash builtin, and sh on debian is dash,
# which would fail this check on a perfectly healthy router.
if docker compose exec -T "$OSRM_SERVICE" \
        bash -c 'exec 3<>/dev/tcp/127.0.0.1/5000' 2>/dev/null; then
    log "router is accepting connections"
else
    log "WARNING: router is not accepting connections on :5000 — check 'docker compose logs ${OSRM_SERVICE}'"
    log "previous dataset is still at ${previous} if you need to roll back"
    exit 1
fi

cat <<EOF

Installed. Verify routing quality from the host:

  curl -s 'http://localhost:5000/nearest/v1/driving/-119.1372,46.2087' | head -c 300

Roll back with:
  rm -rf ${OSRM_DATA_DIR} && mv ${previous} ${OSRM_DATA_DIR} && docker compose up -d --force-recreate ${OSRM_SERVICE}
EOF
