#!/usr/bin/env bash
# Fire a mix of /success, /latency, /failure requests against the test service
# to validate tail-sampling under load.
#
# Tunables (env vars):
#   URL          base URL of the service                    (default: http://localhost:8080)
#   SUCCESS_N    number of /success requests                (default: 1000)
#   LATENCY_N    number of /latency requests                (default: 20)
#   FAILURE_N    number of /failure requests                (default: 20)
#   CONCURRENCY  parallel in-flight requests                (default: 20)
#
# Usage:
#   ./load-test.sh
#   URL=http://otel-test.example.com SUCCESS_N=5000 ./load-test.sh

set -euo pipefail

URL="${URL:-http://localhost:8080}"
SUCCESS_N="${SUCCESS_N:-10000}"
LATENCY_N="${LATENCY_N:-5}"
FAILURE_N="${FAILURE_N:-20}"
CONCURRENCY="${CONCURRENCY:-100}"

TOTAL=$((SUCCESS_N + LATENCY_N + FAILURE_N))

echo "Target:        $URL"
echo "Success:       $SUCCESS_N"
echo "Latency:       $LATENCY_N"
echo "Failure:       $FAILURE_N"
echo "Total:         $TOTAL"
echo "Concurrency:   $CONCURRENCY"
echo

# Build a shuffled list of paths so traffic interleaves like prod.
build_targets() {
  for ((i = 0; i < SUCCESS_N; i++)); do echo /success; done
  for ((i = 0; i < LATENCY_N; i++)); do echo /latency; done
  for ((i = 0; i < FAILURE_N; i++)); do echo /failure; done
}

# Each worker prints "<path> <http_code>" per request; we tally at the end.
hit() {
  local path="$1"
  local code
  code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 "${URL}${path}" || echo "ERR")
  printf '%s %s\n' "$path" "$code"
}
export -f hit
export URL

start=$(date +%s)

results=$(build_targets | shuf | xargs -P "$CONCURRENCY" -I {} bash -c 'hit "$@"' _ {})

elapsed=$(( $(date +%s) - start ))

echo "Results (path, status, count):"
printf '%s\n' "$results" | sort | uniq -c | awk '{printf "  %-10s %-5s %s\n", $2, $3, $1}'
echo
printf 'Elapsed: %ss  (%.0f req/s)\n' "$elapsed" "$(awk -v t=$TOTAL -v e=$elapsed 'BEGIN{ if (e==0) print t; else print t/e }')"
