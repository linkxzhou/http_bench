package distributed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// PostWorker sends a benchmark request to a single worker and returns the
// collected result. httpDeadline caps the round-trip.
func PostWorker(workerURL string, body []byte, httpDeadline time.Duration) (*WorkerResponse, error) {
	client := &http.Client{Timeout: httpDeadline, Transport: &http.Transport{MaxIdleConns: 100, MaxIdleConnsPerHost: 10, IdleConnTimeout: 90 * time.Second}}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, workerURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+APIKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("worker returned %d: %s", resp.StatusCode, string(raw))
	}
	var result WorkerResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &result, nil
}

// PostAllWorkers dispatches benchmark requests to every worker concurrently.
func PostAllWorkers(workerAddrs []string, body []byte, httpTimeout time.Duration) (*DistributedResult, error) {
	if len(workerAddrs) == 0 {
		return nil, fmt.Errorf("no worker addresses provided")
	}
	if httpTimeout <= 0 {
		httpTimeout = 10 * time.Minute
	}
	details := make([]WorkerResultDetail, len(workerAddrs))
	var mu sync.Mutex
	var results []*WorkerResponse
	var wg sync.WaitGroup
	for index, addr := range workerAddrs {
		wg.Add(1)
		go func(i int, url string) {
			defer wg.Done()
			started := time.Now()
			detail := WorkerResultDetail{Address: url}
			result, err := PostWorker(url, body, httpTimeout)
			detail.Elapsed = time.Since(started)
			if err != nil {
				detail.Error = err.Error()
			} else if result != nil {
				detail.Success = true
				detail.Result = result
			}
			mu.Lock()
			details[i] = detail
			if result != nil {
				results = append(results, result)
			}
			mu.Unlock()
		}(index, addr)
	}
	wg.Wait()
	if len(results) == 0 {
		return &DistributedResult{Workers: details}, fmt.Errorf("all %d worker(s) failed", len(workerAddrs))
	}
	merged := mergeResults(results)
	return &DistributedResult{Merged: merged, Workers: details}, nil
}

func mergeResults(results []*WorkerResponse) *WorkerResponse {
	if len(results) == 1 {
		return results[0]
	}
	out := &WorkerResponse{
		StatusCodeCounts: make(map[int]int),
		ErrorCounts:      make(map[string]int),
		LatencyHistogram: make(map[time.Duration]int64),
	}
	hasResult := false
	for _, r := range results {
		if r == nil {
			continue
		}
		out.TotalRequests += r.TotalRequests
		out.FailedRequests += r.FailedRequests
		out.LatencySum += r.LatencySum
		out.BytesReceived += r.BytesReceived
		if r.Duration > out.Duration {
			out.Duration = r.Duration
		}
		if !hasResult {
			out.Fastest = r.Fastest
			hasResult = true
		} else if r.Fastest < out.Fastest {
			out.Fastest = r.Fastest
		}
		if r.Slowest > out.Slowest {
			out.Slowest = r.Slowest
		}
		if r.StopReason != "" {
			out.StopReason = r.StopReason
		}
		for k, v := range r.StatusCodeCounts {
			out.StatusCodeCounts[k] += v
		}
		for k, v := range r.ErrorCounts {
			out.ErrorCounts[k] += v
		}
		for k, v := range r.LatencyHistogram {
			out.LatencyHistogram[k] += v
		}
	}
	if out.Duration > 0 {
		out.RPS = out.TotalRequests * int64(time.Second) / int64(out.Duration)
	}
	if out.TotalRequests > 0 {
		out.Average = out.LatencySum / time.Duration(out.TotalRequests)
	}
	return out
}
