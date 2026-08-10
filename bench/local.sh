#!/usr/bin/env bash
# Optional local PAC performance benches (not part of default go test).
# Usage:
#   ./bench/local.sh
#   ./bench/local.sh -count=10 | tee /tmp/pac-bench.txt
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
COUNT="${COUNT:-5}"
EXTRA=()
if [[ $# -gt 0 ]]; then
  EXTRA=("$@")
fi
echo "Running: go test -tags=bench -run=^\$ -bench=. -benchmem -count=${COUNT} ./internal/pac/ ./internal/proxy/ ${EXTRA[*]:-}"
go test -tags=bench -run='^$' -bench=. -benchmem -count="$COUNT" ./internal/pac/ ./internal/proxy/ "${EXTRA[@]}"
