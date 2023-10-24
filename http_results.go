package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync"
)

const (
	scaleNum = 10000
)

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

func (result *StressResult) print() {
	resultRdMutex.RLock()
	defer resultRdMutex.RUnlock()
	switch result.Output {
	case "csv":
		fmt.Printf("Duration,Count\n")
		for duration, val := range result.Lats {
			fmt.Printf("%s,%d", duration, val/scaleNum)
		}
		return
	default:
		// pass
	}
	if len(result.Lats) > 0 {
		fmt.Printf("Summary:\n")
		fmt.Printf("  Total:\t%4.3f secs\n", float32(result.Duration)/scaleNum)
		fmt.Printf("  Slowest:\t%4.3f secs\n", float32(result.Slowest)/scaleNum)
		fmt.Printf("  Fastest:\t%4.3f secs\n", float32(result.Fastest)/scaleNum)
		fmt.Printf("  Average:\t%4.3f secs\n", float32(result.Average)/scaleNum)
		fmt.Printf("  Requests/sec:\t%4.3f\n", float32(result.Rps)/scaleNum)
		if result.SizeTotal > 1073741824 {
			fmt.Printf("  Total data:\t%4.3f GB\n", float64(result.SizeTotal)/1073741824)
		} else if result.SizeTotal > 1048576 {
			fmt.Printf("  Total data:\t%4.3f MB\n", float64(result.SizeTotal)/1048576)
		} else if result.SizeTotal > 1024 {
			fmt.Printf("  Total data:\t%4.3f KB\n", float64(result.SizeTotal)/1024)
		} else if result.SizeTotal > 0 {
			fmt.Printf("  Total data:\t%4.3f bytes\n", float64(result.SizeTotal))
		}
		fmt.Printf("  Size/request:\t%d bytes\n", result.SizeTotal/result.LatsTotal)
		result.printStatusCodes()
		result.printLatencies()
	}
	if len(result.ErrorDist) > 0 {
		result.printErrors()
	}
}

// Print latency distribution.
func (result *StressResult) printLatencies() {
	pctls := []int{10, 25, 50, 75, 90, 95, 99}
	data := make([]string, len(pctls))
	durationLats := make([]string, 0)
	for duration := range result.Lats {
		durationLats = append(durationLats, duration)
	}
	sort.Strings(durationLats)
	var j int = 0
	var current int64 = 0
	for i := 0; i < len(durationLats) && j < len(pctls); i++ {
		current = current + result.Lats[durationLats[i]]
		if int(current*100/result.LatsTotal) >= pctls[j] {
			data[j] = durationLats[i]
			j++
		}
	}
	fmt.Printf("\nLatency distribution:\n")
	for i := 0; i < len(pctls); i++ {
		fmt.Printf("  %v%% in %s secs\n", pctls[i], data[i])
	}
}

// Print status code distribution.
func (result *StressResult) printStatusCodes() {
	fmt.Printf("\nStatus code distribution:\n")
	for code, num := range result.StatusCodeDist {
		fmt.Printf("  [%d]\t%d responses\n", code, num)
	}
}

func (result *StressResult) printErrors() {
	fmt.Printf("\nError distribution:\n")
	for err, num := range result.ErrorDist {
		fmt.Printf("  [%d]\t%s", num, err)
	}
}

func (result *StressResult) marshal() ([]byte, error) {
	resultRdMutex.RLock()
	defer resultRdMutex.RUnlock()

	return json.Marshal(result)
}

func (result *StressResult) result(res *result) {
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

func (result *StressResult) combine(resultList ...StressResult) {
	resultRdMutex.RLock()
	defer resultRdMutex.RUnlock()

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
	}

	if result.Duration > 0 {
		result.Rps = int64((result.LatsTotal * scaleNum * scaleNum) / result.Duration)
	}

	if result.LatsTotal > 0 {
		result.Average = result.AvgTotal / result.LatsTotal
	}
}
