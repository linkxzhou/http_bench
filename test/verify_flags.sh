#!/usr/bin/env bash
# verify_flags.sh — Exercise every CLI flag from `./http_bench -h` and verify
# expected behavior. Requires Python3 (built-in http.server) for the test target.
#
# Usage:  bash test/verify_flags.sh [path/to/http_bench]
# Exit:   0 = all pass, 1 = at least one failure

set -u

# ── Setup ──────────────────────────────────────────────────────────────────

BENCH="${1:-./http_bench}"
if [ ! -x "$BENCH" ]; then
	echo "ERROR: http_bench binary not found at: $BENCH"
	exit 1
fi

PASS=0
FAIL=0
FAILED_TESTS=()

# Colors
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
	((PASS++))
	echo -e "  ${GREEN}[PASS]${NC} $1"
}
log_fail() {
	((FAIL++))
	FAILED_TESTS+=("$1")
	echo -e "  ${RED}[FAIL]${NC} $1"
	echo -e "       ${YELLOW}detail:${NC} $2"
}

# Verify a substring exists in output
assert_contains() {
	local label="$1" needle="$2" output="$3"
	if echo "$output" | grep -qF "$needle"; then
		log_pass "$label"
	else
		log_fail "$label" "expected substring '$needle' not found in output"
	fi
}

# Verify exit code is 0
assert_ok() {
	local label="$1" exit_code="$2" output="${3:-}"
	if [ "$exit_code" -eq 0 ]; then
		log_pass "$label"
	else
		local snippet
		snippet=$(echo "$output" | tail -3 | tr '\n' ' ')
		log_fail "$label" "exit code $exit_code, output: $snippet"
	fi
}

# Verify no panic in output
assert_no_panic() {
	local label="$1" output="$2"
	if echo "$output" | grep -qi "panic"; then
		log_fail "$label" "panic detected: $(echo "$output" | grep -i panic | head -1)"
	else
		log_pass "$label"
	fi
}

# ── exec_bench: run http_bench, print command + output, store result ───────
# Global results: BENCH_OUT (stdout+stderr), BENCH_RC (exit code)
# Usage:
#   exec_bench <flag...>            — adds -n 1 -c 1 -q 100 and server URL
#   exec_bench_raw <arg...>         — passes args verbatim to http_bench
BENCH_OUT=""
BENCH_RC=0

_print_bench_output() {
	local out="$1" rc="$2"
	local lines
	lines=$(echo "$out" | wc -l | tr -d ' ')
	if [ "$lines" -gt 20 ]; then
		echo "$out" | head -20
		echo -e "  ${DIM}... ($lines lines total, exit=$rc)${NC}"
	else
		echo "$out"
		if [ "$rc" -ne 0 ]; then
			echo -e "  ${RED}(exit=$rc)${NC}"
		fi
	fi
}

exec_bench() {
	echo -e "  ${CYAN}CMD:${NC} $BENCH $* -n 1 -c 1 -q 100 $SRV_URL"
	BENCH_OUT=$("$BENCH" "$@" -n 1 -c 1 -q 100 "$SRV_URL" 2>&1)
	BENCH_RC=$?
	_print_bench_output "$BENCH_OUT" "$BENCH_RC"
}

exec_bench_raw() {
	echo -e "  ${CYAN}CMD:${NC} $BENCH $*"
	BENCH_OUT=$("$BENCH" "$@" 2>&1)
	BENCH_RC=$?
	_print_bench_output "$BENCH_OUT" "$BENCH_RC"
}

# ── Start test HTTP server ─────────────────────────────────────────────────
# Python server that echoes received headers and body to a log file for
# verification, plus responds 200 OK.

PORT=18901
SRV_URL="http://127.0.0.1:${PORT}"
SRV_LOG="$(mktemp /tmp/httpbench_srv.XXXXXX)"
ECHO_LOG="$(mktemp /tmp/httpbench_echo.XXXXXX)"

python3 - "$PORT" "$ECHO_LOG" > "$SRV_LOG" 2>&1 <<'PYEOF' &
import sys, http.server

PORT = int(sys.argv[1])
ECHO_LOG = sys.argv[2]

class Handler(http.server.BaseHTTPRequestHandler):
	def _handle(self):
		length = int(self.headers.get("Content-Length", 0))
		body = self.rfile.read(length).decode("utf-8", errors="replace")
		# Log method, headers, body for verification
		lines = [f"METHOD: {self.command}"]
		for k, v in self.headers.items():
			lines.append(f"HEADER: {k}: {v}")
		if body:
			lines.append(f"BODY: {body}")
		with open(ECHO_LOG, "a") as f:
			f.write("\n".join(lines) + "\n---\n")
		self.send_response(200)
		self.send_header("Content-Type", "text/plain")
		self.end_headers()
		self.wfile.write(b"OK")

	def do_GET(self):    self._handle()
	def do_POST(self):   self._handle()
	def do_PUT(self):    self._handle()
	def do_DELETE(self): self._handle()
	def do_HEAD(self):
		self.send_response(200)
		self.send_header("Content-Type", "text/plain")
		self.end_headers()

	def log_message(self, *args):
		pass  # quiet

http.server.HTTPServer(("127.0.0.1", PORT), Handler).serve_forever()
PYEOF
SRV_PID=$!

cleanup() {
	disown "$SRV_PID" 2>/dev/null || true
	kill "$SRV_PID" 2>/dev/null || true
	wait "$SRV_PID" 2>/dev/null || true
	rm -f "$SRV_LOG" "$ECHO_LOG"
}
trap cleanup EXIT

# Wait for server to be ready
for i in $(seq 1 30); do
	if curl -sf "$SRV_URL/" >/dev/null 2>&1; then
		break
	fi
	sleep 0.1
done

if ! curl -sf "$SRV_URL/" >/dev/null 2>&1; then
	echo "ERROR: test HTTP server failed to start"
	cat "$SRV_LOG"
	exit 1
fi

echo "=== Test HTTP server started on $SRV_URL (PID $SRV_PID) ==="
echo ""

# Clear echo log before each content-verification test
clear_echo() { : > "$ECHO_LOG"; }

# Check echo log contains a substring
assert_echo_contains() {
	local label="$1" needle="$2"
	if grep -qF "$needle" "$ECHO_LOG"; then
		log_pass "$label"
	else
		log_fail "$label" "expected '$needle' not found in server echo log"
	fi
}

# ════════════════════════════════════════════════════════════════════════════
# Flag tests
# ════════════════════════════════════════════════════════════════════════════

echo "--- Testing all flags from \`$BENCH -h\` ---"
echo ""

# ── -H: Custom header (repeatable) ─────────────────────────────────────────
echo -e "${YELLOW}[-H: Custom header]${NC}"
clear_echo
exec_bench -H "X-Custom: testvalue" -H "X-Second: foo"
OUT="$BENCH_OUT"; RC=$BENCH_RC
assert_echo_contains "-H (custom header echoed)" "X-Custom: testvalue"
assert_echo_contains "-H (repeatable header)" "X-Second: foo"
assert_ok    "-H exit code" "$RC"

# ── -a: Basic auth ─────────────────────────────────────────────────────────
echo -e "${YELLOW}[-a: Basic auth]${NC}"
clear_echo
exec_bench -a "user:pass"
OUT="$BENCH_OUT"; RC=$BENCH_RC
assert_echo_contains "-a (basic auth)" "Authorization"
assert_ok    "-a exit code" "$RC"

# ── -body: Request body ────────────────────────────────────────────────────
echo -e "${YELLOW}[-body: Request body]${NC}"
clear_echo
exec_bench -m POST -body "hello-body"
OUT="$BENCH_OUT"; RC=$BENCH_RC
assert_echo_contains "-body (request body)" "hello-body"
assert_ok    "-body exit code" "$RC"

# ── -bodytype: hex body ────────────────────────────────────────────────────
echo -e "${YELLOW}[-bodytype: hex body]${NC}"
# "hello" in hex = 68656c6c6f
clear_echo
exec_bench -m POST -body "68656c6c6f" -bodytype hex
OUT="$BENCH_OUT"; RC=$BENCH_RC
assert_echo_contains "-bodytype hex (decoded)" "BODY: hello"
assert_ok    "-bodytype exit code" "$RC"

# ── -c: Concurrent workers ─────────────────────────────────────────────────
echo -e "${YELLOW}[-c: Concurrent workers]${NC}"
exec_bench_raw -n 20 -c 5 -q 200 "$SRV_URL"
OUT="$BENCH_OUT"; RC=$BENCH_RC
assert_contains "-c (concurrency)" "5" "$OUT"
assert_ok    "-c exit code" "$RC"

# ── -cpus: CPU limit ───────────────────────────────────────────────────────
echo -e "${YELLOW}[-cpus: CPU limit]${NC}"
exec_bench_raw -n 1 -c 1 -cpus 1 "$SRV_URL"
OUT="$BENCH_OUT"; RC=$BENCH_RC
assert_ok "-cpus (CPU limit)" "$RC" "$OUT"

# ── -d: Test duration ──────────────────────────────────────────────────────
echo -e "${YELLOW}[-d: Test duration]${NC}"
# Duration-based test; -n 0 means unlimited, duration stops it.
exec_bench_raw -n 0 -d 1s -c 2 "$SRV_URL"
OUT="$BENCH_OUT"; RC=$BENCH_RC
assert_contains "-d (duration)" "duration" "$OUT"
assert_ok    "-d exit code" "$RC" "$OUT"

# ── -disable-compression ───────────────────────────────────────────────────
echo -e "${YELLOW}[-disable-compression]${NC}"
exec_bench -disable-compression
OUT="$BENCH_OUT"; RC=$BENCH_RC
assert_ok "-disable-compression" "$RC" "$OUT"

# ── -disable-keepalive ─────────────────────────────────────────────────────
echo -e "${YELLOW}[-disable-keepalive]${NC}"
exec_bench -disable-keepalive
OUT="$BENCH_OUT"; RC=$BENCH_RC
assert_ok "-disable-keepalive" "$RC" "$OUT"

# ── -example: Print examples and exit ──────────────────────────────────────
echo -e "${YELLOW}[-example: Print examples]${NC}"
exec_bench_raw -example
OUT="$BENCH_OUT"; RC=$BENCH_RC
assert_contains "-example (examples)" "example" "$OUT"
assert_ok    "-example exit code" "$RC" "$OUT"

# ── -file: URL list file ───────────────────────────────────────────────────
echo -e "${YELLOW}[-file: URL list file]${NC}"
URLFILE=$(mktemp /tmp/httpbench_urls.XXXXXX)
echo "$SRV_URL/" > "$URLFILE"
echo "$SRV_URL/" >> "$URLFILE"
exec_bench_raw -n 2 -c 1 -q 100 -file "$URLFILE"
OUT="$BENCH_OUT"; RC=$BENCH_RC
assert_ok "-file (URL list)" "$RC" "$OUT"
rm -f "$URLFILE"

# ── -http: HTTP protocol ───────────────────────────────────────────────────
echo -e "${YELLOW}[-http: HTTP protocol]${NC}"
exec_bench -http http1
OUT="$BENCH_OUT"; RC=$BENCH_RC
assert_ok "-http http1" "$RC" "$OUT"

# ── -insecure: Skip TLS verification ───────────────────────────────────────
echo -e "${YELLOW}[-insecure: Skip TLS verification]${NC}"
exec_bench -insecure
OUT="$BENCH_OUT"; RC=$BENCH_RC
assert_ok "-insecure (skip TLS)" "$RC" "$OUT"

# ── -listen: Dashboard listen address ──────────────────────────────────────
echo -e "${YELLOW}[-listen: Dashboard listen address]${NC}"
# Start with dashboard, verify it accepts connections, then let it finish
echo -e "  ${CYAN}CMD:${NC} $BENCH -n 1 -c 1 -listen 127.0.0.1:18999 $SRV_URL (background)"
"$BENCH" -n 1 -c 1 -listen "127.0.0.1:18999" "$SRV_URL" >/dev/null 2>&1 &
DASH_PID=$!
sleep 1
if curl -sf "http://127.0.0.1:18999/" >/dev/null 2>&1; then
	log_pass "-listen (dashboard reachable)"
else
	log_fail "-listen (dashboard)" "dashboard at 127.0.0.1:18999 not reachable"
fi
kill "$DASH_PID" 2>/dev/null
wait "$DASH_PID" 2>/dev/null

# ── -m: HTTP method ────────────────────────────────────────────────────────
echo -e "${YELLOW}[-m: HTTP method]${NC}"
clear_echo
exec_bench -m GET
OUT="$BENCH_OUT"; RC=$BENCH_RC
assert_ok "-m GET" "$RC" "$OUT"

clear_echo
exec_bench -m PUT -body "putdata"
OUT="$BENCH_OUT"; RC=$BENCH_RC
assert_echo_contains "-m PUT (method)" "METHOD: PUT"
assert_echo_contains "-m PUT (body)" "putdata"
assert_ok    "-m PUT exit code" "$RC"

# ── -n: Total number of requests ───────────────────────────────────────────
echo -e "${YELLOW}[-n: Total number of requests]${NC}"
exec_bench_raw -n 10 -c 2 -q 200 "$SRV_URL"
OUT="$BENCH_OUT"; RC=$BENCH_RC
assert_contains "-n (total requests)" "10" "$OUT"
assert_ok    "-n exit code" "$RC" "$OUT"

# ── -o: Output format ──────────────────────────────────────────────────────
echo -e "${YELLOW}[-o: Output format]${NC}"
# summary
exec_bench_raw -n 1 -c 1 -o summary "$SRV_URL"
OUT="$BENCH_OUT"; RC=$BENCH_RC
assert_ok "-o summary exit code" "$RC" "$OUT"

# csv
exec_bench_raw -n 1 -c 1 -o csv "$SRV_URL"
OUT="$BENCH_OUT"; RC=$BENCH_RC
assert_contains "-o csv (output)" "," "$OUT"
assert_ok "-o csv exit code" "$RC" "$OUT"

# html
exec_bench_raw -n 1 -c 1 -o html "$SRV_URL"
OUT="$BENCH_OUT"; RC=$BENCH_RC
assert_contains "-o html (output)" "<html" "$OUT"
assert_ok "-o html exit code" "$RC" "$OUT"

# ── -p: Protocol alias ─────────────────────────────────────────────────────
echo -e "${YELLOW}[-p: Protocol alias]${NC}"
exec_bench -p "http1"
OUT="$BENCH_OUT"; RC=$BENCH_RC
assert_ok "-p (protocol alias)" "$RC" "$OUT"

# ── -q: QPS limit ──────────────────────────────────────────────────────────
echo -e "${YELLOW}[-q: QPS limit]${NC}"
exec_bench_raw -n 5 -c 1 -q 10 "$SRV_URL"
OUT="$BENCH_OUT"; RC=$BENCH_RC
assert_contains "-q (QPS limit)" "5" "$OUT"
assert_ok    "-q exit code" "$RC" "$OUT"

# ── -t: Request timeout ────────────────────────────────────────────────────
echo -e "${YELLOW}[-t: Request timeout]${NC}"
exec_bench -t 2s
OUT="$BENCH_OUT"; RC=$BENCH_RC
assert_ok "-t (request timeout)" "$RC" "$OUT"

# ── -url: Target URL ───────────────────────────────────────────────────────
echo -e "${YELLOW}[-url: Target URL]${NC}"
exec_bench_raw -n 1 -c 1 -url "$SRV_URL"
OUT="$BENCH_OUT"; RC=$BENCH_RC
assert_contains "-url (target URL)" "127.0.0.1:${PORT}" "$OUT"
assert_ok "-url exit code" "$RC" "$OUT"

# ── -verbose: Verbosity level ──────────────────────────────────────────────
echo -e "${YELLOW}[-verbose: Verbosity level]${NC}"
# verbose=0 (TRACE) should produce more output
exec_bench_raw -n 1 -c 1 -verbose 0 "$SRV_URL"
OUT="$BENCH_OUT"; RC=$BENCH_RC
assert_ok "-verbose 0 (TRACE)" "$RC" "$OUT"

exec_bench -verbose 4
OUT="$BENCH_OUT"; RC=$BENCH_RC
assert_ok "-verbose 4 (ERROR)" "$RC" "$OUT"

# ── -W / -w: Worker address (repeatable) ───────────────────────────────────
echo -e "${YELLOW}[-W / -w: Worker address]${NC}"
# These require a running worker process; just verify the flag is parsed
# without panic. The tool will fail on connection but should NOT panic.
exec_bench_raw -n 1 -c 1 -W "127.0.0.1:19999" "$SRV_URL"
OUT="$BENCH_OUT"
assert_no_panic "-W (worker addr)" "$OUT"

exec_bench_raw -n 1 -c 1 -w "127.0.0.1:19999" "$SRV_URL"
OUT="$BENCH_OUT"
assert_no_panic "-w (worker alias)" "$OUT"

# ── -x: Proxy address ──────────────────────────────────────────────────────
echo -e "${YELLOW}[-x: Proxy address]${NC}"
# Using a non-existent proxy; should fail gracefully, not panic
exec_bench_raw -n 1 -c 1 -x "http://127.0.0.1:19998" "$SRV_URL"
OUT="$BENCH_OUT"
assert_no_panic "-x (proxy)" "$OUT"

# ── -h: Help output itself ─────────────────────────────────────────────────
echo -e "${YELLOW}[-h: Help output]${NC}"
exec_bench_raw -h
OUT="$BENCH_OUT"
assert_contains "-h (help output)" "Usage of" "$OUT"
assert_no_panic "-h (no panic)" "$OUT"

# ════════════════════════════════════════════════════════════════════════════
# Summary
# ════════════════════════════════════════════════════════════════════════════
echo ""
echo "=== Summary ==="
echo -e "  ${GREEN}Passed:${NC} $PASS"
echo -e "  ${RED}Failed:${NC} $FAIL"
if [ "$FAIL" -gt 0 ]; then
	echo ""
	echo "  Failed tests:"
	for t in "${FAILED_TESTS[@]}"; do
		echo -e "    ${RED}- $t${NC}"
	done
	exit 1
fi
echo ""
echo -e "  ${GREEN}All flag tests passed!${NC}"
exit 0
