# OSRM — delivery route optimization

OSRM decides the *order* of stops on a delivery route. It never talks to Google
or Apple Maps: the driver's phone handles turn-by-turn navigation via URL
schemes, receiving stops in the order OSRM produced. See
`hiri-route-optimization-plan.md` for the whole feature.

## The one rule

**Build on angmar. Serve on prod. Never preprocess on prod.**

Measured on angmar 2026-08-14, from OSRM's own `RAM: peak bytes used` lines:

| | Peak RAM | Wall clock | Where |
|---|---|---|---|
| `osrm-extract` | **4.29GB** | ~90s | angmar.dev (7.6GB + 4GB swap) |
| `osrm-partition` | 1.07GB | ~30s | angmar.dev |
| `osrm-customize` | 1.07GB | ~10s | angmar.dev |
| `osrm-routed --mmap` | **69MB RSS** | — | prod (3.7GB, **no swap**) |

That serving figure is not a typo. With `--mmap` the dataset lives in page cache
rather than the process, so `docker stats` shows ~69MB for `rr-osrm` while the
1.5GB of routing data sits in reclaimable `buff/cache`. Prod's `available`
memory was unchanged after bringing the router up. Under pressure the kernel
evicts those clean pages — queries get slower, nothing gets OOM-killed — and
for the same reason the 2GB `mem_limit` throttles cache instead of killing the
container.

Prod has 3.0GB available and zero swap. A 4.3GB extract there does not
swap-thrash its way to success — it invites the OOM killer, which is as likely
to choose Postgres or the storefront as it is to choose OSRM. `osrm-build.sh`
refuses to run on a host that can't take it.

The whole build takes about 4 minutes on angmar (plus ~30s to download the
extract and ~2 minutes to compress, or seconds if `zstd` is installed) — far
quicker than the 10–20 minutes the OSRM docs' planet-scale figures suggest.

## Files

| File | Runs on | Purpose |
|---|---|---|
| `osrm-build.sh` | angmar | Download the extract, run the MLD pipeline, package a tarball |
| `osrm-install.sh` | prod | Unpack a prepared tarball, swap it in, restart the router |
| `docker-compose.osrm.yml` | prod (reference) | Service definition to merge into the server's compose file |

## First-time setup

**1. Build the dataset on angmar** (~20 minutes, mostly `osrm-extract`):

```bash
scp ops/osrm/osrm-build.sh angmar.dev:~/
ssh angmar.dev 'chmod +x osrm-build.sh && ./osrm-build.sh --push'
```

`--push` scp's the finished tarball to prod's `/tmp` (~570MB with the gzip
fallback; install `zstd` on the build host for a smaller, much faster archive).

Run it as your normal user, **not** under sudo — the script passes `--user` to
Docker so the output belongs to you. (Without that, the OSRM image writes
root-owned files and the packaging step fails at the very end.)

**2. Install the compose service on prod.** Merge the `osrm:` service from
`docker-compose.osrm.yml` into `/opt/rockabilly-roasting/docker-compose.yml`,
and copy the install script into place:

```bash
scp ops/osrm/osrm-install.sh deploy@5.161.245.139:/tmp/
ssh deploy@5.161.245.139 'sudo install -m 755 /tmp/osrm-install.sh /opt/rockabilly-roasting/'
```

**3. Install the data and start the router** (on prod):

```bash
sudo /opt/rockabilly-roasting/osrm-install.sh /tmp/osrm-washington-YYYYMMDD.tar.zst
```

**4. Verify it routes** (on prod). This is the real acceptance test — a
container that is up proves nothing:

```bash
# Nearest road to a Kennewick coordinate
curl -s 'http://localhost:5000/nearest/v1/driving/-119.1372,46.2087' | head -c 300

# A four-stop trip: roastery → Kennewick → Pasco → Richland, roundtrip.
# NOTE: coordinates are lng,lat — OSRM's order, backwards from every maps URL.
curl -s 'http://localhost:5000/trip/v1/driving/-119.1372,46.2087;-119.1006,46.2396;-119.2845,46.2857;-119.1600,46.2540?source=first&roundtrip=true' \
  | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d["code"]); print("order:", [w["waypoint_index"] for w in d["waypoints"]]); print("duration_s:", d["trips"][0]["duration"], "distance_m:", d["trips"][0]["distance"])'
```

Expect `Ok`, a permutation of `[0,1,2,3]` starting at 0, and a duration in the
low thousands of seconds. A duration of 0, or `NoRoute`, means the dataset
loaded but doesn't cover these coordinates — wrong region extract.

**Known-good baseline** (first install, 2026-08-14, `v26.6.5-debian` on the
2026-08-13 Washington extract). Compare against this after a refresh:

```
code:       Ok
order:      [0, 3, 1, 2]     # input 0 → input 2 → input 3 → input 1 → back to 0
duration:   3099.6 s  (51.7 min)
distance:   44921.3 m (27.9 mi)
snapping:   9.5m, 3.1m, 3.9m, 37.8m from the requested points
snapped to: North 8th Avenue, Williams Boulevard, West Dradie Street
```

Two things this baseline is really checking. First, that the order is *not*
`[0,1,2,3]` — a broken optimizer often just echoes its input, and with these
four coordinates the optimal loop genuinely reorders them. Second, that snapping
distances stay within tens of metres: a jump to hundreds of metres, or onto a
highway name nowhere near the point, is the signature of reversed lng/lat or the
wrong regional extract.

Since port 5000 is deliberately **not** published to the host (see below), run
these from inside the compose network if the curl above can't connect:

```bash
docker compose exec app wget -qO- 'http://osrm:5000/nearest/v1/driving/-119.1372,46.2087'
```

## Quarterly data refresh

Road networks change slowly; quarterly is plenty. Same two commands:

```bash
ssh angmar.dev './osrm-build.sh --push'
ssh deploy@5.161.245.139 'sudo /opt/rockabilly-roasting/osrm-install.sh /tmp/osrm-washington-YYYYMMDD.tar.zst'
```

`osrm-install.sh` keeps one generation back at `osrm-data.previous` and prints
the rollback command. Prod is never under memory pressure during any of this.

## Version pinning

Currently pinned to **`v26.6.5-debian`**.

The tag appears in **two** places — `osrm-build.sh` (`OSRM_IMAGE`) and
`docker-compose.osrm.yml` (`image:`). They must match. `osrm-routed` refuses to
load a dataset prepared by a different OSRM version, so bumping one side alone
produces a router that starts and immediately dies on a file version mismatch.
Bump both, then rebuild the data.

Two things to know before bumping:

- **Use a `-debian` tag, not `-alpine`.** The compose healthcheck uses bash's
  `/dev/tcp`, which the alpine image has no bash for.
- **OSRM's tag scheme is not what you'd guess.** It went `v5.27.x` → `v6.0.0` →
  a calendar-ish `26.x.y`, and recent releases publish only some variants per
  point release. List what actually exists rather than guessing:

  ```bash
  token=$(curl -s "https://ghcr.io/token?scope=repository:project-osrm/osrm-backend:pull&service=ghcr.io" | jq -r .token)
  curl -s -H "Authorization: Bearer $token" \
    "https://ghcr.io/v2/project-osrm/osrm-backend/tags/list?n=1000" | jq -r '.tags[]' | grep -- '-debian$' | sort -V | tail
  ```

The three flags the compose file passes were verified against this version's
`osrm-routed`: `--mmap` (defaults to *false*, so passing it matters),
`--algorithm` (defaults to *CH*, so `mld` is required and must match how the
data was prepared), and `--max-table-size` (already defaults to 100).

## Why no published port

OSRM has **no authentication of any kind**. It is designed to sit on a private
network behind something that does the authenticating. The compose service
therefore uses `expose:` rather than `ports:`, and Hiri reaches it at
`http://osrm:5000` over the compose network. Do not add a `ports:` mapping "for
debugging" — that publishes an open routing API to the internet.

The same reasoning is why OSRM runs on prod rather than on angmar with prod
calling across: the two boxes sit in different Hetzner locations (Ashburn vs
Hillsboro), so Hetzner's free private networking doesn't apply, and a
cross-country tunnel plus an availability dependency is a lot of moving parts to
save 1GB.

## Troubleshooting

**Container restarts in a loop.** Almost always a version/data mismatch or a
missing file. `docker compose logs osrm` says which. Rebuild on angmar with the
image tag the compose file uses.

**`NoSegment` from `/nearest`.** The coordinate is outside the extract. Check
lng,lat ordering first — reversed coordinates put Kennewick in Kazakhstan.

**`/trip` returns `TooBig`.** More stops than `--max-table-size` allows
(currently 100). A real delivery day should never approach this; if it does,
raise the limit and re-read the load-test note in the plan's open questions.

**Slow first request after a restart.** Expected with `--mmap` — the kernel is
paging the dataset in. It settles after a few queries.
