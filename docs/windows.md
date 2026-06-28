# Windows deployment

Run px-go on Windows desktops and servers. On Windows, px can authenticate upstream using the **logged-on user's SSPI identity** — no password in config.

Back to [deployment overview](deployment.md). Upstream/client auth details: [Authentication](authentication.md).

## Binaries

**GitHub Releases** ship `px-go.exe` (console) and `pxw-go.exe` (headless) inside the Windows zip.

**Local builds** often use `px.exe` / `pxw.exe` — same behavior, different names.

| Binary | Purpose |
|---|---|
| Console (`px-go.exe` / `px.exe`) | Debugging, manual runs, `--install` |
| Headless (`pxw-go.exe` / `pxw.exe`) | Task Scheduler, autostart, no console window |

### `--install` and headless binary names

`--install` registers autostart using the headless sibling of the executable you run: it looks for `<basename-without-ext>w.exe` next to the console binary.

| You run | `--install` expects headless |
|---|---|
| `px-go.exe` | `px-gow.exe` |
| `px.exe` | `pxw.exe` |

Release zips contain `pxw-go.exe`, not `px-gow.exe`. Either:

- Register Task Scheduler manually with `pxw-go.exe` (recommended for releases), or
- Copy/rename `pxw-go.exe` → `px-gow.exe` beside `px-go.exe` before `--install`.

## Setup

1. Download from [GitHub Releases](https://github.com/BlackDark/px-go/releases) or build:

   ```powershell
   GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -o px.exe ./cmd/px
   GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w -H=windowsgui" -o pxw.exe ./cmd/px
   ```

2. Install to a permanent path (release names shown):

   ```
   C:\Tools\px\px-go.exe
   C:\Tools\px\pxw-go.exe
   C:\Tools\px\px.ini
   ```

3. Autostart — Task Scheduler with headless binary (works with release names):

   ```powershell
   $action = New-ScheduledTaskAction -Execute "C:\Tools\px\pxw-go.exe" `
     -Argument "--config=C:\Tools\px\px.ini" `
     -WorkingDirectory "C:\Tools\px"

   $trigger = New-ScheduledTaskTrigger -AtLogOn
   Register-ScheduledTask -TaskName "px-proxy" -Action $action -Trigger $trigger `
     -RunLevel Highest -Description "px-go outbound proxy" `
     -User $env:USERNAME
   ```

   In Task Scheduler UI, confirm **“Run only when user is logged on”** is selected (required for SSPI).

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

If the upstream proxy needs **no authentication**, set `auth = NONE`. For explicit NTLM/passwords, see [Authentication](authentication.md).

## SSPI and sessions

- Task must run **only when the user is logged on** — SSPI needs an interactive session token.
- Do not use "Run whether user is logged on or not" for SSPI.
- `log=1` (file next to binary) or `log=2` (cwd). `log=4` (stdout) is empty in headless mode.

---

## WSL2: use Windows login via SSPI

Run px on **Windows** (not inside WSL) so upstream auth uses your **existing Windows domain login**. Point WSL tools at the Windows host.

### 1. Windows `px.ini` for WSL clients

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

Start px in a logged-on session:

```powershell
C:\Tools\px\px-go.exe --config=C:\Tools\px\px.ini
```

### 2. WSL proxy environment

**Option A — WSL mirrored networking** (Windows 11 22H2+, `[wsl2] networkingMode=mirrored` in `.wslconfig`):

```bash
# ~/.bashrc or ~/.zshrc
export HTTP_PROXY=http://127.0.0.1:3128
export HTTPS_PROXY=http://127.0.0.1:3128
export NO_PROXY=localhost,127.0.0.1,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,169.254.0.0/16,.local
```

**Option B — classic WSL2 NAT** (default on older setups):

```bash
WIN_HOST=$(grep nameserver /etc/resolv.conf | awk '{print $2}')
export HTTP_PROXY=http://${WIN_HOST}:3128
export HTTPS_PROXY=http://${WIN_HOST}:3128
export NO_PROXY=localhost,127.0.0.1,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,169.254.0.0/16,.local
```

Requires `hostonly=1` so px accepts connections from the WSL virtual NIC.

### 3. Verify from WSL

```bash
curl -x "$HTTP_PROXY" http://example.com
curl -x "$HTTP_PROXY" https://example.com
```

### 4. Do not run px inside WSL for SSPI

The Linux binary inside WSL **cannot** use Windows SSPI. You would need explicit [username/password](authentication.md) in WSL.

### WSL troubleshooting

| Symptom | Fix |
|---|---|
| `connection refused` from WSL | px not running on Windows; enable `hostonly=1` for NAT mode |
| Wrong host IP (VPN / custom DNS) | Try mirrored networking; or `ip route show default \| awk '{print $3}'` for Windows host |
| 407 / auth failure upstream | Domain user logged on? Run `px-go.exe --verbose` in console |
| Looping internal traffic | Check `NO_PROXY` includes private ranges |

---

## Operations

| Action | Command |
|---|---|
| Health check | `curl http://127.0.0.1:3128/health` |
| Graceful stop | `curl http://127.0.0.1:3128/PxQuit` or stop the task |
| Debug | Run `px-go.exe --verbose` or `px.exe --verbose` in a console |

## See also

- [Authentication](authentication.md) — SSPI, NTLM, client auth
- [Security considerations](security.md)
- [VM & bare metal](vm-bare-metal.md) — Windows Server as LAN gateway
