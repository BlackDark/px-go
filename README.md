# px-go

Go port of [px](https://github.com/genotrance/px): a local HTTP proxy that authenticates to upstream NTLM/Negotiate/Kerberos proxies, supports CONNECT tunneling, PAC files, allow/noproxy rules, client auth, `/PxQuit`, and health checks.

## Features

- HTTP proxy with direct and upstream-proxy forwarding
- CONNECT tunneling for HTTPS
- Upstream auth: BASIC, DIGEST, NTLM, NEGOTIATE
- Client auth: BASIC, DIGEST, NTLM, NEGOTIATE (SSPI on Windows)
- PAC execution, INI + `.env` + `PX_*` env + CLI config
- Allow-list and noproxy matching (IPs, CIDRs, wildcards, domains)
- Single static binary (~15 MB), Docker images via Goreleaser

## Quick start

```bash
go run ./cmd/px --server=upstream.proxy:8080 --port=3128 --auth=NONE
curl --proxy http://127.0.0.1:3128 http://example.com
```

Upstream requires NTLM/Negotiate/passwords? See [Authentication](docs/authentication.md).

Config precedence: defaults → `px.ini` → `.env` / `PX_*` → CLI flags. See [px.ini](px.ini).

## Common flags

```bash
--config=path/to/px.ini   --server=proxy:8080   --pac=http://wpad/proxy.pac
--listen=127.0.0.1       --port=3128           --gateway / --hostonly
--username=DOMAIN\\user  --auth=NTLM|NEGOTiate|...
--client-auth=BASIC      --noproxy=localhost,10.0.0.0/8
--log=4                  --log-file=/var/log/px-go/px-go.log
--health-check           --test=http://httpbin.org/get
```

Environment variables use `PX_*` (e.g. `PX_SERVER`, `PX_PASSWORD`, `PX_LOG_FILE`, `PX_CLIENT_AUTH`).

## Build and test

```bash
make tidy fmt test build          # or: go build ./cmd/px
go test ./...
go test -v ./internal/proxy/ -run TestIntegration
```

Windows builds: `px-go.exe` / `pxw-go.exe` (releases) or `px.exe` / `pxw.exe` (local) — see [Windows deployment](docs/windows.md).

## Docker

```bash
docker build -f docker/Dockerfile -t px-go .
docker run --rm -p 3128:3128 px-go \
  --server=corp-proxy:8080 --auth=NONE --gateway --foreground --log=4 \
  --allow='10.0.0.0/8,172.16.0.0/12,192.168.0.0/16' \
  --noproxy='localhost,127.0.0.1,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,169.254.0.0/16'
```

Image: `ghcr.io/blackdark/px-go:latest`. See [Docker & Kubernetes](docs/docker-kubernetes.md) and [network defaults](docs/deployment.md#recommended-network-defaults).

## Documentation

| Guide | Contents |
|---|---|
| [Deployment overview](docs/deployment.md) | Choose a pattern; shared `allow` / `noproxy` defaults |
| [Authentication](docs/authentication.md) | Upstream SSPI, NTLM, Kerberos, client auth, K8s secrets |
| [VM & bare metal](docs/vm-bare-metal.md) | systemd, per-service, `hostonly` / gateway |
| [Docker & Kubernetes](docs/docker-kubernetes.md) | Compose, K8s manifests, sizing |
| [Windows](docs/windows.md) | SSPI, WSL2, Task Scheduler, release binary names |
| [Security](docs/security.md) | Open-proxy risk, credentials, TLS |

Contributor notes: [AGENTS.md](AGENTS.md).

## Notes

- `/health` returns `200 OK`; `/PxQuit` shuts down gracefully (local callers).
- PAC files reload on `proxyreload` interval.
- Windows SSPI is build-tagged without CGO.
