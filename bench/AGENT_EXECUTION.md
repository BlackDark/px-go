# PAC A/B benchmark — agent execution guide

Execute this checklist on a machine where **px runs on the Windows host** and clients (this agent / shell) run from **WSL**. Goal: compare `main` vs `perf/pac-concurrency` (or the PR binary) **without stopping the live proxy**.

## Hard constraints

1. **Never stop / kill the production px process** while measuring — WSL loses internet.
2. Run **two px instances** on different ports (default live `3128`, bench candidate `3129`).
3. Point **only the benchmark** at `3129`. Leave WSL/`HTTP_PROXY` daily traffic on `3128` until cutover is approved by a human.
4. Same PAC, auth, noproxy, threads, idle, socktimeout on both instances (only `port` + `log_file` differ).

## Prerequisites (Windows host)

- Build both binaries (PowerShell or cmd):

```powershell
# from repo checkout
$env:CGO_ENABLED=0
go build -o px-main.exe ./cmd/px
git stash push -u -m "wip"   # only if dirty; prefer clean checkout of each ref
git checkout main
go build -o px-main.exe ./cmd/px
git checkout perf/pac-concurrency   # or PR branch / tag under test
go build -o px-new.exe ./cmd/px
```

- Copy `px.ini` → `px-bench.ini`. Change only:

```ini
[proxy]
port = 3129
listen = 0.0.0.0
; or hostonly/gateway as needed so WSL can reach it — match how 3128 is exposed

[settings]
log = 1
log_file = C:\path\to\px-bench.log
; optional for reload test: proxyreload = 5
```

- Ensure live instance stays on `3128` with existing config.
- Start bench instance **without touching** the live one:

```powershell
.\px-new.exe --config=px-bench.ini --foreground --verbose
```

- Confirm both health endpoints from Windows:

```powershell
curl http://127.0.0.1:3128/health
curl http://127.0.0.1:3129/health
```

- Allow WSL → host:3129 (Windows Firewall if needed). Live 3128 already works.

## Prerequisites (WSL)

```bash
cd /path/to/px-go
chmod +x bench/run.sh
# optional: export PX_HOST=<windows-host-ip>
# default: first nameserver from /etc/resolv.conf
curl -s -o /dev/null -w "%{http_code}\n" http://$PX_HOST:3128/health
curl -s -o /dev/null -w "%{http_code}\n" http://$PX_HOST:3129/health
```

Both must return `200`.

## What the agent must run

From WSL:

```bash
cd /path/to/px-go

export MAIN_PORT=3128
export NEW_PORT=3129
# export PX_HOST=...          # if auto-detect wrong
# export TARGET_HOST=httpbin.org
# export N_HIT=500 N_MISS=200 CONCURRENCY=64

./bench/run.sh
# If PAC is file-based and reload not interesting:
# ./bench/run.sh --skip-reload
```

### Reload scenario (HTTP PAC only)

If production PAC is an `http(s)://` URL:

1. Set `proxyreload = 5` in **bench** ini only (or wait for existing interval).
2. During `reload` section of `run.sh` (script prints a NOTE), ensure a soft reload can occur (interval elapse is enough).
3. Do **not** restart px.

File PAC: use `--skip-reload` or ignore reload rows; miss-storm still valid.

## Windows memory snapshot (agent or human)

While idle (after warm) and mid miss-storm, on Windows:

```powershell
Get-Process px* | Select-Object Name, Id, WorkingSet64, PagedMemorySize64 |
  Format-Table -AutoSize
```

Save output into `bench/results/<run>/memory-*.txt` (copy manually or via agent if it has Windows shell).

## Success criteria (report these)

From `bench/results/<timestamp>/summary.csv` and `*-*.txt`:

| Check | Pass if |
|-------|---------|
| Health | both ports 200 before run |
| Errors | `errors=0` on hit/miss for **new** (main baseline recorded either way) |
| Hit p50/p99 | **new** ≈ **main** (within ~10–20% noise OK) |
| Miss p99 / wall | **new** ≤ **main** (expect improvement under high concurrency) |
| Reload p99 | **new** does not cliff vs **main** when HTTP PAC reloads |
| RSS | **new** only modestly higher (pool; typically low single-digit MB) |

## Agent deliverable

Write a short report (chat or `bench/results/<timestamp>/REPORT.md`) containing:

1. Env: `PX_HOST`, ports, git SHAs / binary names, PAC type (file vs HTTP)
2. Paste `summary.csv`
3. Call **pass / fail / inconclusive** per success-criteria row with numbers
4. Note anomalies (DNS failures to TARGET_HOST, firewall, auth errors)
5. **Do not** change WSL default proxy or stop `3128` unless a human explicitly asks

## Cutover (human only — not agent default)

Only after pass: point clients at 3129 or swap ports in a maintenance window, keeping old binary ready on the other port.

## Troubleshooting

| Symptom | Action |
|---------|--------|
| health fail on 3129 | bench px not listening / firewall / listen=127.0.0.1 only |
| many errors | TARGET_HOST blocked; try internal HTTPS URL; check upstream auth logs |
| MAIN==NEW no diff | concurrency too low or all cache hits; raise `CONCURRENCY` / `N_MISS` |
| WSL cannot reach host | fix `PX_HOST`; mirrored networking; firewall rule for 3129 |
