/*
 * http_bench_test.go — 根包测试（package main，全部合并于此）
 *
 *   ┌──────────────────────────────────────────────────────────────┐
 *   │ 1. CLI 解析          TestParseConfig_*                        │
 *   │ 2. 工具函数          TestNormalizeWorkerAddrs                 │
 *   │ 3. 参数校验          TestValidate* / TestCompileHeaders       │
 *   │ 4. Worker 生命周期   TestHttpbenchWorker*                     │
 *   │ 5. Transport 集成    TestGetRequestBody / TestClientDo*       │
 *   │ 6. Metrics 集成      TestToByteSizeStr / TestStopReason*      │
 *   │ 7. Request 解析      TestRequestSpec_MergeDefaults            │
 *   │ 8. 端到端压测        TestStressHTTP1/2/3 / TestStressWS       │
 *   │ 9. 分布式压测        TestStressMultipleWorkerHTTP1            │
 *   └──────────────────────────────────────────────────────────────┘
 *
 *   端到端压测通过编译并执行 ./http_bench 子进程验证 CLI 行为。
 */

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/quic-go/quic-go/http3"
	"golang.org/x/net/http2"

	bench "github.com/linkxzhou/http_bench/internal"
)

// ══════════════════════════════ 测试基础设施 ══════════════════════════════

var buildTestBinaryOnce sync.Once
var buildTestBinaryErr error

const (
	// Test binary configuration
	TestBinaryPath = "./http_bench" // Path to the compiled http_bench binary

	// Test timing configuration
	TestDuration = 5                // TestDuration in seconds for each test run
	TestTimeout  = 30 * time.Second // Maximum time allowed for test execution

	// Test server configuration
	TestServerHost  = "127.0.0.1" // Test server bind address
	TestServerPort  = "18091"     // Default test server port
	TestWorkerPort1 = "12710"     // First distributed worker port
	TestWorkerPort2 = "12711"     // Second distributed worker port

	// Test file paths
	TestURLsFile = "./test/resturl_test.http"  // File containing multiple test URLs
	TestBodyFile = "./test/restbody_test.http" // File containing test request body
	TestCertFile = "./test/server.crt"         // TLS certificate for HTTPS/HTTP2/HTTP3
	TestKeyFile  = "./test/server.key"         // TLS private key for HTTPS/HTTP2/HTTP3
)

// TestCase defines the structure for a single test case
// It encapsulates the command arguments, description, and expected outcome
type TestCase struct {
	Args        string // Command-line arguments to pass to http_bench
	Description string // Human-readable description of what this test validates
	ExpectError bool   // Whether this test is expected to fail (true) or succeed (false)
}

// CommandRunner handles executing commands with timeout and cancellation support
// It wraps exec.Cmd with context-based lifecycle management
type CommandRunner struct {
	cmd    *exec.Cmd          // The underlying command to execute
	ctx    context.Context    // Context for timeout and cancellation
	cancel context.CancelFunc // Function to cancel the context
}

// Initialize sets up the command with arguments and environment
// It creates a context with timeout and configures the command execution environment
func (c *CommandRunner) Initialize(cmd string, args []string) {
	if _, err := os.Stat(cmd); err != nil {
		buildTestBinaryOnce.Do(func() {
			build := exec.Command("go", "build", "-o", TestBinaryPath, ".")
			buildTestBinaryErr = build.Run()
		})
		if buildTestBinaryErr != nil {
			cmd = "http_bench"
		}
	}
	fmt.Printf("[CommandRunner] Initializing: %s %s\n", cmd, strings.Join(args, " "))

	// Create context with timeout to prevent hanging tests
	c.ctx, c.cancel = context.WithTimeout(context.Background(), TestTimeout)

	// Create command with context for automatic cancellation
	c.cmd = exec.CommandContext(c.ctx, cmd, args...)

	// Inherit environment variables from parent process
	c.cmd.Env = os.Environ()

	// Set working directory to current directory
	if dir, err := os.Getwd(); err == nil {
		c.cmd.Dir = dir
	}
}

// Execute runs the command and returns its combined stdout/stderr output
// Returns an error if the command fails or times out
func (c *CommandRunner) Execute() (string, error) {
	if c.cmd == nil {
		return "", errors.New("command not initialized: call Initialize() first")
	}

	// Run command and capture both stdout and stderr
	output, err := c.cmd.CombinedOutput()

	if err != nil {
		// Check if error is due to context timeout
		if c.ctx.Err() == context.DeadlineExceeded {
			return string(output), fmt.Errorf("command timeout after %v: %w", TestTimeout, err)
		}
	}

	return string(output), err
}

// Stop cancels the command context. CombinedOutput in Execute owns the
// process lifecycle; touching cmd.Process here races with Start and can
// trip the race detector during distributed worker subtests. CommandContext
// kills the child when the context is canceled.
func (c *CommandRunner) Stop() error {
	if c.cancel == nil {
		return errors.New("command not initialized: nothing to stop")
	}
	c.cancel()
	return nil
}

// TestServer represents a generic test server with its configuration
// It supports multiple protocol types: HTTP/1.1, HTTP/2, HTTP/3, and WebSocket
type TestServer struct {
	Type      string          // Server protocol type: "http1", "http2", "http3", or "ws"
	Name      string          // Human-readable server name for logging purposes
	Address   string          // Server listen address in "host:port" format
	Instance  interface{}     // Actual server instance (*http.Server or *http3.Server)
	WaitGroup *sync.WaitGroup // WaitGroup to synchronize server shutdown
}

// createTestServer creates and starts a test server of the specified type
// It configures appropriate handlers and starts the server in a goroutine
func createTestServer(serverType, name, address string) *TestServer {
	var wg sync.WaitGroup
	mux := http.NewServeMux()

	fmt.Printf("[TestServer] Creating %s server on %s\n", serverType, address)

	// Configure handlers based on server type
	switch serverType {
	case "ws":
		// WebSocket server: echo back received messages
		var upgrader = websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true // Allow all origins for testing
			},
		}
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			// Upgrade HTTP connection to WebSocket
			c, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				fmt.Fprintf(os.Stderr, "WebSocket upgrade failed: %v\n", err)
				return
			}
			defer c.Close()

			// Echo loop: read and write back messages
			for {
				mt, message, err := c.ReadMessage()
				if err != nil {
					break // Connection closed or error
				}
				if err = c.WriteMessage(mt, message); err != nil {
					break // Write failed
				}
			}
		})
	default: // http1, http2, http3
		// HTTP server: echo back request body or return default message
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			// Read request body
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, fmt.Sprintf("failed to read body: %v", err), http.StatusBadRequest)
				return
			}
			defer r.Body.Close()

			// Echo body or return default message
			if len(body) == 0 {
				w.Header().Set("Content-Type", "text/plain")
				w.Write([]byte(fmt.Sprintf("empty body response from %s server", name)))
				return
			}

			w.Header().Set("Content-Type", "application/octet-stream")
			w.Write(body)
		})
	}

	// Create server context with extended timeout (2x test timeout)
	_, cancel := context.WithTimeout(context.Background(), TestTimeout*2)
	var instance interface{}
	defer cancel()

	switch serverType {
	case "http3":
		srv := &http3.Server{
			Addr:    address,
			Handler: mux,
		}
		wg.Add(1)
		go func() {
			defer func() {
				wg.Done()
				cancel()
			}()

			srv.ListenAndServeTLS(TestCertFile, TestKeyFile)
			fmt.Fprintf(os.Stdout, "[%s] Server stopped (was listening on %s)\n", name, address)
		}()
		instance = srv

	case "http1", "http2":
		srv := &http.Server{
			Addr:         address,
			Handler:      mux,
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 10 * time.Second,
			IdleTimeout:  30 * time.Second,
		}
		wg.Add(1)
		go func() {
			defer func() {
				cancel()
				wg.Done()
			}()

			srv.ListenAndServeTLS(TestCertFile, TestKeyFile)
			fmt.Fprintf(os.Stdout, "[%s] Server stopped (was listening on %s)\n", name, address)
		}()
		instance = srv
	case "ws":
		srv := &http.Server{
			Addr:         address,
			Handler:      mux,
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 10 * time.Second,
			IdleTimeout:  30 * time.Second,
		}
		wg.Add(1)
		go func() {
			defer func() {
				cancel()
				wg.Done()
			}()

			srv.ListenAndServe()
			fmt.Fprintf(os.Stdout, "[%s] Server stopped (was listening on %s)\n", name, address)
		}()
		instance = srv
	}

	return &TestServer{
		Type:      serverType,
		Name:      name,
		Address:   address,
		Instance:  instance,
		WaitGroup: &wg,
	}
}

// Stop shuts down the test server gracefully
// It waits for the server to complete shutdown before returning
func (ts *TestServer) Stop() {
	fmt.Printf("[TestServer] Stopping %s server on %s\n", ts.Type, ts.Address)

	switch ts.Type {
	case "http3":
		// HTTP/3 server: close immediately
		if err := ts.Instance.(*http3.Server).Close(); err != nil {
			fmt.Fprintf(os.Stderr, "[%s] Error closing server: %v\n", ts.Name, err)
		}

	case "http1", "http2", "ws":
		// HTTP/1.1, HTTP/2, WebSocket: graceful shutdown with timeout
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := ts.Instance.(*http.Server).Shutdown(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "[%s] Error during shutdown: %v\n", ts.Name, err)
		}
	}

	// Wait for server goroutine to complete
	ts.WaitGroup.Wait()
	fmt.Printf("[TestServer] %s server stopped successfully\n", ts.Type)
}

// RunCommand executes a command and validates the result against expectations
// It handles command execution, error checking, and result validation
func RunCommand(t *testing.T, name, args string, expectError bool, description string) string {
	t.Helper() // Mark this as a test helper function
	t.Logf("[%s] Running: %s", name, description)

	// Initialize and execute command
	cmder := CommandRunner{}
	cmder.Initialize(TestBinaryPath, parseArgs(args))

	result, err := cmder.Execute()

	// Determine if there was an error
	// Only check actual command execution error, not output content
	// This avoids false positives from log messages containing "err" or "error"
	hasError := (err != nil && !strings.Contains(err.Error(), "signal: killed"))

	// Validate result against expectations
	if hasError != expectError {
		t.Errorf("[%s] Error mismatch in '%s': got error=%v, expected error=%v\nCommand error: %v\nOutput: %s",
			name, description, hasError, expectError, err, result)
	}

	// Log result summary
	if len(result) > 200 {
		t.Logf("[%s] Result (truncated): %s...", name, result)
	} else {
		t.Logf("[%s] Result: %s", name, result)
	}

	return result
}

// parseArgs parses a command line string into arguments, handling quotes
func parseArgs(input string) []string {
	var args []string
	var currentArg strings.Builder
	var inSingleQuote, inDoubleQuote bool
	var argStarted bool

	for _, r := range input {
		switch r {
		case ' ':
			if !inSingleQuote && !inDoubleQuote {
				if argStarted {
					args = append(args, currentArg.String())
					currentArg.Reset()
					argStarted = false
				}
			} else {
				currentArg.WriteRune(r)
			}
		case '\'':
			if !inDoubleQuote {
				inSingleQuote = !inSingleQuote
				argStarted = true
			} else {
				currentArg.WriteRune(r)
			}
		case '"':
			if !inSingleQuote {
				inDoubleQuote = !inDoubleQuote
				argStarted = true
			} else {
				currentArg.WriteRune(r)
			}
		default:
			currentArg.WriteRune(r)
			argStarted = true
		}
	}

	if argStarted {
		args = append(args, currentArg.String())
	}

	return args
}

// buildServerAddress creates a full server address from host and port
// Returns address in "host:port" format suitable for net.Listen
func buildServerAddress(host, port string) string {
	return fmt.Sprintf("%s:%s", host, port)
}

// commonMethodTestCases returns the request-method / flag coverage shared by
// all HTTP-based protocol tests (HTTP/1, HTTP/2, HTTP/3): DELETE/HEAD/OPTIONS,
// a custom header, Basic Auth, QPS limiting, and keep-alive/compression
// toggles. Extracted to avoid copy-pasted duplication.
func commonMethodTestCases(serverName, serverAddress string) []TestCase {
	return []TestCase{
		{
			Description: "DELETE request",
			Args: fmt.Sprintf(`-c 1 -d %ds -http %s -m DELETE https://%s/`,
				TestDuration, serverName, serverAddress),
			ExpectError: false,
		},
		{
			Description: "HEAD request",
			Args: fmt.Sprintf(`-c 1 -d %ds -http %s -m HEAD https://%s/`,
				TestDuration, serverName, serverAddress),
			ExpectError: false,
		},
		{
			Description: "OPTIONS request",
			Args: fmt.Sprintf(`-c 1 -d %ds -http %s -m OPTIONS https://%s/`,
				TestDuration, serverName, serverAddress),
			ExpectError: false,
		},
		{
			Description: "GET request with custom header",
			Args: fmt.Sprintf(`-c 1 -d %ds -http %s -m GET -H "X-Custom-Header: test-value" https://%s/`,
				TestDuration, serverName, serverAddress),
			ExpectError: false,
		},
		{
			Description: "GET request with Basic Auth",
			Args: fmt.Sprintf(`-c 1 -d %ds -http %s -m GET -a "user:pass" https://%s/`,
				TestDuration, serverName, serverAddress),
			ExpectError: false,
		},
		{
			Description: "GET request with QPS limit",
			Args: fmt.Sprintf(`-c 1 -d %ds -http %s -m GET -q 10 https://%s/`,
				TestDuration, serverName, serverAddress),
			ExpectError: false,
		},
		{
			Description: "GET request with Keep-Alive disabled (single-dash flag)",
			Args: fmt.Sprintf(`-c 1 -d %ds -http %s -m GET -disable-keepalive https://%s/`,
				TestDuration, serverName, serverAddress),
			ExpectError: false,
		},
		{
			Description: "GET request with Compression disabled (single-dash flag)",
			Args: fmt.Sprintf(`-c 1 -d %ds -http %s -m GET -disable-compression https://%s/`,
				TestDuration, serverName, serverAddress),
			ExpectError: false,
		},
		{
			Description: "GET request with Keep-Alive disabled (double-dash flag)",
			Args: fmt.Sprintf(`-c 1 -d %ds -http %s -m GET --disable-keepalive https://%s/`,
				TestDuration, serverName, serverAddress),
			ExpectError: false,
		},
		{
			Description: "GET request with Compression disabled (double-dash flag)",
			Args: fmt.Sprintf(`-c 1 -d %ds -http %s -m GET --disable-compression https://%s/`,
				TestDuration, serverName, serverAddress),
			ExpectError: false,
		},
	}
}

// ════════════════════════════ 1. CLI 解析 ═════════════════════════════════

func TestParseConfig_Defaults(t *testing.T) {
	opts, err := bench.ParseConfig(nil, nil, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if opts.Concurrency == 0 {
		t.Errorf("default concurrency not set")
	}
	if opts.Duration <= 0 {
		t.Errorf("default duration not parsed")
	}
	if opts.Timeout <= 0 {
		t.Errorf("default timeout not parsed")
	}
	if opts.Verbose != 3 {
		t.Errorf("default verbose mismatch: %d", opts.Verbose)
	}
}

func TestParseConfig_Values(t *testing.T) {
	opts, err := bench.ParseConfig([]string{"-n", "100", "-c", "8", "-d", "2s", "-t", "500ms", "-q", "50", "-url", "http://example.com", "-H", "X-Trace: 1", "-W", "127.0.0.1:9001"}, func(string) string { return "" }, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if opts.Count != 100 {
		t.Errorf("Count = %d", opts.Count)
	}
	if opts.Concurrency != 8 {
		t.Errorf("Concurrency = %d", opts.Concurrency)
	}
	if opts.Duration != 2*time.Second {
		t.Errorf("Duration = %v", opts.Duration)
	}
	if opts.Timeout != 500*time.Millisecond {
		t.Errorf("Timeout = %v", opts.Timeout)
	}
	if opts.QPS != 50 {
		t.Errorf("QPS = %d", opts.QPS)
	}
	if opts.URL != "http://example.com" {
		t.Errorf("URL = %q", opts.URL)
	}
	if len(opts.Headers) != 1 || opts.Headers[0] != "X-Trace: 1" {
		t.Errorf("Headers = %v", opts.Headers)
	}
	if len(opts.WorkerAddrs) != 1 || opts.WorkerAddrs[0] != "127.0.0.1:9001" {
		t.Errorf("WorkerAddrs = %v", opts.WorkerAddrs)
	}
}

func TestParseConfig_EnvInjection(t *testing.T) {
	env := map[string]string{"HTTPBENCH_AUTH_KEY": "secret", "HTTPBENCH_GOGC": "200", "HTTPBENCH_WORKERAPI": "/custom"}
	getter := func(k string) string { return env[k] }
	opts, err := bench.ParseConfig(nil, getter, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if opts.AuthKey != "secret" {
		t.Errorf("AuthKey = %q", opts.AuthKey)
	}
	if opts.GCPercent != 200 {
		t.Errorf("GCPercent = %d", opts.GCPercent)
	}
	if opts.WorkerAPIPath != "/custom" {
		t.Errorf("WorkerAPIPath = %q", opts.WorkerAPIPath)
	}
}

func TestParseConfig_InvalidDuration(t *testing.T) {
	_, err := bench.ParseConfig([]string{"-d", "abc"}, nil, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected error for invalid duration")
	}
	if !strings.Contains(err.Error(), "invalid -d") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParseConfig_InvalidTimeout(t *testing.T) {
	_, err := bench.ParseConfig([]string{"-t", "xyz"}, nil, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected error for invalid timeout")
	}
	if !strings.Contains(err.Error(), "invalid -t") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParseConfig_UnknownFlag(t *testing.T) {
	var buf bytes.Buffer
	_, err := bench.ParseConfig([]string{"-unknown"}, nil, &buf)
	if err == nil {
		t.Fatal("expected error for unknown flag")
	}
}

func TestParseConfig_PositionalURL(t *testing.T) {
	opts, err := bench.ParseConfig([]string{"-c", "1", "-d", "1s", "http://example.com"}, nil, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if opts.URL != "http://example.com" {
		t.Errorf("URL = %q, want %q", opts.URL, "http://example.com")
	}
}

func TestParseConfig_ConflictingURL(t *testing.T) {
	_, err := bench.ParseConfig([]string{"-url", "http://a.com", "http://b.com"}, nil, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected error for conflicting URL sources")
	}
	if !strings.Contains(err.Error(), "conflicting URL sources") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestParseConfig_FlagsAfterPositionalURL covers the documented invocation
// style where flags follow the positional URL (README distributed example).
// flag.Parse would otherwise stop at the URL and silently drop -body/-W.
func TestParseConfig_FlagsAfterPositionalURL(t *testing.T) {
	opts, err := bench.ParseConfig([]string{
		"-n", "10000", "-c", "10", "-d", "30s", "-m", "POST",
		"http://www.baidu.com/api/test",
		"-body", `{"key":"value"}`, "-W", "127.0.0.1:12710", "-W", "127.0.0.1:12711",
		"-disable-keepalive", "-insecure=false",
	}, nil, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if opts.URL != "http://www.baidu.com/api/test" {
		t.Errorf("URL = %q", opts.URL)
	}
	if opts.Body != `{"key":"value"}` {
		t.Errorf("Body = %q", opts.Body)
	}
	if len(opts.WorkerAddrs) != 2 || opts.WorkerAddrs[0] != "127.0.0.1:12710" || opts.WorkerAddrs[1] != "127.0.0.1:12711" {
		t.Errorf("WorkerAddrs = %v", opts.WorkerAddrs)
	}
	if !opts.DisableKeepAlives {
		t.Error("DisableKeepAlives should be true")
	}
	if opts.Insecure {
		t.Error("Insecure should be false")
	}
	if opts.Method != "POST" || opts.Count != 10000 || opts.Concurrency != 10 {
		t.Errorf("unexpected opts: %+v", opts)
	}
}

// ════════════════════════════ 2. 工具函数 ═════════════════════════════════

// TestNormalizeWorkerAddrs verifies bare host:port addresses are rewritten
// into full worker API URLs while already-qualified URLs are preserved.
func TestNormalizeWorkerAddrs(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		api  string
		want []string
	}{
		{
			name: "bare host:port gets scheme and default api path",
			in:   []string{"127.0.0.1:12710"},
			api:  "",
			want: []string{"http://127.0.0.1:12710/api"},
		},
		{
			name: "custom worker api path",
			in:   []string{"127.0.0.1:12710"},
			api:  "cb9ab101f9f725cb7c3a355bd5631184",
			want: []string{"http://127.0.0.1:12710/apicb9ab101f9f725cb7c3a355bd5631184"},
		},
		{
			name: "scheme preserved, path appended",
			in:   []string{"http://192.168.1.10:12710"},
			api:  "",
			want: []string{"http://192.168.1.10:12710/api"},
		},
		{
			name: "explicit path preserved",
			in:   []string{"http://192.168.1.10:12710/custom"},
			api:  "",
			want: []string{"http://192.168.1.10:12710/custom"},
		},
		{
			name: "trailing slash treated as empty path",
			in:   []string{"192.168.1.10:12710/"},
			api:  "",
			want: []string{"http://192.168.1.10:12710/api"},
		},
		{
			name: "empty entries dropped",
			in:   []string{"", "  ", "127.0.0.1:12710"},
			api:  "",
			want: []string{"http://127.0.0.1:12710/api"},
		},
	}
	for _, tt := range tests {
		got := bench.NormalizeWorkerAddrs(tt.in, tt.api)
		if len(got) != len(tt.want) {
			t.Errorf("%s: NormalizeWorkerAddrs(%v) = %v, want %v", tt.name, tt.in, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("%s: NormalizeWorkerAddrs(%v)[%d] = %q, want %q", tt.name, tt.in, i, got[i], tt.want[i])
			}
		}
	}
}

// ════════════════════════════ 3. 参数校验 ═════════════════════════════════

func TestValidateParams(t *testing.T) {
	tests := []struct {
		name    string
		params  bench.HttpbenchParameters
		wantErr string
	}{
		{
			name:    "concurrency zero",
			params:  bench.HttpbenchParameters{C: 0, N: 10, Duration: 5 * time.Second},
			wantErr: "concurrency",
		},
		{
			name:    "concurrency negative",
			params:  bench.HttpbenchParameters{C: -1, N: 10, Duration: 5 * time.Second},
			wantErr: "concurrency",
		},
		{
			name:    "n less than c",
			params:  bench.HttpbenchParameters{C: 10, N: 5},
			wantErr: "less than concurrency",
		},
		{
			name:    "neither n nor duration",
			params:  bench.HttpbenchParameters{C: 10},
			wantErr: "either -n",
		},
		{
			name:   "valid n mode",
			params: bench.HttpbenchParameters{C: 10, N: 1000},
		},
		{
			name:   "valid duration mode",
			params: bench.HttpbenchParameters{C: 5, Duration: 10 * time.Second},
		},
		{
			name:   "valid n equals c",
			params: bench.HttpbenchParameters{C: 10, N: 10},
		},
		{
			name:   "duration zero with n positive",
			params: bench.HttpbenchParameters{C: 5, N: 50, Duration: 0},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := bench.ValidateParams(&tt.params)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestValidateOutputFormat(t *testing.T) {
	valid := []string{"", "summary", "csv", "html"}
	for _, o := range valid {
		if err := bench.ValidateOutputFormat(o); err != nil {
			t.Errorf("ValidateOutputFormat(%q) = %v, want nil", o, err)
		}
	}
	if err := bench.ValidateOutputFormat("xml"); err == nil {
		t.Error("ValidateOutputFormat(\"xml\") = nil, want error")
	}
	if err := bench.ValidateOutputFormat("json"); err == nil {
		t.Error("ValidateOutputFormat(\"json\") = nil, want error")
	}
}

func TestValidateProxyURL(t *testing.T) {
	if err := bench.ValidateProxyURL(""); err != nil {
		t.Errorf("ValidateProxyURL(\"\") = %v, want nil", err)
	}
	if err := bench.ValidateProxyURL("http://proxy.example.com:8080"); err != nil {
		t.Errorf("ValidateProxyURL valid = %v, want nil", err)
	}
	if err := bench.ValidateProxyURL("://invalid"); err == nil {
		t.Error("ValidateProxyURL(\"://invalid\") = nil, want error")
	}
}

func TestCompileHeaders(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		h, err := bench.CompileHeaders(nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if h != nil {
			t.Fatalf("expected nil map, got %v", h)
		}
	})

	t.Run("valid headers", func(t *testing.T) {
		h, err := bench.CompileHeaders([]string{"Content-Type: application/json", "X-Trace: abc"}, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := h["Content-Type"]; len(got) != 1 || got[0] != "application/json" {
			t.Errorf("Content-Type = %v, want [application/json]", got)
		}
		if got := h["X-Trace"]; len(got) != 1 || got[0] != "abc" {
			t.Errorf("X-Trace = %v, want [abc]", got)
		}
	})

	t.Run("invalid header", func(t *testing.T) {
		_, err := bench.CompileHeaders([]string{"no-colon-here"}, "")
		if err == nil {
			t.Fatal("expected error for malformed header, got nil")
		}
		if !strings.Contains(err.Error(), "invalid header") {
			t.Errorf("error %q does not mention invalid header", err.Error())
		}
	})

	t.Run("valid auth", func(t *testing.T) {
		h, err := bench.CompileHeaders(nil, "admin:secret")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		auth := h["Authorization"]
		if len(auth) != 1 || !strings.HasPrefix(auth[0], "Basic ") {
			t.Errorf("Authorization = %v, want Basic prefix", auth)
		}
	})

	t.Run("invalid auth", func(t *testing.T) {
		_, err := bench.CompileHeaders(nil, "no-colon")
		if err == nil {
			t.Fatal("expected error for malformed auth, got nil")
		}
		if !strings.Contains(err.Error(), "invalid auth") {
			t.Errorf("error %q does not mention invalid auth", err.Error())
		}
	})

	t.Run("headers and auth combined", func(t *testing.T) {
		h, err := bench.CompileHeaders([]string{"Accept: */*"}, "user:pass")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := h["Accept"]; !ok {
			t.Error("missing Accept header")
		}
		if _, ok := h["Authorization"]; !ok {
			t.Error("missing Authorization header")
		}
	})
}

// ═════════════════════════ 4. Worker 生命周期 ═════════════════════════════

// TestHttpbenchWorkerDo verifies that the worker performs N requests and aggregates results properly.
func TestHttpbenchWorkerDo(t *testing.T) {
	// Setup an echo server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	time.Sleep(100 * time.Millisecond)
	params := bench.HttpbenchParameters{
		URL:             srv.URL,
		RequestMethod:   http.MethodGet,
		RequestBody:     "",
		RequestBodyType: "",
		N:               10,
		C:               2,
		Timeout:         1000 * time.Millisecond,
		QPS:             0,
		SequenceId:      1,
		RequestType:     bench.ProtocolHTTP1,
		Insecure:        true,
	}

	w := bench.NewWorkerForTest(1)
	res, err := w.Run(context.Background(), params)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if len(res.ErrorCounts) != 0 {
		t.Errorf("expected no errors; got %v", res.ErrorCounts)
	}
}

// TestHttpbenchWorkerDurationPrecedence verifies that when both -n and -d are
// set, Duration takes precedence: the test runs for the full duration and the
// request count is treated as unlimited. This matches ab -t, wrk -d, hey -z.
func TestHttpbenchWorkerDurationPrecedence(t *testing.T) {
	bench.SetLevel(bench.LevelError)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	time.Sleep(100 * time.Millisecond)
	params := bench.HttpbenchParameters{
		URL:           srv.URL,
		RequestMethod: http.MethodGet,
		N:             1000, // high count that would finish in <1s
		C:             2,
		Duration:      2 * time.Second,
		Timeout:       1000 * time.Millisecond,
		SequenceId:    3,
		RequestType:   bench.ProtocolHTTP1,
		Insecure:      true,
	}

	w := bench.NewWorkerForTest(3)
	start := time.Now()
	res, err := w.Run(context.Background(), params)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	// Should run for the full duration, not stop after 1000 requests
	if elapsed < 1500*time.Millisecond {
		t.Errorf("expected >=1.5s elapsed; got %v (duration precedence not working)", elapsed)
	}
	if res.StopReason != "duration" {
		t.Errorf("expected stopReason=duration; got %q", res.StopReason)
	}
	// Should have sent more than N requests during the duration
	if res.TotalRequests <= int64(params.N) {
		t.Errorf("expected >%d requests with duration precedence; got %d",
			params.N, res.TotalRequests)
	}
}

func TestHttpbenchWorkerStop(t *testing.T) {
	bench.SetLevel(0)
	// Setup server that delays response
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(10 * time.Millisecond)
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	time.Sleep(100 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	params := bench.HttpbenchParameters{
		URL:             srv.URL,
		RequestMethod:   http.MethodGet,
		RequestBody:     "",
		RequestBodyType: "",
		N:               100,
		C:               1,
		Timeout:         1000 * time.Millisecond,
		QPS:             0,
		SequenceId:      2,
		RequestType:     bench.ProtocolHTTP1,
		Insecure:        true,
	}

	w := bench.NewWorkerForTest(2)
	done := make(chan struct{})
	go func() {
		w.Run(ctx, params)
		close(done)
	}()
	// Let some requests proceed
	time.Sleep(1 * time.Second)
	cancel()
	<-done

	res := w.GetResult()
	if res == nil {
		t.Fatalf("GetResult returned nil")
	}
	// Should complete fewer requests than requested
	if res.TotalRequests >= int64(params.N) {
		t.Errorf("expected fewer than %d requests; got %d", params.N, res.TotalRequests)
	}
}

// ═════════════════════════ 5. Transport 集成 ═════════════════════════════

// TestGetRequestBody for various input scenarios
func TestGetRequestBody(t *testing.T) {
	p := bench.HttpbenchParameters{}
	b, r := p.GetRequestBody()
	if b != nil || r != nil {
		t.Fatalf("expected nil,nil; got %v,%v", b, r)
	}

	// ordinary string
	p.RequestBody = "hello"
	p.RequestBodyType = ""
	b, r = p.GetRequestBody()
	if !bytes.Equal(b, []byte("hello")) {
		t.Errorf("expected body bytes %q; got %q", "hello", b)
	}
	buf := new(bytes.Buffer)
	io.Copy(buf, r)
	if buf.String() != "hello" {
		t.Errorf("reader content mismatch; got %q", buf.String())
	}

	// hex format
	p.RequestBody = hex.EncodeToString([]byte("world"))
	p.RequestBodyType = bench.BodyHex
	b, r = p.GetRequestBody()
	if !bytes.Equal(b, []byte("world")) {
		t.Errorf("expected decoded bytes %q; got %q", "world", b)
	}
	buf.Reset()
	io.Copy(buf, r)
	if buf.String() != "world" {
		t.Errorf("hex reader content mismatch; got %q", buf.String())
	}
}

// TestClientLifecycle verifies independent client initialization and cleanup.
func TestClientLifecycle(t *testing.T) {
	client := &bench.Client{}
	if err := client.Init(bench.ClientOpts{Protocol: bench.ProtocolHTTP1, Params: bench.HttpbenchParameters{URL: "http://127.0.0.1"}, Insecure: true}); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

// Test HTTP/1.1 client Do method
func TestClientDoHTTP1(t *testing.T) {
	// Setup a simple echo server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Write(body)
	}))
	defer srv.Close()

	time.Sleep(100 * time.Millisecond)
	params := bench.HttpbenchParameters{
		URL:                srv.URL,
		RequestMethod:      http.MethodPost,
		RequestBody:        "ping",
		RequestBodyType:    "",
		RequestType:        bench.ProtocolHTTP1,
		Timeout:            500 * time.Millisecond,
		DisableCompression: false,
		DisableKeepAlives:  false,
		Headers:            map[string][]string{"X-Test": {"yes"}},
	}

	c := &bench.Client{}
	if err := c.Init(bench.ClientOpts{Protocol: bench.ProtocolHTTP1, Params: params}); err != nil {
		t.Fatalf("Init error: %v", err)
	}

	code, length, err := c.Do(context.Background(), []byte(params.URL), []byte(params.RequestBody), 0)
	if err != nil {
		t.Fatalf("Do error: %v", err)
	}
	if code != http.StatusOK {
		t.Errorf("expected status 200; got %d", code)
	}
	if int(length) != len("ping") {
		t.Errorf("expected length %d; got %d", len("ping"), length)
	}
}

// Test HTTP/2 client Do method
func TestClientDoHTTP2(t *testing.T) {
	// Use TLS with HTTP/2
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Write(body)
	}))
	// 加载自定义服务器证书并设置 ALPN
	cert, err := tls.LoadX509KeyPair("./test/server.crt", "./test/server.key")
	if err != nil {
		t.Fatalf("load server cert error: %v", err)
	}
	srv.TLS = &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"h2", "http/1.1"},
	}
	http2.ConfigureServer(srv.Config, &http2.Server{})
	srv.StartTLS()
	defer srv.Close()

	time.Sleep(100 * time.Millisecond)
	params := bench.HttpbenchParameters{
		URL:             srv.URL,
		RequestMethod:   http.MethodPost,
		RequestBody:     "hello2",
		RequestBodyType: "",
		Timeout:         500 * time.Millisecond,
		RequestType:     bench.ProtocolHTTP2,
	}

	c := &bench.Client{}
	if err := c.Init(bench.ClientOpts{Protocol: bench.ProtocolHTTP2, Params: params, Insecure: true}); err != nil {
		t.Fatalf("Init HTTP2 error: %v", err)
	}
	code, length, err := c.Do(context.Background(), []byte(params.URL), []byte(params.RequestBody), 0)
	if err != nil {
		t.Fatalf("Do HTTP2 error: %v", err)
	}
	if code != http.StatusOK {
		t.Errorf("expected status 200; got %d", code)
	}
	if int(length) != len("hello2") {
		t.Errorf("expected length %d; got %d", len("hello2"), length)
	}
}

// Test WebSocket client Do method
func TestClientDoWS(t *testing.T) {
	// Start a WebSocket echo server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("upgrade error: %v", err)
		}
		defer conn.Close()
		for {
			mt, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			conn.WriteMessage(mt, msg)
		}
	}))
	defer srv.Close()

	time.Sleep(100 * time.Millisecond)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	params := bench.HttpbenchParameters{
		URL:             wsURL,
		RequestMethod:   http.MethodGet,
		RequestBody:     "pingws",
		RequestBodyType: "",
		Timeout:         500 * time.Millisecond,
		RequestType:     bench.ProtocolWS,
	}

	c := &bench.Client{}
	if err := c.Init(bench.ClientOpts{Protocol: bench.ProtocolWS, Params: params}); err != nil {
		t.Fatalf("Init WS error: %v", err)
	}
	code, length, err := c.Do(context.Background(), []byte(params.URL), []byte(params.RequestBody), 0)
	if err != nil {
		t.Fatalf("Do WS error: %v", err)
	}
	if code != http.StatusOK {
		t.Errorf("expected status 200; got %d", code)
	}
	if int(length) != len("pingws") {
		t.Errorf("expected length %d; got %d", len("pingws"), length)
	}
}

// Test HTTP/3 client Do method (skipped)
func TestClientDoHTTP3(t *testing.T) {
	t.Skip("HTTP3 test requires QUIC environment")
}

// Test Do method timeout behavior
func TestClientDoTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	params := bench.HttpbenchParameters{
		URL:           srv.URL,
		RequestMethod: http.MethodGet,
		Timeout:       10, // very short timeout
	}

	c := &bench.Client{}
	if err := c.Init(bench.ClientOpts{Protocol: bench.ProtocolHTTP1, Params: params}); err != nil {
		t.Fatalf("Init error: %v", err)
	}

	_, _, err := c.Do(context.Background(), []byte(params.URL), []byte(params.RequestBody), 0)
	if err == nil {
		t.Fatal("expected timeout error; got nil")
	}
}

// BenchmarkClient_Do benchmarks the performance of HTTP client requests
func BenchmarkClient_Do(b *testing.B) {
	// Setup a simple echo server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	params := bench.HttpbenchParameters{
		URL:                srv.URL,
		RequestMethod:      http.MethodGet,
		RequestType:        bench.ProtocolHTTP1,
		Timeout:            500,
		DisableCompression: true,
		DisableKeepAlives:  false,
	}

	c := &bench.Client{}
	if err := c.Init(bench.ClientOpts{Protocol: bench.ProtocolHTTP1, Params: params}); err != nil {
		b.Fatalf("Init error: %v", err)
	}

	urlBytes := []byte(params.URL)
	reqBody := []byte("benchmark")

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		code, _, err := c.Do(context.Background(), urlBytes, reqBody, 0)
		if err != nil {
			b.Fatalf("Do error: %v", err)
		}
		if code != http.StatusOK {
			b.Fatalf("expected status 200; got %d", code)
		}
	}
}

// ═════════════════════════ 6. Metrics 集成 ═══════════════════════════════

// small helper to create a dummy internal result
func makeRes(code int, durSec float64, size int64, errMsg string) *bench.Result {
	// treat empty errMsg as no error
	var errObj error
	if errMsg != "" {
		errObj = errorString(errMsg)
	}
	return &bench.Result{
		StatusCode:    code,
		Duration:      durationFromSec(durSec),
		ContentLength: size,
		Err:           errObj,
	}
}

// errorString implements error interface
type errorString string

func (e errorString) Error() string { return string(e) }

// durationFromSec for tests
func durationFromSec(s float64) (d time.Duration) {
	return time.Duration(s * float64(time.Second))
}

func TestToByteSizeStr(t *testing.T) {
	tests := []struct {
		bytes    float64
		contains string
	}{
		{500, "500"},        // bytes
		{2 * bench.KB, "K"}, // kilobytes
		{3 * bench.MB, "M"}, // megabytes
		{4 * bench.GB, "G"}, // gigabytes
	}
	for _, tc := range tests {
		got := bench.ToByteSizeStr(tc.bytes)
		if !strings.Contains(got, tc.contains) {
			t.Errorf("ToByteSizeStr(%f) = %q, want contains %q", tc.bytes, got, tc.contains)
		}
	}
}

func TestGetCollectResultDefaults(t *testing.T) {
	r := bench.NewCollectResult()
	if r.LatencyHistogram == nil || r.ErrorCounts == nil || r.StatusCodeCounts == nil {
		t.Fatal("maps not initialized")
	}
	if r.Slowest != time.Duration(bench.IntMin) || r.Fastest != time.Duration(bench.IntMax) {
		t.Fatal("bad initial Fastest/Slowest")
	}
}

func TestAppendAndMarshal(t *testing.T) {
	r := bench.NewCollectResult()
	// append two successes and one error
	r.Record(makeRes(200, 0.01, 100, ""))
	r.Record(makeRes(500, 0.02, 0, ""))
	r.Record(makeRes(200, 0.01, 50, ""))

	if r.StatusCodeCounts[200] != 2 || r.StatusCodeCounts[500] != 1 {
		t.Errorf("unexpected status counts: %#v", r.StatusCodeCounts)
	}

	// Check latencies: 0.01s = 10ms
	if val, ok := r.LatencyHistogram[10*time.Millisecond]; !ok || val != 2 {
		t.Errorf("expected 2 count for duration 10ms, got %d", val)
	}

	data, err := r.Marshal()
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	var check bench.CollectResult
	if err := json.Unmarshal(data, &check); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if check.StatusCodeCounts[200] != r.StatusCodeCounts[200] {
		t.Error("roundtrip mismatch")
	}
	if val, ok := check.LatencyHistogram[10*time.Millisecond]; !ok || val != 2 {
		t.Errorf("roundtrip lats mismatch: expected 2, got %d", val)
	}
}

// TestStopReasonSnapshot verifies Snapshot copies StopReason.
// Without this, the reason set by SetStopReason is lost when handleStartup
// reads the result via GetCollectResult.
func TestStopReasonSnapshot(t *testing.T) {
	r := bench.NewCollectResult()
	r.StopReason = "count"
	snap := r.Snapshot()
	if snap.StopReason != "count" {
		t.Fatalf("Snapshot lost StopReason: got %q want %q", snap.StopReason, "count")
	}
}

// TestStopReasonMerge verifies Merge carries StopReason from the source
// result. handleStartup calls Merge(nil, result) which creates a fresh
// CollectResult — the reason must propagate or the report loses it.
func TestStopReasonMerge(t *testing.T) {
	src := bench.NewCollectResult()
	src.StopReason = "duration"
	merged := bench.Merge(nil, src)
	if merged.StopReason != "duration" {
		t.Fatalf("Merge lost StopReason: got %q want %q", merged.StopReason, "duration")
	}
}

// ═════════════════════════ 7. Request 解析 ═══════════════════════════════

func TestRequestSpec_MergeDefaults(t *testing.T) {
	defaults := &bench.HttpbenchParameters{
		N:                 100,
		C:                 10,
		Duration:          5 * time.Second,
		Timeout:           30 * time.Second,
		QPS:               0,
		ProxyURL:          "http://proxy:8080",
		DisableKeepAlives: true,
		URL:               "https://default.example.com",
		RequestMethod:     "GET",
		Headers:           map[string][]string{"X-Default": {"v"}},
		RequestBody:       "default-body",
		RequestBodyType:   "text",
		Output:            "json",
	}

	t.Run("spec_overrides_defaults", func(t *testing.T) {
		spec := bench.Spec{
			Method: "POST",
			URL:    "https://spec.example.com/api",
			Headers: map[string][]string{
				"Authorization": {"Bearer t"},
				"Content-Type":  {"application/json"},
			},
			Body: `{"k":"v"}`,
		}
		p := spec.MergeDefaults(defaults)

		// Spec fields win.
		if p.RequestMethod != "POST" {
			t.Errorf("method = %s, want POST (spec)", p.RequestMethod)
		}
		if p.URL != "https://spec.example.com/api" {
			t.Errorf("url = %s, want spec URL", p.URL)
		}
		if p.RequestBody != `{"k":"v"}` {
			t.Errorf("body = %s, want spec body", p.RequestBody)
		}
		if got := p.Headers["Authorization"]; len(got) != 1 || got[0] != "Bearer t" {
			t.Errorf("headers = %v, want spec headers", p.Headers)
		}

		// Execution params come from defaults.
		if p.N != 100 || p.C != 10 {
			t.Errorf("N/C = %d/%d, want 100/10 (defaults)", p.N, p.C)
		}
		if p.Duration != 5*time.Second || p.Timeout != 30*time.Second {
			t.Errorf("duration/timeout mismatch: %v/%v", p.Duration, p.Timeout)
		}
		if p.ProxyURL != "http://proxy:8080" {
			t.Errorf("proxy = %s, want default proxy", p.ProxyURL)
		}
		if !p.DisableKeepAlives {
			t.Error("DisableKeepAlives should come from defaults")
		}
		if p.Output != "json" {
			t.Errorf("output = %s, want json (defaults)", p.Output)
		}
	})

	t.Run("empty_spec_uses_defaults", func(t *testing.T) {
		spec := bench.Spec{}
		p := spec.MergeDefaults(defaults)

		if p.RequestMethod != "GET" {
			t.Errorf("method = %s, want GET (defaults)", p.RequestMethod)
		}
		if p.URL != "https://default.example.com" {
			t.Errorf("url = %s, want default URL", p.URL)
		}
		if p.RequestBody != "default-body" {
			t.Errorf("body = %s, want default body", p.RequestBody)
		}
		if got := p.Headers["X-Default"]; len(got) != 1 || got[0] != "v" {
			t.Errorf("headers = %v, want default headers", p.Headers)
		}
	})
}

// ════════════════════ 8. 端到端压测 (HTTP1/2/3/WS) ═══════════════════════

// TestStressHTTP1 tests HTTP/1.1 protocol functionality
// It validates various HTTP/1.1 request scenarios including GET, POST, and file-based inputs
func TestStressHTTP1(t *testing.T) {
	t.Parallel() // Run in parallel with other tests

	serverName := "http1"
	serverAddress := buildServerAddress(TestServerHost, TestServerPort)
	testServer := createTestServer(serverName, serverName, serverAddress)
	defer testServer.Stop()

	// Give server time to start
	time.Sleep(1 * time.Second)

	// Define test cases
	testCases := append([]TestCase{
		{
			Description: "GET request with empty body",
			Args: fmt.Sprintf(`-c 1 -d %ds -http %s -m GET -url https://%s/`,
				TestDuration, serverName, serverAddress),
			ExpectError: false,
		},
		{
			Description: "GET request with URLs from file",
			Args: fmt.Sprintf(`-c 1 -d %ds -http %s -m GET -file %s`,
				TestDuration, serverName, TestURLsFile),
			ExpectError: false,
		},
		{
			Description: "POST request with JSON body",
			Args: fmt.Sprintf(`-c 1 -d %ds -http %s -m POST -body '%s' https://%s/`,
				TestDuration, serverName, `{"key":"value"}`, serverAddress),
			ExpectError: false,
		},
		{
			Description: "Requests from file",
			Args: fmt.Sprintf(`-c 1 -d %ds -http %s -file %s`,
				TestDuration, serverName, TestBodyFile),
			ExpectError: false,
		},
		{
			Description: "PUT request with JSON body",
			Args: fmt.Sprintf(`-c 1 -d %ds -http %s -m PUT -body '%s' https://%s/`,
				TestDuration, serverName, `{"update":"true"}`, serverAddress),
			ExpectError: false,
		},
	}, append(commonMethodTestCases(serverName, serverAddress), TestCase{
		Description: "GET request with multiple connections",
		Args: fmt.Sprintf(`-c 10 -d %ds -http %s -m GET https://%s/`,
			TestDuration, serverName, serverAddress),
		ExpectError: false,
	})...)

	// Run all test cases
	for _, tc := range testCases {
		RunCommand(t, serverName, tc.Args, tc.ExpectError, tc.Description)
	}
}

// TestStressHTTP2 tests HTTP/2 protocol functionality
// It validates HTTP/2 over TLS with various request types
func TestStressHTTP2(t *testing.T) {
	t.Parallel() // Run in parallel with other tests

	serverName := "http2"
	serverAddress := buildServerAddress(TestServerHost, TestServerPort)
	testServer := createTestServer(serverName, serverName, serverAddress)
	defer testServer.Stop()

	// Give server time to start
	time.Sleep(1 * time.Second)

	// Define test cases
	testCases := append([]TestCase{
		{
			Description: "GET request with empty body",
			Args: fmt.Sprintf(`-c 1 -d %ds -http %s -m GET -url https://%s/`,
				TestDuration, serverName, serverAddress),
			ExpectError: false,
		},
		{
			Description: "POST request with JSON body",
			Args: fmt.Sprintf(`-c 1 -d %ds -http %s -m POST -body '%s' https://%s/`,
				TestDuration, serverName, `{"key":"value"}`, serverAddress),
			ExpectError: false,
		},
		{
			Description: "Requests from file",
			Args: fmt.Sprintf(`-c 1 -d %ds -http %s -file %s`,
				TestDuration, serverName, TestBodyFile),
			ExpectError: false,
		},
		{
			Description: "PUT request with JSON body",
			Args: fmt.Sprintf(`-c 1 -d %ds -http %s -m PUT -body '%s' https://%s/`,
				TestDuration, serverName, `{"update":"true"}`, serverAddress),
			ExpectError: false,
		},
	}, append(commonMethodTestCases(serverName, serverAddress), TestCase{
		Description: "GET request with multiple connections",
		Args: fmt.Sprintf(`-c 5 -d %ds -http %s -m GET https://%s/`,
			TestDuration, serverName, serverAddress),
		ExpectError: false,
	})...)

	// Run all test cases
	for _, tc := range testCases {
		RunCommand(t, serverName, tc.Args, tc.ExpectError, tc.Description)
	}
}

// TestStressHTTP3 tests HTTP/3 (QUIC) protocol functionality
// It validates HTTP/3 over QUIC with TLS and filters out expected UDP buffer warnings
func TestStressHTTP3(t *testing.T) {
	t.Parallel() // Run in parallel with other tests

	// Capture and filter stderr to suppress expected UDP buffer warnings
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	// Restore stderr when test completes
	defer func() {
		w.Close()
		os.Stderr = oldStderr
	}()

	// Background goroutine to filter stderr output
	go func() {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			text := scanner.Text()
			// Filter out expected UDP buffer size warnings (common in HTTP/3)
			if !strings.Contains(text, "failed to sufficiently increase send buffer size") {
				fmt.Fprintln(oldStderr, text)
			}
		}
	}()

	serverName := "http3"
	serverAddress := buildServerAddress(TestServerHost, TestServerPort)
	testServer := createTestServer(serverName, serverName, serverAddress)
	defer testServer.Stop()

	// Give HTTP/3 server extra time to start (QUIC initialization)
	time.Sleep(1 * time.Second)

	// Define test cases
	testCases := append([]TestCase{
		{
			Description: "GET request with empty body",
			Args: fmt.Sprintf(`-c 1 -d %ds -http %s -m GET -url https://%s/`,
				TestDuration, serverName, serverAddress),
			ExpectError: false,
		},
		{
			Description: "POST request with JSON body",
			Args: fmt.Sprintf(`-c 1 -d %ds -http %s -m POST -body '%s' https://%s/`,
				TestDuration, serverName, `{"key":"value"}`, serverAddress),
			ExpectError: false,
		},
		{
			Description: "Requests from file",
			Args: fmt.Sprintf(`-c 1 -d %ds -http %s -file %s`,
				TestDuration, serverName, TestBodyFile),
			ExpectError: false,
		},
		{
			Description: "PUT request with JSON body",
			Args: fmt.Sprintf(`-c 1 -d %ds -http %s -m PUT -body '%s' https://%s/`,
				TestDuration, serverName, `{"update":"true"}`, serverAddress),
			ExpectError: false,
		},
	}, commonMethodTestCases(serverName, serverAddress)...)

	// Run all test cases
	for _, tc := range testCases {
		RunCommand(t, serverName, tc.Args, tc.ExpectError, tc.Description)
	}
}

// TestStressWS tests WebSocket protocol functionality
// It validates WebSocket connections with various message types and concurrency levels
func TestStressWS(t *testing.T) {
	t.Parallel() // Run in parallel with other tests

	serverName := "ws"
	serverAddress := buildServerAddress(TestServerHost, TestServerPort)
	testServer := createTestServer(serverName, serverName, serverAddress)
	defer testServer.Stop()

	// Give server time to start
	time.Sleep(1 * time.Second)

	// Define test cases
	testCases := []TestCase{
		{
			Description: "WebSocket connection (WS)",
			Args: fmt.Sprintf(`-c 1 -d %ds -http %s -url ws://%s/`,
				TestDuration, serverName, serverAddress),
			ExpectError: false,
		},
		{
			Description: "WebSocket connection (WSS)",
			Args: fmt.Sprintf(`-c 1 -d %ds -http %s -url wss://%s/`,
				TestDuration, serverName, serverAddress),
			ExpectError: false,
		},
		{
			Description: "WebSocket with POST and JSON body",
			Args: fmt.Sprintf(`-c 1 -d %ds -http %s -m POST -body '%s' ws://%s/`,
				TestDuration, serverName, `{"key":"value"}`, serverAddress),
			ExpectError: false,
		},
		{
			Description: "WebSocket with custom header",
			Args: fmt.Sprintf(`-c 1 -d %ds -http %s -H "X-Custom-Header: test-value" ws://%s/`,
				TestDuration, serverName, serverAddress),
			ExpectError: false,
		},
		{
			Description: "WebSocket with Basic Auth",
			Args: fmt.Sprintf(`-c 1 -d %ds -http %s -a "user:pass" ws://%s/`,
				TestDuration, serverName, serverAddress),
			ExpectError: false,
		},
		{
			Description: "WebSocket with QPS limit",
			Args: fmt.Sprintf(`-c 1 -d %ds -http %s -q 10 ws://%s/`,
				TestDuration, serverName, serverAddress),
			ExpectError: false,
		},
		{
			Description: "WebSocket with multiple connections",
			Args: fmt.Sprintf(`-c 3 -d %ds -http %s -url ws://%s/`,
				TestDuration, serverName, serverAddress),
			ExpectError: false,
		},
	}

	// Run all test cases
	for _, tc := range testCases {
		RunCommand(t, serverName, tc.Args, tc.ExpectError, tc.Description)
	}
}

// ════════════════════════ 9. 分布式压测 ═══════════════════════════════════

// TestStressMultipleWorkerHTTP1 tests distributed worker functionality
// It validates coordinated load testing across multiple worker nodes
func TestStressMultipleWorkerHTTP1(t *testing.T) {
	// Note: Not parallel as it uses specific ports that might conflict

	serverName := "http1"
	serverAddress := buildServerAddress(TestServerHost, TestServerPort)
	testServer := createTestServer(serverName, serverName, serverAddress)
	defer testServer.Stop()

	// Give server time to start
	time.Sleep(1 * time.Second)

	// Define worker addresses
	workerAddresses := []string{
		buildServerAddress(TestServerHost, TestWorkerPort1),
		buildServerAddress(TestServerHost, TestWorkerPort2),
	}

	testCases := []struct {
		Description string
		MainArgs    string
		WorkerArgs  []string
		ExpectError bool
	}{
		{
			Description: "Distributed testing with multiple workers",
			MainArgs: fmt.Sprintf(`-c 1 -d %ds -http %s -m POST -body "%s" -url https://%s/ -W %s -W %s`,
				TestDuration, serverName, `{"test":"distributed"}`, serverAddress,
				workerAddresses[0], workerAddresses[1]),
			WorkerArgs: []string{
				fmt.Sprintf(`-listen %s`, workerAddresses[0]),
				fmt.Sprintf(`-listen %s`, workerAddresses[1]),
			},
			ExpectError: false,
		},
		{
			Description: "Distributed testing with GET request",
			MainArgs: fmt.Sprintf(`-c 2 -d %ds -http %s -m GET -url https://%s/ -W %s -W %s`,
				TestDuration, serverName, serverAddress,
				workerAddresses[0], workerAddresses[1]),
			WorkerArgs: []string{
				fmt.Sprintf(`-listen %s`, workerAddresses[0]),
				fmt.Sprintf(`-listen %s`, workerAddresses[1]),
			},
			ExpectError: false,
		},
		{
			Description: "Distributed testing with QPS limit",
			MainArgs: fmt.Sprintf(`-c 2 -d %ds -http %s -m GET -q 10 -url https://%s/ -W %s -W %s`,
				TestDuration, serverName, serverAddress,
				workerAddresses[0], workerAddresses[1]),
			WorkerArgs: []string{
				fmt.Sprintf(`-listen %s`, workerAddresses[0]),
				fmt.Sprintf(`-listen %s`, workerAddresses[1]),
			},
			ExpectError: false,
		},
		{
			Description: "Distributed testing with custom headers",
			MainArgs: fmt.Sprintf(`-c 1 -d %ds -http %s -m GET -H "X-Custom: distributed" -url https://%s/ -W %s -W %s`,
				TestDuration, serverName, serverAddress,
				workerAddresses[0], workerAddresses[1]),
			WorkerArgs: []string{
				fmt.Sprintf(`-listen %s`, workerAddresses[0]),
				fmt.Sprintf(`-listen %s`, workerAddresses[1]),
			},
			ExpectError: false,
		},
		{
			Description: "Distributed testing with Basic Auth",
			MainArgs: fmt.Sprintf(`-c 1 -d %ds -http %s -m GET -a "user:pass" -url https://%s/ -W %s -W %s`,
				TestDuration, serverName, serverAddress,
				workerAddresses[0], workerAddresses[1]),
			WorkerArgs: []string{
				fmt.Sprintf(`-listen %s`, workerAddresses[0]),
				fmt.Sprintf(`-listen %s`, workerAddresses[1]),
			},
			ExpectError: false,
		},
		{
			Description: "Distributed testing with Keep-Alive disabled",
			MainArgs: fmt.Sprintf(`-c 1 -d %ds -http %s -m GET -disable-keepalive -url https://%s/ -W %s -W %s`,
				TestDuration, serverName, serverAddress,
				workerAddresses[0], workerAddresses[1]),
			WorkerArgs: []string{
				fmt.Sprintf(`-listen %s`, workerAddresses[0]),
				fmt.Sprintf(`-listen %s`, workerAddresses[1]),
			},
			ExpectError: false,
		},
		{
			Description: "Distributed testing with Compression disabled",
			MainArgs: fmt.Sprintf(`-c 1 -d %ds -http %s -m GET -disable-compression -url https://%s/ -W %s -W %s`,
				TestDuration, serverName, serverAddress,
				workerAddresses[0], workerAddresses[1]),
			WorkerArgs: []string{
				fmt.Sprintf(`-listen %s`, workerAddresses[0]),
				fmt.Sprintf(`-listen %s`, workerAddresses[1]),
			},
			ExpectError: false,
		},
		{
			Description: "Distributed testing with body from file",
			MainArgs: fmt.Sprintf(`-c 1 -d %ds -http %s -m POST -file %s -url https://%s/ -W %s -W %s`,
				TestDuration, serverName, TestBodyFile, serverAddress,
				workerAddresses[0], workerAddresses[1]),
			WorkerArgs: []string{
				fmt.Sprintf(`-listen %s`, workerAddresses[0]),
				fmt.Sprintf(`-listen %s`, workerAddresses[1]),
			},
			ExpectError: false,
		},
		{
			Description: "Distributed testing with QPS limit",
			MainArgs: fmt.Sprintf(`-c 2 -d %ds -http %s -m GET -q 10 -url https://%s/ -W %s -W %s`,
				TestDuration, serverName, serverAddress,
				workerAddresses[0], workerAddresses[1]),
			WorkerArgs: []string{
				fmt.Sprintf(`-listen %s`, workerAddresses[0]),
				fmt.Sprintf(`-listen %s`, workerAddresses[1]),
			},
			ExpectError: false,
		},
		{
			Description: "Distributed testing with custom headers",
			MainArgs: fmt.Sprintf(`-c 1 -d %ds -http %s -m GET -H "X-Custom: distributed" -url https://%s/ -W %s -W %s`,
				TestDuration, serverName, serverAddress,
				workerAddresses[0], workerAddresses[1]),
			WorkerArgs: []string{
				fmt.Sprintf(`-listen %s`, workerAddresses[0]),
				fmt.Sprintf(`-listen %s`, workerAddresses[1]),
			},
			ExpectError: false,
		},
		{
			Description: "Distributed testing with Basic Auth",
			MainArgs: fmt.Sprintf(`-c 1 -d %ds -http %s -m GET -a "user:pass" -url https://%s/ -W %s -W %s`,
				TestDuration, serverName, serverAddress,
				workerAddresses[0], workerAddresses[1]),
			WorkerArgs: []string{
				fmt.Sprintf(`-listen %s`, workerAddresses[0]),
				fmt.Sprintf(`-listen %s`, workerAddresses[1]),
			},
			ExpectError: false,
		},
		{
			Description: "Distributed testing with Keep-Alive disabled",
			MainArgs: fmt.Sprintf(`-c 1 -d %ds -http %s -m GET -disable-keepalive -url https://%s/ -W %s -W %s`,
				TestDuration, serverName, serverAddress,
				workerAddresses[0], workerAddresses[1]),
			WorkerArgs: []string{
				fmt.Sprintf(`-listen %s`, workerAddresses[0]),
				fmt.Sprintf(`-listen %s`, workerAddresses[1]),
			},
			ExpectError: false,
		},
		{
			Description: "Distributed testing with Compression disabled",
			MainArgs: fmt.Sprintf(`-c 1 -d %ds -http %s -m GET -disable-compression -url https://%s/ -W %s -W %s`,
				TestDuration, serverName, serverAddress,
				workerAddresses[0], workerAddresses[1]),
			WorkerArgs: []string{
				fmt.Sprintf(`-listen %s`, workerAddresses[0]),
				fmt.Sprintf(`-listen %s`, workerAddresses[1]),
			},
			ExpectError: false,
		},
		{
			Description: "Distributed testing with body from file",
			MainArgs: fmt.Sprintf(`-c 1 -d %ds -http %s -m POST -file %s -url https://%s/ -W %s -W %s`,
				TestDuration, serverName, TestBodyFile, serverAddress,
				workerAddresses[0], workerAddresses[1]),
			WorkerArgs: []string{
				fmt.Sprintf(`-listen %s`, workerAddresses[0]),
				fmt.Sprintf(`-listen %s`, workerAddresses[1]),
			},
			ExpectError: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Description, func(t *testing.T) {
			var workerRunners []*CommandRunner
			var workerWg sync.WaitGroup
			var startupWg sync.WaitGroup

			// Start worker processes
			for _, workerArg := range tc.WorkerArgs {
				workerWg.Add(1)
				startupWg.Add(1)
				workerRunner := &CommandRunner{}
				workerRunner.Initialize(TestBinaryPath, parseArgs(workerArg))

				go func(runner *CommandRunner) {
					defer workerWg.Done()

					// Signal that worker is starting
					startupWg.Done()

					// Execute worker and log result
					workerResult, err := runner.Execute()
					if err != nil {
						t.Logf("Worker execution: %v", err)
					}
					if len(workerResult) > 100 {
						t.Logf("Worker result (truncated): %s...", workerResult[:100])
					} else {
						t.Logf("Worker result: %s", workerResult)
					}
				}(workerRunner)

				workerRunners = append(workerRunners, workerRunner)
			}

			// Wait for all workers to start initializing
			startupWg.Wait()
			t.Logf("All workers initialized, waiting for startup...")

			// Give workers time to fully start and listen
			time.Sleep(5 * time.Second)

			// Run the main benchmark command
			t.Logf("Starting main benchmark command...")
			RunCommand(t, serverName, tc.MainArgs, tc.ExpectError, tc.Description)

			// Stop all worker processes
			t.Logf("Stopping all workers...")
			for i, runner := range workerRunners {
				if err := runner.Stop(); err != nil {
					t.Logf("Error stopping worker %d: %v", i, err)
				}
			}

			// Wait for all workers to terminate
			workerWg.Wait()
			t.Logf("All workers stopped")
		})
	}
}
