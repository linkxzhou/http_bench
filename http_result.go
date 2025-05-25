package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"
)

const scaleNum = 10000

var pctls = []int{10, 25, 50, 75, 90, 95, 99}
var resultRdMutex sync.RWMutex

// CollectResult record result
type (
	result struct {
		err           error
		statusCode    int
		duration      time.Duration
		contentLength int64
	}

	CollectResult struct {
		ErrCode        int              `json:"err_code"`
		ErrMsg         string           `json:"err_msg"`
		ErrTotal       int64            `json:"err_total"`
		AvgTotal       int64            `json:"avg_total"`
		Fastest        int64            `json:"fastest"`
		Slowest        int64            `json:"slowest"`
		Average        int64            `json:"average"`
		Rps            int64            `json:"rps"`
		ErrorDist      map[string]int   `json:"error_dist"`
		StatusCodeDist map[int]int      `json:"status_code_dist"`
		Lats           map[string]int64 `json:"lats"`
		LatsTotal      int64            `json:"lats_total"`
		SizeTotal      int64            `json:"size_total"`
		Duration       int64            `json:"duration"`
		Output         string           `json:"output"`
		mutex          sync.RWMutex     `json:"-"`
	}
)

const (
	GB = 1 << 30
	MB = 1 << 20
	KB = 1 << 10
)

// Pre-allocate common string formats to avoid repeated allocations
var (
	durationFormat = "%4.3f"
	bytesFormat    = "%4.3f bytes"
	kbFormat       = "%4.3f KB"
	mbFormat       = "%4.3f MB"
	gbFormat       = "%4.3f GB"
)

// toByteSizeStr converts bytes to human readable string
func toByteSizeStr(size float64) string {
	switch {
	case size >= GB:
		return fmt.Sprintf(gbFormat, size/GB)
	case size >= MB:
		return fmt.Sprintf(mbFormat, size/MB)
	case size >= KB:
		return fmt.Sprintf(kbFormat, size/KB)
	default:
		return fmt.Sprintf(bytesFormat, size)
	}
}

// NewCollectResult creates and initializes a new CollectResult
func NewCollectResult() *CollectResult {
	return &CollectResult{
		ErrorDist:      make(map[string]int, 10),    // Pre-allocate with expected capacity
		StatusCodeDist: make(map[int]int, 5),        // Most APIs use few status codes
		Lats:           make(map[string]int64, 100), // Pre-allocate for latency buckets
		Slowest:        int64(IntMin),
		Fastest:        int64(IntMax),
	}
}

func (result *CollectResult) print() {
	result.mutex.RLock()
	defer result.mutex.RUnlock()

	switch result.Output {
	case "csv":
		println("Duration,Count")
		for duration, val := range result.Lats {
			println("%s,%d", duration, val/scaleNum)
		}
		return
	case "html":
		println("<html><head><meta charset=\"UTF-8\"><title>Benchmark Result</title></head><body>")
		println("<h1>Benchmark Summary</h1>")
		println("<p>Total: %.3f secs<br>Slowest: %.3f secs<br>Fastest: %.3f secs<br>Average: %.3f secs<br>Requests/sec: %.3f<br>Total Data: %s<br>Size/request: %d bytes</p>\n",
			float32(result.Duration),
			float32(result.Slowest)/scaleNum,
			float32(result.Fastest)/scaleNum,
			float32(result.Average)/scaleNum,
			float32(result.Rps)/scaleNum,
			toByteSizeStr(float64(result.SizeTotal)),
			result.SizeTotal/result.LatsTotal)
		// Status codes
		println("<h2>Status Codes</h2><table border=\"1\"><tr><th>Code</th><th>Count</th></tr>")
		for code, cnt := range result.StatusCodeDist {
			println("<tr><td>%d</td><td>%d</td></tr>\n", code, cnt)
		}
		println("</table>")
		// Latency distribution
		println("<h2>Latency Distribution</h2><table border=\"1\"><tr><th>Duration</th><th>Count</th></tr>")
		for duration, val := range result.Lats {
			fmt.Printf("<tr><td>%s</td><td>%d</td></tr>\n", duration, val/scaleNum)
		}
		println("</table>")
		// Errors
		if len(result.ErrorDist) > 0 {
			println("<h2>Errors</h2><table border=\"1\"><tr><th>Error</th><th>Count</th></tr>")
			for errMsg, cnt := range result.ErrorDist {
				println("<tr><td>%s</td><td>%d</td></tr>\n", errMsg, cnt)
			}
			println("</table>")
		}
		println("</body></html>")
		return
	}

	if len(result.Lats) > 0 {
		println("Summary:")
		println("  Total:\t%4.3f secs", float32(result.Duration))
		println("  Slowest:\t%4.3f secs", float32(result.Slowest)/scaleNum)
		println("  Fastest:\t%4.3f secs", float32(result.Fastest)/scaleNum)
		println("  Average:\t%4.3f secs", float32(result.Average)/scaleNum)
		println("  Requests/sec:\t%4.3f", float32(result.Rps)/scaleNum)
		println("  Total data:\t%s", toByteSizeStr(float64(result.SizeTotal)))
		println("  Size/request:\t%d bytes", result.SizeTotal/result.LatsTotal)
		result.printStatusCodes()
		result.printLatencies()
	}

	if len(result.ErrorDist) > 0 {
		result.printErrors()
	}
}

// printLatencies Print latency distribution.
func (result *CollectResult) printLatencies() {
	result.mutex.RLock()
	defer result.mutex.RUnlock()

	pctlData := make([]string, len(pctls))
	durationLats := make([]string, 0, len(result.Lats)) // Pre-allocate capacity

	for duration := range result.Lats {
		durationLats = append(durationLats, duration)
	}
	sort.Strings(durationLats)

	var dCounts int64
	for i, j := 0, 0; i < len(durationLats) && j < len(pctls); i++ {
		dCounts += result.Lats[durationLats[i]]
		if percentage := dCounts * 100 / result.LatsTotal; int(percentage) >= pctls[j] {
			pctlData[j] = durationLats[i]
			j++
		}
	}

	println("\nLatency distribution:")
	for i, pctl := range pctls {
		println("  %v%% in %s secs", pctl, pctlData[i])
	}
}

// printStatusCodes Print status code distribution.
func (result *CollectResult) printStatusCodes() {
	result.mutex.RLock()
	defer result.mutex.RUnlock()

	println("\nStatus code distribution:")
	for code, num := range result.StatusCodeDist {
		println("  [%d]\t%d responses", code, num)
	}
}

// printErrors Print response errors
func (result *CollectResult) printErrors() {
	result.mutex.RLock()
	defer result.mutex.RUnlock()

	println("\nError distribution:")
	for err, num := range result.ErrorDist {
		println("  [%d times] %s", num, err)
	}
}

func (result *CollectResult) marshal() ([]byte, error) {
	result.mutex.RLock()
	defer result.mutex.RUnlock()

	return json.Marshal(result)
}

// append adds a result to the CollectResult with proper locking
func (result *CollectResult) append(res *result) {
	result.mutex.Lock()
	defer result.mutex.Unlock()

	if res.err != nil {
		result.ErrorDist[res.err.Error()]++
		result.ErrTotal++
		return
	}

	// Format duration once and reuse
	durationStr := fmt.Sprintf(durationFormat, res.duration.Seconds())
	result.Lats[durationStr]++

	duration := int64(res.duration.Seconds() * scaleNum)
	result.LatsTotal++
	result.Slowest = max(result.Slowest, duration)
	result.Fastest = min(result.Fastest, duration)
	result.AvgTotal += duration
	result.StatusCodeDist[res.statusCode]++
	if res.contentLength > 0 {
		result.SizeTotal += res.contentLength
	}
}

func (result *CollectResult) isCircuitBreak() bool {
	if result.LatsTotal > 0 {
		return result.ErrTotal*100/result.LatsTotal > 50
	}
	return false
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

// mergeStrInt64Map merges counts from src into dest for map[string]int64
func mergeStrInt64Map(dest, src map[string]int64) {
	for k, v := range src {
		dest[k] += v
	}
}

// mergeCollectResult aggregates multiple mergeCollectResult into one
func mergeCollectResult(result *CollectResult, resultList ...*CollectResult) *CollectResult {
	resultRdMutex.Lock()
	defer resultRdMutex.Unlock()

	if result == nil {
		result = NewCollectResult()
	}

	duration := result.Duration

	// Use more efficient way to merge results
	for _, v := range resultList {
		result.Slowest = max(result.Slowest, v.Slowest)
		result.Fastest = min(result.Fastest, v.Fastest)
		result.LatsTotal += v.LatsTotal
		result.AvgTotal += v.AvgTotal
		result.SizeTotal += v.SizeTotal

		// Merge maps
		mergeIntMap(result.StatusCodeDist, v.StatusCodeDist)
		mergeStrIntMap(result.ErrorDist, v.ErrorDist)
		mergeStrInt64Map(result.Lats, v.Lats)
		duration = max(duration, v.Duration)
	}

	if duration > 0 {
		result.Duration = duration
		result.Rps = (result.LatsTotal * scaleNum) / duration
	}

	if result.LatsTotal > 0 {
		result.Average = result.AvgTotal / result.LatsTotal
	}

	return result
}
