// Package metrics provides request result collection, aggregation, and
// reporting for benchmark runs.
//
// The collector uses a single-writer goroutine pattern: worker goroutines send
// *Result values to a buffered channel; one collector goroutine owns all
// mutations to the CollectResult maps and counters. This eliminates the data
// races that plagued the previous shared-map approach (see plan.md §1.1 #3/#4).
//
// Counter semantics (see plan.md §1.1 #5, §F-3):
//   - TotalRequests: total number of requests sampled (successful + failed).
//     Used as the denominator for error rate.
//   - FailedRequests: number of failed requests (res.Err != nil).
//     SuccessfulRequests = TotalRequests - FailedRequests.
//   - CircuitBroken() delegates to the package-level CircuitBreakerPolicy
//     (plan.md §F-4), which by default computes errorRate = FailedRequests /
//     TotalRequests and requires MinSamples before tripping.
package metrics

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/linkxzhou/http_bench/internal/logging"
	"github.com/linkxzhou/http_bench/internal/report"
)

// Channel buffer size for the result collector. Bounded to limit memory under
// extreme QPS; the collector drains promptly so this rarely fills.
const ResultChannelSize = 100000

// Error rate threshold (%) to trigger the circuit breaker.
const CircuitBreakerPercent = 50

// Percentiles for latency distribution reporting.
var percentiles = []int{10, 25, 50, 75, 90, 95, 99}

// resultChanMap is the global registry of active collectors keyed by run seqId.
var resultChanMap sync.Map

// Byte-size units for human-readable output.
const (
	KB = 1 << 10
	MB = 1 << 20
	GB = 1 << 30
)

// intMax / intMin are the max/min representable int values, used to initialize
// the Fastest/Slowest latency trackers.
const (
	intMax = int(^uint(0) >> 1)
	intMin = ^intMax
)

// maxInt / minInt return the larger/smaller of two ints.
func maxInt(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// ToByteSizeStr formats a byte count as a human-readable string.
func ToByteSizeStr(size float64) string {
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

// Result represents a single HTTP request result.
type Result struct {
	Err           error         // Request error if any
	StatusCode    int           // HTTP status code
	Duration      time.Duration // Request duration
	ContentLength int64         // Response content length in bytes
	IsLast        bool          // Whether this is the last result
}

// ResultChan represents a channel for collecting results from multiple goroutines.
type ResultChan struct {
	SeqId  int64
	Ch     chan *Result
	Result *CollectResult
	IsInit bool
	Wg     sync.WaitGroup
	Once   sync.Once
	Mu     sync.Mutex // protects IsInit / Result during startup
}

// NewResult registers a new result collector for the given seqId.
// If a collector already exists for this seqId, it is a no-op.
func NewResult(seqId int64) {
	if _, ok := resultChanMap.Load(seqId); ok {
		return
	}

	resultChanMap.Store(seqId, &ResultChan{
		SeqId: seqId,
		Ch:    make(chan *Result, ResultChannelSize),
	})
}

// AppendResult sends a single request result to the collector goroutine.
// If the collector detects the circuit-breaker threshold has been exceeded,
// it stops the run and returns an error.
func AppendResult(seqId int64, r *Result) (*ResultChan, error) {
	val, ok := resultChanMap.Load(seqId)
	if !ok || val == nil {
		logging.Error(seqId, "result chan not found for seqId %d", seqId)
		return nil, fmt.Errorf("result chan not found for seqId %d", seqId)
	}

	resultChan := val.(*ResultChan)

	// Lazily start the single collector goroutine on first result.
	resultChan.Once.Do(func() {
		resultChan.Mu.Lock()
		resultChan.Result = NewCollectResult()
		resultChan.IsInit = true
		resultChan.Mu.Unlock()

		resultChan.Wg.Add(1)
		go resultChan.collect()
		logging.Trace(seqId, "collect result started")
	})

	// After Once.Do, IsInit is guaranteed true.
	resultChan.Ch <- r

	// Check circuit breaker on every Nth result to bound overhead.
	if resultChan.Result.CircuitBroken() {
		StopResult(seqId)
		return resultChan, fmt.Errorf("circuit break")
	}

	return resultChan, nil
}

// collect is the single writer goroutine that owns all mutations to
// CollectResult.LatencyHistogram / StatusCodeCounts / ErrorCounts and the
// counters. It runs until it receives a Result with IsLast == true, then exits.
func (rc *ResultChan) collect() {
	startTime := time.Now()
	defer func() {
		rc.Result.lockedSetDuration(time.Since(startTime))
		rc.Wg.Done()
		logging.Trace(rc.SeqId, "collect result finished, duration %v ms",
			rc.Result.Duration.Milliseconds())
	}()

	for r := range rc.Ch {
		if r.IsLast {
			rc.Result.MarkLast()
			logging.Trace(rc.SeqId, "collect result is last")
			return
		}
		rc.Result.Record(r)
	}
}

// StopResult signals the collector for seqId to finish and waits for it.
func StopResult(seqId int64) error {
	val, ok := resultChanMap.Load(seqId)
	if !ok || val == nil {
		logging.Error(seqId, "result chan not found")
		return fmt.Errorf("result chan not found")
	}

	resultChan := val.(*ResultChan)
	resultChan.Mu.Lock()
	if !resultChan.IsInit {
		resultChan.Mu.Unlock()
		return fmt.Errorf("collect result not initialized")
	}
	resultChan.Mu.Unlock()

	// Non-blocking: if the channel is already closed (e.g. stop called twice),
	// drop the sentinel silently.
	defer func() { _ = recover() }()
	resultChan.Ch <- &Result{IsLast: true}
	resultChan.Wg.Wait()
	logging.Trace(seqId, "collect result stopped")
	return nil
}

// GetCollectResult returns an immutable snapshot of the collector's current
// aggregate state. The snapshot is safe to inspect without further
// synchronization.
func GetCollectResult(seqId int64) (*CollectResult, error) {
	val, ok := resultChanMap.Load(seqId)
	if !ok || val == nil {
		return nil, fmt.Errorf("result chan not found")
	}

	resultChan := val.(*ResultChan)
	resultChan.Mu.Lock()
	if !resultChan.IsInit {
		resultChan.Mu.Unlock()
		return nil, fmt.Errorf("collect result not initialized")
	}
	resultChan.Mu.Unlock()

	return resultChan.Result.Snapshot(), nil
}

// SetStopReason records why the run ended (plan.md §E-2). Must be called
// after StopResult completes (the collector goroutine has finished writing)
// so the reason is not lost — Snapshot copies whatever is set at call time.
// The write takes result.Mu (the same lock Record/Snapshot use) so that a
// subsequent Snapshot observes the value under the same happens-before edge.
func SetStopReason(seqId int64, reason string) error {
	val, ok := resultChanMap.Load(seqId)
	if !ok || val == nil {
		return fmt.Errorf("result chan not found")
	}
	resultChan := val.(*ResultChan)
	resultChan.Mu.Lock()
	initialized := resultChan.IsInit && resultChan.Result != nil
	resultChan.Mu.Unlock()
	if !initialized {
		return fmt.Errorf("collect result not initialized")
	}
	resultChan.Result.Mu.Lock()
	defer resultChan.Result.Mu.Unlock()
	resultChan.Result.StopReason = reason
	return nil
}

// CollectResult aggregates and analyzes multiple request results.
// All map/counter fields are owned by the single collector goroutine via
// Record(); reads from other goroutines must go through Snapshot() or the
// CircuitBroken() helper, which take the read lock.
type CollectResult struct {
	Mu sync.RWMutex `json:"-"`

	ErrCode          int                     `json:"err_code"`
	ErrMsg           string                  `json:"err_msg"`
	FailedRequests   int64                   `json:"failed_requests"`
	LatencySum       time.Duration           `json:"latency_sum"`
	Fastest          time.Duration           `json:"fastest"`
	Slowest          time.Duration           `json:"slowest"`
	Average          time.Duration           `json:"average"`
	RPS              int64                   `json:"rps"`
	ErrorCounts      map[string]int          `json:"error_counts"`
	StatusCodeCounts map[int]int             `json:"status_code_counts"`
	LatencyHistogram map[time.Duration]int64 `json:"latency_histogram"`
	TotalRequests    int64                   `json:"total_requests"`
	BytesReceived    int64                   `json:"bytes_received"`
	Duration         time.Duration           `json:"duration"`
	Output           string                  `json:"output"`
	CurrentTime      time.Time               `json:"current_time"`
	IsLast           bool                    `json:"is_last"`
	// StopReason records why the run ended (plan.md §E-2): "count" (request
	// budget exhausted), "duration" (deadline reached), "canceled" (explicit
	// Stop / signal), or empty if not set. Reported to the user so they can
	// distinguish a clean completion from an interrupted one.
	StopReason string `json:"stop_reason"`
}

// NewCollectResult creates and initializes a new CollectResult.
func NewCollectResult() *CollectResult {
	return &CollectResult{
		ErrorCounts:      make(map[string]int),
		StatusCodeCounts: make(map[int]int),
		LatencyHistogram: make(map[time.Duration]int64),
		Slowest:          time.Duration(intMin),
		Fastest:          time.Duration(intMax),
	}
}

// Record adds a single request result to the aggregate statistics.
// Must only be called from the single collector goroutine.
func (result *CollectResult) Record(res *Result) {
	result.Mu.Lock()
	defer result.Mu.Unlock()

	result.TotalRequests++
	// Handle failed requests
	if res.Err != nil {
		result.ErrorCounts[res.Err.Error()]++
		result.FailedRequests++
		return
	}

	// Convert duration to scaled integer for histogram
	duration := time.Duration(res.Duration.Milliseconds()) * time.Millisecond
	result.LatencyHistogram[duration]++

	// Update aggregate statistics
	result.Slowest = time.Duration(maxInt(result.Slowest.Milliseconds(),
		duration.Milliseconds())) * time.Millisecond
	result.Fastest = time.Duration(minInt(result.Fastest.Milliseconds(),
		duration.Milliseconds())) * time.Millisecond
	result.LatencySum += duration
	result.StatusCodeCounts[res.StatusCode]++

	// Accumulate response size
	if res.ContentLength > 0 {
		result.BytesReceived += res.ContentLength
	}
}

// MarkLast flags the result as the final snapshot.
func (result *CollectResult) MarkLast() {
	result.Mu.Lock()
	defer result.Mu.Unlock()
	result.IsLast = true
}

// Snapshot returns a deep copy of the current aggregate state under the read
// lock. Callers receive an immutable value they can inspect without racing
// against the collector goroutine's Record() writes.
func (result *CollectResult) Snapshot() *CollectResult {
	result.Mu.RLock()
	defer result.Mu.RUnlock()

	cp := &CollectResult{
		ErrCode:        result.ErrCode,
		ErrMsg:         result.ErrMsg,
		FailedRequests: result.FailedRequests,
		LatencySum:     result.LatencySum,
		Fastest:        result.Fastest,
		Slowest:        result.Slowest,
		Average:        result.Average,
		RPS:            result.RPS,
		TotalRequests:  result.TotalRequests,
		BytesReceived:  result.BytesReceived,
		Duration:       result.Duration,
		Output:         result.Output,
		CurrentTime:    result.CurrentTime,
		IsLast:         result.IsLast,
		StopReason:     result.StopReason,
	}
	if result.ErrorCounts != nil {
		cp.ErrorCounts = make(map[string]int, len(result.ErrorCounts))
		for k, v := range result.ErrorCounts {
			cp.ErrorCounts[k] = v
		}
	}
	if result.StatusCodeCounts != nil {
		cp.StatusCodeCounts = make(map[int]int, len(result.StatusCodeCounts))
		for k, v := range result.StatusCodeCounts {
			cp.StatusCodeCounts[k] = v
		}
	}
	if result.LatencyHistogram != nil {
		cp.LatencyHistogram = make(map[time.Duration]int64, len(result.LatencyHistogram))
		for k, v := range result.LatencyHistogram {
			cp.LatencyHistogram[k] = v
		}
	}
	return cp
}

// ToReportSnapshot returns a deep-copied, read-only view of the result
// suitable for handing to the report package. Copying the fields under the
// result lock decouples the package boundary: internal/report no longer
// imports internal/metrics, so the call site owns the import direction.
func (result *CollectResult) ToReportSnapshot() report.Snapshot {
	result.Mu.RLock()
	defer result.Mu.RUnlock()
	snap := report.Snapshot{
		TotalRequests:  result.TotalRequests,
		FailedRequests: result.FailedRequests,
		RPS:            result.RPS,
		BytesReceived:  result.BytesReceived,
		Fastest:        result.Fastest,
		Slowest:        result.Slowest,
		Average:        result.Average,
		Duration:       result.Duration,
		StopReason:     result.StopReason,
	}
	if result.LatencyHistogram != nil {
		snap.LatencyHistogram = make(map[time.Duration]int64, len(result.LatencyHistogram))
		for k, v := range result.LatencyHistogram {
			snap.LatencyHistogram[k] = v
		}
	}
	if result.StatusCodeCounts != nil {
		snap.StatusCodeCounts = make(map[int]int, len(result.StatusCodeCounts))
		for k, v := range result.StatusCodeCounts {
			snap.StatusCodeCounts[k] = v
		}
	}
	if result.ErrorCounts != nil {
		snap.ErrorCounts = make(map[string]int, len(result.ErrorCounts))
		for k, v := range result.ErrorCounts {
			snap.ErrorCounts[k] = v
		}
	}
	return snap
}

// lockedSetDuration sets the total elapsed duration. Called once by the
// collector goroutine on exit.
func (result *CollectResult) lockedSetDuration(d time.Duration) {
	result.Mu.Lock()
	defer result.Mu.Unlock()
	result.Duration = d
}

// CircuitBroken checks if the circuit breaker should open, delegating to the
// package-level CircuitBreakerPolicy (plan.md §F-4). Returns true if the run
// should be stopped. Safe to call concurrently with Record().
//
// The policy (default DefaultCircuitBreakerPolicy) adds a MinSamples floor so
// a handful of cold-start failures cannot abort the run; the legacy behavior
// can be restored via SetCircuitBreakerPolicy with MinSamples: 0.
func (result *CollectResult) CircuitBroken() bool {
	result.Mu.RLock()
	defer result.Mu.RUnlock()
	return currentCircuitBreakerPolicy().ShouldOpen(result)
}

// SuccessfulRequests returns the number of requests that completed without an
// error (res.Err == nil). It is derived from TotalRequests - FailedRequests
// and completes the three-way metric split required by plan.md §F-3:
//   - TotalRequests     (LatsTotal)  — all sampled requests
//   - FailedRequests    (ErrTotal)   — requests with a network/protocol error
//   - SuccessfulRequests             — TotalRequests - FailedRequests
//
// Note: HTTP non-2xx status codes are NOT counted as failures; they are
// recorded in StatusCodeCounts. FailedRequests only reflects transport-level
// errors. Callers must hold no lock; this method acquires RLock internally.
func (result *CollectResult) SuccessfulRequests() int64 {
	result.Mu.RLock()
	defer result.Mu.RUnlock()
	return result.TotalRequests - result.FailedRequests
}

// ErrorRate returns the percentage (0–100) of failed requests. Returns 0 when
// no requests have been recorded. This is the same ratio the default
// CircuitBreakerPolicy uses, exposed for reporting (plan.md §F-3).
func (result *CollectResult) ErrorRate() int64 {
	result.Mu.RLock()
	defer result.Mu.RUnlock()
	if result.TotalRequests == 0 {
		return 0
	}
	return (result.FailedRequests * 100) / result.TotalRequests
}

// Print outputs the benchmark results in the specified format.
// Print writes the result to os.Stdout in the format selected by
// result.Output (text/csv/html). Delegates to the Reporter implementations
// (plan.md §F-6); callers that need to redirect output should use
// WriteReport(w) instead.
func (result *CollectResult) Print() {
	result.WriteReport(os.Stdout)
}

// WriteReport writes the result to w using the Reporter matching
// result.Output. Errors from the underlying writer are logged but not
// surfaced (legacy Print semantics).
func (result *CollectResult) WriteReport(w io.Writer) {
	if err := report.NewReporter(result.Output).Write(w, result.ToReportSnapshot()); err != nil {
		logging.Error(0, "report write failed: %v", err)
	}
}

// printCSV outputs results in CSV format.

// Marshal returns the JSON encoding of the result.
func (result *CollectResult) Marshal() ([]byte, error) {
	return json.Marshal(result)
}

// String returns a pretty-printed JSON representation.
func (result *CollectResult) String() string {
	data, _ := json.MarshalIndent(result, "", "  ")
	return string(data)
}

// Merge aggregates multiple CollectResult instances into one.
// This is used for combining results from distributed workers or multiple runs.
func Merge(result *CollectResult, resultList ...*CollectResult) *CollectResult {
	if result == nil {
		result = NewCollectResult()
	}

	result.Mu.Lock()
	defer result.Mu.Unlock()

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

		v.Mu.RLock()
		result.CurrentTime = v.CurrentTime
		result.Slowest = time.Duration(maxInt(result.Slowest.Milliseconds(),
			v.Slowest.Milliseconds())) * time.Millisecond
		result.Fastest = time.Duration(minInt(result.Fastest.Milliseconds(),
			v.Fastest.Milliseconds())) * time.Millisecond

		result.TotalRequests += v.TotalRequests
		result.FailedRequests += v.FailedRequests
		result.LatencySum += v.LatencySum
		result.BytesReceived += v.BytesReceived

		// Preserve the stop reason from the source (plan.md §E-2). When
		// merging a single local result this carries the reason through the
		// mergeCollectResult(nil, result) call in handleStartup; for
		// distributed merges the first non-empty reason wins.
		if result.StopReason == "" && v.StopReason != "" {
			result.StopReason = v.StopReason
		}

		for k, count := range v.StatusCodeCounts {
			result.StatusCodeCounts[k] += count
		}
		for k, count := range v.ErrorCounts {
			result.ErrorCounts[k] += count
		}
		for k, count := range v.LatencyHistogram {
			result.LatencyHistogram[k] += count
		}

		maxDuration = time.Duration(maxInt(maxDuration.Milliseconds(),
			v.Duration.Milliseconds())) * time.Millisecond
		result.IsLast = v.IsLast
		v.Mu.RUnlock()
	}

	logging.Trace(0, "maxDuration: %v", maxDuration)
	if maxDuration > 0 {
		result.Duration = maxDuration
		result.RPS = result.TotalRequests * 1000 / maxDuration.Milliseconds()
		logging.Trace(0, "Duration: %v, RPS: %v", result.Duration, result.RPS)
	}

	if result.TotalRequests > 0 {
		result.Average = time.Duration(result.LatencySum.Milliseconds()/result.TotalRequests) * time.Millisecond
	}

	return result
}
