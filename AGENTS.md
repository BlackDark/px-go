# px-go

Go reimplementation of [px](https://github.com/genotrance/px) — a local proxy server that authenticates with upstream corporate (NTLM/Kerberos/Negotiate) proxies using Windows SSPI or explicit credentials.

## Architecture

```
cmd/px/main.go          Entry point, CLI flags, signal handling
internal/
  config/               INI/env/CLI config loading, logger setup
  proxy/                HTTP server, CONNECT tunnel, route resolution
  auth/                 Upstream proxy auth (SSPI, NTLM, Negotiate, Basic, Digest)
  clientauth/           Client-side auth (authenticating clients to px)
  pac/                  PAC file evaluation (JavaScript via goja)
  platform/             OS-specific proxy discovery (WinHTTP on Windows)
  network/              IP/CIDR/wildcard matching for allow/noproxy rules
  kerberos/             Kerberos ticket management
  version/              Build-time version info
```

## Build & Run

```bash
# Build (Linux)
go build ./cmd/px

# Build (Windows cross-compile from WSL)
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o px.exe ./cmd/px

# Run with config
./px --config=px.ini --foreground

# Run with verbose debug logging
./px --config=px.ini --verbose

# Quiet mode (suppress startup info)
./px --config=px.ini --quiet
```

## Testing

```bash
# Run all tests
go test ./...

# Run with race detector
go test -race ./...

# Run proxy integration test (handler_test.go)
go test -v ./internal/proxy/ -run TestDirect
```

### Integration Testing (automated)

The `internal/proxy/integration_test.go` file contains full integration tests:
- Direct HTTP/HTTPS proxying
- All HTTP methods (GET, POST, PUT, DELETE, PATCH)
- Noproxy bypass rules
- Health endpoint
- Client allow/deny with gateway mode
- Upstream proxy chaining (HTTP + CONNECT)
- Upstream proxy with Basic auth
- Large body transfers (1MB)
- Graceful shutdown via /PxQuit

Run: `go test -v ./internal/proxy/ -run TestIntegration`

### Integration Testing (manual, Windows)

1. Build for Windows: `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o px.exe ./cmd/px`
2. Copy binary + px.ini to Windows host
3. Run: `px.exe --config=px.ini --verbose`
4. Test health: `curl http://127.0.0.1:<port>/health`
5. Test HTTP proxy: `curl --proxy http://127.0.0.1:<port> http://httpbin.org/get`
6. Test HTTPS proxy: `curl --proxy http://127.0.0.1:<port> https://httpbin.org/get`
7. Test quit: `curl http://127.0.0.1:<port>/PxQuit`

### What to verify

- Health endpoint returns 200
- HTTP requests proxy correctly (direct and via upstream)
- HTTPS CONNECT tunnels work
- SSPI/Negotiate auth succeeds against corporate proxy (check with `--verbose`)
- PAC file discovery from platform works
- Noproxy rules bypass upstream for matching hosts
- `--quiet` suppresses startup logs
- `--test` mode exits cleanly after verifying connectivity

## Linting

```bash
# Requires golangci-lint v2.x (see .mise.toml for exact version)
golangci-lint run ./...
```

CI uses `golangci-lint-action@v7` with golangci-lint v2.12.2. The `.golangci.yml` uses v2 config format.

## CI/CD

- **CI** (`.github/workflows/ci.yml`): Tests on ubuntu/macos/windows + lint
- **Release** (`.github/workflows/release.yml`): goreleaser on tag push, builds multi-platform binaries + Docker images

## Key Design Decisions

- **SSPI with SPN**: `InitializeSecurityContextW` receives `HTTP/<proxy-host>` as target name for Negotiate to work with Kerberos
- **Session.Close()**: Auth sessions (especially SSPI) hold native handles — always closed via defer after use
- **PAC parsing**: Strips "PROXY " prefixes from PAC results; handles semicolons as separators
- **Zero-cost debug logs**: `slog.Debug` calls are no-ops when log level > DEBUG — no allocations in hot path
- **Platform proxy discovery**: On Windows uses WinHTTP to get IE proxy config (PAC URL or server list); logged at INFO on first discovery

## Config

See `px.ini` for full commented config. Key flags:
- `--server`: Explicit upstream proxy (host:port)
- `--pac`: PAC file URL or local path
- `--port`: Local listen port (default 3128)
- `--auth`: Force auth type (ANY, NTLM, NEGOTIATE, BASIC, DIGEST, NONE)
- `--noproxy`: Direct connect rules (bypasses upstream)
- `--verbose`: Debug logging to stdout
- `--quiet`/`--silent`: Suppress startup info logs
- `--test`: Start, make a test request, exit
- `--version`, `--health-check`, `--quit`, `--restart`, `--install`, `--uninstall`
