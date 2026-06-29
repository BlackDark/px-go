# ADR-001: Retain bigkraig/go-ntlm at current locked version

## Status

Accepted (2026-05-31)

## Context

px-go uses `github.com/bigkraig/go-ntlm` for **server-side NTLM authentication** — acting as
an NTLM challenge-response server to verify clients connecting to the local proxy.
The library's last commit was February 2016. The original upstream
(`ThomsonReutersEikon/go-ntlm`) has been deleted from GitHub.

Usage is narrow: ~45 lines in `internal/clientauth/negotiate.go` calling five server-side APIs:
- `CreateServerSession` / `SetUserInfo`
- `ProcessNegotiateMessage` → `GenerateChallengeMessage`
- `ParseAuthenticateMessage` → `ProcessAuthenticateMessage`

**Alternatives evaluated:**

| Option | Status | Notes |
|--------|--------|-------|
| `github.com/tsybot/go-ntlm` | 0 stars, last commit 2023 | Unvalidated fork, unknown quality |
| `github.com/Madnikulin50/go-ntlm` | 0 stars, last commit 2020 | Same concerns |
| `github.com/Azure/go-ntlmssp` | Actively maintained | **Client-only** — no server-side API |
| Vendor into `internal/ntlm/` | — | See Alternatives Considered below |
| Re-implement NTLM server | — | Out of scope; MS-NLMP is complex |

**Security scan (`govulncheck ./...`):** No vulnerabilities found.

## Decision

Retain `github.com/bigkraig/go-ntlm` pinned at the exact commit hash already in `go.sum`.
No migration, no vendoring, no code changes.

## Consequences

**Positive:**
- Zero maintenance burden and no regression risk.
- `go.sum` hash (`h1:KZ+jr/jshAr1Vy75zFwUTpoP0VMkTFw2mKuIumj4w9E=`) provides
  cryptographic supply-chain integrity — the module content is verified on every build.
- Go module proxy (`proxy.golang.org`) has cached the exact content. Even if the GitHub
  repo disappears, builds continue. This is evidenced by `ThomsonReutersEikon/go-ntlm`
  (the original ancestor, already 404 on GitHub) still resolving as an indirect dep.
- NTLM v2 (MS-NLMP) is a **frozen protocol** (stable since at least 2012). A 2016
  implementation remains correct today and on modern Windows 10/11/Server 2025.

**Negative:**
- No upstream security patches if a bug is discovered.
  **Mitigation:** `govulncheck` in CI (see `.github/workflows/ci.yml` `vuln` job) queries
  the Go vulnerability database on every push. A future CVE would cause immediate CI failure.
- Renovate generates noise PRs for this package.
  **Mitigation:** Explicit `enabled: false` rule in `.github/renovate.json5`.

## Trigger to Revisit

Any `govulncheck` CI failure against this package. If that occurs, the response is:
1. Assess severity and whether px-go's specific code path is affected.
2. If exploitable: fork, patch, and replace the `go.mod` reference with the fork.

## Alternatives Considered

**Vendor into `internal/ntlm/`**: Rejected. Transfers maintenance ownership without
meaningful benefit — `go.sum` already delivers supply-chain determinism. Vendoring would
actually *reduce* flexibility by making it harder to switch to a fork if a bug surfaces.

**Migrate to `tsybot/go-ntlm`**: Rejected. Zero community adoption. "More recent commit"
is not evidence of quality. Risk exceeds benefit.
