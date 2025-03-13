package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync"
)

const scaleNum = 10000

var pctls = []int{10, 25, 50, 75, 90, 95, 99}
var resultRdMutex sync.RWMutex

// StressResult record result
type StressResult struct {
	ErrCode  int    `json:"err_code"`
	ErrMsg   string `json:"err_msg"`
	AvgTotal int64  `json:"avg_total"`
	Fastest  int64  `json:"fastest"`
	Slowest  int64  `json:"slowest"`
	Average  int64  `json:"average"`
	Rps      int64  `json:"rps"`

	ErrorDist      map[string]int   `json:"error_dist"`
	StatusCodeDist map[int]int      `json:"status_code_dist"`
	Lats           map[string]int64 `json:"lats"`
	LatsTotal      int64            `json:"lats_total"`
	SizeTotal      int64            `json:"size_total"`
	Duration       int64            `json:"duration"`
	Output         string           `json:"output"`
}

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

func println(vfmt string, args ...interface{}) {
	fmt.Printf(vfmt+"\n", args...)
}

// GetStressResult creates and initializes a new StressResult
func GetStressResult() *StressResult {
	return &StressResult{
		ErrorDist:      make(map[string]int, 10),      // Pre-allocate with expected capacity
		StatusCodeDist: make(map[int]int, 5),          // Most APIs use few status codes
		Lats:           make(map[string]int64, 100),   // Pre-allocate for latency buckets
		Slowest:        int64(IntMin),
		Fastest:        int64(IntMax),
	}
}

func (result *StressResult) print() {
	resultRdMutex.RLock()
	defer resultRdMutex.RUnlock()

	switch result.Output {
	case "csv":
		println("Duration,Count")
		for duration, val := range result.Lats {
			println("%s,%d", duration, val/scaleNum)
		}
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
func (result *StressResult) printLatencies() {
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
func (result *StressResult) printStatusCodes() {
	println("\nStatus code distribution:")
	for code, num := range result.StatusCodeDist {
		println("  [%d]\t%d responses", code, num)
	}
}

// printErrors Print response errors
func (result *StressResult) printErrors() {
	println("\nError distribution:")
	for err, num := range result.ErrorDist {
		println("  [%d]\t%s", num, err)
	}
}

func (result *StressResult) marshal() ([]byte, error) {
	resultRdMutex.RLock()
	defer resultRdMutex.RUnlock()

	return json.Marshal(result)
}

// append adds a result to the StressResult with proper locking
func (result *StressResult) append(res *result) {
	resultRdMutex.Lock()
	defer resultRdMutex.Unlock()

	if res.err != nil {
		result.ErrorDist[res.err.Error()]++
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

// calculateMultiStressResult calculate multi stress result
func calculateMultiStressResult(result *StressResult, resultList ...StressResult) *StressResult {
	if result == nil {
		result = GetStressResult()
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
		for code, count := range v.StatusCodeDist {
			result.StatusCodeDist[code] += count
		}
		for errMsg, count := range v.ErrorDist {
			result.ErrorDist[errMsg] += count
		}
		for lats, count := range v.Lats {
			result.Lats[lats] += count
		}
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
