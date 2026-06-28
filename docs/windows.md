# Windows deployment

Run px-go on Windows desktops and servers. On Windows, px can authenticate upstream using the **logged-on user's SSPI identity** — no password in config.

Back to [deployment overview](deployment.md). Upstream/client auth details: [Authentication](authentication.md).

## Binaries

Each release zip contains:

- **`px.exe`** — console build for interactive debugging or manual runs.
- **`pxw.exe`** — headless build (`-H=windowsgui`) for Task Scheduler and autostart.

`--install` registers `pxw.exe` automatically if it sits alongside `px.exe`.

## Setup

1. Download from [GitHub Releases](https://github.com/BlackDark/px-go/releases) or build:

   ```powershell
   GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -o px.exe ./cmd/px
   GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w -H=windowsgui" -o pxw.exe ./cmd/px
   ```

2. Install to a permanent path:

   ```
   C:\Tools\px\px.exe
   C:\Tools\px\pxw.exe
   C:\Tools\px\px.ini
   ```

3. Autostart:

   ```powershell
   C:\Tools\px\px.exe --config=C:\Tools\px\px.ini --install
   ```

## Recommended `px.ini`

Uses [network defaults](deployment.md#recommended-network-defaults). Upstream auth via SSPI (logged-on user):

```ini
[proxy]
server = corp-proxy.example.com:8080
auth = NEGOTIATE
; no username/password — uses Windows SSPI

listen = 127.0.0.1
port = 3128
noproxy = localhost,127.0.0.1,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,169.254.0.0/16,.local

[settings]
log = 1
threads = 128
idle = 300
foreground = 0
```

If the upstream proxy needs **no authentication**, set `auth = NONE` and omit credentials. If it needs explicit NTLM/passwords instead of SSPI, see [Authentication](authentication.md).

## SSPI and sessions

- Task must run **only when the user is logged on** — SSPI needs an interactive session token.
- Do not use "Run whether user is logged on or not" for SSPI.
- `log=1` (file next to binary) or `log=2` (cwd). `log=4` (stdout) is empty in headless `pxw.exe`.

---

## WSL2: use Windows login via SSPI

Run px on **Windows** (not inside WSL) so upstream auth uses your **existing Windows domain login**. Point WSL tools at the Windows host.

### 1. Windows `px.ini` for WSL clients

Allow WSL to connect via `hostonly` (recommended) or mirrored localhost:

```ini
[proxy]
server = corp-proxy.example.com:8080
auth = NEGOTIATE

hostonly = 1
port = 3128
noproxy = localhost,127.0.0.1,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,169.254.0.0/16,.local

[settings]
threads = 128
idle = 300
foreground = 0
log = 1
```

Start px (logged-on user session):

```powershell
C:\Tools\px\px.exe --config=C:\Tools\px\px.ini
# or register autostart via --install / Task Scheduler
```

### 2. WSL proxy environment

**Option A — WSL mirrored networking** (Windows 11 22H2+, `[wsl2] networkingMode=mirrored` in `.wslconfig`):

Windows services on `127.0.0.1` are reachable from WSL directly:

```bash
# ~/.bashrc or ~/.zshrc
export HTTP_PROXY=http://127.0.0.1:3128
export HTTPS_PROXY=http://127.0.0.1:3128
export NO_PROXY=localhost,127.0.0.1,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,169.254.0.0/16,.local
```

**Option B — classic WSL2 NAT** (default on older setups):

Use the Windows host IP from WSL (usually the `/etc/resolv.conf` nameserver):

```bash
WIN_HOST=$(grep nameserver /etc/resolv.conf | awk '{print $2}')
export HTTP_PROXY=http://${WIN_HOST}:3128
export HTTPS_PROXY=http://${WIN_HOST}:3128
export NO_PROXY=localhost,127.0.0.1,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,169.254.0.0/16,.local
```

Requires `hostonly=1` in `px.ini` so px accepts connections from the WSL virtual NIC.

### 3. Verify from WSL

```bash
curl -x "$HTTP_PROXY" http://example.com
curl -x "$HTTP_PROXY" https://example.com
```

### 4. Do not run px inside WSL for SSPI

The Linux binary inside WSL **cannot** use Windows SSPI. You would need explicit [username/password or Kerberos](authentication.md) in WSL — defeating the purpose of using the Windows login.

### WSL troubleshooting

| Symptom | Fix |
|---|---|
| `connection refused` from WSL | px not running on Windows; wrong host IP; enable `hostonly=1` for NAT mode |
| 407 / auth failure upstream | User not domain-joined or not logged on; try `px.exe --verbose` in console |
| Works in Windows, not WSL | Use `WIN_HOST` IP or enable mirrored networking |
| Looping internal traffic | Check `NO_PROXY` includes private ranges |

---

## Task Scheduler (alternative to `--install`)

```powershell
$action = New-ScheduledTaskAction -Execute "C:\Tools\px\pxw.exe" `
  -Argument "--config=C:\Tools\px\px.ini" `
  -WorkingDirectory "C:\Tools\px"

$trigger = New-ScheduledTaskTrigger -AtLogOn

$settings = New-ScheduledTaskSettingsSet `
  -AllowStartIfOnBatteries `
  -DontStopIfGoingOnBatteries `
  -ExecutionTimeLimit (New-TimeSpan) `
  -RestartCount 3 `
  -RestartInterval (New-TimeSpan -Minutes 1)

Register-ScheduledTask -TaskName "px-proxy" `
  -Action $action -Trigger $trigger -Settings $settings `
  -RunLevel Highest -Description "px-go local auth proxy"
```

## Operations

| Action | Command |
|---|---|
| Health check | `curl http://127.0.0.1:3128/health` |
| Graceful stop | `curl http://127.0.0.1:3128/PxQuit` or stop the task |
| Debug | Run `px.exe --verbose` in a console |

## See also

- [Authentication](authentication.md) — SSPI, NTLM, client auth
- [Security considerations](security.md)
- [VM & bare metal](vm-bare-metal.md) — Windows Server as LAN gateway
