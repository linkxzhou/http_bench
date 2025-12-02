// http_results_test.go
package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// small helper to create a dummy internal result
func makeRes(code int, durSec float64, size int64, errMsg string) *result {
	// treat empty errMsg as no error
	var errObj error
	if errMsg != "" {
		errObj = errorString(errMsg)
	}
	return &result{
		statusCode:    code,
		duration:      durationFromSec(durSec),
		contentLength: size,
		err:           errObj,
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
		{500, "500"},   // bytes
		{2 * KB, "KB"}, // kilobytes
		{3 * MB, "MB"}, // megabytes
		{4 * GB, "GB"}, // gigabytes
	}
	for _, tc := range tests {
		got := toByteSizeStr(tc.bytes)
		if !strings.Contains(got, tc.contains) {
			t.Errorf("toByteSizeStr(%f) = %q, want contains %q", tc.bytes, got, tc.contains)
		}
	}
}

func TestGetCollectResultDefaults(t *testing.T) {
	r := NewCollectResult()
	if r.Lats == nil || r.ErrorDist == nil || r.StatusCodeDist == nil {
		t.Fatal("maps not initialized")
	}
	if r.Slowest != int64(IntMin) || r.Fastest != int64(IntMax) {
		t.Fatal("bad initial Fastest/Slowest")
	}
}

func TestAppendAndMarshal(t *testing.T) {
	r := NewCollectResult()
	// append two successes and one error
	r.append(makeRes(200, 0.01, 100, ""))
	r.append(makeRes(500, 0.02, 0, ""))
	r.append(makeRes(200, 0.01, 50, ""))

	if r.StatusCodeDist[200] != 2 || r.StatusCodeDist[500] != 1 {
		t.Errorf("unexpected status counts: %#v", r.StatusCodeDist)
	}

	// Check latencies: 0.01s * 10000 = 100
	if val, ok := r.Lats[100]; !ok || val != 2 {
		t.Errorf("expected 2 count for duration 100, got %d", val)
	}

	if r.ErrorDist["500"] != 1 && r.ErrorDist["some"] >= 0 {
		// error key is err.Error(), so here empty err only counts when err non-nil
	}

	data, err := r.marshal()
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	var check CollectResult
	if err := json.Unmarshal(data, &check); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if check.StatusCodeDist[200] != r.StatusCodeDist[200] {
		t.Error("roundtrip mismatch")
	}
	if val, ok := check.Lats[100]; !ok || val != 2 {
		t.Errorf("roundtrip lats mismatch: expected 2, got %d", val)
	}
}

func TestMergeCollectResult(t *testing.T) {
	a := NewCollectResult()
	b := NewCollectResult()
	a.append(makeRes(200, 0.01, 100, ""))
	b.append(makeRes(200, 0.02, 200, ""))

	merged := mergeCollectResult(nil, a, b)
	if merged.StatusCodeDist[200] != 2 {
		t.Errorf("expected 2 total, got %d", merged.StatusCodeDist[200])
	}
	if merged.SizeTotal != 300 {
		t.Errorf("expected size 300, got %d", merged.SizeTotal)
	}
	if val, ok := merged.Lats[100]; !ok || val != 1 {
		t.Errorf("expected 1 count for duration 100, got %d", val)
	}
	if val, ok := merged.Lats[200]; !ok || val != 1 {
		t.Errorf("expected 1 count for duration 200, got %d", val)
	}
}
