# Security considerations

Review this before running px-go as a shared, gateway, or cluster-wide proxy.

## Critical risks

### Open proxy / relay abuse

`gateway=1` with a permissive `allow` (e.g. `*.*.*.*`) turns px into an **open HTTP CONNECT relay** through your corporate network. Publishing `-p 3128:3128` without `--allow` and `--server` is especially dangerous in Docker examples.

**Mitigate:** Restrict `allow` to known pod, VM, or office CIDRs. Enable [client authentication](authentication.md#client-authentication) when the listener is reachable beyond loopback.

### No TLS on the client → px hop

px-go speaks plain HTTP as a proxy. Proxy credentials embedded in `http://user:pass@host:port` URLs are visible to anyone who can capture traffic on that network segment.

**Mitigate:** NetworkPolicy / firewall / VLAN isolation; avoid putting credentials in URLs where possible; terminate TLS in front of px only if your client stack supports it.

### Upstream credential theft

Service-account passwords, keytabs, and `.env` files are high-value targets. A compromised host, pod, or backup exposes corporate egress.

**Mitigate:** Dedicated least-privilege accounts, rotation, restrictive file permissions (`0600`), secrets managers, and RBAC on Kubernetes Secrets.

### SSPI / OS identity unavailable in Linux containers

Negotiate/NTLM via the logged-on Windows user **does not work** in a default Linux container. Use explicit `--username` / `--password` (Kubernetes Secrets).

**Mitigate:** See [Authentication → Linux Negotiate](authentication.md#upstream-kerberos-on-linux) and [Kubernetes upstream credentials](authentication.md#kubernetes-upstream-credentials); never assume SSPI in Docker.

### Administrative endpoints

- `/health` — liveness; low risk.
- `/PxQuit` — graceful shutdown; allowed from loopback and local interface IPs. Do not expose px to untrusted networks without considering accidental or malicious shutdown.

**Mitigate:** Prefer `SIGTERM` via systemd or Kubernetes lifecycle. Restrict who can reach the proxy port.

## Operational risks

| Risk | Impact | Mitigation |
|---|---|---|
| Single px instance | All outbound traffic fails when px is down | systemd `Restart=`, K8s replicas + probes, monitoring on `/health` |
| PAC / WPAD unreachable | Wrong or stale upstream route | Static PAC file mount, `dnsConfig`, fallback `--server` |
| File descriptor exhaustion | Proxy stops accepting connections under heavy CONNECT load | `LimitNOFILE` (systemd), raise container limits, monitor open fds |
| Audit gap | Hard to trace who abused egress | Corporate proxy logs + restrict `allow` + client auth |
| Credential in process list | `ps` / `/proc` may show env vars | `EnvironmentFile` with restricted permissions; avoid CLI passwords |

## Pattern-specific notes

- **[VM & bare metal](vm-bare-metal.md)** — `gateway=1` on a VM exposes every machine that can route to it; pair with host firewall.
- **[Docker & Kubernetes](docker-kubernetes.md)** — Default cluster networking is flat; NetworkPolicy is strongly recommended.
- **[Windows](windows.md)** — SSPI requires an interactive logon session; do not run headless SSPI as "any user".
