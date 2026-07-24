package main

import (
	_ "embed"
	"time"
)

//go:embed index.html
var dashboardHtml string

// Protocol types are re-exported from internal/transport.
// Body format types (transport.BodyHex/transport.BodyString) are re-exported from internal/transport.
// transport.DefaultConcurrency is re-exported from internal/transport.
// Command enum (CmdStart/CmdStop/CmdMetrics) lives in internal/transport.

// Worker and performance constants
const (
	defaultWorkerTimeout = 10 * time.Second // Default worker timeout
)

// Default values
const (
	defaultTimeout      = "3s"  // Default request timeout
	defaultDuration     = "10s" // Default test duration
	defaultVerboseLevel = 3     // Default log level (WARN)
)

const (
	usage = `Usage: http_bench [options] <url>

Load Testing Options:
  -n  <number>         Total number of requests to send
  -c  <number>         Number of concurrent workers (default: 50)
  -q  <number>         Rate limit in queries per second (QPS)
  -d  <duration>       Test duration (e.g., 10s, 2m, 1h)
  -t  <duration>       Request timeout (e.g., 3s, 500ms) (default: 3s)

HTTP Request Options:
  -m  <method>         HTTP method: GET, POST, PUT, DELETE, HEAD, OPTIONS (default: GET)
  -H  <header>         Add custom header (repeatable), e.g., -H "Content-Type: application/json"
      -body <data>     Request body content (string or hex format)
      -bodytype <type> Body format: string or hex (default: string)
  -a  <user:pass>      HTTP Basic Authentication credentials
      -http <version>  HTTP protocol: http1, http2, http3, ws, wss (default: http1)

HTTP transport.Client Options:
  -x  <host:port>      HTTP proxy address
      -disable-compression    Disable response compression
      -disable-keepalive      Disable HTTP keep-alive connections
      -insecure               Skip TLS certificate verification (default: true)

Input/Output Options:
  -o  <format>         Output format: summary (default), csv, or html
      -file <path>     Read target URLs from file (one per line)
      -verbose <level> Log verbosity: 0=TRACE, 1=DEBUG, 2=INFO, 3=WARN, 4=ERROR (default: 3)

Distributed Testing:
      -listen <addr>   Start dashboard and worker node on address (e.g., 127.0.0.1:12710)
  -w, -W  <addr>       Worker node addresses for distributed testing (repeatable)

System Options:
      -cpus <number>   Number of CPU cores to use (default: all available)
      -example         Show usage examples and exit

Examples:
  Run -example flag to see detailed usage examples
`

	examples = `
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
                            HTTP_BENCH USAGE EXAMPLES
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

1. Basic GET Request
   Send 1000 requests with 10 concurrent workers:
   $ http_bench -n 1000 -c 10 http://127.0.0.1/api/test

2. POST Request with JSON Body
   Test POST endpoint for 30 seconds:
   $ http_bench -d 30s -c 20 -m POST http://127.0.0.1/api/users \
     -H "Content-Type: application/json" \
     -body '{"name":"John","email":"john@example.com"}'

3. Rate Limited Testing
   Send requests at 100 QPS for 1 minute:
   $ http_bench -d 1m -q 100 -c 10 http://127.0.0.1/api/test

4. HTTP/2 Testing
   Test HTTP/2 endpoint with custom timeout:
   $ http_bench -d 10s -c 10 -t 5000 -http http2 \
     https://127.0.0.1/api/test

5. WebSocket Testing
   Test WebSocket connection:
   $ http_bench -d 10s -c 10 -http ws \
     ws://127.0.0.1/ws -body '{"message":"hello"}'

6. Multiple URLs from File
   Test multiple endpoints (urls.http contains one URL per line):
   $ http_bench -n 1000 -c 10 -file urls.http

7. Authentication Testing
   Test with Basic Auth:
   $ http_bench -n 500 -c 10 -a "username:password" \
     http://127.0.0.1/api/protected

8. Using Proxy
   Route requests through HTTP proxy:
   $ http_bench -n 1000 -c 10 -x "proxy.example.com:8080" \
     http://target.example.com/api/test

9. CSV Output for Analysis
   Export results in CSV format:
   $ http_bench -n 1000 -c 10 -o csv http://127.0.0.1/api/test > results.csv

10. Dashboard & Worker Mode
    Start web dashboard and worker node:
    $ http_bench -listen 127.0.0.1:12345 -verbose 1
    Then open http://127.0.0.1:12345 in your browser

11. Distributed Testing (Multi-Node)
    Step 1 - Start worker nodes on different machines:
    $ http_bench -listen 192.168.1.10:12710 -verbose 1
    $ http_bench -listen 192.168.1.11:12710 -verbose 1
    
    Step 2 - Run controller to coordinate workers:
    $ http_bench -n 10000 -c 100 -d 30s \
      -m POST http://target.example.com/api/test \
      -body '{"key":"value"}' \
      -W 192.168.1.10:12710 -W 192.168.1.11:12710

12. High Concurrency Testing
    Test with 1000 concurrent connections:
    $ http_bench -d 1m -c 1000 -cpus 8 \
      -disable-keepalive http://127.0.0.1/api/test

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Tips:
  • Use -d for duration-based tests or -n for fixed request count
  • When both -n and -d are set, -d takes precedence (runs full duration)
  • Adjust -c (concurrency) based on your system and target capacity
  • Use -verbose 1 for debugging, -verbose 4 for production
  • Distributed mode scales testing across multiple machines
  • Dashboard provides real-time metrics visualization
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
`
)
