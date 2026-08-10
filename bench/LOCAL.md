# Local PAC performance benches (opt-in)

These are **not** run by normal `go test ./...`. They use the `bench` build tag.

## Why here

| Location | Role |
|----------|------|
| `internal/pac/bench_test.go` | Microbench: cache hit/miss, parallel miss, eval during soft-reload |
| `internal/proxy/pac_bench_test.go` | E2E: local httptest PAC + backend + `proxy.Server` |

Same module as production code; no second module. Default CI/tests stay clean.

## Run once

```bash
chmod +x bench/local.sh
./bench/local.sh
# or:
go test -tags=bench -run='^$' -bench=. -benchmem -count=5 ./internal/pac/ ./internal/proxy/
```

## Compare two git refs (recommended)

```bash
# terminal 1 — main
git checkout main
./bench/local.sh | tee /tmp/pac-bench-main.txt

# terminal 2 — PR branch
git checkout perf/pac-concurrency
./bench/local.sh | tee /tmp/pac-bench-new.txt

# needs: go install golang.org/x/perf/cmd/benchstat@latest
benchstat /tmp/pac-bench-main.txt /tmp/pac-bench-new.txt
```

## What to look at

| Benchmark | Meaning |
|-----------|---------|
| `FindProxyCacheHit` / `…Parallel` | Cache path (should be ~flat across branches) |
| `FindProxyCacheMiss` / `…Parallel` | goja pool vs single-mutex (NEW should win on Parallel) |
| `FindProxyDuringReload` | Soft-reload must not serialize (NEW much better if OLD blocked) |
| `PACProxyE2EHit(Parallel)` | Full proxy + PAC DIRECT + local backend |
| `PACProxyE2EMissParallel` | E2E with unique URLs (PAC cache miss) |

## Notes

- No corporate proxy / WSL required.
- Noise: close other heavy processes; use `-count=10` + `benchstat` for confidence.
- For real Windows+PAC field data, see `bench/AGENT_EXECUTION.md` + `bench/run.sh`.
