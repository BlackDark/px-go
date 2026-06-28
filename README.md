# px-go

`px-go` is a Go port of the Python `px` proxy: a local HTTP proxy that authenticates to upstream NTLM/Negotiate/Kerberos proxies, supports CONNECT tunneling, PAC files, allow/noproxy rules, client auth, `/PxQuit`, and health checks.

## Features
- HTTP proxy with direct and upstream-proxy forwarding
- CONNECT tunneling for HTTPS
- Upstream auth: BASIC, DIGEST, NTLM, NEGOTIATE
- Client auth: BASIC, DIGEST, NTLM, NEGOTIATE (SSPI-backed on Windows)
- PAC execution via `goja`
- INI + `.env` + `PX_*` env + CLI config precedence
- Allow-list and noproxy matching for IPs, CIDRs, ranges, wildcards, and domains
- Kerberos ticket management helpers (`kinit`/`klist`) on Unix-like systems
- Windows-specific registry install/uninstall and IE/WinHTTP proxy discovery
- Structured logging via `log/slog`
- Docker, GitHub Actions CI, and Goreleaser release automation

## Why Go over Python

`px-go` replaces the original Python `px` with a statically compiled Go binary. Key improvements:

| | Python px | px-go |
|---|---|---|
| **Startup time** | ~1–2 s (interpreter + imports) | ~10 ms |
| **Memory per connection** | ~50–100 KB (Python objects) | ~4–8 KB (goroutine stack) |
| **Concurrency model** | asyncio single-threaded event loop | goroutines, true multi-core |
| **Binary distribution** | Python runtime + pip deps required | single static binary, zero deps |
| **Binary size** | 50 MB+ (runtime + quickjs + wheels) | ~15 MB (stripped) |
| **Docker image** | 100–200 MB | ~20 MB (distroless) |

Additional benefits:
- **No GIL** — NTLM auth handshakes across hundreds of connections run in parallel.
- **Shared transport pool** — HTTP/S direct requests reuse keep-alive connections instead of opening new ones per request.
- **Zero-copy tunnel relay** — CONNECT tunnels use 32 KB buffers with direct TCP forwarding.
- **Instant cold-start** — important in Kubernetes sidecars or short-lived CI containers.
- **Single static binary** — deploy with `COPY` in Docker, no interpreter, no virtualenv, no pip.
- **Cross-compilation** — one `go build` command produces binaries for Linux, macOS, and Windows (amd64 + arm64) without extra toolchains.

Expected throughput under load (100+ concurrent NTLM-authenticated CONNECT tunnels): **3–10× higher** than the Python original, with significantly lower tail latency.

## Configuration precedence
1. defaults
2. `px.ini`
3. `.env` / `PX_*`
4. CLI flags

## Quick start
```bash
go run ./cmd/px --server=upstream.proxy:8080 --username=DOMAIN\\user --port=3128
curl --proxy http://127.0.0.1:3128 http://example.com
```

## Common flags
```bash
--config=path/to/px.ini
--server=proxy:8080
--pac=http://wpad/proxy.pac
--listen=127.0.0.1
--port=3128
--username=DOMAIN\\user
--auth=ANY|NEGOTIATE|NTLM|DIGEST|BASIC|NONE
--client-auth=NONE|ANY|ANYSAFE|NEGOTIATE|NTLM|DIGEST|BASIC
--noproxy=localhost,10.0.0.0/8,example.com
--allow=127.0.*.*
--quit
--save
--health-check
--test=http://httpbin.org/get
```

## Environment variables
Use `PX_*` names matching CLI/config keys, for example:
- `PX_SERVER`
- `PX_PORT`
- `PX_USERNAME`
- `PX_PASSWORD`
- `PX_CLIENT_AUTH`
- `PX_CLIENT_PASSWORD`
- `PX_LOG_LEVEL`

## Build and test
```bash
# Quick (uses Makefile)
make tidy fmt test build

# Linux / macOS
go build ./cmd/px

# Windows — console build (px.exe): shows terminal window, use for interactive debugging
# or running manually from cmd / PowerShell
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -o px.exe ./cmd/px

# Windows — headless build (pxw.exe): no console popup, for Task Scheduler and autostart
# --install automatically registers pxw.exe if it exists alongside px.exe
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w -H=windowsgui" -o pxw.exe ./cmd/px

# Run tests
go test ./...

# Run with race detector
go test -race ./...

# Run integration tests
go test -v ./internal/proxy/ -run TestIntegration
```

## Windows Task Scheduler

px-go ships two Windows binaries in each release zip:

- **`px.exe`** — console build: shows a terminal window, use for interactive debugging or running manually from cmd / PowerShell.
- **`pxw.exe`** — headless build (`-H=windowsgui`): no console popup, for Task Scheduler and autostart registry entries.

`--install` automatically registers `pxw.exe` in the autostart registry entry if it exists alongside `px.exe`.

### Setup

1. Download the Windows zip from GitHub Releases (contains both `px.exe` and `pxw.exe`) or build both:
   ```powershell
   # Console build — interactive debugging
   GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -o px.exe ./cmd/px

   # Headless build — Task Scheduler / autostart
   GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w -H=windowsgui" -o pxw.exe ./cmd/px
   ```

2. Place `px.exe`, `pxw.exe`, and `px.ini` in a permanent location:
   ```
   C:\Tools\px\px.exe
   C:\Tools\px\pxw.exe
   C:\Tools\px\px.ini
   ```

3. Register autostart (uses `pxw.exe` automatically):
   ```powershell
   C:\Tools\px\px.exe --config=C:\Tools\px\px.ini --install
   ```

   Or create a Scheduled Task (PowerShell as Administrator):
   ```powershell
   $action = New-ScheduledTaskAction -Execute "C:\Tools\px\pxw.exe" `
     -Argument "--config=C:\Tools\px\px.ini" `
     -WorkingDirectory "C:\Tools\px"

   $trigger = New-ScheduledTaskTrigger -AtLogOn

   $settings = New-ScheduledTaskSettingsSet `
     -AllowStartIfOnBatteries `
     -DontStopIfGoingOnBatteries `
     -ExecutionTimeLimit (New-TimeSpan) `
     -RestartCount 3 `
     -RestartInterval (New-TimeSpan -Minutes 1)

   Register-ScheduledTask -TaskName "px-proxy" `
     -Action $action -Trigger $trigger -Settings $settings `
     -RunLevel Highest -Description "px-go local auth proxy"
   ```

4. Recommended `px.ini` settings for headless operation:
   ```ini
   [settings]
   ; File-based logging (stdout unavailable in headless mode)
   log = 1

   ; High concurrency for AI agents (Copilot, Cursor, etc.)
   threads = 128

   ; Long idle timeout — AI tools keep connections open for minutes
   idle = 300

   foreground = 0
   ```

### Important notes

- **SSPI authentication** requires the task to run **only when user is logged on** (SSPI needs an interactive session token). Do not use "Run whether user is logged on or not".
- **Logging**: Use `log=1` (file in script directory) or `log=2` (cwd). `log=4` (stdout) produces no output in headless mode.
- **Health check**: `curl http://127.0.0.1:3128/health` from PowerShell to verify the proxy is running.
- **Graceful stop**: `curl http://127.0.0.1:3128/PxQuit` or stop the scheduled task.
- **Verbose debugging**: Temporarily use `px.exe` (console build, without `-H=windowsgui`) and run manually with `--verbose` to see full debug output.

## Docker

```bash
docker build -f docker/Dockerfile -t px-go .
docker run --rm -p 3128:3128 px-go --gateway --foreground --log=4
```

Released images: `ghcr.io/blackdark/px-go:latest` (multi-arch).

### Cluster / shared proxy (Docker & Kubernetes)

Use px-go as a **central outbound proxy** when many containers or hosts must reach the internet through a corporate upstream (NTLM/Negotiate/Basic). Linux containers **cannot use Windows SSPI** — provide explicit upstream credentials or Kerberos keytabs.

#### Recommended `px.ini` (shared proxy)

```ini
[proxy]
server = corp-proxy.example.com:8080
username = DOMAIN\service-account
; password via PX_PASSWORD env / K8s Secret — never commit

gateway = 1
; Restrict clients — never leave allow wide open on a shared proxy
allow = 10.0.0.0/8,172.16.0.0/12,192.168.0.0/16
; Bypass upstream for private/cluster traffic
noproxy = .svc,.cluster.local,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,localhost,127.0.0.1

auth = NTLM
port = 3128

[client]
; Require auth from clients when the proxy is reachable beyond localhost
client_auth = BASIC
client_username = proxyuser
; client password via PX_CLIENT_PASSWORD env / Secret

[settings]
threads = 128
idle = 300
socktimeout = 20
proxyreload = 300
foreground = 1
log = 4
log_level = INFO
```

CLI equivalents for containers without a config file:

```bash
px-go \
  --gateway \
  --server=corp-proxy:8080 \
  --username='DOMAIN\user' \
  --auth=NTLM \
  --allow='10.0.0.0/8,172.16.0.0/12' \
  --noproxy='.svc,.cluster.local,10.0.0.0/8' \
  --client-auth=BASIC \
  --client-username=proxyuser \
  --foreground \
  --log=4
```

#### Docker Compose (shared service)

```yaml
services:
  px:
    image: ghcr.io/blackdark/px-go:latest
    ports:
      - "3128:3128"
    environment:
      PX_SERVER: corp-proxy.example.com:8080
      PX_USERNAME: "DOMAIN\\service-account"
      PX_PASSWORD: ${PX_PASSWORD}
      PX_CLIENT_AUTH: BASIC
      PX_CLIENT_USERNAME: proxyuser
      PX_CLIENT_PASSWORD: ${PX_CLIENT_PASSWORD}
    command:
      - --gateway
      - --allow=172.16.0.0/12,192.168.0.0/16
      - --noproxy=.local,localhost,127.0.0.1,10.0.0.0/8,172.16.0.0/12
      - --foreground
      - --log=4
    deploy:
      resources:
        limits:
          memory: 512Mi
          cpu: "1"
        requests:
          memory: 128Mi
          cpu: 100m
    healthcheck:
      test: ["CMD", "/px-go", "--health-check", "--port=3128"]
      interval: 30s
      timeout: 5s
      retries: 3
```

Point other services at `http://px:3128` via `HTTP_PROXY` / `HTTPS_PROXY` and set `NO_PROXY` for internal hosts.

#### Kubernetes (Deployment + Service)

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: px-credentials
stringData:
  PX_PASSWORD: "change-me"
  PX_CLIENT_PASSWORD: "change-me"
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: px-go
spec:
  replicas: 2
  selector:
    matchLabels:
      app: px-go
  template:
    metadata:
      labels:
        app: px-go
    spec:
      containers:
        - name: px-go
          image: ghcr.io/blackdark/px-go:latest
          ports:
            - containerPort: 3128
              name: proxy
          env:
            - name: PX_SERVER
              value: corp-proxy.example.com:8080
            - name: PX_USERNAME
              value: "DOMAIN\\service-account"
            - name: PX_PASSWORD
              valueFrom:
                secretKeyRef:
                  name: px-credentials
                  key: PX_PASSWORD
            - name: PX_CLIENT_AUTH
              value: BASIC
            - name: PX_CLIENT_USERNAME
              value: proxyuser
            - name: PX_CLIENT_PASSWORD
              valueFrom:
                secretKeyRef:
                  name: px-credentials
                  key: PX_CLIENT_PASSWORD
          args:
            - --gateway
            - --allow=10.0.0.0/8,172.16.0.0/12,192.168.0.0/16
            - --noproxy=.svc,.cluster.local,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,localhost,127.0.0.1
            - --foreground
            - --log=4
          resources:
            requests:
              cpu: 100m
              memory: 128Mi
            limits:
              cpu: "1"
              memory: 512Mi
          livenessProbe:
            exec:
              command: ["/px-go", "--health-check", "--port=3128"]
            initialDelaySeconds: 5
            periodSeconds: 30
          readinessProbe:
            exec:
              command: ["/px-go", "--health-check", "--port=3128"]
            periodSeconds: 10
---
apiVersion: v1
kind: Service
metadata:
  name: px-go
spec:
  selector:
    app: px-go
  ports:
    - port: 3128
      targetPort: proxy
```

Wire client pods:

```yaml
env:
  - name: HTTP_PROXY
    value: http://proxyuser:$(PX_CLIENT_PASSWORD)@px-go:3128
  - name: HTTPS_PROXY
    value: http://proxyuser:$(PX_CLIENT_PASSWORD)@px-go:3128
  - name: NO_PROXY
    value: .svc,.cluster.local,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,localhost,127.0.0.1
```

Add **NetworkPolicy** so only intended namespaces can reach port 3128.

**Sidecar vs shared service**

| Pattern | When to use |
|---|---|
| **Shared Deployment/Service** | Many pods, one credential set, easier ops |
| **Sidecar per pod** | Pod-specific upstream auth or isolation |
| **DaemonSet (hostNetwork)** | Node-level proxy for non-K8s workloads on the same host; pair with `hostonly=1` |

#### Sizing guidelines

| Load | Replicas | CPU (limit) | Memory (limit) | `threads` |
|---|---|---|---|---|
| Dev / CI (~10 concurrent setups) | 1 | 250m | 128Mi | 64 |
| Typical cluster (~50 pods, bursty) | 2 | 500m–1 | 256–512Mi | 128 |
| Heavy AI / long-lived tunnels | 2–3 | 1–2 | 512Mi–1Gi | 256 |

Notes:

- `threads` limits **connection setup** (dial + upstream auth), not active CONNECT tunnels — hundreds of idle tunnels are fine with `threads=128`.
- Raise `idle` (300+) for agents that keep connections open between bursts.
- Watch **file descriptors** under heavy tunnel load; increase container `ulimit` / node limits if you see `too many open files`.
- Distroless image has no shell — use `exec` probes and env/CLI config only.

#### Security warnings (read before exposing cluster-wide)

1. **Open proxy risk** — `gateway=1` with permissive `allow` turns px into an open relay through your corporate network. Restrict `allow` to pod/node CIDRs and add **client_auth**.
2. **No TLS to px** — Traffic between clients and px-go is plain HTTP. Anyone on the cluster network can intercept credentials in proxy URLs unless you isolate with NetworkPolicy or place px behind an TLS-terminating front-end.
3. **Upstream credentials in Secrets** — Service accounts with domain passwords are high-value targets. Use dedicated accounts, rotation, and minimal RBAC on the Secret.
4. **SSPI unavailable in Linux containers** — Negotiate/NTLM via OS identity will not work; use explicit `--username` / `--password` or Kerberos keytab (`kerberos=1` + mounted keytab).
5. **`/PxQuit` shutdown** — Callable from loopback and local interface IPs. Do not expose px admin paths outside the trust boundary; prefer SIGTERM via Kubernetes lifecycle.
6. **Single point of failure** — If px-go is down, all configured outbound traffic fails. Run ≥2 replicas and use a Service with readiness probes.
7. **PAC / WPAD** — If using `--pac`, the container must reach the PAC URL; corporate DNS may differ from cluster DNS — configure `dnsConfig` or static PAC mounts.
8. **Audit & abuse** — A compromised pod with proxy access can exfiltrate data via CONNECT. Combine NetworkPolicy, client auth, and corporate egress logging.

## Notes
- Windows SSPI is build-tagged and compiled without CGO.
- PAC files are reloaded based on `proxyreload`.
- `/health` returns `200 OK`; `/PxQuit` performs graceful shutdown for local callers.
