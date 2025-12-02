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
	TestDuration = 5                // Duration in seconds for each test run
	TestTimeout  = 30 * time.Second // Maximum time allowed for test execution

	// Test server configuration
	TestServerHost  = "127.0.0.1" // Test server bind address
	TestServerPort  = "18091"     // Default test server port
	TestWorkerPort1 = "12710"     // First distributed worker port
	TestWorkerPort2 = "12711"     // Second distributed worker port

	// Test file paths
	TestURLsFile = "./test/urls.txt"   // File containing multiple test URLs
	TestBodyFile = "./test/body.txt"   // File containing test request body
	TestCertFile = "./test/server.crt" // TLS certificate for HTTPS/HTTP2/HTTP3
	TestKeyFile  = "./test/server.key" // TLS private key for HTTPS/HTTP2/HTTP3
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
	ctx, cancel := context.WithTimeout(context.Background(), TestTimeout*2)
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

			errCh := make(chan error, 1)
			go func() {
				errCh <- srv.ListenAndServeTLS(TestCertFile, TestKeyFile)
			}()

			select {
			case err := <-errCh:
				if err != nil {
					fmt.Fprintf(os.Stderr, "[%s] Server error: %v\n", name, err)
				}
			case <-ctx.Done():
				// Context timeout or cancellation - server shutting down
				fmt.Fprintf(os.Stdout, "[%s] Server context done\n", name)
			}

			fmt.Fprintf(os.Stdout, "[%s] Server stopped (was listening on %s)\n", name, address)
		}()
		instance = srv

	case "ws", "http1", "http2":
		srv := &http.Server{
			Addr:         address,
			Handler:      mux,
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 10 * time.Second,
			IdleTimeout:  30 * time.Second,
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer cancel()

			var err error
			errCh := make(chan error, 1)

			go func() {
				if serverType == "http2" {
					errCh <- srv.ListenAndServeTLS(TestCertFile, TestKeyFile)
				} else {
					errCh <- srv.ListenAndServe()
				}
			}()

			select {
			case err = <-errCh:
				if err != nil && err != http.ErrServerClosed {
					fmt.Fprintf(os.Stderr, "[%s] Server error: %v\n", name, err)
				}
			case <-ctx.Done():
				// Context timeout or cancellation - gracefully shutdown
				fmt.Fprintf(os.Stdout, "[%s] Shutting down server...\n", name)
				shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer shutdownCancel()
				srv.Shutdown(shutdownCtx)
			}

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
	cmder.Initialize(TestBinaryPath, strings.Split(args, " "))

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
		t.Logf("[%s] Result (truncated): %s...", name, result[:200])
	} else {
		t.Logf("[%s] Result: %s", name, result)
	}

	return result
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
	time.Sleep(100 * time.Millisecond)

	// Define test cases
	testCases := []TestCase{
		{
			Description: "GET request with empty body",
			Args: fmt.Sprintf(`-c 1 -d %ds -http %s -m GET -url http://%s/`,
				TestDuration, serverName, serverAddress),
			ExpectError: false,
		},
		{
			Description: "GET request with URLs from file",
			Args: fmt.Sprintf(`-c 1 -d %ds -http %s -m GET -url-file %s`,
				TestDuration, serverName, TestURLsFile),
			ExpectError: false,
		},
		{
			Description: "POST request with JSON body",
			Args: fmt.Sprintf(`-c 1 -d %ds -http %s -m POST -body '%s' http://%s/`,
				TestDuration, serverName, `{"key":"value"}`, serverAddress),
			ExpectError: false,
		},
		{
			Description: "POST request with body from file",
			Args: fmt.Sprintf(`-c 1 -d %ds -http %s -m POST -body-file %s http://%s/`,
				TestDuration, serverName, TestBodyFile, serverAddress),
			ExpectError: false,
		},
		{
			Description: "GET request with multiple connections",
			Args: fmt.Sprintf(`-c 10 -d %ds -http %s -m GET http://%s/`,
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
	time.Sleep(100 * time.Millisecond)

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
			Description: "POST request with body from file",
			Args: fmt.Sprintf(`-c 1 -d %ds -http %s -m POST -body-file %s https://%s/`,
				TestDuration, serverName, TestBodyFile, serverAddress),
			ExpectError: false,
		},
		{
			Description: "GET request with multiple connections",
			Args: fmt.Sprintf(`-c 5 -d %ds -http %s -m GET https://%s/`,
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
	time.Sleep(200 * time.Millisecond)

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
			Description: "POST request with body from file",
			Args: fmt.Sprintf(`-c 1 -d %ds -http %s -m POST -body-file %s https://%s/`,
				TestDuration, serverName, TestBodyFile, serverAddress),
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
	time.Sleep(100 * time.Millisecond)

	// Define test cases
	testCases := []TestCase{
		{
			Description: "WebSocket connection",
			Args: fmt.Sprintf(`-c 1 -d %ds -http %s -url ws://%s/`,
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
			Description: "WebSocket with POST and body from file",
			Args: fmt.Sprintf(`-c 1 -d %ds -http %s -m POST -body-file %s ws://%s/`,
				TestDuration, serverName, TestBodyFile, serverAddress),
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

// TestStressMultipleWorkerHTTP1 tests distributed worker functionality
// It validates coordinated load testing across multiple worker nodes
func TestStressMultipleWorkerHTTP1(t *testing.T) {
	// Note: Not parallel as it uses specific ports that might conflict

	serverName := "http1"
	serverAddress := buildServerAddress(TestServerHost, TestServerPort)
	testServer := createTestServer(serverName, serverName, serverAddress)
	defer testServer.Stop()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

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
			MainArgs: fmt.Sprintf(`-c 1 -d %ds -http %s -m POST -body "%s" -url http://%s/ -W %s -W %s`,
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
			MainArgs: fmt.Sprintf(`-c 2 -d %ds -http %s -m GET -url http://%s/ -W %s -W %s`,
				TestDuration, serverName, serverAddress,
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
				workerRunner.Initialize(TestBinaryPath, strings.Split(workerArg, " "))

				go func(runner *CommandRunner) {
					defer workerWg.Done()

					// Signal that worker is starting
					startupWg.Done()

					// Execute worker and log result
					workerResult, err := runner.Execute()
					if err != nil {
						t.Logf("Worker execution error: %v", err)
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
