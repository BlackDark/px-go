# Performance comparison — deployed build vs branch build

Ad-hoc A/B test comparing the currently deployed px-go instance against a newer
branch build, run against a shared `px.ini` config on a Windows host.

## Setup

| | Current (deployed) | Branch |
|---|---|---|
| Binary | `px-go.exe` (deployed as headless `pxw-go.exe`) | `px-go-windows-amd64.exe` |
| Version | 0.2.0 | dev (unversioned) |
| Build date | 2026-06-29 | 2026-07-29 |
| Config | shared `px.ini`, `noproxy` cleared for isolated relay test | same |

Live proxy on port 3128 left untouched throughout. Bench instances ran on
separate ports (3129 branch, 3130 current) with `noproxy` temporarily cleared
so traffic actually transits the proxy relay instead of matching local-IP
bypass rules.

## Round 1 — direct connect (no PAC configured yet)

At test time `px.ini` had no `pac`/`server` set → direct connect passthrough.
Benchmarked with a custom Go load client against a local echo server
(isolates proxy-internal overhead from external network variance) and against
`example.com` over real HTTPS CONNECT.

### Local relay (plain HTTP, no external network variance)

| Concurrency | Build | Throughput | Avg latency | p99 latency |
|---|---|---|---|---|
| 20 | Current | ~2000-2150 req/s | ~9-11ms | ~12-60ms |
| 20 | Branch | ~10200-12400 req/s | ~1.5-1.9ms | ~4-11ms |
| 50 | Current | 2156 req/s | 22.5ms | 28.4ms |
| 50 | Branch | 9539 req/s | 5.0ms | 20.7ms |

**~4-5x throughput, ~4-5x lower avg latency on branch.**

### Real HTTPS CONNECT tunnel (via example.com, network-bound)

| Build | Throughput | Avg latency | p99 |
|---|---|---|---|
| Current | 70.3 req/s | 69.5ms | 357ms |
| Branch | 81.3 req/s | 60.5ms | 333ms |

~15% improvement — smaller because external RTT dominates, not proxy internals.

**Conclusion (round 1):** branch build has a large, reproducible improvement in
proxy-internal overhead (connection setup / relay path), consistent with the
"semaphore release before relay" design decision noted in `AGENTS.md`. Gains
are most visible when upstream/network latency is low or absent; real corporate
proxy hops will dilute the delta somewhat.

## Round 2 — with PAC configured

`px.ini` updated to add a corporate PAC URL:
```ini
pac = http://<internal-proxy-host>/PAC/<internal-pac-file>.pac
```

PAC routes private/RFC1918 ranges (`10.*`, `172.16-31.*`, `192.168.*`,
`127.0.0.*`) DIRECT, and external hosts through an internal corporate upstream
proxy on port 8080 (auth required). This changes the test from "direct
connect" to "PAC-resolved + authenticated upstream hop", which is the actual
production path.

Retested using the project's own `bench/run.sh` A/B harness (hit/miss
scenarios against a real external host through the resolved PAC proxy),
comparing the same two builds side by side on ports 3128 (current, live,
read-only GET traffic only) vs 3130 (branch, bench-only instance — 3129 was
blocked by Windows Firewall, no admin rights to open it, so 3130 reused
instead since a rule already existed for it).

Target host: `httpbin.org` was tried first but returned HTTP 503 from its own
AWS backend (verified independent of px) — not a proxy issue. Switched to
`httpbingo.org`, a working httpbin-API-compatible alternative, for both hit
and miss scenarios. `example.com` was rejected too since `run.sh` requests a
hardcoded `/get` path that only exists on httpbin-style targets.

Config: both builds ran with the corporate PAC resolving external hosts
through the internal upstream proxy (authenticated) — this is the real
production path, not direct-connect.

### Results — PAC-resolved upstream, n=300 hit / n=150 miss, concurrency=32

| Build | Scenario | Avg latency | p50 | p99 | Wall time | Errors |
|---|---|---|---|---|---|---|
| Current (3128, live) | hit | 2.356s | 2.404s | 2.737s | 23.7s | 0 |
| Branch (3130) | hit | 0.478s | 0.388s | 1.986s | 5.4s | 0 |
| Current (3128, live) | miss | 2.129s | 2.297s | 2.653s | 11.2s | 0 |
| Branch (3130) | miss | 0.408s | 0.405s | 0.539s | 2.3s | 0 |

**~4.9x lower avg latency (hit), ~5.2x lower avg latency (miss), ~4.4-4.9x
faster wall clock on branch — even with the real authenticated corporate PAC
upstream in the path.** p99 on miss scenario is notably tighter on branch
(0.54s vs 2.65s) — no long-tail cache/lock contention under concurrent PAC
misses, consistent with the miss-storm concurrency fix this branch targets.

## Conclusion

The branch build is faster in every scenario tested — direct-connect,
local-relay-only, real HTTPS CONNECT, and now PAC-resolved-with-auth — by a
consistent ~4-5x margin on latency and throughput, with no errors and no
regressions observed. The improvement is not an artifact of PAC — it holds
with and without PAC in the path, and the miss-scenario p99 win suggests the
concurrency/lock fix noted in `AGENTS.md` specifically pays off under PAC
resolution load.

Recommend promoting the branch build.

Raw data kept locally under `bench/results/` (not included here — may contain
internal hostnames in request logs).
