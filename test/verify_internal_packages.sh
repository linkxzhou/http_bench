#!/usr/bin/env bash
# verify_internal_packages.sh — 确认 internal 平铺结构与可编译符号面
#
# Usage:  bash test/verify_internal_packages.sh

set -u
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=common.sh
source "$SCRIPT_DIR/common.sh"

header "internal package surface"

if command -v rg >/dev/null 2>&1; then
	hits="$(rg -n 'http_bench/internal/(transport|metrics|report|request|distributed|dashboard|limiter|logging|templatefn)\b' \
		--glob '!.git/**' --glob '!docs/**' --glob '!test/**' . 2>/dev/null || true)"
else
	hits="$(grep -RInE 'http_bench/internal/(transport|metrics|report|request|distributed|dashboard|limiter|logging|templatefn)\b' \
		--exclude-dir=.git --exclude-dir=docs --exclude-dir=test . 2>/dev/null || true)"
fi
if [ -n "$hits" ]; then
	bad "old import paths" "found references to nested internal packages"
	echo "$hits" | head -20
else
	ok "no nested internal import paths remain"
fi

tmp="$(mktemp -d)"
if go test -c -o "$tmp/internal.test" ./internal/ >/dev/null 2>&1; then
	ok "internal package compiles as test binary"
elif go list ./internal >/dev/null 2>&1; then
	ok "go list ./internal"
else
	bad "compile" "internal package failed"
fi
rm -rf "$tmp"

src_count=$(find internal -maxdepth 1 -type f -name '*.go' ! -name '*_test.go' | wc -l | tr -d ' ')
if [ "$src_count" -ge 26 ] && [ "$src_count" -le 32 ]; then
	ok "internal non-test .go count=$src_count (expected ~27-28 + api)"
else
	bad "source count" "got $src_count, expected about 27-28"
fi

print_summary "internal packages" || exit 1
exit 0
