package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// serveDistributedWorker handles HTTP requests for distributed benchmark execution.
// It accepts POST requests with HttpbenchParameters and returns CollectResult.
func serveDistributedWorker(w http.ResponseWriter, r *http.Request) {
	// Set CORS headers for cross-origin requests
	setCORSHeaders(w)

	// Handle preflight OPTIONS request
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Only accept POST requests
	if r.Method != http.MethodPost {
		logWarn("invalid method %s, only POST is allowed", r.Method)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse request parameters
	var params HttpbenchParameters
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		logError("failed to decode request body: %v", err)
		http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	logDebug("received benchmark request: %s", params.String())

	// Execute benchmark
	worker := NewWorker(params.SequenceId)
	result, err := HttpBenchStartup(worker, params)
	if err != nil {
		logError("benchmark execution failed: %v", err)
		http.Error(w, fmt.Sprintf("Benchmark failed: %v", err), http.StatusInternalServerError)
		return
	}

	if result == nil {
		logError("benchmark returned nil result")
		http.Error(w, "Internal error: nil result", http.StatusInternalServerError)
		return
	}

	// Send JSON response
	w.Header().Set("Content-Type", httpContentTypeJSON)
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(result); err != nil {
		logError("failed to encode response: %v", err)
	}
}

// setCORSHeaders sets Cross-Origin Resource Sharing headers
func setCORSHeaders(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
}

// postDistributedWorker sends a benchmark request to a distributed worker node.
// It uses a 5-minute timeout to allow for long-running benchmarks.
func postDistributedWorker(uri string, body []byte) (*CollectResult, error) {
	logDebug("sending request to worker %s, body size: %d bytes", uri, len(body))

	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: 5 * time.Minute, // Allow time for benchmark execution
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
		},
	}

	// Create request with context
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uri, bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", httpContentTypeJSON)

	// Send request
	resp, err := client.Do(req)
	if err != nil {
		logError("failed to send request to worker %s: %v", uri, err)
		return nil, fmt.Errorf("worker request failed: %w", err)
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		logError("worker %s returned status %d: %s", uri, resp.StatusCode, string(body))
		return nil, fmt.Errorf("worker returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response
	var result CollectResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	logDebug("received result from worker %s: %d requests completed", uri, result.LatsTotal)
	return &result, nil
}

// postAllDistributedWorkers sends benchmark requests to all distributed worker nodes concurrently.
// It collects results from all workers and merges them into a single result.
// Workers that fail are logged but don't cause the entire operation to fail.
func postAllDistributedWorkers(workerAddrs flagSlice, jsonParams []byte) (*CollectResult, error) {
	if len(workerAddrs) == 0 {
		return nil, fmt.Errorf("no worker addresses provided")
	}

	logInfo("distributing benchmark to %d worker(s)", len(workerAddrs))

	var (
		wg         sync.WaitGroup
		mu         sync.Mutex
		resultList []*CollectResult
		failedCnt  int
	)

	// Send requests to all workers concurrently
	for _, addr := range workerAddrs {
		wg.Add(1)

		workerURL := buildWorkerURL(addr)
		logDebug("dispatching to worker: %s", workerURL)

		go func(url string) {
			defer wg.Done()

			result, err := postDistributedWorker(url, jsonParams)
			if err != nil {
				logWarn("worker %s failed: %v", url, err)
				mu.Lock()
				failedCnt++
				mu.Unlock()
				return
			}

			if result != nil {
				mu.Lock()
				resultList = append(resultList, result)
				mu.Unlock()
				logInfo("worker %s completed successfully", url)
			}
		}(workerURL)
	}

	// Wait for all workers to complete
	wg.Wait()

	// Check if any workers succeeded
	if len(resultList) == 0 {
		return nil, fmt.Errorf("all %d worker(s) failed", len(workerAddrs))
	}

	if failedCnt > 0 {
		logWarn("%d out of %d worker(s) failed", failedCnt, len(workerAddrs))
	}

	logInfo("collected results from %d worker(s), merging...", len(resultList))

	// Merge all results
	mergedResult := mergeCollectResult(nil, resultList...)
	return mergedResult, nil
}

// buildWorkerURL constructs the full worker API URL from an address.
// It adds the http:// scheme if not present and appends the API path.
func buildWorkerURL(workerAddr string) string {
	// Trim whitespace
	workerAddr = strings.TrimSpace(workerAddr)

	// Check if scheme is already present
	if strings.HasPrefix(workerAddr, "http://") || strings.HasPrefix(workerAddr, "https://") {
		// Remove trailing slash if present
		workerAddr = strings.TrimSuffix(workerAddr, "/")
		return fmt.Sprintf("%s%s", workerAddr, httpWorkerApiPath)
	}

	// Add default http:// scheme
	return fmt.Sprintf("http://%s%s", workerAddr, httpWorkerApiPath)
}
