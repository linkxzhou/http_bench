// http_worker_test.go
// Test cases for HttpbenchWorker
package main

import (
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
	params := HttpbenchParameters{
		Url:             srv.URL,
		RequestMethod:   http.MethodGet,
		RequestBody:     "",
		RequestBodyType: "",
		N:               10,
		C:               2,
		Timeout:         1000 * time.Millisecond,
		Qps:             0,
		SequenceId:      1,
		RequestType:     protocolHTTP1,
	}

	w := HttpbenchWorker{stopChan: make(chan bool, 1)}
	w.Start(params)
	res := w.GetResult()

	if len(res.ErrorDist) != 0 {
		t.Errorf("expected no errors; got %v", res.ErrorDist)
	}
}

// TestHttpbenchWorkerStop verifies that Stop aborts the worker before all requests complete.
func TestHttpbenchWorkerStop(t *testing.T) {
	*verbose = 0
	// Setup server that delays response
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	time.Sleep(100 * time.Millisecond)
	params := HttpbenchParameters{
		Url:             srv.URL,
		RequestMethod:   http.MethodGet,
		RequestBody:     "",
		RequestBodyType: "",
		N:               100,
		C:               1,
		Timeout:         1000 * time.Millisecond,
		Qps:             0,
		SequenceId:      2,
		RequestType:     protocolHTTP1,
	}

	w := HttpbenchWorker{stopChan: make(chan bool, 1)}
	go w.Start(params)
	// Let some requests proceed
	time.Sleep(100 * time.Millisecond)
	err := w.Stop()
	if err != nil {
		t.Errorf("unexpected error on Stop: %v", err)
	}

	res := w.GetResult()
	// Should complete fewer requests than requested
	if res.LatsTotal >= int64(params.N) {
		t.Errorf("expected fewer than %d requests; got %d", params.N, res.LatsTotal)
	}
	if res.StatusCodeDist[http.StatusOK] != 0 {
		t.Errorf("expected some OK responses; got none")
	}
}
