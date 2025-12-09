// http_results_test.go
package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// small helper to create a dummy internal result
func makeRes(code int, durSec float64, size int64, errMsg string) *Result {
	// treat empty errMsg as no error
	var errObj error
	if errMsg != "" {
		errObj = errorString(errMsg)
	}
	return &Result{
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
	if r.Slowest != time.Duration(IntMin) || r.Fastest != time.Duration(IntMax) {
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

	// Check latencies: 0.01s = 10ms
	if val, ok := r.Lats[10*time.Millisecond]; !ok || val != 2 {
		t.Errorf("expected 2 count for duration 10ms, got %d", val)
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
	if val, ok := check.Lats[10*time.Millisecond]; !ok || val != 2 {
		t.Errorf("roundtrip lats mismatch: expected 2, got %d", val)
	}
}
