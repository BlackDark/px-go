# Authentication

How px-go authenticates **to the upstream corporate proxy** and **from clients** connecting to px. Deployment examples assume an upstream proxy that needs **no credentials**; use this guide when yours requires NTLM, Negotiate, Basic, or Digest.

Back to [deployment overview](deployment.md).

## Two auth layers

```
Client app  ──►  px-go (client auth?)  ──►  corporate proxy (upstream auth?)  ──►  internet
```

| Layer | Config keys | When it applies |
|---|---|---|
| **Upstream** | `username`, `password` / `PX_PASSWORD`, `auth`, `kerberos` | Corporate proxy requires login |
| **Client** | `client_auth`, `client_username`, `client_password` / `PX_CLIENT_PASSWORD` | Other hosts/containers connect to px (`gateway`, `hostonly`) |

If px binds `127.0.0.1` only and only local apps connect, leave `client_auth = NONE` (default).

---

## Upstream: no authentication

Use when the corporate proxy accepts connections without credentials (common in examples and lab setups):

```ini
[proxy]
server = corp-proxy.example.com:8080
auth = NONE
```

Or omit `username` / `auth` and px discovers or connects directly.

---

## Upstream: explicit username and password

Required in **Linux containers** and anywhere SSPI/OS identity is unavailable.

```ini
[proxy]
server = corp-proxy.example.com:8080
username = DOMAIN\service-account
auth = NTLM
```

```bash
# /etc/px-go/px.env or Kubernetes Secret
PX_PASSWORD='secret'
```

| `auth` value | Use when |
|---|---|
| `NTLM` | Corporate proxy expects NTLM |
| `BASIC` | Plain Basic auth to upstream |
| `DIGEST` | Digest auth to upstream |
| `NEGOTIATE` | SPNEGO/Kerberos to upstream (needs ticket or SSPI) |
| `ANY` | Try methods until one works (default when unset) |

Docker / Kubernetes: store `PX_PASSWORD` in a Secret — see [Kubernetes upstream auth](#kubernetes-upstream-credentials).

---

## Upstream: Windows SSPI (logged-on user)

On Windows, px can authenticate upstream using the **interactive user's domain identity** — no password in config.

```ini
[proxy]
server = corp-proxy.example.com:8080
auth = NEGOTIATE
; no username / password — SSPI uses logged-on session
```

Requirements:

- px runs **while the user is logged on** (Task Scheduler: "Run only when user is logged on").
- Do not use "Run whether user is logged on or not" for SSPI.
- Works for desktop dev, WSL host proxy, and interactive server sessions.

See [Windows + WSL](windows.md#wsl2-use-windows-login-via-sspi).

---

## Upstream: Kerberos on Linux

For Negotiate without embedding passwords on Linux VMs:

```ini
[proxy]
server = corp-proxy.example.com:8080
kerberos = 1
auth = NEGOTIATE
```

1. Join the host to the domain (realmd/sssd).
2. Obtain a ticket before px starts:

   ```bash
   kinit -kt /etc/px-go/service.keytab HTTP/corp-proxy.example.com@CORP.EXAMPLE.COM
   ```

3. Optional systemd `ExecStartPre`:

   ```ini
   ExecStartPre=/usr/bin/kinit -kt /etc/px-go/service.keytab HTTP/corp-proxy.example.com@CORP.EXAMPLE.COM
   ```

---

## Upstream: PAC instead of fixed server

```ini
[proxy]
pac = http://wpad.corp.example.com/wpad.dat
auth = NEGOTIATE
username = DOMAIN\user
```

Or PAC only with SSPI on Windows (no username). Ensure the host/container can resolve and reach the PAC URL.

---

## Client authentication

Enable when px listens beyond strict loopback (`gateway=1` or `hostonly=1`) and you want clients to prove identity before using the proxy.

```ini
[client]
client_auth = BASIC
client_username = pxclient
```

```bash
PX_CLIENT_PASSWORD='client-secret'
```

Clients set:

```bash
HTTP_PROXY=http://pxclient:client-secret@px-host:3128
```

| `client_auth` | Notes |
|---|---|
| `NONE` | Default; fine for loopback-only or when NetworkPolicy/firewall is the control |
| `BASIC` | Simplest for containers and scripts |
| `NTLM` / `NEGOTIATE` | Windows clients with SSPI; see `client_nosspi` |

See [security considerations](security.md) before exposing px without client auth on a shared network.

---

## Kubernetes upstream credentials

Add when your corporate proxy requires auth (not covered by the default manifests):

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: px-upstream-auth
stringData:
  PX_USERNAME: "DOMAIN\\service-account"
  PX_PASSWORD: "change-me"
---
# In Deployment container env:
env:
  - name: PX_USERNAME
    valueFrom:
      secretKeyRef:
        name: px-upstream-auth
        key: PX_USERNAME
  - name: PX_PASSWORD
    valueFrom:
      secretKeyRef:
        name: px-upstream-auth
        key: PX_PASSWORD
args:
  - --auth=NTLM
  # ... other args unchanged
```

For client auth on the px Service, add `PX_CLIENT_USERNAME` / `PX_CLIENT_PASSWORD` similarly — see [client authentication](#client-authentication).

---

## Quick reference

| Environment | Upstream auth approach |
|---|---|
| Windows desktop / WSL host | SSPI (`auth=NEGOTIATE`, logged-on user) — [Windows + WSL](windows.md#wsl2-use-windows-login-via-sspi) |
| Linux VM / systemd | Service account + `PX_PASSWORD`, or Kerberos keytab |
| Docker / Kubernetes | **Always** explicit credentials or keytab; SSPI unavailable |
| Upstream needs no login | `auth=NONE`, `server=` only — used in [deployment examples](deployment.md#recommended-network-defaults) |

## See also

- [Security considerations](security.md)
- [Windows deployment](windows.md)
- [VM & bare metal](vm-bare-metal.md)
- [Docker & Kubernetes](docker-kubernetes.md)
