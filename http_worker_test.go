// http_worker_test.go
// Test cases for HttpbenchWorker
package main

import (
	"context"
	"github.com/linkxzhou/http_bench/internal/logging"
	"github.com/linkxzhou/http_bench/internal/transport"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestHttpbenchWorkerDo verifies that the worker performs N requests and aggregates results properly.
func TestHttpbenchWorkerDo(t *testing.T) {
	// Setup an echo server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	time.Sleep(100 * time.Millisecond)
	params := transport.HttpbenchParameters{
		URL:             srv.URL,
		RequestMethod:   http.MethodGet,
		RequestBody:     "",
		RequestBodyType: "",
		N:               10,
		C:               2,
		Timeout:         1000 * time.Millisecond,
		QPS:             0,
		SequenceId:      1,
		RequestType:     transport.ProtocolHTTP1,
		Insecure:        true,
	}

	w := HttpbenchWorker{seqId: 1}
	res, err := w.Run(context.Background(), params)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if len(res.ErrorCounts) != 0 {
		t.Errorf("expected no errors; got %v", res.ErrorCounts)
	}
}

// TestHttpbenchWorkerDurationPrecedence verifies that when both -n and -d are
// set, Duration takes precedence: the test runs for the full duration and the
// request count is treated as unlimited (remaining is nil). This matches
// ab -t, wrk -d, and hey -z semantics.
func TestHttpbenchWorkerDurationPrecedence(t *testing.T) {
	logging.SetLevel(logging.LevelError)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	time.Sleep(100 * time.Millisecond)
	params := transport.HttpbenchParameters{
		URL:           srv.URL,
		RequestMethod: http.MethodGet,
		N:             1000, // high count that would finish in <1s
		C:             2,
		Duration:      2 * time.Second,
		Timeout:       1000 * time.Millisecond,
		SequenceId:    3,
		RequestType:   transport.ProtocolHTTP1,
		Insecure:      true,
	}

	w := HttpbenchWorker{seqId: 3}
	start := time.Now()
	res, err := w.Run(context.Background(), params)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	// Should run for the full duration, not stop after 1000 requests
	if elapsed < 1500*time.Millisecond {
		t.Errorf("expected >=1.5s elapsed; got %v (duration precedence not working)", elapsed)
	}
	if res.StopReason != "duration" {
		t.Errorf("expected stopReason=duration; got %q", res.StopReason)
	}
	// Should have sent more than N requests during the duration
	if res.TotalRequests <= int64(params.N) {
		t.Errorf("expected >%d requests with duration precedence; got %d",
			params.N, res.TotalRequests)
	}
}
func TestHttpbenchWorkerStop(t *testing.T) {
	logging.SetLevel(0)
	// Setup server that delays response
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(10 * time.Millisecond)
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	time.Sleep(100 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	params := transport.HttpbenchParameters{
		URL:             srv.URL,
		RequestMethod:   http.MethodGet,
		RequestBody:     "",
		RequestBodyType: "",
		N:               100,
		C:               1,
		Timeout:         1000 * time.Millisecond,
		QPS:             0,
		SequenceId:      2,
		RequestType:     transport.ProtocolHTTP1,
		Insecure:        true,
	}

	w := HttpbenchWorker{seqId: 2}
	done := make(chan struct{})
	go func() {
		w.Run(ctx, params)
		close(done)
	}()
	// Let some requests proceed
	time.Sleep(1 * time.Second)
	cancel()
	<-done

	res := w.GetResult()
	if res == nil {
		t.Fatalf("GetResult returned nil")
	}
	// Should complete fewer requests than requested
	if res.TotalRequests >= int64(params.N) {
		t.Errorf("expected fewer than %d requests; got %d", params.N, res.TotalRequests)
	}
}
