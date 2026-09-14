#!/usr/bin/env bash
# common.sh — shared helpers for test/verify_*.sh
# Source from repo scripts:  # shellcheck source=common.sh
#   SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
#   # shellcheck source=common.sh
#   source "$SCRIPT_DIR/common.sh"

# Idempotent
if [ "${HTTP_BENCH_TEST_COMMON_LOADED:-0}" = "1" ]; then
	return 0 2>/dev/null || exit 0
fi
HTTP_BENCH_TEST_COMMON_LOADED=1

TEST_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$TEST_DIR/.." && pwd)"
cd "$ROOT"

PASS=0
FAIL=0
FAILED_TESTS=()

if [ -t 1 ]; then
	GREEN='\033[0;32m'
	RED='\033[0;31m'
	YELLOW='\033[1;33m'
	CYAN='\033[0;36m'
	DIM='\033[2m'
	NC='\033[0m'
else
	GREEN=''; RED=''; YELLOW=''; CYAN=''; DIM=''; NC=''
fi

log_pass() {
	PASS=$((PASS + 1))
	echo -e "  ${GREEN}[PASS]${NC} $1"
}

log_fail() {
	FAIL=$((FAIL + 1))
	FAILED_TESTS+=("$1")
	echo -e "  ${RED}[FAIL]${NC} $1"
	if [ -n "${2:-}" ]; then
		echo -e "       ${YELLOW}detail:${NC} $2"
	fi
}

# Aliases used by newer scripts
ok()  { log_pass "$1"; }
bad() { log_fail "$1" "${2:-}"; }

print_summary() {
	local title="${1:-Summary}"
	echo ""
	echo "=== $title ==="
	echo -e "  ${GREEN}Passed:${NC} $PASS"
	echo -e "  ${RED}Failed:${NC} $FAIL"
	if [ "$FAIL" -gt 0 ]; then
		echo ""
		echo "  Failed:"
		local t
		for t in "${FAILED_TESTS[@]}"; do
			echo -e "    ${RED}- $t${NC}"
		done
		return 1
	fi
	echo -e "  ${GREEN}All checks passed.${NC}"
	return 0
}

# Resolve http_bench binary; build if missing when second arg is "build"
# Usage: resolve_bench [path] [build]
# Sets BENCH to absolute or relative executable path.
resolve_bench() {
	local candidate="${1:-./http_bench}"
	local do_build="${2:-}"
	if [ -x "$candidate" ]; then
		BENCH="$candidate"
		return 0
	fi
	if [ "$do_build" = "build" ]; then
		echo "building ./http_bench ..."
		if go build -o ./http_bench .; then
			BENCH="./http_bench"
			return 0
		fi
		echo "ERROR: go build failed"
		return 1
	fi
	echo "ERROR: http_bench binary not found at: $candidate"
	echo "       build with: go build -o http_bench .   or pass path as \$1"
	return 1
}

section() {
	echo -e "${YELLOW}[$*]${NC}"
}

header() {
	echo -e "${CYAN}== $* ==${NC}"
}
