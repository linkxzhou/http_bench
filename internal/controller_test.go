package bench

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPostAllWorkers_NoAddrs(t *testing.T) {
	_, err := PostAllWorkers(nil, []byte(`{}`), time.Second)
	if err == nil || !strings.Contains(err.Error(), "no worker") {
		t.Fatalf("err=%v", err)
	}
}

func TestPostAllWorkers_AllFail(t *testing.T) {
	_, err := PostAllWorkers([]string{"http://127.0.0.1:1/"}, []byte(`{}`), 200*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "all") {
		t.Fatalf("err=%v", err)
	}
}

func TestPostWorker_HTTPErrorAndAPIKey(t *testing.T) {
	oldKey := APIKey
	APIKey = "tok"
	defer func() { APIKey = oldKey }()

	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		http.Error(w, "nope", http.StatusBadGateway)
	}))
	defer srv.Close()

	_, err := PostWorker(srv.URL, []byte(`{"c":1,"n":1}`), time.Second)
	if err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("err=%v", err)
	}
	if gotAuth != "Bearer tok" {
		t.Fatalf("Authorization=%q", gotAuth)
	}
}

func TestPostWorker_SuccessDecode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(&WorkerResponse{TotalRequests: 7, RPS: 3})
	}))
	defer srv.Close()
	res, err := PostWorker(srv.URL, []byte(`{}`), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalRequests != 7 || res.RPS != 3 {
		t.Fatalf("%+v", res)
	}
}

func TestMergeResults_Aggregates(t *testing.T) {
	a := &WorkerResponse{
		TotalRequests:    10,
		FailedRequests:   1,
		BytesReceived:    100,
		LatencySum:       100 * time.Millisecond,
		Duration:         2 * time.Second,
		Fastest:          5 * time.Millisecond,
		Slowest:          40 * time.Millisecond,
		StopReason:       "duration",
		StatusCodeCounts: map[int]int{200: 9},
		ErrorCounts:      map[string]int{"x": 1},
		LatencyHistogram: map[time.Duration]int64{5 * time.Millisecond: 9},
	}
	b := &WorkerResponse{
		TotalRequests:    5,
		FailedRequests:   0,
		BytesReceived:    50,
		LatencySum:       50 * time.Millisecond,
		Duration:         1 * time.Second,
		Fastest:          1 * time.Millisecond,
		Slowest:          80 * time.Millisecond,
		StatusCodeCounts: map[int]int{200: 5},
		ErrorCounts:      map[string]int{"x": 2},
		LatencyHistogram: map[time.Duration]int64{5 * time.Millisecond: 5},
	}
	out := mergeResults([]*WorkerResponse{a, b, nil})
	if out.TotalRequests != 15 || out.FailedRequests != 1 || out.BytesReceived != 150 {
		t.Fatalf("counters %+v", out)
	}
	if out.Fastest != time.Millisecond || out.Slowest != 80*time.Millisecond {
		t.Fatalf("latency bounds fastest=%v slowest=%v", out.Fastest, out.Slowest)
	}
	if out.Duration != 2*time.Second {
		t.Fatalf("Duration=%v", out.Duration)
	}
	if out.StatusCodeCounts[200] != 14 || out.ErrorCounts["x"] != 3 {
		t.Fatalf("maps %#v %#v", out.StatusCodeCounts, out.ErrorCounts)
	}
	if out.RPS <= 0 || out.Average <= 0 {
		t.Fatalf("RPS=%d Average=%v", out.RPS, out.Average)
	}
	single := mergeResults([]*WorkerResponse{a})
	if single != a {
		t.Fatal("single result should return same pointer")
	}
}

func TestPostAllWorkers_PartialSuccess(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(&WorkerResponse{TotalRequests: 3, Duration: time.Second})
	}))
	defer ok.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "fail", 500)
	}))
	defer bad.Close()

	res, err := PostAllWorkers([]string{ok.URL, bad.URL}, []byte(`{}`), time.Second)
	if err != nil {
		t.Fatalf("partial success should not hard-fail all: %v", err)
	}
	if res.Merged == nil || res.Merged.TotalRequests != 3 {
		t.Fatalf("merged=%+v", res.Merged)
	}
	if len(res.Workers) != 2 {
		t.Fatalf("workers=%d", len(res.Workers))
	}
	successes := 0
	for _, w := range res.Workers {
		if w.Success {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successes=%d details=%+v", successes, res.Workers)
	}
}
