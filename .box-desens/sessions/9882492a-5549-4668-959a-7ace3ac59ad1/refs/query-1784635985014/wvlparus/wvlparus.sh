#!/bin/sh
set -eu

# Long protocol smoke test. Set URL/SECONDS to target an environment.
URL=${URL:-http://127.0.0.1:8080/}
SECONDS=${SECONDS:-300}
CONCURRENCY=${CONCURRENCY:-50}
QPS=${QPS:-0}

run() {
  protocol=$1
  target=$URL
  case "$protocol" in
    ws) target=${WS_URL:-ws://127.0.0.1:8080/} ;;
  esac
  echo "== $protocol: ${SECONDS}s, concurrency=${CONCURRENCY}, qps=${QPS} =="
  ./http_bench -d "${SECONDS}s" -c "$CONCURRENCY" -q "$QPS" -http "$protocol" -url "$target" -o csv
}

for protocol in http1 http2 http3 ws; do
  run "$protocol" || echo "protocol $protocol unavailable; continuing"
done
