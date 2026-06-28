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
go run ./cmd/px --server=upstream.proxy:8080 --username=DOMAIN\\user --port=3128
curl --proxy http://127.0.0.1:3128 http://example.com
```

Config precedence: defaults → `px.ini` → `.env` / `PX_*` → CLI flags. See [px.ini](px.ini).

## Common flags

```bash
--config=path/to/px.ini   --server=proxy:8080   --pac=http://wpad/proxy.pac
--listen=127.0.0.1       --port=3128           --gateway / --hostonly
--username=DOMAIN\\user  --auth=NTLM|NEGOTiate|...
--client-auth=BASIC      --noproxy=localhost,10.0.0.0/8
--health-check           --test=http://httpbin.org/get
```

Environment variables use `PX_*` (e.g. `PX_SERVER`, `PX_PASSWORD`, `PX_CLIENT_AUTH`).

## Build and test

```bash
make tidy fmt test build          # or: go build ./cmd/px
go test ./...
go test -v ./internal/proxy/ -run TestIntegration
```

Windows builds: console `px.exe` and headless `pxw.exe` — see [Windows deployment](docs/windows.md).

## Docker

```bash
docker build -f docker/Dockerfile -t px-go .
docker run --rm -p 3128:3128 px-go --gateway --foreground --log=4
```

Image: `ghcr.io/blackdark/px-go:latest`. For Compose, Kubernetes, and cluster-wide proxy setup see [Docker & Kubernetes](docs/docker-kubernetes.md).

## Documentation

| Guide | Contents |
|---|---|
| [Deployment overview](docs/deployment.md) | Choose a pattern (VM, per-service, Docker, K8s, Windows) |
| [VM & bare metal](docs/vm-bare-metal.md) | systemd, per-service instances, `hostonly` / gateway on servers |
| [Docker & Kubernetes](docs/docker-kubernetes.md) | Compose, K8s manifests, sizing |
| [Windows](docs/windows.md) | Task Scheduler, SSPI, headless `pxw.exe` |
| [Security](docs/security.md) | Open-proxy risk, credentials, TLS, operational hazards |

Contributor notes: [AGENTS.md](AGENTS.md).

## Notes

- `/health` returns `200 OK`; `/PxQuit` shuts down gracefully (local callers).
- PAC files reload on `proxyreload` interval.
- Windows SSPI is build-tagged without CGO.
