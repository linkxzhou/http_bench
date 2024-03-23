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

func toByteSizeStr(size float64) string {
	switch {
	case size > 1073741824:
		return fmt.Sprintf("%4.3f GB", size/1073741824)
	case size > 1048576:
		return fmt.Sprintf("%4.3f MB", size/1048576)
	case size > 1024:
		return fmt.Sprintf("%4.3f KB", size/1024)
	}
	return fmt.Sprintf("%4.3f bytes", size)
}

func println(f string, args ...interface{}) {
	fmt.Printf(f+"\n", args...)
}

func GetStressResult() *StressResult {
	return &StressResult{
		ErrorDist:      make(map[string]int, 0),
		StatusCodeDist: make(map[int]int, 0),
		Lats:           make(map[string]int64, 0),
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
	data := make([]string, len(pctls))
	durationLats := make([]string, 0)
	for duration := range result.Lats {
		durationLats = append(durationLats, duration)
	}

	sort.Strings(durationLats)

	for i, j, dCounts := 0, 0, int64(0); i < len(durationLats) && j < len(pctls); i = i + 1 {
		dCounts = dCounts + result.Lats[durationLats[i]]
		if int(dCounts*100/result.LatsTotal) >= pctls[j] {
			data[j] = durationLats[i]
			j++
		}
	}

	println("\nLatency distribution:")
	for i := 0; i < len(pctls); i++ {
		fmt.Printf("  %v%% in %s secs\n", pctls[i], data[i])
	}
}

// printStatusCodes Print status code distribution.
func (result *StressResult) printStatusCodes() {
	println("\nStatus code distribution:")
	for code, num := range result.StatusCodeDist {
		fmt.Printf("  [%d]\t%d responses\n", code, num)
	}
}

// printErrors Print response errors
func (result *StressResult) printErrors() {
	println("\nError distribution:")
	for err, num := range result.ErrorDist {
		fmt.Printf("  [%d]\t%s", num, err)
	}
}

func (result *StressResult) marshal() ([]byte, error) {
	resultRdMutex.RLock()
	defer resultRdMutex.RUnlock()

	return json.Marshal(result)
}

func (result *StressResult) append(res *result) {
	resultRdMutex.Lock()
	defer resultRdMutex.Unlock()

	if res.err != nil {
		result.ErrorDist[res.err.Error()]++
	} else {
		result.Lats[fmt.Sprintf("%4.3f", res.duration.Seconds())]++
		duration := int64(res.duration.Seconds() * scaleNum)
		result.LatsTotal++
		if result.Slowest < duration {
			result.Slowest = duration
		}
		if result.Fastest > duration {
			result.Fastest = duration
		}
		result.AvgTotal += duration
		result.StatusCodeDist[res.statusCode]++
		if res.contentLength > 0 {
			result.SizeTotal += res.contentLength
		}
	}
}

// calMutliStressResult calculate mutli stress result
func calMutliStressResult(result *StressResult, resultList ...StressResult) *StressResult {
	if result == nil {
		result = GetStressResult()
	}

	var duration int64 = result.Duration

	for _, v := range resultList {
		if result.Slowest < v.Slowest {
			result.Slowest = v.Slowest
		}
		if result.Fastest > v.Fastest {
			result.Fastest = v.Fastest
		}
		result.LatsTotal += v.LatsTotal
		result.AvgTotal += v.AvgTotal
		for code, c := range v.StatusCodeDist {
			result.StatusCodeDist[code] += c
		}
		result.SizeTotal += v.SizeTotal
		for code, c := range v.ErrorDist {
			result.ErrorDist[code] += c
		}
		for lats, c := range v.Lats {
			result.Lats[lats] += c
		}

		if duration < v.Duration {
			duration = v.Duration
		}
	}

	if duration > 0 {
		result.Duration = duration
		result.Rps = int64((result.LatsTotal * scaleNum) / duration)
	}

	if result.LatsTotal > 0 {
		result.Average = result.AvgTotal / result.LatsTotal
	}

	return result
}
