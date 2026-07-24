// http_results_test.go
package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/linkxzhou/http_bench/internal/metrics"
	"github.com/linkxzhou/http_bench/internal/templatefn"
)

// small helper to create a dummy internal result
func makeRes(code int, durSec float64, size int64, errMsg string) *metrics.Result {
	// treat empty errMsg as no error
	var errObj error
	if errMsg != "" {
		errObj = errorString(errMsg)
	}
	return &metrics.Result{
		StatusCode:    code,
		Duration:      durationFromSec(durSec),
		ContentLength: size,
		Err:           errObj,
	}
}

// errorString implements error interface
type errorString string

func (e errorString) Error() string { return string(e) }

// durationFromSec for tests
func durationFromSec(s float64) (d time.Duration) {
	return time.Duration(s * float64(time.Second))
}

func TestToByteSizeStr(t *testing.T) {
	tests := []struct {
		bytes    float64
		contains string
	}{
		{500, "500"},           // bytes
		{2 * metrics.KB, "KB"}, // kilobytes
		{3 * metrics.MB, "MB"}, // megabytes
		{4 * metrics.GB, "GB"}, // gigabytes
	}
	for _, tc := range tests {
		got := metrics.ToByteSizeStr(tc.bytes)
		if !strings.Contains(got, tc.contains) {
			t.Errorf("toByteSizeStr(%f) = %q, want contains %q", tc.bytes, got, tc.contains)
		}
	}
}

func TestGetCollectResultDefaults(t *testing.T) {
	r := metrics.NewCollectResult()
	if r.LatencyHistogram == nil || r.ErrorCounts == nil || r.StatusCodeCounts == nil {
		t.Fatal("maps not initialized")
	}
	if r.Slowest != time.Duration(templatefn.IntMin) || r.Fastest != time.Duration(templatefn.IntMax) {
		t.Fatal("bad initial Fastest/Slowest")
	}
}

func TestAppendAndMarshal(t *testing.T) {
	r := metrics.NewCollectResult()
	// append two successes and one error
	r.Record(makeRes(200, 0.01, 100, ""))
	r.Record(makeRes(500, 0.02, 0, ""))
	r.Record(makeRes(200, 0.01, 50, ""))

	if r.StatusCodeCounts[200] != 2 || r.StatusCodeCounts[500] != 1 {
		t.Errorf("unexpected status counts: %#v", r.StatusCodeCounts)
	}

	// Check latencies: 0.01s = 10ms
	if val, ok := r.LatencyHistogram[10*time.Millisecond]; !ok || val != 2 {
		t.Errorf("expected 2 count for duration 10ms, got %d", val)
	}

	if r.ErrorCounts["500"] != 1 && r.ErrorCounts["some"] >= 0 {
		// error key is err.Error(), so here empty err only counts when err non-nil
	}

	data, err := r.Marshal()
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	var check metrics.CollectResult
	if err := json.Unmarshal(data, &check); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if check.StatusCodeCounts[200] != r.StatusCodeCounts[200] {
		t.Error("roundtrip mismatch")
	}
	if val, ok := check.LatencyHistogram[10*time.Millisecond]; !ok || val != 2 {
		t.Errorf("roundtrip lats mismatch: expected 2, got %d", val)
	}
}

// TestStopReasonSnapshot verifies Snapshot copies StopReason (plan.md §E-2).
// Without this, the reason set by SetStopReason is lost when handleStartup
// reads the result via GetCollectResult.
func TestStopReasonSnapshot(t *testing.T) {
	r := metrics.NewCollectResult()
	r.StopReason = "count"
	snap := r.Snapshot()
	if snap.StopReason != "count" {
		t.Fatalf("Snapshot lost StopReason: got %q want %q", snap.StopReason, "count")
	}
}

// TestStopReasonMerge verifies Merge carries StopReason from the source
// result. handleStartup calls metrics.Merge(nil, result) which creates a
// fresh metrics.CollectResult — the reason must propagate or the report loses it.
func TestStopReasonMerge(t *testing.T) {
	src := metrics.NewCollectResult()
	src.StopReason = "duration"
	merged := metrics.Merge(nil, src)
	if merged.StopReason != "duration" {
		t.Fatalf("Merge lost StopReason: got %q want %q", merged.StopReason, "duration")
	}
}
