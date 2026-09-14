#!/usr/bin/env bash
# verify_cli_smoke.sh — 快速 CLI 冒烟（不含完整 flag 矩阵；见 verify_flags.sh）
#
# Usage:  bash test/verify_cli_smoke.sh [path/to/http_bench]

set -u
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=common.sh
source "$SCRIPT_DIR/common.sh"

resolve_bench "${1:-./http_bench}" build || exit 1

header "CLI smoke"

out="$("$BENCH" -h 2>&1 || true)"
echo "$out" | grep -q 'Usage\|usage\|http_bench' && ok "-h prints help" || bad "-h" "help text missing"

out="$("$BENCH" -example 2>&1 || true)"
echo "$out" | grep -qi 'example\|http' && ok "-example prints examples" || bad "-example" "no examples"

out="$("$BENCH" -n 2 -c 1 -q 100 https://example.com -http http1 2>&1)"; rc=$?
if echo "$out" | grep -qi panic; then bad "flags after URL" "panic"
else ok "flags after URL no panic (rc=$rc)"; fi

out="$("$BENCH" -n 3 -c 1 -q 50 -http http1 https://httpbin.org/get 2>&1)"; rc=$?
if echo "$out" | grep -qi 'Summary\|Requests/sec\|Total'; then
	ok "tiny live GET shows summary"
elif echo "$out" | grep -qi panic; then
	bad "tiny live GET" "panic"
else
	ok "tiny live GET network fail ok (offline/rc=$rc)"
fi

print_summary "CLI smoke" || exit 1
exit 0
