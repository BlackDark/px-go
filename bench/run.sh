#!/usr/bin/env bash
# A/B PAC proxy benchmark from WSL against Windows-host px instances.
# Never stops the live proxy — compare MAIN vs NEW on different ports.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"
RESULTS_DIR="${RESULTS_DIR:-$ROOT/results/$(date +%Y%m%d-%H%M%S)}"
N_HIT="${N_HIT:-500}"
N_MISS="${N_MISS:-200}"
CONCURRENCY="${CONCURRENCY:-64}"
TARGET_HOST="${TARGET_HOST:-httpbin.org}"

die() { echo "error: $*" >&2; exit 1; }

detect_host() {
  if [[ -n "${PX_HOST:-}" ]]; then
    echo "$PX_HOST"
    return
  fi
  if [[ -f /etc/resolv.conf ]]; then
    local ns
    ns="$(awk '/^nameserver/{print $2; exit}' /etc/resolv.conf || true)"
    if [[ -n "$ns" ]]; then
      echo "$ns"
      return
    fi
  fi
  die "set PX_HOST to Windows host IP reachable from WSL"
}

pct() {
  # stdin: one float latency per line → print n avg p50 p99
  awk '
    NF==0 { next }
    { v[n++]=$1+0; sum+=$1 }
    END {
      if (n==0) { print "n=0 avg=0 p50=0 p99=0"; exit }
      asort(v)
      p50=v[int(n*0.50)]
      p99=v[int(n*0.99)]
      if (p99==0) p99=v[n]
      printf "n=%d avg=%.4f p50=%.4f p99=%.4f\n", n, sum/n, p50, p99
    }'
}

health_check() {
  local base="$1"
  local code
  code="$(curl -s -o /dev/null -w '%{http_code}' --connect-timeout 3 --max-time 5 "${base}/health" || true)"
  [[ "$code" == "200" ]] || die "health failed for ${base}/health (got ${code:-none})"
}

run_scenario() {
  local label="$1" proxy="$2" mode="$3" n="$4"
  local out="${RESULTS_DIR}/${label}-${mode}.lat"
  local meta="${RESULTS_DIR}/${label}-${mode}.txt"
  local errors=0
  : >"$out"

  echo "==> ${label} ${mode} n=${n} c=${CONCURRENCY} proxy=${proxy}"

  local wall_start wall_end wall
  wall_start="$(date +%s.%N)"

  if [[ "$mode" == "hit" ]]; then
    # Warm one request, then repeat same URL (cache-friendly).
    curl -s -o /dev/null -x "$proxy" --connect-timeout 10 --max-time 30 \
      "https://${TARGET_HOST}/get" || true
    seq 1 "$n" | xargs -P "$CONCURRENCY" -I{} bash -c '
      proxy="$1"; host="$2"; out="$3"
      line="$(curl -s -o /dev/null -w "%{http_code} %{time_total}" -x "$proxy" \
        --connect-timeout 10 --max-time 30 "https://${host}/get" || echo "000 0")"
      code="${line%% *}"; t="${line##* }"
      echo "$t" >>"$out"
      if [[ "$code" != "200" ]]; then echo fail >>"${out}.err"; fi
    ' _ "$proxy" "$TARGET_HOST" "$out"
  else
    # Unique query → url+host cache miss (stresses PAC pool / lock).
    seq 1 "$n" | xargs -P "$CONCURRENCY" -I{} bash -c '
      proxy="$1"; host="$2"; out="$3"; i="$4"
      line="$(curl -s -o /dev/null -w "%{http_code} %{time_total}" -x "$proxy" \
        --connect-timeout 10 --max-time 30 "https://${host}/get?i=${i}&r=$RANDOM" || echo "000 0")"
      code="${line%% *}"; t="${line##* }"
      echo "$t" >>"$out"
      if [[ "$code" != "200" ]]; then echo fail >>"${out}.err"; fi
    ' _ "$proxy" "$TARGET_HOST" "$out" {}
  fi

  wall_end="$(date +%s.%N)"
  wall="$(awk -v a="$wall_start" -v b="$wall_end" 'BEGIN{printf "%.3f", b-a}')"
  if [[ -f "${out}.err" ]]; then
    errors="$(wc -l <"${out}.err" | tr -d ' ')"
  fi

  local stats
  stats="$(pct <"$out")"
  {
    echo "label=${label}"
    echo "mode=${mode}"
    echo "proxy=${proxy}"
    echo "concurrency=${CONCURRENCY}"
    echo "wall_s=${wall}"
    echo "errors=${errors}"
    echo "$stats"
  } | tee "$meta"

  # CSV line for aggregation
  # run,build,scenario,concurrency,n,avg,p50,p99,wall_s,errors
  local n_avg p50 p99
  n_avg="$(echo "$stats" | sed -n 's/.*n=\([0-9]*\).*/\1/p')"
  local avg p50v p99v
  avg="$(echo "$stats" | sed -n 's/.*avg=\([0-9.]*\).*/\1/p')"
  p50v="$(echo "$stats" | sed -n 's/.*p50=\([0-9.]*\).*/\1/p')"
  p99v="$(echo "$stats" | sed -n 's/.*p99=\([0-9.]*\).*/\1/p')"
  echo "${label},${mode},${CONCURRENCY},${n_avg},${avg},${p50v},${p99v},${wall},${errors}" >>"${RESULTS_DIR}/summary.csv"
}

reload_probe() {
  local label="$1" proxy="$2"
  local out="${RESULTS_DIR}/${label}-reload.lat"
  local meta="${RESULTS_DIR}/${label}-reload.txt"
  : >"$out"
  echo "==> ${label} reload-probe (background miss storm + observe p99)"

  # Background miss storm for ~25s
  (
    end=$((SECONDS + 25))
    i=0
    while (( SECONDS < end )); do
      i=$((i + 1))
      seq 1 "$CONCURRENCY" | xargs -P "$CONCURRENCY" -I{} bash -c '
        proxy="$1"; host="$2"; out="$3"; i="$4"
        line="$(curl -s -o /dev/null -w "%{http_code} %{time_total}" -x "$proxy" \
          --connect-timeout 10 --max-time 30 "https://${host}/get?reload=${i}-{}" || echo "000 0")"
        t="${line##* }"
        echo "$t" >>"$out"
      ' _ "$proxy" "$TARGET_HOST" "$out" "$i" || true
      sleep 0.2
    done
  ) &
  local bg=$!

  echo "NOTE: if PAC is HTTP, arrange a soft reload now (wait proxyreload, or touch upstream PAC)."
  echo "      File PAC: this probe still records concurrent miss latency shape."
  wait "$bg" || true

  local stats errors=0
  stats="$(pct <"$out")"
  if [[ -f "${out}.err" ]]; then errors="$(wc -l <"${out}.err" | tr -d ' ')"; fi
  {
    echo "label=${label}"
    echo "mode=reload"
    echo "proxy=${proxy}"
    echo "errors=${errors}"
    echo "$stats"
  } | tee "$meta"
}

usage() {
  cat <<EOF
Usage: $0 [--skip-reload]

Env:
  PX_HOST          Windows host IP (default: WSL /etc/resolv.conf nameserver)
  MAIN_PORT        default 3128
  NEW_PORT         default 3129
  N_HIT / N_MISS   request counts (default 500 / 200)
  CONCURRENCY      parallel curls (default 64)
  TARGET_HOST      HTTPS target (default httpbin.org)
  RESULTS_DIR      output dir (default bench/results/<timestamp>)
  SKIP_RELOAD=1    skip reload probe
EOF
}

main() {
  local skip_reload=0
  while [[ $# -gt 0 ]]; do
    case "$1" in
      -h|--help) usage; exit 0 ;;
      --skip-reload) skip_reload=1; shift ;;
      *) die "unknown arg: $1" ;;
    esac
  done
  [[ "${SKIP_RELOAD:-0}" == "1" ]] && skip_reload=1

  command -v curl >/dev/null || die "curl required"
  command -v xargs >/dev/null || die "xargs required"
  command -v awk >/dev/null || die "awk required"

  local host main_port new_port main_proxy new_proxy main_base new_base
  host="$(detect_host)"
  main_port="${MAIN_PORT:-3128}"
  new_port="${NEW_PORT:-3129}"
  main_base="http://${host}:${main_port}"
  new_base="http://${host}:${new_port}"
  main_proxy="$main_base"
  new_proxy="$new_base"

  mkdir -p "$RESULTS_DIR"
  echo "build,scenario,concurrency,n,avg,p50,p99,wall_s,errors" >"${RESULTS_DIR}/summary.csv"
  echo "PX_HOST=$host MAIN=$main_proxy NEW=$new_proxy" | tee "${RESULTS_DIR}/env.txt"
  echo "N_HIT=$N_HIT N_MISS=$N_MISS CONCURRENCY=$CONCURRENCY TARGET=$TARGET_HOST" | tee -a "${RESULTS_DIR}/env.txt"

  health_check "$main_base"
  health_check "$new_base"

  # Order: MAIN then NEW per scenario to reduce cross-talk
  run_scenario main "$main_proxy" hit "$N_HIT"
  run_scenario new "$new_proxy" hit "$N_HIT"
  sleep 2
  run_scenario main "$main_proxy" miss "$N_MISS"
  run_scenario new "$new_proxy" miss "$N_MISS"

  if [[ "$skip_reload" -eq 0 ]]; then
    sleep 2
    reload_probe main "$main_proxy"
    sleep 2
    reload_probe new "$new_proxy"
  fi

  echo
  echo "=== summary.csv ==="
  column -t -s, "${RESULTS_DIR}/summary.csv" 2>/dev/null || cat "${RESULTS_DIR}/summary.csv"
  echo "Results: $RESULTS_DIR"
}

main "$@"
