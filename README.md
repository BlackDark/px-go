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

## Notes
- Windows SSPI is build-tagged and compiled without CGO.
- PAC files are reloaded based on `proxyreload`.
- `/health` returns `200 OK`; `/PxQuit` performs graceful shutdown for local callers.
