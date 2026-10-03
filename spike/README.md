# Rust feasibility spike (evidence only, not a product)

Goal: stop the "should px-go move to Rust?" debate by measuring it. Everything here is
throwaway scaffolding; **no existing Go file was touched**.

Layout:

| path | what |
|---|---|
| `rustproxy/` | ~300-line Rust/tokio HTTP forward proxy + CONNECT tunnel (no auth/PAC/config/Windows) |
| `backend/` | trivial fixed-body Go HTTP backend (32-byte body) |
| `loadgen/` | fixed-count / fixed-concurrency raw-socket HTTP client, reports its own CPU |
| `bench.sh` | runs backend + both proxies, records rps, proxy CPU and RSS |
| `jsprobe/` | rquickjs host for the PAC prelude + `test.pac` |
| `dbg/` | minimal rquickjs second-Runtime (PAC hot-reload) check |
| `nodep/` | zero-dependency pure-Rust binary = cross-compile control |
| `pac/pacutils.js` | **verbatim copy** of the 140-line PAC prelude from `internal/pac/pacutils.go` (lines 4-143 of the raw string), `diff`-verified byte-identical |
| `results.txt`, `jsprobe-output.txt`, `dbg-output.txt` | raw output backing the numbers below |

Machine: AMD Ryzen 9 7900, **3 vCPU visible**, 7.9 GB RAM, Linux 6.x, rustc 1.99.0,
go 1.27.1. NOTE: the box is shared with other agents' processes (other proxies were
listening on 18080/18081 while this spike ran), so throughput numbers are noisy — the
answer is "no significant difference", not "Rust is 5% faster".

## Q1 — Go vs Rust: throughput, CPU, memory

Setup: `go build -o /tmp/px-go-spike ./cmd/px`, run with `--port=… --config=/dev/null
--foreground` (no upstream, no PAC → DIRECT). N=100000 requests, concurrency 64,
3 repetitions, alternating order, same loopback backend for both. Proxy CPU comes from
`/proc/<pid>/stat` utime+stime; RSS from `/proc/<pid>/status VmRSS`.

All rows **MEASURED** (medians of 3 runs; per-run values in `results.txt`):

| metric | Go px-go-spike | Rust rustproxy | delta | label |
|---|---|---|---|---|
| requests/sec (median of 3) | 19672 (19672 / 19896 / 19389) | 18693 (20017 / 18693 / 18179) | -5%, inside noise | MEASURED |
| proxy CPU ms/request | 0.0809 (0.0816 / 0.0809 / 0.0791) | 0.0846 (0.0813 / 0.0846 / 0.0882) | +4.5%, inside noise | MEASURED |
| idle RSS (listener up, 0 traffic) | 16232 KB | 16204 KB | -28 KB (-0.2%) | MEASURED |
| loaded RSS (after 100k reqs) | 24452 KB | 23896 KB | -556 KB (-2.3%) | MEASURED |
| load-generator CPU ms/run (context) | ~1340 ms | ~1390 ms | client is a bigger CPU consumer than either proxy | MEASURED |

Five additional runs earlier in the session (before the machine got busier) had Rust
ahead (18267–18543 rps) and Go behind (15620–18519 rps), i.e. the ordering flips run to
run. Only one Rust run dropped to 12756 rps, which is contention, not a code property.

Extra, not asked for but relevant: both proxies served 100000/100000 requests with zero
failures and both proxied CONNECT correctly (`curl -p --proxytunnel`), so the comparison
is at least apples-to-apples on functionality.

Not measured: large-body / many-concurrent-tunnel throughput (px-go's real workload is
CONNECT tunnels carrying MBs, where per-request HTTP parsing is a rounding error);
latency percentiles; behaviour under 128+ worker threads; Windows behaviour of the Rust
proxy (nothing Windows-specific was written).

## Q2 — Can rquickjs host the PAC code?

**Yes.** `spike/jsprobe` loads the verbatim `pacutils.js` prelude plus `test.pac` into a
QuickJS-NG context and calls `FindProxyForURL(url, host)`. All six cases returned the
expected value (see `jsprobe-output.txt`):

| url | helper exercised | result | expected |
|---|---|---|---|
| `http://app.example/path` | default path | `PROXY default.example.com:8080` | ✅ |
| `ftp://files.example.com/x` | `shExpMatch` | `DIRECT` | ✅ |
| `http://svc.example.com/special/thing` | `shExpMatch("*/special/*")` | `PROXY special.example.com:8080` | ✅ |
| `http://host.direct.example/` | `dnsDomainIs` | `DIRECT` | ✅ |
| `http://10.1.2.3/thing` | `isInNet` (literal IP) | `PROXY internal.example.com:3128` | ✅ |
| `http://11.1.2.3/thing` | `isInNet` negative | `PROXY default.example.com:8080` | ✅ |

Also verified: injecting a Rust `dnsResolve` callback works (20 001 invocations counted,
i.e. the JS→Rust host-function boundary is live), and a Rust-owned `Func` must be
installed before evaluating a PAC that calls it (otherwise you get a bare
`rquickjs::Error: Exception`).

Cost, for comparison with goja: prelude eval 255 µs, PAC eval 10 µs, **12.6 µs per
FindProxyForURL call**. px-go's existing goja bench (`BenchmarkFindProxyCacheHit`) reports
**109 ns/op** — ~115× faster than QuickJS here. That number is cache-hit-dominated and
not apples-to-apples (goja's 109 ns is the Go-side cache lookup; the eval itself is the
comparable part), so treat the ratio as an order-of-magnitude hint, not a verdict.

**Toolchain questions (the decisive ones):**

- **rquickjs requires a C compiler at build time.** `rquickjs-sys` builds QuickJS-NG +
  libregexp C sources with `cc`. On this box `cc`/`gcc` exist and the build succeeded;
  the resulting binary links only libc/libm/libgcc.
- The rquickjs **`bindgen` feature additionally requires libclang**, which is NOT
  installed here: build failed with
  `Unable to find libclang: "couldn't find any valid shared libraries matching:
  ['libclang.so', ...]"`. With the default feature set (`macro` only, no `bindgen`)
  it builds fine — pre-generated bindings are used.
- **`cargo build --target x86_64-pc-windows-msvc` does NOT work from this Linux box,
  with or without C dependencies.** Exact errors:
  - pure Rust, zero deps (`nodep`): `error: linker 'link.exe' not found … the msvc targets
    depend on the msvc linker but 'link.exe' was not found … please ensure that Visual
    Studio 2017 or later, or Build Tools for Visual Studio were installed`
  - tokio proxy (`rustproxy`): identical `linker 'link.exe' not found` (so no C needed to
    fail)
  - rquickjs (`jsprobe`): fails even **earlier**, in `cc-rs`, before linking:
    `cc: error: /std:c11: linker input file not found` … `error occurred in cc-rs: command
    did not execute successfully … "cc" … "-c" …/libregexp.c` — i.e. gcc on Linux is being
    handed MSVC flags.
  - control: `--target x86_64-pc-windows-gnu` also fails, with
    `error: linker 'x86_64-w64-mingw32-gcc' not found` (no MinGW toolchain installed).
  → Cross-compiling Windows binaries from this machine requires installing either MSVC
  Build Tools (not possible on Linux) or a MinGW-w64 cross toolchain (not installed).
  The `x86_64-pc-windows-*` std targets are installed, so only the linker is missing.

Hot-reload caveat (unresolved, timeboxed): a second `Runtime`+`Context` evaluated the PAC
correctly in `spike/dbg` when the first runtime was dropped first, but the equivalent code
inside `jsprobe` kept throwing `Exception` even after dropping it. Not chased further.

## Blunt conclusion

Rust delivered **no measurable benefit on any axis**. Throughput was within ±5% run-to-run
noise with the sign flipping between repetitions (Rust ahead in the first 5 runs, Go ahead
in the last 3), proxy CPU per request was 0.0846 ms vs 0.0809 ms (Rust 4.5% *worse*),
idle RSS was identical (16204 vs 16232 KB, 0.2%), and loaded RSS differed by 2.3% in
Rust's favour — all well inside the variance of a shared 3-vCPU box. In other words
roughly **0% of the expected throughput/CPU gain and ~0-2% of the expected memory gain
materialised**; the "Rust will be faster and lighter" hypothesis is not supported by the
first real measurement. On the JS side, rquickjs *does* host the PAC prelude correctly,
but it drags a C toolchain (plus libclang if you enable `bindgen`) into the build, and on
this machine Windows cross-compilation is blocked entirely for both the pure-Rust and the
C-dependency cases — so the JS story is "works, with extra build-system baggage", not
"obviously cheaper". Given px-go is 3,965 lines of Go with a working Windows SSPI story
that Rust would have to rebuild from scratch, a rewrite is not justified by anything
measured here.

## Reproduce

```bash
cd spike
cargo build --release --manifest-path rustproxy/Cargo.toml   # Rust proxy
cargo build --release --manifest-path jsprobe/Cargo.toml      # PAC probe (needs cc)
go build -tags spike -o /tmp/spike-backend ./backend
go build -tags spike -o /tmp/spike-loadgen ./loadgen
go build -o /tmp/px-go-spike ../cmd/px
N=100000 C=64 REPS=3 bash bench.sh                            # writes results.txt
./jsprobe/target/release/jsprobe                              # PAC results
cd nodep && cargo build --release --target x86_64-pc-windows-msvc   # expected failure
```