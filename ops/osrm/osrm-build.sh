#!/usr/bin/env bash
# Build the OSRM routing dataset for delivery route planning.
#
# WHERE THIS RUNS: angmar.dev (or any box with ~5GB of free RAM), NEVER prod.
# osrm-extract peaks around 5GB on the Washington extract; prod has 3.7GB and
# no swap, so running this there would invite the OOM killer to pick between
# OSRM, Postgres, and the storefront. Prod only ever runs osrm-routed, which
# needs about 1GB. See ops/osrm/README.md.
#
# Output is a tarball of the prepared .osrm.* fileset, ready to ship to prod
# with osrm-install.sh.
#
# Usage:
#   ./osrm-build.sh                 # download, build, package
#   ./osrm-build.sh --skip-download # reuse the .osm.pbf already on disk
#   ./osrm-build.sh --push          # also scp the tarball to prod

set -euo pipefail

# The image tag is load-bearing and shared with docker-compose.osrm.yml:
# osrm-routed refuses to load data prepared by a different OSRM version
# ("file version mismatch"). Bump both together, and rebuild the data when
# you do.
: "${OSRM_IMAGE:=ghcr.io/project-osrm/osrm-backend:v26.6.5-debian}"

# Geofabrik's Washington state extract covers the Tri-Cities and every
# plausible delivery radius around it. ~344MB as of 2026-08.
: "${OSRM_REGION:=washington}"
: "${OSRM_PBF_URL:=https://download.geofabrik.de/north-america/us/${OSRM_REGION}-latest.osm.pbf}"

: "${OSRM_DATA_DIR:=${HOME}/osrm-build}"
: "${OSRM_PROFILE:=/opt/car.lua}"

# Where --push sends the tarball. The install script on the far end unpacks it.
: "${PROD_SSH_HOST:=deploy@5.161.245.139}"
: "${PROD_STAGING_PATH:=/tmp}"

skip_download=false
push=false
for arg in "$@"; do
    case "$arg" in
        --skip-download) skip_download=true ;;
        --push)          push=true ;;
        -h|--help)       sed -n '2,20p' "$0"; exit 0 ;;
        *) echo "unknown argument: $arg" >&2; exit 2 ;;
    esac
done

log() { printf '[%s] %s\n' "$(date -u +%FT%TZ)" "$*"; }

command -v docker >/dev/null || { echo "docker is required" >&2; exit 1; }

# Refuse to run somewhere that will thrash or OOM. Preprocessing is the whole
# reason this script exists as a separate step, so failing loudly here beats
# discovering it 20 minutes in.
avail_kb=$(awk '/MemAvailable/ {print $2}' /proc/meminfo 2>/dev/null || echo 0)
avail_gb=$(( avail_kb / 1024 / 1024 ))
swap_kb=$(awk '/SwapTotal/ {print $2}' /proc/meminfo 2>/dev/null || echo 0)
if (( avail_gb < 5 )); then
    if (( swap_kb < 2 * 1024 * 1024 )); then
        cat >&2 <<EOF
REFUSING TO RUN: ${avail_gb}GB available RAM and under 2GB of swap.
osrm-extract peaks around 5GB on the ${OSRM_REGION} extract.

Run this on angmar.dev instead, or set OSRM_FORCE=1 if you know what you
are doing (and are certain this is not the production host).
EOF
        [[ "${OSRM_FORCE:-}" == "1" ]] || exit 1
    fi
    log "WARNING: only ${avail_gb}GB available; the build will lean on swap and be slow"
fi

mkdir -p "$OSRM_DATA_DIR"
cd "$OSRM_DATA_DIR"

pbf="${OSRM_REGION}-latest.osm.pbf"
base="${OSRM_REGION}-latest"

if [[ "$skip_download" == false ]]; then
    log "downloading ${OSRM_PBF_URL}"
    curl -fSL --retry 3 -o "${pbf}.tmp" "$OSRM_PBF_URL"
    mv "${pbf}.tmp" "$pbf"
else
    [[ -f "$pbf" ]] || { echo "--skip-download but ${OSRM_DATA_DIR}/${pbf} is missing" >&2; exit 1; }
    log "reusing existing ${pbf}"
fi
log "extract size: $(du -h "$pbf" | cut -f1)"

# The MLD pipeline: extract the road graph, partition it into cells, then
# customize (weight) those cells. osrm-routed --algorithm mld needs all three.
#
# --user is not optional: the OSRM image runs as root, so without it every
# .osrm.* file lands root-owned in your directory and the tar at the end fails
# with "Permission denied" after twenty minutes of work. Running the whole
# script under sudo also "fixes" that, at the cost of doing the build as root
# and leaving the output in /root.
run_osrm() {
    docker run --rm -t \
        --user "$(id -u):$(id -g)" \
        -v "${OSRM_DATA_DIR}:/data" \
        "$OSRM_IMAGE" \
        "$@"
}

log "osrm-extract (this is the memory-hungry step; 10-20 min is normal)"
run_osrm osrm-extract -p "$OSRM_PROFILE" "/data/${pbf}"

log "osrm-partition"
run_osrm osrm-partition "/data/${base}.osrm"

log "osrm-customize"
run_osrm osrm-customize "/data/${base}.osrm"

# Package every prepared file. The .osm.pbf itself is deliberately excluded —
# prod never needs the raw extract, and it would add 345MB to the transfer.
#
# zstd is worth having here: it compresses this dataset both smaller and far
# faster than gzip (the gzip fallback spends ~2 minutes single-threaded on what
# zstd does in seconds).
tarball="osrm-${OSRM_REGION}-$(date -u +%Y%m%d).tar.zst"
log "packaging ${tarball}"
if command -v zstd >/dev/null; then
    tar --zstd -cf "$tarball" "${base}.osrm."*
else
    tarball="${tarball%.zst}.gz"
    log "zstd not found, falling back to gzip (${tarball}) — this takes a few minutes"
    log "  install it for a faster, smaller archive:  sudo dnf install zstd"
    tar -czf "$tarball" "${base}.osrm."*
fi

log "done: ${OSRM_DATA_DIR}/${tarball} ($(du -h "$tarball" | cut -f1))"

if [[ "$push" == true ]]; then
    log "copying to ${PROD_SSH_HOST}:${PROD_STAGING_PATH}/"
    scp "$tarball" "${PROD_SSH_HOST}:${PROD_STAGING_PATH}/"
    cat <<EOF

Now, on prod:
    sudo /opt/rockabilly-roasting/osrm-install.sh ${PROD_STAGING_PATH}/${tarball}
EOF
fi
