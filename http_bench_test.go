package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/quic-go/quic-go/http3"
)

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

// Stop terminates the command gracefully by canceling its context
// If the process doesn't stop, it forcefully kills it
func (c *CommandRunner) Stop() error {
	if c.cmd == nil {
		return errors.New("command not initialized: nothing to stop")
	}

	// Cancel context first (graceful shutdown)
	if c.cancel != nil {
		c.cancel()
	}

	// Force kill if process still exists
	if c.cmd.Process != nil {
		return c.cmd.Process.Kill()
	}

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
//
// Parameters:
//   - serverType: Protocol type ("http1", "http2", "http3", "ws")
//   - name: Server name for logging
//   - address: Listen address in "host:port" format
//
// Returns:
//   - *TestServer: Configured and running test server
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
//
// Parameters:
//   - t: Testing context
//   - name: Test name for logging
//   - args: Command-line arguments string
//   - expectError: Whether an error is expected
//   - description: Test description
//
// Returns:
//   - string: Command output
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
	testCases := []TestCase{
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
			Description: "GET request with Keep-Alive disabled",
			Args: fmt.Sprintf(`-c 1 -d %ds -http %s -m GET -disable-keepalive https://%s/`,
				TestDuration, serverName, serverAddress),
			ExpectError: false,
		},
		{
			Description: "GET request with Compression disabled",
			Args: fmt.Sprintf(`-c 1 -d %ds -http %s -m GET -disable-compression https://%s/`,
				TestDuration, serverName, serverAddress),
			ExpectError: false,
		},
		{
			Description: "GET request with multiple connections",
			Args: fmt.Sprintf(`-c 10 -d %ds -http %s -m GET https://%s/`,
				TestDuration, serverName, serverAddress),
			ExpectError: false,
		},
		{
			Description: "PUT request with JSON body",
			Args: fmt.Sprintf(`-c 1 -d %ds -http %s -m PUT -body '%s' https://%s/`,
				TestDuration, serverName, `{"update":"true"}`, serverAddress),
			ExpectError: false,
		},
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
			Description: "GET request with Keep-Alive disabled",
			Args: fmt.Sprintf(`-c 1 -d %ds -http %s -m GET --disable-keepalive https://%s/`,
				TestDuration, serverName, serverAddress),
			ExpectError: false,
		},
		{
			Description: "GET request with Compression disabled",
			Args: fmt.Sprintf(`-c 1 -d %ds -http %s -m GET --disable-compression https://%s/`,
				TestDuration, serverName, serverAddress),
			ExpectError: false,
		},
	}

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
	testCases := []TestCase{
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
			Description: "GET request with Keep-Alive disabled",
			Args: fmt.Sprintf(`-c 1 -d %ds -http %s -m GET -disable-keepalive https://%s/`,
				TestDuration, serverName, serverAddress),
			ExpectError: false,
		},
		{
			Description: "GET request with Compression disabled",
			Args: fmt.Sprintf(`-c 1 -d %ds -http %s -m GET -disable-compression https://%s/`,
				TestDuration, serverName, serverAddress),
			ExpectError: false,
		},
		{
			Description: "GET request with multiple connections",
			Args: fmt.Sprintf(`-c 5 -d %ds -http %s -m GET https://%s/`,
				TestDuration, serverName, serverAddress),
			ExpectError: false,
		},
		{
			Description: "PUT request with JSON body",
			Args: fmt.Sprintf(`-c 1 -d %ds -http %s -m PUT -body '%s' https://%s/`,
				TestDuration, serverName, `{"update":"true"}`, serverAddress),
			ExpectError: false,
		},
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
			Description: "GET request with Keep-Alive disabled",
			Args: fmt.Sprintf(`-c 1 -d %ds -http %s -m GET --disable-keepalive https://%s/`,
				TestDuration, serverName, serverAddress),
			ExpectError: false,
		},
		{
			Description: "GET request with Compression disabled",
			Args: fmt.Sprintf(`-c 1 -d %ds -http %s -m GET --disable-compression https://%s/`,
				TestDuration, serverName, serverAddress),
			ExpectError: false,
		},
	}

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
	testCases := []TestCase{
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
			Description: "GET request with Keep-Alive disabled",
			Args: fmt.Sprintf(`-c 1 -d %ds -http %s -m GET -disable-keepalive https://%s/`,
				TestDuration, serverName, serverAddress),
			ExpectError: false,
		},
		{
			Description: "GET request with Compression disabled",
			Args: fmt.Sprintf(`-c 1 -d %ds -http %s -m GET -disable-compression https://%s/`,
				TestDuration, serverName, serverAddress),
			ExpectError: false,
		},
	}

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
