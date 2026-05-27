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
make tidy fmt test build
```

## Docker
```bash
docker build -f docker/Dockerfile -t px-go .
docker run --rm -p 3128:3128 px-go --gateway --foreground --log=4
```

## Notes
- Windows SSPI is build-tagged and compiled without CGO.
- PAC files are reloaded based on `proxyreload`.
- `/health` returns `200 OK`; `/PxQuit` performs graceful shutdown for local callers.
