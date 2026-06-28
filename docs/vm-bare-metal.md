# VM & bare metal

Run px-go on Linux or Windows VMs and physical servers — locally for one app, per-service on a shared host, or as a gateway for other machines on the network.

Back to [deployment overview](deployment.md).

## When to use this guide

| Scenario | Approach |
|---|---|
| Single app on a VM needs corporate egress | [Local loopback proxy](#local-loopback-one-app-per-vm) |
| Several services on one VM, different credentials or isolation | [Per-service on one host](#per-service-on-one-host) |
| Docker / Podman / LXC on the same VM without publishing port 3128 | [Shared host proxy](#shared-proxy-on-a-vm) with `hostonly=1` |
| Other VMs or LAN clients use this host as outbound proxy | [Gateway mode](#vm-as-lan-gateway) with firewall + `allow` |
| Fleet of identical VMs (CI workers, app servers) | [Fleet rollout](#fleet-rollout-golden-image) |

For containers orchestrated by Kubernetes or Compose, see [Docker & Kubernetes](docker-kubernetes.md).

---

## Local loopback (one app per VM)

Default mode: bind `127.0.0.1:3128`, only processes on the same machine connect. Best for a dedicated VM running one application stack.

### Install binary

```bash
curl -sL https://github.com/BlackDark/px-go/releases/latest/download/px-go_linux_amd64.tar.gz | tar xz
sudo install -m 755 px-go /usr/local/bin/px-go
```

Or build: `CGO_ENABLED=0 go build -ldflags="-s -w" -o px-go ./cmd/px`

### Config (`/etc/px-go/px.ini`)

```ini
[proxy]
server = corp-proxy.example.com:8080
username = DOMAIN\svc-myapp
; password in /etc/px-go/px.env (PX_PASSWORD)

listen = 127.0.0.1
port = 3128
auth = NTLM
noproxy = localhost,127.0.0.1,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,.local

[settings]
threads = 128
idle = 300
socktimeout = 20
foreground = 1
log = 1
log_level = INFO
```

### Secrets (`/etc/px-go/px.env`)

```bash
PX_PASSWORD='change-me'
```

```bash
sudo chmod 600 /etc/px-go/px.env
sudo chown root:pxgo /etc/px-go/px.env
```

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
EnvironmentFile=/etc/px-go/px.env
ExecStart=/usr/local/bin/px-go --config=/etc/px-go/px.ini --foreground
Restart=on-failure
RestartSec=5
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
```

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
export NO_PROXY=localhost,127.0.0.1,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,.local
```

Add these to your app's systemd unit, `/etc/environment`, or service-specific env file.

---

## Per-service on one host

Run **separate px instances** when services on the same VM need:

- Different upstream credentials (separate domain accounts)
- Independent restart / upgrade
- Isolation (one misbehaving client cannot exhaust another's upstream auth)

Each instance gets its own config directory, port, and systemd unit.

### Layout

```
/etc/px-go/
  billing/
    px.ini
    px.env
  reporting/
    px.ini
    px.env
/usr/local/bin/px-go
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
EnvironmentFile=/etc/px-go/%i/px.env
ExecStart=/usr/local/bin/px-go --config=/etc/px-go/%i/px.ini --foreground
Restart=on-failure
RestartSec=5
LimitNOFILE=32768

[Install]
WantedBy=multi-user.target
```

### Example service config (`/etc/px-go/billing/px.ini`)

```ini
[proxy]
server = corp-proxy.example.com:8080
username = DOMAIN\svc-billing
listen = 127.0.0.1
port = 3128
auth = NTLM
noproxy = localhost,127.0.0.1,10.0.0.0/8

[settings]
threads = 64
idle = 300
foreground = 1
log = 1
```

```ini
# /etc/px-go/reporting/px.ini — different port and account
[proxy]
server = corp-proxy.example.com:8080
username = DOMAIN\svc-reporting
listen = 127.0.0.1
port = 3129
auth = NTLM
noproxy = localhost,127.0.0.1,10.0.0.0/8

[settings]
threads = 64
idle = 300
foreground = 1
log = 1
```

```bash
sudo systemctl enable --now px-go@billing px-go@reporting
```

Wire each application to its port:

| Service | `HTTP_PROXY` |
|---|---|
| billing | `http://127.0.0.1:3128` |
| reporting | `http://127.0.0.1:3129` |

### Sizing per instance

| Service profile | `threads` | Memory (typical) |
|---|---|---|
| Light batch / cron | 32–64 | 32–64 Mi |
| Web app / API | 64–128 | 64–128 Mi |
| AI agent / long-lived HTTPS | 128–256 | 128–256 Mi |

One px-go process handles many concurrent tunnels; separate processes are for **credential and blast-radius isolation**, not raw throughput.

---

## Shared proxy on a VM

### `hostonly` — Docker / containers on the same host

Use when container workloads on the VM need px but you do **not** want LAN-wide exposure.

```ini
[proxy]
server = corp-proxy.example.com:8080
username = DOMAIN\svc-vm
hostonly = 1
port = 3128
auth = NTLM
noproxy = localhost,127.0.0.1,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16

[settings]
threads = 128
idle = 300
foreground = 1
log = 1
```

`hostonly=1` binds `:3128` on all interfaces but only accepts clients from loopback and IPs assigned to local NICs (including Docker bridge gateways).

Point containers at the host gateway IP (often `172.17.0.1` for default Docker bridge):

```yaml
# docker-compose snippet
services:
  app:
    environment:
      HTTP_PROXY: http://172.17.0.1:3128
      HTTPS_PROXY: http://172.17.0.1:3128
      NO_PROXY: localhost,127.0.0.1,10.0.0.0/8
```

Or run px in `--network host` mode so containers use `127.0.0.1:3128`.

### VM as LAN gateway

Use when **other machines** (not just local processes) should send traffic through this VM's px instance.

```ini
[proxy]
server = corp-proxy.example.com:8080
username = DOMAIN\svc-gateway
gateway = 1
allow = 10.50.0.0/24
port = 3128
auth = NTLM
noproxy = 10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,localhost

[client]
client_auth = BASIC
client_username = pxclient
; PX_CLIENT_PASSWORD in env file

[settings]
threads = 256
idle = 300
foreground = 1
log = 1
```

**Required:** restrict `allow` to client subnets and enable `client_auth`. Add host firewall rules so only trusted sources reach port 3128:

```bash
# firewalld example
sudo firewall-cmd --permanent --add-rich-rule='rule family="ipv4" source address="10.50.0.0/24" port port="3128" protocol="tcp" accept'
sudo firewall-cmd --reload
```

See [security considerations](security.md) before enabling gateway mode.

---

## Kerberos on Linux VMs

For Negotiate without embedding passwords:

1. Join the VM to the domain (realmd/sssd or equivalent).
2. Enable Kerberos in config:

   ```ini
   [proxy]
   kerberos = 1
   auth = NEGOTIATE
   server = corp-proxy.example.com:8080
   ```

3. Ensure a valid ticket before px starts (cron or systemd `ExecStartPre`):

   ```bash
   kinit -kt /etc/px-go/service.keytab HTTP/corp-proxy.example.com@CORP.EXAMPLE.COM
   ```

4. Run px under a user that can read the keytab, or use `kinit` in the systemd unit.

Windows VMs can use SSPI without explicit passwords when the service runs in an interactive user session — see [Windows deployment](windows.md).

---

## Fleet rollout (golden image)

For many identical VMs (build agents, app tiers):

1. Bake `px-go` binary and a base `px.ini` into the image.
2. Inject per-VM secrets at boot (cloud-init, Ansible, Puppet) into `/etc/px-go/px.env`.
3. Enable `px-go.service` in the image.
4. Validate with `--test`:

   ```bash
   px-go --config=/etc/px-go/px.ini --test=http://httpbin.org/get
   ```

5. Monitor `/health` from your observability stack.

### Ansible sketch

```yaml
- name: Install px-go
  ansible.builtin.unarchive:
    src: "https://github.com/BlackDark/px-go/releases/download/v{{ px_version }}/px-go_linux_amd64.tar.gz"
    dest: /usr/local/bin
    remote_src: true
    extra_opts: [--strip-components=0]

- name: Deploy config
  ansible.builtin.template:
    src: px.ini.j2
    dest: /etc/px-go/px.ini
    mode: "0640"
    owner: pxgo
    group: pxgo

- name: Enable px-go
  ansible.builtin.systemd:
    name: px-go
    enabled: true
    state: started
```

---

## Windows Server as application host

Same patterns apply:

| Pattern | Config |
|---|---|
| Local app | Default `listen=127.0.0.1`, Windows service or Task Scheduler |
| Per-service | Multiple scheduled tasks or services, different `--port` and `--config` |
| Container host | `hostonly=1` or publish port with firewall rules |

Use `pxw.exe` for unattended services. SSPI works when the service account has a suitable logon session — see [Windows deployment](windows.md).

---

## Sizing (single VM)

| VM role | CPU | RAM for px | `threads` | Notes |
|---|---|---|---|---|
| Single app server | 1–2 vCPU | 64–128 Mi | 64–128 | Loopback only |
| Multi-service host | 2–4 vCPU | 128–256 Mi per instance | 64–128 each | One px process per service |
| Gateway for LAN clients | 4+ vCPU | 256–512 Mi | 128–256 | Higher setup concurrency; watch fds |

px-go memory stays low (~15 MB base + ~4–8 KB per active tunnel). CPU spikes during NTLM handshakes, not during idle CONNECT relays.

---

## Troubleshooting

| Symptom | Check |
|---|---|
| `connection refused` on 3128 | `systemctl status px-go`, firewall, correct `listen` / `gateway` / `hostonly` |
| 407 / auth failures upstream | Credentials in env file, `auth` type, corporate lockout |
| Works on host, not in container | Use host bridge IP or `hostonly=1`; verify Docker `NO_PROXY` |
| `too many open files` | Raise `LimitNOFILE` in systemd, kernel `fs.file-max` |
| PAC not applied | VM DNS must resolve WPAD/PAC URL; or use static `--server` |

---

## See also

- [Security considerations](security.md)
- [Docker & Kubernetes](docker-kubernetes.md)
- [Windows deployment](windows.md)
