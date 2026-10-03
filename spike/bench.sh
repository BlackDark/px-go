#!/usr/bin/env bash
# Go px vs Rust proxy spike: throughput, proxy-process CPU, and RSS.
# Every bash call in this spike stays under the 120s cap; each run is bounded to
# a fixed request count.
set -u
ROOT=/home/claudy/workspace/px-go.pi-subagents-delegate-SMALL-TIMEBOXED-RUST-SPIKE.-This-is-an-isolated-experiment-in-your-own-worktre-ce4d144c-80d356d-0d27-s0-t0
RUST=$ROOT/spike/rustproxy/target/release/rustproxy
GO=/tmp/px-go-spike
BACKEND=/tmp/spike-backend
LOADGEN=/tmp/spike-loadgen
N=${N:-100000}
C=${C:-64}
REPS=${REPS:-3}
OUT=$ROOT/spike/results.txt

rss_kb() { awk '/^VmRSS:/{print $2}' /proc/$1/status 2>/dev/null; }
cpu_ms() { awk '{print ($14+$15)*10}' /proc/$1/stat 2>/dev/null; }

wait_port() {
  for _ in $(seq 1 60); do
    if (exec 3<>/dev/tcp/127.0.0.1/$1) 2>/dev/null; then exec 3<&- 3>&-; return 0; fi
    sleep 0.2
  done
  return 1
}

"$BACKEND" --listen 127.0.0.1:29701 >/tmp/backend.log 2>&1 &
BPID=$!
wait_port 29701 || { echo "backend did not start"; exit 1; }

run_one() {
  local name=$1 port=$2
  "$GO" --port=$port --config=/dev/null --foreground --quiet >/tmp/proxy-$name.log 2>&1 &
  local pid=$!
  wait_port $port || { echo "$name: proxy did not start"; kill $pid 2>/dev/null; return 1; }
  sleep 1
  local idle_rss cpu0 cpu1 load_rss out elapsed ok
  idle_rss=$(rss_kb $pid)
  cpu0=$(cpu_ms $pid)
  out=$("$LOADGEN" -n $N -c $C -proxy 127.0.0.1:$port -target http://127.0.0.1:29701/hello)
  cpu1=$(cpu_ms $pid)
  load_rss=$(rss_kb $pid)
  echo "$name|$out|proxy_cpu_ms=$((cpu1-cpu0))|idle_rss_kb=$idle_rss|loaded_rss_kb=$load_rss" | tee -a $OUT
  kill $pid 2>/dev/null; wait $pid 2>/dev/null
  sleep 1
}

: > $OUT
echo "N=$N C=$C REPS=$REPS" | tee -a $OUT
for r in $(seq 1 $REPS); do
  echo "--- rep $r ---" | tee -a $OUT
  run_one rust 28701
  run_one go   28702
done
kill $BPID 2>/dev/null
echo "done"