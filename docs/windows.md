# Windows deployment

Run px-go on Windows desktops and servers with SSPI, Task Scheduler, or autostart.

Back to [deployment overview](deployment.md).

## Binaries

Each release zip contains:

- **`px.exe`** — console build for interactive debugging or manual runs from cmd / PowerShell.
- **`pxw.exe`** — headless build (`-H=windowsgui`) for Task Scheduler and autostart; no console window.

`--install` automatically registers `pxw.exe` if it exists alongside `px.exe`.

## Setup

1. Download the Windows zip from [GitHub Releases](https://github.com/BlackDark/px-go/releases) or build both:

   ```powershell
   GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -o px.exe ./cmd/px
   GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w -H=windowsgui" -o pxw.exe ./cmd/px
   ```

2. Place binaries and config in a permanent location:

   ```
   C:\Tools\px\px.exe
   C:\Tools\px\pxw.exe
   C:\Tools\px\px.ini
   ```

3. Register autostart:

   ```powershell
   C:\Tools\px\px.exe --config=C:\Tools\px\px.ini --install
   ```

   Or create a Scheduled Task (PowerShell as Administrator):

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

## Recommended `px.ini` (headless)

```ini
[settings]
log = 1
threads = 128
idle = 300
foreground = 0
```

Use `log=1` (file next to binary) or `log=2` (cwd). `log=4` (stdout) produces no output in headless mode.

## SSPI and sessions

- **SSPI authentication** requires the task to run **only when the user is logged on**. Do not use "Run whether user is logged on or not".
- Upstream Negotiate/NTLM can use the interactive user's Windows identity when credentials are not set in config.

## Operations

| Action | Command |
|---|---|
| Health check | `curl http://127.0.0.1:3128/health` |
| Graceful stop | `curl http://127.0.0.1:3128/PxQuit` or stop the scheduled task |
| Debug | Run `px.exe --verbose` manually in a console |

## See also

- [Security considerations](security.md) — SSPI session requirements
- [VM & bare metal](vm-bare-metal.md) — if running px on Windows Server as a gateway for other hosts
