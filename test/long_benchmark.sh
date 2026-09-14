#!/usr/bin/env bash
# Compatibility wrapper — prefer test/verify_long_benchmark.sh
exec bash "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/verify_long_benchmark.sh" "$@"
