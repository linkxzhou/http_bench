package metrics

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// ------------------------------------------------------- JSON tag compat ---

// TestJSONTagCompat_F3 verifies the F-3 field rename preserved the legacy
// JSON tags (plan.md §F-3). The dashboard (index.html) and distributed worker
// API depend on these snake_case keys; renaming them would break cross-version
// compatibility.
func TestJSONTags_F3(t *testing.T) {
	r := NewCollectResult()
	r.TotalRequests = 5
	r.FailedRequests = 2
	r.BytesReceived = 1024
	r.LatencySum = 50 * time.Millisecond
	r.RPS = 100
	r.StatusCodeCounts = map[int]int{200: 3}
	r.ErrorCounts = map[string]int{"timeout": 2}
	r.LatencyHistogram = map[time.Duration]int64{10 * time.Millisecond: 3}
	r.StopReason = "count"

	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	s := string(data)

	// Semantic JSON keys exposed by the new metrics API.
	required := []string{
		`"total_requests":5`,
		`"failed_requests":2`,
		`"bytes_received":1024`,
		`"latency_sum"`,
		`"rps":100`,
		`"status_code_counts"`,
		`"error_counts"`,
		`"latency_histogram"`,
		`"stop_reason":"count"`,
	}
	for _, key := range required {
		if !strings.Contains(s, key) {
			t.Errorf("JSON missing legacy key %q in: %s", key, s)
		}
	}

	// Roundtrip: unmarshal back into a CollectResult and verify the renamed
	// Go fields are populated from the legacy tags.
	var back CollectResult
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if back.TotalRequests != 5 {
		t.Errorf("TotalRequests roundtrip = %d, want 5", back.TotalRequests)
	}
	if back.FailedRequests != 2 {
		t.Errorf("FailedRequests roundtrip = %d, want 2", back.FailedRequests)
	}
	if back.BytesReceived != 1024 {
		t.Errorf("BytesReceived roundtrip = %d, want 1024", back.BytesReceived)
	}
	if back.RPS != 100 {
		t.Errorf("RPS roundtrip = %d, want 100", back.RPS)
	}
	if back.StatusCodeCounts[200] != 3 {
		t.Errorf("StatusCodeCounts roundtrip lost: %#v", back.StatusCodeCounts)
	}
	if back.ErrorCounts["timeout"] != 2 {
		t.Errorf("ErrorCounts roundtrip lost: %#v", back.ErrorCounts)
	}
}

// --------------------------------------------------------------- Bench ---

func BenchmarkCollectResultRecord(b *testing.B) {
	result := NewCollectResult()
	sample := &Result{StatusCode: 200, Duration: 5 * time.Millisecond, ContentLength: 128}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result.Record(sample)
	}
}

func BenchmarkCollectResultSnapshot(b *testing.B) {
	result := NewCollectResult()
	result.StatusCodeCounts = map[int]int{200: 100, 500: 2}
	result.ErrorCounts = map[string]int{"timeout": 2}
	result.LatencyHistogram = map[time.Duration]int64{time.Millisecond: 100}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = result.Snapshot()
	}
}
