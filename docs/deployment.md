# Deployment guide

How to run px-go beyond a single developer laptop. Pick a pattern, then follow the linked guide.

## Choose a pattern

| Pattern | Best for | Guide |
|---|---|---|
| **Local per machine** | One app or user on a VM/bare-metal host; bind `127.0.0.1` | [VM & bare metal](vm-bare-metal.md) |
| **Per-service on a host** | Multiple apps on one VM, each with its own px instance/port | [VM & bare metal — per-service](vm-bare-metal.md#per-service-on-one-host) |
| **VM / host gateway** | Docker/VM workloads via `hostonly`, or LAN clients via `gateway` | [VM & bare metal — shared host](vm-bare-metal.md#shared-proxy-on-a-vm) |
| **Windows + WSL** | WSL tools use Windows login (SSPI) via px on the host | [Windows — WSL](windows.md#wsl2-use-windows-login-via-sspi) |
| **Docker Compose** | Small stack of containers sharing one outbound proxy | [Docker & Kubernetes](docker-kubernetes.md#docker-compose) |
| **Kubernetes** | Cluster-wide or sidecar outbound proxy | [Docker & Kubernetes](docker-kubernetes.md) |
| **Windows desktop / server** | SSPI, Task Scheduler, autostart | [Windows](windows.md) |

## Recommended network defaults

Use these in every deployment. Adjust CIDRs to match your environment (especially Kubernetes pod/service CIDRs).

### `noproxy` — bypass upstream for private traffic

Traffic to these destinations goes **direct**, not through the corporate proxy:

```ini
noproxy = localhost,127.0.0.1,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,169.254.0.0/16,.local
```

Kubernetes — append cluster DNS suffixes:

```ini
noproxy = .svc,.svc.cluster.local,.cluster.local,localhost,127.0.0.1,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,169.254.0.0/16
```

Client env (`NO_PROXY`) should mirror the same list.

### `allow` — who may connect to px

Only relevant when `gateway=1` or `hostonly=1`. **Do not use `*.*.*.*`.** Restrict to private RFC 1918 ranges at minimum:

```ini
allow = 10.0.0.0/8,172.16.0.0/12,192.168.0.0/16
```

Tighten further when you know client subnets (e.g. `10.244.0.0/16` for pod CIDR only).

### Upstream auth in examples

Deployment examples assume an upstream proxy with **no authentication**. If your corporate proxy requires NTLM, Negotiate, or passwords, see [Authentication](authentication.md).

## Config basics

Precedence: defaults → `px.ini` → `.env` / `PX_*` → CLI flags. See [px.ini](../px.ini) for every option.

| Mode | Bind | Who can connect |
|---|---|---|
| Default (`listen=127.0.0.1`) | Loopback only | Processes on the same host |
| `hostonly=1` | All interfaces (`:port`) | Loopback + IPs on local NICs (Docker/WSL on same host) |
| `gateway=1` | All interfaces (`:port`) | Remote clients matching `allow` |

## Security

Shared or gateway deployments carry real risk (open relay, credential exposure, no TLS to px). Read [security considerations](security.md) before exposing px beyond localhost.

## Related

| Doc | Contents |
|---|---|
| [Authentication](authentication.md) | Upstream SSPI, NTLM, Kerberos, client auth, K8s secrets |
| [Security](security.md) | Open-proxy risk, credentials, TLS |
| [README](../README.md) | Quick start, build, flags |
| [AGENTS.md](../AGENTS.md) | Architecture and CI for contributors |
