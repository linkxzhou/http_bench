#!/usr/bin/env bash
# verify_build_gate.sh — plan3.0 §8 质量门禁：gofmt / build / vet / race tests
#
# Usage:  bash test/verify_build_gate.sh [--short] [--no-race-root]
# Env:    SHORT=1, SKIP_ROOT_RACE=1

set -u
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=common.sh
source "$SCRIPT_DIR/common.sh"

SHORT="${SHORT:-0}"
SKIP_ROOT_RACE="${SKIP_ROOT_RACE:-0}"
for a in "$@"; do
	case "$a" in
		--short) SHORT=1 ;;
		--no-race-root) SKIP_ROOT_RACE=1 ;;
	esac
done

fail=0
step() { header "$*"; }

step "gofmt -l ."
unfmt="$(gofmt -l .)"
if [ -n "$unfmt" ]; then
	echo "FAIL: unformatted files:"; echo "$unfmt"; fail=1
else
	echo "PASS: gofmt clean"
fi

step "go build ./..."
if go build ./...; then echo "PASS: build"; else echo "FAIL: build"; fail=1; fi

step "go vet ./..."
if go vet ./...; then echo "PASS: vet"; else echo "FAIL: vet"; fail=1; fi

step "go test -race ./internal/..."
if go test -race -count=1 ./internal/...; then echo "PASS: internal race"
else echo "FAIL: internal race"; fail=1; fi

if [ "$SKIP_ROOT_RACE" = "1" ]; then
	step "skip root race (SKIP_ROOT_RACE=1 / --no-race-root)"
else
	root_flags=(-race -count=1)
	[ "$SHORT" = "1" ] && root_flags+=(-short)
	step "go test ${root_flags[*]} ."
	if go test "${root_flags[@]}" .; then echo "PASS: root race"
	else echo "FAIL: root race"; fail=1; fi
fi

echo
if [ "$fail" -eq 0 ]; then echo "ALL GATES PASSED"; exit 0; fi
echo "SOME GATES FAILED"; exit 1
