# VM & bare metal

Run px-go on Linux or Windows VMs and physical servers — locally for one app, per-service on a shared host, or as a gateway for other machines on the network.

Examples assume the **upstream proxy requires no authentication**. See [Authentication](authentication.md) for NTLM, SSPI, Kerberos, and client auth.

Network defaults (`allow`, `noproxy`): [deployment guide](deployment.md#recommended-network-defaults).

Back to [deployment overview](deployment.md).

## When to use this guide

| Scenario | Approach |
|---|---|
| Single app on a VM needs corporate egress | [Local loopback proxy](#local-loopback-one-app-per-vm) |
| Several services on one VM, different ports or isolation | [Per-service on one host](#per-service-on-one-host) |
| Docker / Podman / LXC on the same VM | [Shared host proxy](#shared-proxy-on-a-vm) with `hostonly=1` |
| Other VMs or LAN clients use this host as outbound proxy | [Gateway mode](#vm-as-lan-gateway) with firewall + `allow` |
| Fleet of identical VMs (CI workers, app servers) | [Fleet rollout](#fleet-rollout-golden-image) |

For containers orchestrated by Kubernetes or Compose, see [Docker & Kubernetes](docker-kubernetes.md). For Windows + WSL with SSPI, see [Windows — WSL](windows.md#wsl2-use-windows-login-via-sspi).

---

## Local loopback (one app per VM)

Default mode: bind `127.0.0.1:3128`, only processes on the same machine connect.

### Install binary

```bash
# From GitHub Releases (replace VERSION, e.g. 0.1.0):
VERSION=0.1.0
curl -sL "https://github.com/BlackDark/px-go/releases/download/v${VERSION}/px-go_${VERSION}_linux_amd64.tar.gz" | tar xz
sudo install -m 755 px-go /usr/local/bin/px-go

# Or with GitHub CLI:
gh release download --repo BlackDark/px-go --pattern 'px-go_*_linux_amd64.tar.gz' --output - | tar xz
sudo install -m 755 px-go /usr/local/bin/px-go
```

Or build: `CGO_ENABLED=0 go build -ldflags="-s -w" -o px-go ./cmd/px`

### Config (`/etc/px-go/px.ini`)

```ini
[proxy]
server = corp-proxy.example.com:8080
auth = NONE

listen = 127.0.0.1
port = 3128
noproxy = localhost,127.0.0.1,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,169.254.0.0/16,.local

[settings]
threads = 128
idle = 300
socktimeout = 20
foreground = 1
log = 1
log_file = /var/log/px-go/px-go.log
log_level = INFO
```

> Upstream requires NTLM/passwords? See [Authentication → explicit credentials](authentication.md#upstream-explicit-username-and-password).
>
> Logging paths: [deployment guide → Logging](deployment.md#logging).

### systemd unit

```ini
# /etc/systemd/system/px-go.service
[Unit]
Description=px-go outbound proxy
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=pxgo
Group=pxgo
LogsDirectory=px-go
ExecStart=/usr/local/bin/px-go --config=/etc/px-go/px.ini --foreground
Restart=on-failure
RestartSec=5
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
```

`LogsDirectory=px-go` creates `/var/log/px-go` owned by the service user (matches `log_file` above).

```bash
sudo useradd -r -s /usr/sbin/nologin pxgo
sudo systemctl daemon-reload
sudo systemctl enable --now px-go
curl -x http://127.0.0.1:3128 http://example.com
```

### Point the application at px

```bash
export HTTP_PROXY=http://127.0.0.1:3128
export HTTPS_PROXY=http://127.0.0.1:3128
export NO_PROXY=localhost,127.0.0.1,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,169.254.0.0/16,.local
```

---

## Per-service on one host

Separate px instances when services need different ports, configs, or [upstream credentials](authentication.md#upstream-explicit-username-and-password).

### Layout

```
/etc/px-go/
  billing/px.ini      → port 3128
  reporting/px.ini    → port 3129
/etc/systemd/system/px-go@.service
```

### Template unit

```ini
# /etc/systemd/system/px-go@.service
[Unit]
Description=px-go proxy for %i
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=pxgo
Group=pxgo
ExecStart=/usr/local/bin/px-go --config=/etc/px-go/%i/px.ini --foreground
Restart=on-failure
RestartSec=5
LimitNOFILE=32768

[Install]
WantedBy=multi-user.target
```

### Example configs

```ini
# /etc/px-go/billing/px.ini
[proxy]
server = corp-proxy.example.com:8080
auth = NONE
listen = 127.0.0.1
port = 3128
noproxy = localhost,127.0.0.1,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,169.254.0.0/16,.local

[settings]
threads = 64
idle = 300
foreground = 1
log = 1
```

```ini
# /etc/px-go/reporting/px.ini
[proxy]
server = corp-proxy.example.com:8080
auth = NONE
listen = 127.0.0.1
port = 3129
noproxy = localhost,127.0.0.1,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,169.254.0.0/16,.local

[settings]
threads = 64
idle = 300
foreground = 1
log = 1
```

```bash
sudo systemctl enable --now px-go@billing px-go@reporting
```

| Service | `HTTP_PROXY` |
|---|---|
| billing | `http://127.0.0.1:3128` |
| reporting | `http://127.0.0.1:3129` |

---

## Shared proxy on a VM

### `hostonly` — Docker / containers on the same host

```ini
[proxy]
server = corp-proxy.example.com:8080
auth = NONE
hostonly = 1
port = 3128
noproxy = localhost,127.0.0.1,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,169.254.0.0/16,.local

[settings]
threads = 128
idle = 300
foreground = 1
log = 1
```

`hostonly=1` accepts clients from loopback and local NIC IPs (including Docker bridge gateways). No `allow` rule needed.

Point containers at the host gateway IP (often `172.17.0.1`):

```yaml
environment:
  HTTP_PROXY: http://172.17.0.1:3128
  HTTPS_PROXY: http://172.17.0.1:3128
  NO_PROXY: localhost,127.0.0.1,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,169.254.0.0/16,.local
```

### VM as LAN gateway

```ini
[proxy]
server = corp-proxy.example.com:8080
auth = NONE
gateway = 1
allow = 10.50.0.0/24
port = 3128
noproxy = 10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,169.254.0.0/16,localhost,127.0.0.1

[client]
client_auth = NONE

[settings]
threads = 256
idle = 300
foreground = 1
log = 1
```

Replace `10.50.0.0/24` with your client subnet. Add firewall rules and consider [client authentication](authentication.md#client-authentication) — see [security](security.md).

```bash
sudo firewall-cmd --permanent --add-rich-rule='rule family="ipv4" source address="10.50.0.0/24" port port="3128" protocol="tcp" accept'
sudo firewall-cmd --reload
```

---

## Fleet rollout (golden image)

1. Bake `px-go` and base `px.ini` into the image.
2. Inject secrets at boot only if [upstream auth](authentication.md) is required.
3. Enable `px-go.service`.
4. Validate: `px-go --config=/etc/px-go/px.ini --test=http://httpbin.org/get`
5. Monitor `/health`.

---

## Windows Server as application host

| Pattern | Config |
|---|---|
| Local app | `listen=127.0.0.1`, Task Scheduler or service |
| Per-service | Multiple tasks, different `--port` and `--config` |
| Container host | `hostonly=1` + firewall |

Use `pxw-go.exe` / `pxw.exe` headless. SSPI: [Windows deployment](windows.md).

---

## Sizing (single VM)

| VM role | CPU | RAM for px | `threads` |
|---|---|---|---|
| Single app server | 1–2 vCPU | 64–128 Mi | 64–128 |
| Multi-service host | 2–4 vCPU | 128–256 Mi each | 64–128 each |
| Gateway for LAN clients | 4+ vCPU | 256–512 Mi | 128–256 |

---

## Troubleshooting

| Symptom | Check |
|---|---|
| Service exits immediately (code 1) | `log=1` without writable path — set `log_file` or `log=2` + `WorkingDirectory`; see [Logging](deployment.md#logging) |
| `connection refused` on 3128 | `systemctl status px-go`, firewall, `listen` / `gateway` / `hostonly` |
| 407 / auth failures upstream | [Authentication](authentication.md) — credentials, `auth` type |
| Works on host, not in container | Host bridge IP, `hostonly=1`, `NO_PROXY` |
| `too many open files` | `LimitNOFILE` in systemd |
| PAC not applied | DNS reachability; or use static `--server` |

---

## See also

- [Authentication](authentication.md)
- [Security considerations](security.md)
- [Docker & Kubernetes](docker-kubernetes.md)
- [Windows deployment](windows.md)
