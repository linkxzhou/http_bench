package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Time scale factor: converts seconds to 0.0001s units for precision
const timeScaleFactor = 10000

// Percentiles for latency distribution reporting
var percentiles = []int{10, 25, 50, 75, 90, 95, 99}

// result represents a single HTTP request result
type result struct {
	err           error         // Request error if any
	statusCode    int           // HTTP status code
	duration      time.Duration // Request duration
	contentLength int64         // Response content length in bytes
}

// CollectResult aggregates and analyzes multiple request results
type CollectResult struct {
	ErrCode        int             `json:"err_code"`         // Error code for the entire test
	ErrMsg         string          `json:"err_msg"`          // Error message for the entire test
	ErrTotal       int64           `json:"err_total"`        // Total number of failed requests
	AvgTotal       int64           `json:"avg_total"`        // Sum of all request durations (scaled)
	Fastest        int64           `json:"fastest"`          // Fastest request duration (scaled)
	Slowest        int64           `json:"slowest"`          // Slowest request duration (scaled)
	Average        int64           `json:"average"`          // Average request duration (scaled)
	Rps            int64           `json:"rps"`              // Requests per second (scaled)
	ErrorDist      map[string]int  `json:"error_dist"`       // Error message distribution
	StatusCodeDist map[int]int     `json:"status_code_dist"` // HTTP status code distribution
	Lats           map[int64]int64 `json:"lats"`             // Latency distribution histogram
	LatsTotal      int64           `json:"lats_total"`       // Total number of successful requests
	SizeTotal      int64           `json:"size_total"`       // Total response size in bytes
	Duration       int64           `json:"duration"`         // Total test duration (scaled)
	Output         string          `json:"output"`           // Output format (summary/csv/html)
	mutex          sync.RWMutex    `json:"-"`                // Protects concurrent access
}

const (
	KB = 1 << 10
	MB = 1 << 20
	GB = 1 << 30
)

// toByteSizeStr converts bytes to human-readable string
func toByteSizeStr(size float64) string {
	switch {
	case size >= GB:
		return fmt.Sprintf("%.3f GB", size/GB)
	case size >= MB:
		return fmt.Sprintf("%.3f MB", size/MB)
	case size >= KB:
		return fmt.Sprintf("%.3f KB", size/KB)
	default:
		return fmt.Sprintf("%.0f bytes", size)
	}
}

// NewCollectResult creates and initializes a new CollectResult
func NewCollectResult() *CollectResult {
	return &CollectResult{
		ErrorDist:      make(map[string]int),
		StatusCodeDist: make(map[int]int),
		Lats:           make(map[int64]int64),
		Slowest:        int64(IntMin),
		Fastest:        int64(IntMax),
	}
}

// print outputs the benchmark results in the specified format
func (result *CollectResult) print() {
	result.mutex.RLock()
	defer result.mutex.RUnlock()

	switch result.Output {
	case "csv":
		result.printCSV()
	case "html":
		result.printHTML()
	default:
		result.printSummary()
	}
}

// printCSV outputs results in CSV format
func (result *CollectResult) printCSV() {
	fmt.Printf("Duration,Count\n")
	for duration, count := range result.Lats {
		fmt.Printf("%.4f,%d\n", float64(duration)/timeScaleFactor, count)
	}
}

// printHTML outputs results in HTML format
func (result *CollectResult) printHTML() {
	fmt.Printf("<html><head><meta charset=\"UTF-8\"><title>Benchmark Result</title></head><body>\n")
	fmt.Printf("<h1>Benchmark Summary</h1>\n")

	// Summary statistics
	avgSizePerRequest := int64(0)
	if result.LatsTotal > 0 {
		avgSizePerRequest = result.SizeTotal / result.LatsTotal
	}
	fmt.Printf("<p>Total: %.4f secs<br>Slowest: %.4f secs<br>Fastest: %.4f secs<br>Average: %.4f secs<br>Requests/sec: %.2f<br>Total Data: %s<br>Size/request: %d bytes</p>\n",
		float64(result.Duration)/timeScaleFactor,
		float64(result.Slowest)/timeScaleFactor,
		float64(result.Fastest)/timeScaleFactor,
		float64(result.Average)/timeScaleFactor,
		float64(result.Rps)/timeScaleFactor,
		toByteSizeStr(float64(result.SizeTotal)),
		avgSizePerRequest)

	// Status codes table
	fmt.Printf("<h2>Status Codes</h2><table border=\"1\"><tr><th>Code</th><th>Count</th></tr>\n")
	for code, count := range result.StatusCodeDist {
		fmt.Printf("<tr><td>%d</td><td>%d</td></tr>\n", code, count)
	}
	fmt.Printf("</table>\n")

	// Latency distribution table
	fmt.Printf("<h2>Latency Distribution</h2><table border=\"1\"><tr><th>Duration (secs)</th><th>Count</th></tr>\n")
	for duration, count := range result.Lats {
		fmt.Printf("<tr><td>%.4f</td><td>%d</td></tr>\n", float64(duration)/timeScaleFactor, count)
	}
	fmt.Printf("</table>\n")

	// Errors table
	if len(result.ErrorDist) > 0 {
		fmt.Printf("<h2>Errors</h2><table border=\"1\"><tr><th>Error</th><th>Count</th></tr>\n")
		for errMsg, count := range result.ErrorDist {
			fmt.Printf("<tr><td>%s</td><td>%d</td></tr>\n", errMsg, count)
		}
		fmt.Printf("</table>\n")
	}
	fmt.Printf("</body></html>\n")
}

// printSummary outputs results in human-readable summary format
func (result *CollectResult) printSummary() {
	if len(result.Lats) == 0 {
		return
	}

	fmt.Printf("Summary:\n")
	fmt.Printf("  Total:\t\t%4.4f secs\n", float64(result.Duration)/timeScaleFactor)
	fmt.Printf("  Slowest:\t%4.4f secs\n", float64(result.Slowest)/timeScaleFactor)
	fmt.Printf("  Fastest:\t%4.4f secs\n", float64(result.Fastest)/timeScaleFactor)
	fmt.Printf("  Average:\t%4.4f secs\n", float64(result.Average)/timeScaleFactor)
	fmt.Printf("  Requests/sec:\t%4.2f\n", float64(result.Rps)/timeScaleFactor)
	fmt.Printf("  Total data:\t%s\n", toByteSizeStr(float64(result.SizeTotal)))
	if result.LatsTotal > 0 {
		fmt.Printf("  Size/request:\t%d bytes\n", result.SizeTotal/result.LatsTotal)
	}

	result.printStatusCodes()
	result.printLatencies()

	if len(result.ErrorDist) > 0 {
		result.printErrors()
	}
}

// printLatencies prints latency distribution percentiles
// Note: This method assumes the caller already holds a read lock
func (result *CollectResult) printLatencies() {
	if result.LatsTotal == 0 {
		return
	}

	percentileData := make([]float64, len(percentiles))
	sortedDurations := make([]int64, 0, len(result.Lats))

	// Collect all durations
	for duration := range result.Lats {
		sortedDurations = append(sortedDurations, duration)
	}

	// Sort durations in ascending order
	sort.Slice(sortedDurations, func(i, j int) bool {
		return sortedDurations[i] < sortedDurations[j]
	})

	// Calculate percentiles using cumulative distribution
	var cumulativeCount int64
	percentileIndex := 0

	for _, duration := range sortedDurations {
		if percentileIndex >= len(percentiles) {
			break
		}

		cumulativeCount += result.Lats[duration]
		percentage := (cumulativeCount * 100) / result.LatsTotal

		for percentileIndex < len(percentiles) && int(percentage) >= percentiles[percentileIndex] {
			percentileData[percentileIndex] = float64(duration) / timeScaleFactor
			percentileIndex++
		}
	}

	fmt.Printf("\nLatency distribution:\n")
	for i, pctl := range percentiles {
		fmt.Printf("  %d%% in %4.4f secs\n", pctl, percentileData[i])
	}
}

// printStatusCodes prints HTTP status code distribution
// Note: This method assumes the caller already holds a read lock
func (result *CollectResult) printStatusCodes() {
	if len(result.StatusCodeDist) == 0 {
		return
	}

	fmt.Printf("\nStatus code distribution:\n")

	// Sort status codes for consistent output
	codes := make([]int, 0, len(result.StatusCodeDist))
	for code := range result.StatusCodeDist {
		codes = append(codes, code)
	}
	sort.Ints(codes)

	for _, code := range codes {
		count := result.StatusCodeDist[code]
		fmt.Printf("  [%d]\t%d responses\n", code, count)
	}
}

// printErrors prints error distribution
// Note: This method assumes the caller already holds a read lock
func (result *CollectResult) printErrors() {
	if len(result.ErrorDist) == 0 {
		return
	}

	fmt.Printf("\nError distribution:\n")
	for errMsg, count := range result.ErrorDist {
		fmt.Printf("  [%d times] %s\n", count, errMsg)
	}
}

func (result *CollectResult) marshal() ([]byte, error) {
	result.mutex.RLock()
	defer result.mutex.RUnlock()

	return json.Marshal(result)
}

// append adds a single request result to the aggregate statistics
// This method is thread-safe and can be called concurrently
func (result *CollectResult) append(res *result) {
	result.mutex.Lock()
	defer result.mutex.Unlock()

	// Handle failed requests
	if res.err != nil {
		result.ErrorDist[res.err.Error()]++
		result.ErrTotal++
		return
	}

	// Convert duration to scaled integer for histogram
	duration := int64(res.duration.Seconds() * timeScaleFactor)
	result.Lats[duration]++

	// Update aggregate statistics
	result.LatsTotal++
	result.Slowest = max(result.Slowest, duration)
	result.Fastest = min(result.Fastest, duration)
	result.AvgTotal += duration
	result.StatusCodeDist[res.statusCode]++

	// Accumulate response size
	if res.contentLength > 0 {
		result.SizeTotal += res.contentLength
	}
}

// isCircuitBreak checks if the error rate exceeds the circuit breaker threshold
// Returns true if the circuit should be opened to stop further requests
func (result *CollectResult) isCircuitBreak() bool {
	result.mutex.RLock()
	defer result.mutex.RUnlock()

	totalRequests := result.LatsTotal + result.ErrTotal
	if totalRequests == 0 {
		return false
	}

	errorRate := (result.ErrTotal * 100) / totalRequests
	return errorRate > circuitBreakerPercent
}

// mergeIntMap merges counts from src into dest for map[int]int
func mergeIntMap(dest, src map[int]int) {
	for k, v := range src {
		dest[k] += v
	}
}

// mergeStrIntMap merges counts from src into dest for map[string]int
func mergeStrIntMap(dest, src map[string]int) {
	for k, v := range src {
		dest[k] += v
	}
}

// mergeInt64Map merges counts from src into dest for map[int64]int64
func mergeInt64Map(dest, src map[int64]int64) {
	for k, v := range src {
		dest[k] += v
	}
}

// mergeCollectResult aggregates multiple CollectResult instances into one
// This is used for combining results from distributed workers or multiple test runs
func mergeCollectResult(result *CollectResult, resultList ...*CollectResult) *CollectResult {
	if result == nil {
		result = NewCollectResult()
	}

	maxDuration := result.Duration

	// Preserve Output field from the first non-empty result
	if result.Output == "" && len(resultList) > 0 {
		for _, v := range resultList {
			if v != nil && v.Output != "" {
				result.Output = v.Output
				break
			}
		}
	}

	for _, v := range resultList {
		if v == nil {
			continue
		}

		// Update min/max latencies
		result.Slowest = max(result.Slowest, v.Slowest)
		result.Fastest = min(result.Fastest, v.Fastest)

		// Accumulate totals
		result.LatsTotal += v.LatsTotal
		result.ErrTotal += v.ErrTotal
		result.AvgTotal += v.AvgTotal
		result.SizeTotal += v.SizeTotal

		// Merge distribution maps
		mergeIntMap(result.StatusCodeDist, v.StatusCodeDist)
		mergeStrIntMap(result.ErrorDist, v.ErrorDist)
		mergeInt64Map(result.Lats, v.Lats)

		// Track maximum duration across all results
		maxDuration = max(maxDuration, v.Duration)
	}

	// Calculate derived metrics
	if maxDuration > 0 {
		result.Duration = maxDuration
		result.Rps = (result.LatsTotal * timeScaleFactor) / maxDuration
	}

	if result.LatsTotal > 0 {
		result.Average = result.AvgTotal / result.LatsTotal
	}

	return result
}
