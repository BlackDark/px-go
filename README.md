<div align="center">

# px-go

**Local proxy that signs in to corporate NTLM, Negotiate, and Kerberos proxies.**<br>
For developers and AI agents on Windows, macOS, and Linux. Go port of [px](https://github.com/genotrance/px).

[![CI](https://github.com/BlackDark/px-go/actions/workflows/ci.yml/badge.svg)](https://github.com/BlackDark/px-go/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/BlackDark/px-go)](https://github.com/BlackDark/px-go/releases/latest)
[![License](https://img.shields.io/github/license/BlackDark/px-go)](LICENSE)

</div>

## Why

Corporate proxies ask for NTLM or Kerberos. curl, editors, package managers, and agents usually cannot answer. px-go listens on localhost, authenticates upstream, and forwards what those tools already send.

- **One binary**: about 15 MB, no runtime to install. Windows releases also include a headless build for Task Scheduler.
- **Existing clients**: point them at px-go like any HTTP proxy. CONNECT covers HTTPS.
- **Wherever the proxy is**: an upstream host, a PAC file, or the Windows system proxy.

## Install

Download a build from [GitHub Releases](https://github.com/BlackDark/px-go/releases/latest) (`px-go` on Linux and macOS, `px-go.exe` and headless `pxw-go.exe` on Windows). Or run the image:

```bash
docker run --rm -p 3128:3128 ghcr.io/blackdark/px-go:latest \
  --server=corp-proxy:8080 --auth=NONE --gateway --foreground --log=4 \
  --allow='10.0.0.0/8,172.16.0.0/12,192.168.0.0/16' \
  --noproxy='localhost,127.0.0.1,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,169.254.0.0/16'
```

```bash
px-go --server=upstream.proxy:8080 --port=3128 --auth=NONE
curl --proxy http://127.0.0.1:3128 http://example.com
```

Upstream needs NTLM, Negotiate, or a password? See [Authentication](docs/authentication.md).

Config precedence: defaults, then `px.toml` (else `px.ini`), then `.env` / `PX_*`, then CLI flags. See [px.toml](px.toml) or [px.ini](px.ini).

```bash
--config=path/to/px.toml  --server=proxy:8080   --pac=http://wpad/proxy.pac
--listen=127.0.0.1       --port=3128           --gateway / --hostonly
--username=DOMAIN\\user  --auth=NTLM|NEGOTiate|...
--client-auth=BASIC      --noproxy=localhost,10.0.0.0/8
--log=4                  --log-file=/var/log/px-go/px-go.log
--health-check           --test=http://httpbin.org/get
```

Environment variables use `PX_*` (for example `PX_SERVER`, `PX_PASSWORD`, `PX_LOG_FILE`, `PX_CLIENT_AUTH`).

## Features

### Proxy

- **HTTP and CONNECT**: direct connections, or forwarding through an upstream proxy.
- **Upstream auth**: BASIC, DIGEST, NTLM, NEGOTIATE. Windows can use SSPI. Other systems use a username and password, and Kerberos from a ticket or a password.
- **Client auth**: BASIC, DIGEST, NTLM, NEGOTIATE for programs that connect to px-go.
- **PAC, allow, noproxy**: run a PAC file, and match IPs, CIDRs, wildcards, and domains.

### Run it

- **Health**: `GET /health` returns 200. `GET /PxQuit` from a local caller shuts the process down.
- **PAC reload**: PAC files reload on the `proxyreload` interval.
- **Logs**: stdout or a file. The Windows headless build has no console, so set `log=1` or `--log-file`.

## FAQ

### How do I sign in to an NTLM proxy?

On Windows, SSPI uses the signed-in user. Elsewhere, pass `--username` and a password (prompt, keyring, or `PX_PASSWORD`). See [Authentication](docs/authentication.md).

### Can I run it in Docker or Kubernetes?

Image: `ghcr.io/blackdark/px-go`. Compose and manifests: [Docker & Kubernetes](docs/docker-kubernetes.md). Build a local image with `mise run docker-build`.

### Which Windows binary do I want?

`px-go.exe` has a console. `pxw-go.exe` does not; Task Scheduler should run that one. `--install` looks for a headless sibling named from the binary you started (`pxw.exe` beside `px.exe`, `px-gow.exe` beside `px-go.exe`). See [Windows](docs/windows.md).

## Documentation

| Guide | Contents |
|---|---|
| [Deployment overview](docs/deployment.md) | Choose a pattern; shared `allow` / `noproxy` defaults |
| [Authentication](docs/authentication.md) | Upstream SSPI, NTLM, Kerberos, client auth, K8s secrets |
| [VM & bare metal](docs/vm-bare-metal.md) | systemd, per-service, `hostonly` / gateway |
| [Docker & Kubernetes](docs/docker-kubernetes.md) | Compose, K8s manifests, sizing |
| [Windows](docs/windows.md) | SSPI, WSL2, Task Scheduler, release binary names |
| [Security](docs/security.md) | Open-proxy risk, credentials, TLS |

## Development

Requires [mise](https://mise.jdx.dev).

```bash
git clone https://github.com/BlackDark/px-go && cd px-go
mise install
mise run check
```

Proxy changes: `go test -v ./internal/proxy/ -run TestIntegration`.

Contributor notes: [AGENTS.md](AGENTS.md).

---

<div align="center"><sub>

[Report a bug](https://github.com/BlackDark/px-go/issues/new) · License: [Apache-2.0](LICENSE)<br>
px-go is not affiliated with genotrance/px.

</sub></div>
