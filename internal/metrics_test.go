/*
 * metrics_test.go — 结果采集/聚合测试（含 JSON tag 兼容性与基准）
 *
 *   TestJSONTags_F3 — JSON tag 属跨节点协议（dashboard/worker 依赖）不可变
 *   BenchmarkCollectResultRecord / Snapshot — 性能基准
 */

package bench

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestJSONTags_F3 verifies the F-3 field rename preserved the legacy JSON
// tags. The dashboard (index.html) and distributed worker API depend on these
// snake_case keys; renaming them would break cross-version compatibility.
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

	// Semantic JSON keys exposed by the metrics API.
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

func TestCollectResult_RecordSnapshotMerge(t *testing.T) {
	seq := GenSequenceId()
	NewResult(seq)
	if _, err := AppendResult(seq, &Result{StatusCode: 200, Duration: 10 * time.Millisecond, ContentLength: 12}); err != nil {
		t.Fatalf("append ok: %v", err)
	}
	if _, err := AppendResult(seq, &Result{StatusCode: 500, Duration: 20 * time.Millisecond, Err: errBoom{}}); err != nil && err.Error() != "circuit break" {
		// circuit break is acceptable under aggressive policy; otherwise require nil
		if err.Error() != "circuit break" {
			t.Fatalf("append fail sample: %v", err)
		}
	}
	if err := StopResult(seq); err != nil {
		t.Fatalf("StopResult: %v", err)
	}
	if err := SetStopReason(seq, "count"); err != nil {
		t.Fatalf("SetStopReason: %v", err)
	}
	got, err := GetCollectResult(seq)
	if err != nil || got == nil {
		t.Fatalf("GetCollectResult: %v %#v", err, got)
	}
	if got.TotalRequests < 1 {
		t.Fatalf("TotalRequests=%d", got.TotalRequests)
	}
	snap := got.Snapshot()
	if snap == nil || snap.TotalRequests != got.TotalRequests {
		t.Fatalf("snapshot %+v", snap)
	}
	other := NewCollectResult()
	other.TotalRequests = 3
	other.Duration = time.Second
	other.StatusCodeCounts = map[int]int{200: 3}
	merged := Merge(NewCollectResult(), got, other)
	if merged.TotalRequests < got.TotalRequests+3 {
		t.Fatalf("merge %+v", merged)
	}
	if ToByteSizeStr(1536) == "" {
		t.Fatal("ToByteSizeStr empty")
	}
	_ = got.String()
	if _, err := got.Marshal(); err != nil {
		t.Fatal(err)
	}
}

type errBoom struct{}

func (errBoom) Error() string { return "boom" }

func TestToByteSizeStr_Ranges(t *testing.T) {
	if got := ToByteSizeStr(500); got != "500.00 B" {
		t.Fatalf("500 -> %q", got)
	}
	if got := ToByteSizeStr(float64(KB)); got != "1.00 KB" {
		t.Fatalf("KB -> %q", got)
	}
	if got := ToByteSizeStr(float64(MB)); got != "1.00 MB" {
		t.Fatalf("MB -> %q", got)
	}
	if got := ToByteSizeStr(float64(GB)); got != "1.00 GB" {
		t.Fatalf("GB -> %q", got)
	}
}
