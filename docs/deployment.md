# Deployment guide

How to run px-go beyond a single developer laptop. Pick a pattern, then follow the linked guide.

## Choose a pattern

| Pattern | Best for | Guide |
|---|---|---|
| **Local per machine** | One app or user on a VM/bare-metal host; bind `127.0.0.1` | [VM & bare metal](vm-bare-metal.md) |
| **Per-service on a host** | Multiple apps on one VM, each with its own px instance/port/credentials | [VM & bare metal — per-service](vm-bare-metal.md#per-service-on-one-host) |
| **VM / host gateway** | Same host serves Docker/VM workloads via `hostonly` or LAN clients via `gateway` | [VM & bare metal — shared host](vm-bare-metal.md#shared-proxy-on-a-vm) |
| **Docker Compose** | Small stack of containers sharing one outbound proxy | [Docker & Kubernetes](docker-kubernetes.md#docker-compose) |
| **Kubernetes** | Cluster-wide or sidecar outbound proxy | [Docker & Kubernetes](docker-kubernetes.md) |
| **Windows desktop / server** | SSPI, Task Scheduler, autostart | [Windows](windows.md) |

## Config basics (all patterns)

Precedence: defaults → `px.ini` → `.env` / `PX_*` → CLI flags. See [px.ini](../px.ini) for every option.

Common modes:

| Mode | Bind | Who can connect |
|---|---|---|
| Default (`listen=127.0.0.1`) | Loopback only | Processes on the same host |
| `hostonly=1` | All interfaces (`:port`) | Loopback + IPs assigned to local NICs (Docker/VM on same host) |
| `gateway=1` | All interfaces (`:port`) | Remote clients allowed by `allow` rules |

**Always set `noproxy`** for private ranges and internal DNS suffixes so cluster/LAN traffic does not loop through the corporate upstream.

## Security

Shared or gateway deployments carry real risk (open relay, credential exposure, no TLS to px). Read [security considerations](security.md) before exposing px beyond localhost.

## Related

- [README](../README.md) — quick start, build, flags
- [AGENTS.md](../AGENTS.md) — architecture and CI for contributors
