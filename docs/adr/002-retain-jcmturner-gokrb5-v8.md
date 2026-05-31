# ADR-002: Retain jcmturner/gokrb5/v8 at v8.4.4

## Status

Accepted (2026-05-31)

## Context

px-go uses `github.com/jcmturner/gokrb5/v8` for **Kerberos client authentication** —
generating SPNEGO Negotiate headers as a fallback when Windows SSPI is unavailable
(non-Windows platforms, or `--noSSPI` mode).

Usage in `internal/auth/auth.go`:
- `krbclient.NewWithPassword` + `client.Login` — authenticate with KDC using password
- `krbconfig.Load` — parse `krb5.conf`
- `krbspnego.SetSPNEGOHeader` — attach Negotiate token to outgoing HTTP request

**Library state:**

| Metric | Value |
|--------|-------|
| Stars | 778 |
| Forks | 283 |
| Importers | 276 packages |
| Last release | v8.4.4, Feb 25, 2023 |
| Last commit | May 6, 2023 |
| Open issues | 105 (poor triage, no active maintainer response) |
| Archived | No |
| Known CVEs | None |

The repo is not archived. The author still owns it and community PRs occasionally
merge (e.g., SASL security layers PR, Oct–Nov 2025). This is a **stable, maintenance-mode**
library — not an abandoned one.

**Alternatives evaluated:**

| Option | Status | Notes |
|--------|--------|-------|
| `oiweiwei/gokrb5.fork/v9` | 3 stars, last commit Nov 2025 | Single-author fork, minimal adoption |
| `golang.org/x/net` | No Kerberos support | Not applicable |
| System GSSAPI (`cgo`) | Platform-dependent | Would require CGO, breaks pure-Go builds |
| Drop Kerberos support | — | Regresses Linux/macOS users against Kerberos-only proxies |

**Security scan (`govulncheck ./...`):** No vulnerabilities found.

## Decision

Retain `github.com/jcmturner/gokrb5/v8 v8.4.4`. Do not migrate.

## Consequences

**Positive:**
- Zero migration cost and no regression risk.
- Kerberos (RFC 4120) and SPNEGO (RFC 4178) are **stable protocols**. v8.4.4 correctly
  implements the specific API surface px-go uses — password-based SPNEGO for HTTP proxies
  is the most common and well-tested code path in the library.
- Wide adoption (276 importers) means any CVE would be immediately publicly visible and
  quickly escalated by the ecosystem.

**Negative:**
- No upstream bugfixes for edge cases. **Mitigation:** px-go's usage covers the core path
  (`NewWithPassword` → `SetSPNEGOHeader`), not advanced features like PKINIT or FAST where
  issues are more likely.
- Library may eventually be archived. **Mitigation:** annual review (see Trigger).

## Trigger to Revisit

Any of the following, whichever comes first:
1. **`govulncheck` CI failure** against this package → assess and migrate immediately.
2. **Repo archived** by author → evaluate `oiweiwei/gokrb5.fork/v9` or fork ourselves.
3. **oiweiwei fork reaches ≥50 stars and active issue triage** → migration becomes low-risk.

Annual review checkpoint: May 2027.

## Alternatives Considered

**Migrate to `oiweiwei/gokrb5.fork/v9`**: Rejected. Three reasons: (1) 3 stars — single
author with no community validation. (2) Non-trivial migration: module path change across
`internal/auth/` plus API compatibility audit. (3) The risk calculus is backwards — trading
a working, widely-adopted library for an experimental fork to solve a problem that has not
yet materialized.

**Drop Kerberos support**: Rejected. Kerberos is a legitimate and required auth path for
corporate proxy environments on Linux and macOS. Removing it would break functionality for
users running against Kerberos-only upstream proxies without SSPI.
