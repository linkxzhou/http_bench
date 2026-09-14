#!/usr/bin/env bash
# verify_all.sh — 统一入口：跑完 test/ 下 verify_* 套件
#
# Usage:
#   bash test/verify_all.sh                 # checklist + internal + build(gate, no root race) + smoke + flags
#   bash test/verify_all.sh --full          # 同上，且根包 go test -race -short
#   bash test/verify_all.sh --with-long     # 额外跑 verify_long_benchmark（默认 DURATION=30）
#   bash test/verify_all.sh --only flags    # 只跑某一套：checklist|internal|build|smoke|flags|long
#   bash test/verify_all.sh ./http_bench    # 指定二进制（smoke/flags/long）
#
# Exit: 0 = all selected suites pass

set -u
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=common.sh
source "$SCRIPT_DIR/common.sh"

FULL=0
WITH_LONG=0
ONLY=""
BENCH_ARG=""

while [ $# -gt 0 ]; do
	case "$1" in
		--full) FULL=1 ;;
		--short) FULL=0 ;;
		--with-long) WITH_LONG=1 ;;
		--only)
			shift
			ONLY="${1:-}"
			if [ -z "$ONLY" ]; then echo "ERROR: --only needs a suite name"; exit 2; fi
			;;
		-h|--help)
			sed -n '2,16p' "$0" | sed 's/^# \{0,1\}//'
			exit 0
			;;
		*)
			BENCH_ARG="$1"
			;;
	esac
	shift
done

should_run() {
	local name="$1"
	[ -z "$ONLY" ] || [ "$ONLY" = "$name" ]
}

run_suite() {
	local name="$1"
	shift
	echo ""
	echo "######## $name ########"
	if "$@"; then
		echo "OK $name"
		return 0
	fi
	echo "FAIL $name"
	return 1
}

fail=0

if should_run checklist; then
	run_suite checklist bash "$SCRIPT_DIR/verify_plan3_checklist.sh" || fail=1
fi

if should_run internal; then
	run_suite internal bash "$SCRIPT_DIR/verify_internal_packages.sh" || fail=1
fi

if should_run build; then
	if [ "$FULL" = "1" ]; then
		run_suite build bash "$SCRIPT_DIR/verify_build_gate.sh" --short || fail=1
	else
		run_suite build bash "$SCRIPT_DIR/verify_build_gate.sh" --no-race-root || fail=1
	fi
fi

bench_for() {
	if [ -n "$BENCH_ARG" ]; then
		echo "$BENCH_ARG"
		return 0
	fi
	resolve_bench ./http_bench build >/dev/null || return 1
	echo "$BENCH"
}

if should_run smoke; then
	b="$(bench_for)" || fail=1
	[ -n "${b:-}" ] && run_suite smoke bash "$SCRIPT_DIR/verify_cli_smoke.sh" "$b" || fail=1
fi

if should_run flags; then
	b="$(bench_for)" || fail=1
	[ -n "${b:-}" ] && run_suite flags bash "$SCRIPT_DIR/verify_flags.sh" "$b" || fail=1
fi

# long: only with --with-long, or --only long
if [ "$ONLY" = "long" ] || [ "$WITH_LONG" = "1" ]; then
	export DURATION="${DURATION:-30}"
	export CONCURRENCY="${CONCURRENCY:-10}"
	b="$(bench_for)" || fail=1
	[ -n "${b:-}" ] && run_suite long bash "$SCRIPT_DIR/verify_long_benchmark.sh" "$b" || fail=1
fi

echo ""
if [ "$fail" -eq 0 ]; then
	echo "ALL selected verify suites PASSED"
	exit 0
fi
echo "SOME verify suites FAILED"
exit 1
