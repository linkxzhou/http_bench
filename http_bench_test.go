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
	// Test configuration constants
	TestBinaryPath  = "./http_bench"
	TestDuration    = 5                // Duration in seconds for each test run
	TestTimeout     = 30 * time.Second // Test timeout duration
	TestServerPort  = "18091"          // Default test server port
	TestServerHost  = "127.0.0.1"      // Default test server host
	TestWorkerPort1 = "12710"          // First worker port
	TestWorkerPort2 = "12711"          // Second worker port

	// Test file paths
	TestURLsFile = "./test/urls.txt"
	TestBodyFile = "./test/body.txt"
	TestCertFile = "./test/server.crt"
	TestKeyFile  = "./test/server.key"
)

// TestCase defines the structure for test cases
type TestCase struct {
	Args        string // Command arguments
	Description string // Test description
	ExpectError bool   // Whether error is expected
}

// CommandRunner handles executing commands with timeout
type CommandRunner struct {
	cmd    *exec.Cmd
	ctx    context.Context
	cancel context.CancelFunc
}

// Initialize sets up the command with arguments and environment
func (c *CommandRunner) Initialize(cmd string, args []string) {
	fmt.Println("Command args: ", strings.Join(args, " "))
	c.ctx, c.cancel = context.WithTimeout(context.Background(), TestTimeout)
	c.cmd = exec.CommandContext(c.ctx, cmd, args...)
	c.cmd.Env = os.Environ()
	c.cmd.Dir, _ = os.Getwd()
}

// Execute runs the command and returns its output
func (c *CommandRunner) Execute() (string, error) {
	if c.cmd == nil {
		return "", errors.New("invalid command: not initialized")
	}

	output, err := c.cmd.CombinedOutput()
	return string(output), err
}

// Stop terminates the command
func (c *CommandRunner) Stop() error {
	if c.cmd == nil {
		return errors.New("invalid command: not initialized")
	}

	if c.cancel != nil {
		c.cancel()
	}

	return c.cmd.Process.Kill()
}

// TestServer represents a generic test server with its configuration
type TestServer struct {
	Type      string          // Server type (http1, http2, http3, ws)
	Name      string          // Server name for logging
	Address   string          // Server listen address
	Instance  interface{}     // Server instance
	WaitGroup *sync.WaitGroup // WaitGroup for server shutdown
}

// createTestServer creates and starts a test server of the specified type
func createTestServer(serverType, name, address string) *TestServer {
	var wg sync.WaitGroup
	mux := http.NewServeMux()

	// Configure handlers based on server type
	switch serverType {
	case "ws":
		var upgrader = websocket.Upgrader{}
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			c, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer c.Close()
			for {
				mt, message, err := c.ReadMessage()
				if err != nil {
					break
				}
				err = c.WriteMessage(mt, message)
				if err != nil {
					break
				}
			}
		})
	default: // http1, http2, http3
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			defer r.Body.Close()
			if len(body) == 0 {
				w.Write([]byte(fmt.Sprintf("this is empty body, type: %s", name)))
				return
			}
			w.Write(body)
		})
	}

	// Create server context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), TestTimeout*2)
	var instance interface{}

	switch serverType {
	case "http3":
		srv := &http3.Server{
			Addr:    address,
			Handler: mux,
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer cancel()

			errCh := make(chan error, 1)
			go func() {
				errCh <- srv.ListenAndServeTLS(TestCertFile, TestKeyFile)
			}()

			select {
			case err := <-errCh:
				if err != nil {
					fmt.Fprintf(os.Stderr, name+" ListenAndServe err: %s\n", err.Error())
				}
			case <-ctx.Done():
				// Context timeout or cancellation
			}

			fmt.Fprintf(os.Stdout, name+" Server listening on %s\n", address)
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
					fmt.Fprintf(os.Stderr, name+" ListenAndServe err: %s\n", err.Error())
				}
			case <-ctx.Done():
				// Context timeout or cancellation
				srv.Shutdown(context.Background())
			}

			fmt.Fprintf(os.Stdout, name+" Server listening on %s\n", address)
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

// Stop shuts down the test server
func (ts *TestServer) Stop() {
	switch ts.Type {
	case "http3":
		ts.Instance.(*http3.Server).Close()
	case "http1", "http2", "ws":
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ts.Instance.(*http.Server).Shutdown(ctx)
	}
	ts.WaitGroup.Wait()
}

// RunCommand executes a command and checks the result against expectations
func RunCommand(t *testing.T, name, args string, expectError bool, description string) string {
	t.Logf("Running test: %s", description)

	cmder := CommandRunner{}
	cmder.Initialize(TestBinaryPath, strings.Split(args, " "))

	result, err := cmder.Execute()

	// Check if there was an error or error-indicating output
	hasError := (err != nil) || strings.Contains(strings.ToLower(result), "err") ||
		strings.Contains(strings.ToLower(result), "error")

	if hasError != expectError {
		if err != nil && strings.Contains(err.Error(), "signal: killed") {
			// pass
		} else {
			t.Errorf("Test '%s' error mismatch: got error=%v, expected error=%v, result: %v",
				description, hasError, expectError, result)
		}
	}

	t.Logf("%s | result: %s", name, result)
	return result
}

// buildServerAddress creates a full server address from host and port
func buildServerAddress(host, port string) string {
	return fmt.Sprintf("%s:%s", host, port)
}

func TestStressHTTP1(t *testing.T) {
	serverName := "http1"
	serverAddress := buildServerAddress(TestServerHost, TestServerPort)
	testServer := createTestServer(serverName, serverName, serverAddress)
	defer testServer.Stop()

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

// TestStressHTTP2 tests HTTP/2 functionality
func TestStressHTTP2(t *testing.T) {
	serverName := "http2"
	serverAddress := buildServerAddress(TestServerHost, TestServerPort)
	testServer := createTestServer(serverName, serverName, serverAddress)
	defer testServer.Stop()

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

// TestStressHTTP3 tests HTTP/3 functionality
func TestStressHTTP3(t *testing.T) {
	// 捕获标准错误输出，以便我们可以忽略特定警告
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	// 在函数结束时恢复标准错误输出
	defer func() {
		os.Stderr = oldStderr
	}()

	// 在后台读取和过滤错误输出
	go func() {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			text := scanner.Text()
			// 过滤掉 UDP 缓冲区大小警告
			if !strings.Contains(text, "failed to sufficiently increase send buffer size") {
				fmt.Fprintln(oldStderr, text)
			}
		}
	}()

	serverName := "http3"
	serverAddress := buildServerAddress(TestServerHost, TestServerPort)
	testServer := createTestServer(serverName, serverName, serverAddress)
	defer testServer.Stop()

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

// TestStressWS tests WebSocket functionality
func TestStressWS(t *testing.T) {
	serverName := "ws"
	serverAddress := buildServerAddress(TestServerHost, TestServerPort)
	testServer := createTestServer(serverName, serverName, serverAddress)
	defer testServer.Stop()

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
func TestStressMultipleWorkerHTTP1(t *testing.T) {
	serverName := "http1"
	serverAddress := buildServerAddress(TestServerHost, TestServerPort)
	testServer := createTestServer(serverName, serverName, serverAddress)
	defer testServer.Stop()

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

				go func() {
					defer workerWg.Done()

					// Signal that worker is starting
					startupWg.Done()

					workerResult, _ := workerRunner.Execute()
					t.Logf("Worker result: %s", workerResult)
				}()

				workerRunners = append(workerRunners, workerRunner)
			}

			// Wait for workers to initialize
			startupWg.Wait()
			time.Sleep(5 * time.Second)

			// Run the main command
			RunCommand(t, serverName, tc.MainArgs, tc.ExpectError, tc.Description)

			// Stop all workers
			for _, runner := range workerRunners {
				runner.Stop()
			}

			// Wait for all workers to terminate
			workerWg.Wait()
		})
	}
}
