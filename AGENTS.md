# px-go

Go reimplementation of [px](https://github.com/genotrance/px): a local proxy that authenticates to upstream corporate proxies (NTLM/Kerberos/Negotiate) via Windows SSPI or explicit credentials. Used by developers and AI agents on Windows, macOS and Linux.

## Verify

`mise run check` green before every commit: gofmt, `go vet`, golangci-lint, build, race tests, govulncheck. CI runs the same task on Ubuntu, plus `go test` on macOS and Windows.

Extra checks:
- Proxy integration tests after proxy changes: `go test -v ./internal/proxy/ -run TestIntegration`
- Windows SSPI/Negotiate and PAC discovery cannot run in CI: test manually (see below) after touching `internal/auth`, `internal/platform` or `internal/pac`.

Manual Windows test: build `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o px.exe ./cmd/px`, run `px.exe --config=px.ini --verbose`, then `curl http://127.0.0.1:<port>/health`, `curl --proxy http://127.0.0.1:<port> https://httpbin.org/get`, `curl http://127.0.0.1:<port>/PxQuit`. Verify SSPI auth succeeds against the corporate proxy (`--verbose`), `--quiet` suppresses startup logs, `--test` exits cleanly.

CI time is a cost: after touching `.github/workflows/`, deps or the build, check the run's job timings. New slow step: cache it (main saves, PRs restore, key on `go.sum`) or skip it by path.

## Stack

Follows https://github.com/BlackDark/agent-presets/blob/main/stack/core.md (Go branch: stdlib `net/http`, `mise` for toolchain and tools).

Deviations:
- Go toolchain and tools pinned in `.mise.toml`. The `go` directive in `go.mod` equals the mise pin (Renovate bumps both in one PR).
- `goreleaser` for release binaries; no framework.
- Frozen dependency: `github.com/bigkraig/go-ntlm` (see `.github/renovate.json5`).

## Map

- `cmd/px/`: entry point, CLI flags, signal handling
- `internal/config/`: INI/env/CLI config, logger setup
- `internal/proxy/`: HTTP server, CONNECT tunnel, route resolution, integration tests
- `internal/auth/`: upstream auth (SSPI, NTLM, Negotiate, Basic, Digest)
- `internal/clientauth/`: client-side auth (authenticating clients to px)
- `internal/pac/`: PAC evaluation (JavaScript via goja)
- `internal/platform/`: OS proxy discovery (WinHTTP on Windows)
- `internal/network/`: IP/CIDR/wildcard matching for allow/noproxy rules
- `internal/kerberos/`: Kerberos ticket management
- `internal/version/`: build-time version info
- `docs/`, `px.ini` (commented full config), `px.toml`

Build variants:
- Windows console: `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o px.exe ./cmd/px`
- Windows headless (Task Scheduler, autostart): add `-ldflags="-s -w -H=windowsgui"` and name it `pxw.exe`; `--install` registers `pxw.exe` when it sits beside `px.exe`.

## Every change

Same PR, only what the change touches:
- tests for the changed behaviour
- docs in `docs/` and `px.ini` for new or changed config keys
- README when a user-visible feature changes

## Code style

- Data in, data out: pure functions; I/O, time and randomness at the edges.
- Expected failures are `error` values; panic only for bugs.
- Auth sessions (SSPI especially) hold native handles: always `Session.Close()` via defer.
- Zero-cost debug logs: `slog.Debug` calls must not allocate in hot paths.
- PAC results: strip the `PROXY` prefix; treat `;` as separator.
- CONNECT tunnels release their concurrency slot after setup (dial + auth); the relay runs without a slot.
- SSPI target name is `HTTP/<proxy-host>` so Negotiate can use Kerberos.

## Stewardship

Leave touched areas cleaner than found. Small fixes beside the change go in their own commit in the same PR. Larger problems: report with `file:line`, keep the PR on topic.

## Git

- Conventional Commits. One topic per PR.
- Merge: rebase when commits are logically separate, squash otherwise.
- Force-push: `--force-with-lease` on own branches only.

## Boundaries

- `sudo`, system packages, global installs: the user runs them.
- Running px instances stay up; test on other ports.
- Language: code, comments, docs and commits in English.

## Gotchas

- Windows headless build (`-H=windowsgui`) has no console: file logging is required (`log=1` or `log_file`).
- Config keys for agent workloads (recommended):

```ini
[settings]
threads = 128 # gates connection setup only, not active tunnels
idle = 300 # AI tools keep connections idle for minutes between bursts
socktimeout = 20 # auth handshake timeout
log = 1
log_file = /var/log/px-go/px-go.log # explicit path for systemd (overrides binary-dir default)
```
