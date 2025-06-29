package main

import (
	"flag"
	"os"
	"runtime"

	_ "embed"
)

//go:embed index.html
var dashboardHtml string

// Command types for stress testing control
const (
	cmdStart   int = iota // Start stress testing
	cmdStop               // Stop stress testing
	cmdMetrics            // Get metrics of stress testing
)

// Protocol types supported by the stress tester
const (
	protocolHTTP1 = "http1" // HTTP/1.1 protocol
	protocolHTTP2 = "http2" // HTTP/2 protocol
	protocolHTTP3 = "http3" // HTTP/3 protocol
	protocolWS    = "ws"    // WebSocket protocol
	protocolWSS   = "wss"   // WebSocket Secure protocol
	protocolGRPC  = "grpc"  // gRPC protocol (planned)
)

// Body format types
const (
	bodyHex = "hex" // Hexadecimal body format
)

// Log levels for verbose output
const (
	logLevelTrace = iota // Trace level logging
	logLevelDebug        // Debug level logging
	logLevelInfo         // Info level logging
	logLevelWarn         // Warn level logging
	logLevelError        // Error level logging
)

const (
	usage = `Usage: http_bench [options] <url>

Options:
  -n, --requests       Total number of requests (required)
  -c, --concurrency    Concurrent requests (<= total)
  -q, --qps            Rate limit (queries per second)
  -d, --duration       Test duration (e.g. 10s, 2m, 1h)
  -t, --timeout        Request timeout in ms (default 3000)
  -o, --output         Output: summary (default) or csv
  -m, --method         HTTP method: GET, POST, PUT, DELETE, HEAD, OPTIONS
  -H, --header         Custom header; repeatable: -H "Key: Value"
      --body           Request body (string or hex)
      --body-type      Body format: string or hex (default string)
  -a, --auth           Basic auth user:pass
  -x, --proxy          HTTP proxy host:port
      --disable-compression  Disable response compression
      --disable-keepalive   Disable HTTP keep-alives
      --cpus           CPU cores to use (default %d)
      --url-file       File with list of URLs
      --body-file      File with request body
      --dashboard      Dashboard mode listen address
      --listen         Worker mode listen addr
  -w, -W               Worker addresses for distributed test
      --verbose        Log level: 0 TRACE,1 DEBUG,2 INFO,3 ERROR
      --example        Print usage examples
`

	examples = `
Basic:
  http_bench -n 1000 -c 10 -t 3000 -m GET http://127.0.0.1/test1
  http_bench -n 1000 -c 10 -t 3000 -m GET http://127.0.0.1/test1 --url-file urls.txt

HTTP/2:
  http_bench -d 10s -c 10 --http http2 -m POST https://127.0.0.1/test1 --body '{"key":"value"}'

WebSocket:
  http_bench -d 10s -c 10 --http ws ws://127.0.0.1/ws --body '{"message":"hello"}'

Dashboard:
  http_bench --dashboard 127.0.0.1:12345 --verbose 1

Distributed:
  # Start worker
  http_bench --listen 127.0.0.1:12710 --verbose 1
  # Start controller
  http_bench -n 100 -c 50 -d 10s -m POST http://127.0.0.1/test1 --body '{"key":"value"}' -W 127.0.0.1:12710
`
)

var (
	headerRegexp = `^([\w-]+):\s*(.+)`
	authRegexp   = `^(.+):([^\s].+)`

	stopSignal chan os.Signal

	m          = flag.String("m", "GET", "")
	body       = flag.String("body", "", "")
	bodyType   = flag.String("bodytype", "", "")
	authHeader = flag.String("a", "", "")

	output = flag.String("o", "", "") // Output type

	c        = flag.Int("c", 50, "")                  // Number of requests to run concurrently
	n        = flag.Int("n", 0, "")                   // Number of requests to run
	q        = flag.Int("q", 0, "")                   // Rate limit, in seconds (QPS)
	d        = flag.String("d", "10s", "")            // Duration for stress test
	t        = flag.Int("t", 3000, "")                // Timeout in ms
	httpType = flag.String("http", protocolHTTP1, "") // HTTP Version
	pType    = flag.String("p", "", "")               // TCP/UDP Type

	printExample = flag.Bool("example", false, "")

	cpus = flag.Int("cpus", runtime.GOMAXPROCS(-1), "")

	disableCompression = flag.Bool("disable-compression", false, "")
	disableKeepAlives  = flag.Bool("disable-keepalive", false, "")
	proxyAddr          = flag.String("x", "", "")

	urlstr    = flag.String("url", "", "")
	verbose   = flag.Int("verbose", 3, "")
	listen    = flag.String("listen", "", "")
	dashboard = flag.String("dashboard", "", "")

	urlFile    = flag.String("url-file", "", "")
	bodyFile   = flag.String("body-file", "", "")
	scriptFile = flag.String("script", "", "")
)
