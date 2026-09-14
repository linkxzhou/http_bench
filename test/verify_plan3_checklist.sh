#!/usr/bin/env bash
# verify_plan3_checklist.sh — 对照 docs/plan3.0.md 验收布局与符号约定
#
# Usage:  bash test/verify_plan3_checklist.sh
# Exit:   0 = all pass, 1 = at least one failure

set -u
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=common.sh
source "$SCRIPT_DIR/common.sh"

header "plan3.0 layout checklist"

root_go=(*.go)
if [ "${#root_go[@]}" -eq 2 ] && [ -f http_bench.go ] && [ -f http_bench_test.go ]; then
	ok "root has only http_bench.go + http_bench_test.go"
else
	bad "root Go sources" "expected exactly http_bench.go and http_bench_test.go, got: ${root_go[*]}"
fi

if find internal -mindepth 1 -type d | grep -q .; then
	bad "internal flat" "unexpected subdirectories under internal/"
else
	ok "internal/ has no subdirectories"
fi

required=(
	worker.go cli.go config.go validate.go util.go
	transport.go protocols.go tls.go sender.go
	metrics.go circuit.go report.go
	distributed.go controller.go dashboard.go
	limiter.go logging.go request.go
	templatefn.go tmpl_string.go tmpl_crypto.go tmpl_time.go
	tmpl_random.go tmpl_data.go tmpl_math.go api.go
)
for f in "${required[@]}"; do
	if [ -f "internal/$f" ]; then ok "exists internal/$f"
	else bad "exists internal/$f" "missing"; fi
done

for d in transport metrics report request distributed dashboard limiter logging templatefn; do
	if [ -d "internal/$d" ]; then bad "old package gone" "internal/$d still exists"
	else ok "old package removed: internal/$d"; fi
done

pkg_bad=0
for f in internal/*.go; do
	if ! grep -q '^package bench$' "$f"; then
		bad "package bench" "$f is not package bench"
		pkg_bad=1
	fi
done
[ "$pkg_bad" -eq 0 ] && ok "all internal/*.go are package bench"

hdr_bad=0
for f in internal/*.go; do
	case "$f" in *_test.go) continue ;; esac
	if ! head -1 "$f" | grep -q '/\*'; then
		bad "ASCII header" "$f missing leading block comment"
		hdr_bad=1
	fi
done
[ "$hdr_bad" -eq 0 ] && ok "non-test internal sources have ASCII headers"

if [ -f index.html ] && grep -q '//go:embed index.html' http_bench.go && grep -q 'SetDashboardHTML' http_bench.go; then
	ok "index.html at repo root, embedded from package main"
else
	bad "embed index.html" "root index.html or main //go:embed/SetDashboardHTML missing"
fi
if [ -f internal/index.html ]; then
	bad "index.html location" "internal/index.html should not exist (moved to repo root)"
fi

if grep -q 'func ToByteSizeStr' internal/report.go && \
   grep -q 'func toByteSizeStr' internal/metrics.go && \
   grep -q 'func tmplToByteSizeStr' internal/tmpl_math.go; then
	ok "ToByteSizeStr conflict resolved (report export / metrics+tmpl private)"
else
	bad "ToByteSizeStr" "expected export in report.go and private helpers in metrics/tmpl_math"
fi

if grep -Eq 'KB\s*=\s*1\s*<<\s*10' internal/util.go; then
	ok "KB/MB/GB centralized in util.go"
else
	bad "KB/MB/GB" "not found in internal/util.go"
fi

if grep -q 'maxDurationMs' internal/metrics.go && grep -q 'func maxDuration' internal/tls.go; then
	ok "maxDuration rename (metrics → maxDurationMs, tls keeps maxDuration)"
else
	bad "maxDuration" "metrics/tls naming not as plan §6"
fi

need_sections=(
	"1. CLI 解析"
	"2. 工具函数"
	"3. 参数校验"
	"4. Worker 生命周期"
	"5. Transport 集成"
	"6. Metrics 集成"
	"7. Request 解析"
	"8. 端到端压测"
	"9. 分布式压测"
)
sec_bad=0
for s in "${need_sections[@]}"; do
	if ! grep -qF "$s" http_bench_test.go; then
		bad "test section" "missing marker: $s"
		sec_bad=1
	fi
done
[ "$sec_bad" -eq 0 ] && ok "http_bench_test.go has plan §3.2 section markers"

print_summary "plan3 checklist" || exit 1
exit 0
