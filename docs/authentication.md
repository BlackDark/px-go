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

Use when the corporate proxy accepts connections without credentials (used in Linux/Docker/K8s deployment examples):

```ini
[proxy]
server = corp-proxy.example.com:8080
auth = NONE
```

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
| `NEGOTIATE` | SPNEGO/Kerberos to upstream (Linux: needs password below) |
| `ANY` | Try methods until one works (default when unset) |

Docker / Kubernetes: store `PX_PASSWORD` in a Secret — see [Kubernetes upstream credentials](#kubernetes-upstream-credentials).

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

See [Windows + WSL](windows.md#wsl2-use-windows-login-via-sspi).

---

## Upstream: Kerberos on Linux

On Linux, Negotiate uses **username and password** via gokrb5 (`NewWithPassword`). Optional `kerberos=1` runs periodic `kinit` to refresh tickets in a credential cache.

```ini
[proxy]
server = corp-proxy.example.com:8080
username = svc-account@CORP.EXAMPLE.COM
auth = NEGOTIATE
kerberos = 1
```

```bash
PX_PASSWORD='secret'
```

Requirements:

- Valid `/etc/krb5.conf` (or `KRB5_CONFIG`) on the host.
- `username` must include the realm (`user@REALM` or `DOMAIN\user`).

> **Keytab-only:** Not supported by px-go today. Negotiate on Linux always uses `username` + `PX_PASSWORD`. External `kinit -kt` alone does not replace that unless you extend px-go.

---

## Upstream: PAC instead of fixed server

```ini
[proxy]
pac = http://wpad.corp.example.com/wpad.dat
auth = NEGOTIATE
username = DOMAIN\user
```

Or PAC with SSPI on Windows (no username). Ensure the host/container can resolve and reach the PAC URL.

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

For client auth on the px Service, add `PX_CLIENT_USERNAME` / `PX_CLIENT_PASSWORD` similarly.

---

## Troubleshooting

| Symptom | Check |
|---|---|
| 407 from upstream | `auth` type, `PX_PASSWORD`, account lockout |
| SSPI fails on Windows | User logged on? Task set to "Run only when user is logged on"? |
| Negotiate fails on Linux | `krb5.conf`, realm in username, password correct |
| Works locally, not in K8s | Secret mounted? Linux pods cannot use SSPI |
| Client 407 to px | `client_auth` enabled? Credentials in proxy URL? |

---

## Quick reference

| Environment | Upstream auth approach |
|---|---|
| Windows desktop / WSL host | SSPI (`auth=NEGOTIATE`, logged-on user) — [Windows + WSL](windows.md#wsl2-use-windows-login-via-sspi) |
| Linux VM / systemd | Service account + `PX_PASSWORD`, or Negotiate + `kerberos=1` |
| Docker / Kubernetes | **Always** explicit credentials; SSPI unavailable |
| Upstream needs no login | `auth=NONE`, `server=` only — used in [deployment examples](deployment.md#recommended-network-defaults) |

## See also

- [Security considerations](security.md)
- [Windows deployment](windows.md)
- [VM & bare metal](vm-bare-metal.md)
- [Docker & Kubernetes](docker-kubernetes.md)
