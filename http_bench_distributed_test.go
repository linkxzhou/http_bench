package main

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

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
