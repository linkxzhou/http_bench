package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Percentiles for latency distribution reporting
var percentiles = []int{10, 25, 50, 75, 90, 95, 99}
var resultChanMap sync.Map

// Result represents a single HTTP request result
type Result struct {
	err           error         // Request error if any
	statusCode    int           // HTTP status code
	duration      time.Duration // Request duration
	contentLength int64         // Response content length in bytes
	isLast        bool          // Whether this is the last result
}

// ResultChan represents a channel for collecting results from multiple goroutines
type ResultChan struct {
	seqId         int64
	ch            chan *Result
	CollectResult *CollectResult
	isInit        bool
	wg            sync.WaitGroup
	once          sync.Once
}

func NewResult(seqId int64) {
	if _, ok := resultChanMap.Load(seqId); ok {
		return
	}

	resultChanMap.Store(seqId, &ResultChan{
		seqId:  seqId,
		ch:     make(chan *Result, resultChannelSize),
		isInit: false,
	})
}

func appendResult(seqId int64, r *Result) (*ResultChan, error) {
	val, ok := resultChanMap.Load(seqId)
	if !ok || val == nil {
		logError(seqId, "result chan not found for seqId %d", seqId)
		return nil, fmt.Errorf("result chan not found for seqId %d", seqId)
	}

	resultChan := val.(*ResultChan)
	if resultChan.isInit {
		resultChan.ch <- r

		// Check if circuit break should be triggered
		if resultChan.CollectResult.isCircuitBreak() {
			stopResult(seqId)
			return resultChan, fmt.Errorf("circuit break")
		}

		return resultChan, nil
	}

	resultChan.once.Do(func() {
		// Initialize the CollectResult if not done already
		resultChan.isInit = true
		resultChan.CollectResult = NewCollectResult()

		resultChan.wg.Add(1)
		go func(seqId int64, resultChan *ResultChan) {
			startTime := time.Now()
			defer func() {
				resultChan.CollectResult.Duration = time.Since(startTime)
				resultChan.wg.Done()
				logTrace(seqId, "collect result finished, duration %v ms",
					resultChan.CollectResult.Duration.Milliseconds())
			}()

			for {
				select {
				case result := <-resultChan.ch:
					resultChan.CollectResult.CurrentTime = time.Now()
					if result.isLast {
						resultChan.CollectResult.IsLast = true
						logTrace(seqId, "collect result is last")
						return
					}
					resultChan.CollectResult.append(result)
				default:
					time.Sleep(100 * time.Millisecond)
				}
			}
		}(seqId, resultChan)
		logTrace(seqId, "collect result started")
	})

	return resultChan, nil
}

func stopResult(seqId int64) error {
	val, ok := resultChanMap.Load(seqId)
	if !ok || val == nil {
		logError(seqId, "result chan not found")
		return fmt.Errorf("result chan not found")
	}

	resultChan := val.(*ResultChan)
	if !resultChan.isInit {
		return fmt.Errorf("collect result not initialized")
	}

	resultChan.ch <- &Result{
		isLast: true,
	}
	resultChan.wg.Wait()
	logTrace(seqId, "collect result stopped")
	return nil
}

func getCollectResult(seqId int64) (*CollectResult, error) {
	val, ok := resultChanMap.Load(seqId)
	if !ok || val == nil {
		return nil, fmt.Errorf("result chan not found")
	}

	resultChan := val.(*ResultChan)
	if !resultChan.isInit {
		return nil, fmt.Errorf("collect result not initialized")
	}

	return resultChan.CollectResult, nil
}

// CollectResult aggregates and analyzes multiple request results
type CollectResult struct {
	ErrCode        int                     `json:"err_code"`         // Error code for the entire test
	ErrMsg         string                  `json:"err_msg"`          // Error message for the entire test
	ErrTotal       int64                   `json:"err_total"`        // Total number of failed requests
	AvgTotal       time.Duration           `json:"avg_total"`        // Sum of all request durations (scaled)
	Fastest        time.Duration           `json:"fastest"`          // Fastest request duration
	Slowest        time.Duration           `json:"slowest"`          // Slowest request duration
	Average        time.Duration           `json:"average"`          // Average request duration
	Rps            int64                   `json:"rps"`              // Requests per second (scaled)
	ErrorDist      map[string]int          `json:"error_dist"`       // Error message distribution
	StatusCodeDist map[int]int             `json:"status_code_dist"` // HTTP status code distribution
	Lats           map[time.Duration]int64 `json:"lats"`             // Latency distribution histogram
	LatsTotal      int64                   `json:"lats_total"`       // Total number of successful requests
	SizeTotal      int64                   `json:"size_total"`       // Total response size in bytes
	Duration       time.Duration           `json:"duration"`         // Total test duration
	Output         string                  `json:"output"`           // Output format (summary/csv/html)
	CurrentTime    time.Time               `json:"current_time"`     // Current time of the test
	IsLast         bool                    `json:"is_last"`          // Whether this is the last result
}

// NewCollectResult creates and initializes a new CollectResult
func NewCollectResult() *CollectResult {
	return &CollectResult{
		ErrorDist:      make(map[string]int),
		StatusCodeDist: make(map[int]int),
		Lats:           make(map[time.Duration]int64),
		Slowest:        time.Duration(IntMin),
		Fastest:        time.Duration(IntMax),
	}
}

// print outputs the benchmark results in the specified format
func (result *CollectResult) print() {
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
		fmt.Printf("%.4f,%d\n", duration.Seconds(), count)
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
		result.Duration.Seconds(),
		result.Slowest.Seconds(),
		result.Fastest.Seconds(),
		result.Average.Seconds(),
		float64(result.Rps),
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
		fmt.Printf("<tr><td>%.4f</td><td>%d</td></tr>\n", duration.Seconds(), count)
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
	fmt.Printf("  Total:\t%4.4f secs\n", result.Duration.Seconds())
	fmt.Printf("  Slowest:\t%4.4f secs\n", result.Slowest.Seconds())
	fmt.Printf("  Fastest:\t%4.4f secs\n", result.Fastest.Seconds())
	fmt.Printf("  Average:\t%4.4f secs\n", result.Average.Seconds())
	fmt.Printf("  Requests/sec:\t%4.2f\n", float64(result.Rps))
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
	sortedDurations := make([]time.Duration, 0, len(result.Lats))

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

		cumulativeCount += int64(result.Lats[duration])
		percentage := (cumulativeCount * 100) / result.LatsTotal

		for percentileIndex < len(percentiles) && int(percentage) >= percentiles[percentileIndex] {
			percentileData[percentileIndex] = float64(duration.Seconds())
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
	return json.Marshal(result)
}

func (result *CollectResult) String() string {
	data, _ := json.MarshalIndent(result, "", "  ")
	return string(data)
}

// append adds a single request result to the aggregate statistics
// This method is thread-safe and can be called concurrently
func (result *CollectResult) append(res *Result) {
	result.LatsTotal++
	// Handle failed requests
	if res.err != nil {
		result.ErrorDist[res.err.Error()]++
		result.ErrTotal++
		return
	}

	// Convert duration to scaled integer for histogram
	duration := time.Duration(res.duration.Milliseconds()) * time.Millisecond
	result.Lats[duration]++

	// Update aggregate statistics
	result.Slowest = time.Duration(max(result.Slowest.Milliseconds(),
		duration.Milliseconds())) * time.Millisecond
	result.Fastest = time.Duration(min(result.Fastest.Milliseconds(),
		duration.Milliseconds())) * time.Millisecond
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
	totalRequests := result.LatsTotal + result.ErrTotal
	if totalRequests == 0 {
		return false
	}

	errorRate := (result.ErrTotal * 100) / totalRequests
	return errorRate > circuitBreakerPercent
}

// mergeCollectResult aggregates multiple CollectResult instances into one
// This is used for combining results from distributed workers or multiple test runs
func mergeCollectResult(result *CollectResult, resultList ...*CollectResult) *CollectResult {
	if result == nil {
		result = NewCollectResult()
	}

	maxDuration := result.Duration

	// Preserve Output field from the first non-empty result
	if result.Output == "" {
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

		result.CurrentTime = v.CurrentTime
		// Update min/max latencies
		result.Slowest = time.Duration(max(result.Slowest.Milliseconds(),
			v.Slowest.Milliseconds())) * time.Millisecond
		result.Fastest = time.Duration(min(result.Fastest.Milliseconds(),
			v.Fastest.Milliseconds())) * time.Millisecond

		// Accumulate totals
		result.LatsTotal += v.LatsTotal
		result.ErrTotal += v.ErrTotal
		result.AvgTotal += v.AvgTotal
		result.SizeTotal += v.SizeTotal

		// Merge distribution maps
		for k, count := range v.StatusCodeDist {
			result.StatusCodeDist[k] += count
		}
		for k, count := range v.ErrorDist {
			result.ErrorDist[k] += count
		}
		for k, count := range v.Lats {
			result.Lats[k] += count
		}

		// Track maximum duration across all results
		maxDuration = time.Duration(max(maxDuration.Milliseconds(),
			v.Duration.Milliseconds())) * time.Millisecond
		result.IsLast = v.IsLast
	}

	logTrace(0, "maxDuration: %v", maxDuration)
	// Calculate derived metrics
	if maxDuration > 0 {
		result.Duration = maxDuration
		result.Rps = result.LatsTotal * 1000 / maxDuration.Milliseconds()
		logTrace(0, "Duration: %v, Rps: %v", result.Duration, result.Rps)
	}

	if result.LatsTotal > 0 {
		result.Average = time.Duration(result.AvgTotal.Milliseconds()/result.LatsTotal) * time.Millisecond
	}

	return result
}
