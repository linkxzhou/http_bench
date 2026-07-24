#!/usr/bin/env bash
set -eu

# Long protocol smoke test. Starts a local test server (HTTP/1.1, HTTPS,
# WebSocket), then benchmarks each protocol (http1, http2, http3, ws) for
# DURATION seconds.
#
# Usage:  bash test/long_benchmark.sh [path/to/http_bench]
# Env:    DURATION (default 300), CONCURRENCY (default 50), QPS (default 0)
#
# NOTE: do not name the duration variable SECONDS — it is a shell special
# variable (elapsed shell time) and would silently override the default.

BENCH="${1:-./http_bench}"
if [ ! -x "$BENCH" ]; then
	echo "ERROR: http_bench binary not found at: $BENCH"
	exit 1
fi

DURATION=${DURATION:-300}
CONCURRENCY=${CONCURRENCY:-50}
QPS=${QPS:-0}

PORT_HTTP=18910
PORT_HTTPS=18911
PORT_WS=18912
HTTP_URL="http://127.0.0.1:${PORT_HTTP}/"
HTTPS_URL="https://127.0.0.1:${PORT_HTTPS}/"
WS_URL="ws://127.0.0.1:${PORT_WS}/"

SRV_LOG="$(mktemp /tmp/httpbench_longsrv.XXXXXX)"

# ── Start combined test server (HTTP + HTTPS + WS) ─────────────────────────
python3 - "$PORT_HTTP" "$PORT_HTTPS" "$PORT_WS" > "$SRV_LOG" 2>&1 <<'PYEOF' &
import sys, os, ssl, http.server, threading, struct, hashlib, base64, socket, time

PORT_HTTP  = int(sys.argv[1])
PORT_HTTPS = int(sys.argv[2])
PORT_WS    = int(sys.argv[3])

# Locate TLS certs
CERT = KEY = None
for p in ("test/server.crt", "server.crt"):
    if os.path.exists(p): CERT = p; break
for p in ("test/server.key", "server.key"):
    if os.path.exists(p): KEY = p; break

class QuietHandler(http.server.BaseHTTPRequestHandler):
    def _handle(self):
        body = b"OK"
        self.send_response(200)
        self.send_header("Content-Type", "text/plain")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)
    def do_GET(self):    self._handle()
    def do_POST(self):   self._handle()
    def do_PUT(self):    self._handle()
    def do_DELETE(self): self._handle()
    def do_HEAD(self):
        self.send_response(200)
        self.send_header("Content-Type", "text/plain")
        self.end_headers()
    def log_message(self, *a): pass

# HTTP/1.1 plain
httpd = http.server.HTTPServer(("127.0.0.1", PORT_HTTP), QuietHandler)
threading.Thread(target=httpd.serve_forever, daemon=True).start()

# HTTPS (TLS, for http2/http3 attempts — ALPN advertises h2)
if CERT and KEY:
    ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    ctx.load_cert_chain(CERT, KEY)
    try:
        ctx.set_alpn_protocols(["h2", "http/1.1"])
    except Exception:
        pass
    httpsd = http.server.HTTPServer(("127.0.0.1", PORT_HTTPS), QuietHandler)
    httpsd.socket = ctx.wrap_socket(httpsd.socket, server_side=True)
    threading.Thread(target=httpsd.serve_forever, daemon=True).start()
    print(f"HTTPS server on {PORT_HTTPS} (cert={CERT})", flush=True)
else:
    print("WARNING: no TLS certs found; https/http2/http3 will fail", flush=True)

# ── Minimal WebSocket echo server (RFC 6455) ───────────────────────────────
def ws_server(port):
    srv = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    srv.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    srv.bind(("127.0.0.1", port))
    srv.listen(5)
    while True:
        conn, _ = srv.accept()
        try:
            data = b""
            while b"\r\n\r\n" not in data:
                data += conn.recv(4096)
            key = None
            for line in data.split(b"\r\n"):
                if line.lower().startswith(b"sec-websocket-key:"):
                    key = line.split(b":", 1)[1].strip().decode()
            if not key:
                conn.close(); continue
            accept = base64.b64encode(hashlib.sha1(
                (key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11").encode()
            ).digest()).decode()
            conn.sendall((
                "HTTP/1.1 101 Switching Protocols\r\n"
                "Upgrade: websocket\r\n"
                "Connection: Upgrade\r\n"
                f"Sec-WebSocket-Accept: {accept}\r\n\r\n"
            ).encode())
            while True:
                hdr = conn.recv(2)
                if len(hdr) < 2: break
                opcode = hdr[0] & 0x0f
                masked = hdr[1] & 0x80
                plen   = hdr[1] & 0x7f
                if plen == 126:
                    plen = struct.unpack(">H", conn.recv(2))[0]
                elif plen == 127:
                    plen = struct.unpack(">Q", conn.recv(8))[0]
                mask = conn.recv(4) if masked else b""
                payload = b""
                while len(payload) < plen:
                    payload += conn.recv(plen - len(payload))
                if masked:
                    payload = bytes(payload[i] ^ mask[i % 4] for i in range(len(payload)))
                if opcode == 0x8: break  # close frame
                conn.sendall(bytes([0x81, len(payload)]) + payload)
            conn.close()
        except Exception:
            pass

threading.Thread(target=ws_server, args=(PORT_WS,), daemon=True).start()
print(f"Servers ready: http={PORT_HTTP} https={PORT_HTTPS} ws={PORT_WS}", flush=True)
while True:
    time.sleep(3600)
PYEOF
SRV_PID=$!

cleanup() {
	disown "$SRV_PID" 2>/dev/null || true
	kill "$SRV_PID" 2>/dev/null || true
	wait "$SRV_PID" 2>/dev/null || true
	rm -f "$SRV_LOG"
}
trap cleanup EXIT

# Wait for HTTP server to be ready
for i in $(seq 1 50); do
	if curl -sf "$HTTP_URL" >/dev/null 2>&1; then
		break
	fi
	sleep 0.1
done

if ! curl -sf "$HTTP_URL" >/dev/null 2>&1; then
	echo "ERROR: test server failed to start"
	cat "$SRV_LOG"
	exit 1
fi

echo "=== Test server started ==="
echo "  HTTP : $HTTP_URL"
echo "  HTTPS: $HTTPS_URL"
echo "  WS   : $WS_URL"
echo ""
echo "=== Benchmark: ${DURATION}s, concurrency=${CONCURRENCY}, qps=${QPS} ==="
echo ""

# ── Run benchmark per protocol ─────────────────────────────────────────────
run() {
	protocol=$1
	case "$protocol" in
		http1)       target=$HTTP_URL ;;
		http2|http3) target=$HTTPS_URL ;;
		ws)          target=$WS_URL ;;
	esac
	echo "── $protocol ──────────────────────────────────────────────────"
	echo "  CMD: $BENCH -d ${DURATION}s -c $CONCURRENCY -q $QPS -http $protocol -url $target -o csv"
	echo ""
	"$BENCH" -d "${DURATION}s" -c "$CONCURRENCY" -q "$QPS" \
		-http "$protocol" -url "$target" -o csv 2>&1 \
		|| echo "  (protocol $protocol unavailable; continuing)"
	echo ""
}

for protocol in http1 http2 http3 ws; do
	run "$protocol"
done

echo "=== All protocols tested ==="
