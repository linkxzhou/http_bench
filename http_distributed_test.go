package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestPostDistributedWorker_Success verifies that postDistributedWorker can correctly parse and return results
func TestPostDistributedWorker_Success(t *testing.T) {
	// Set up a test server that returns fixed JSON
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		resp := CollectResult{ErrCode: 0, ErrMsg: "ok", Rps: 123}
		data, _ := json.Marshal(resp)
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Call the function
	res, err := postDistributedWorker(srv.URL, []byte(`{"foo":1}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ErrCode != 0 || res.ErrMsg != "ok" || res.Rps != 123 {
		t.Errorf("got %+v; want ErrCode=0, ErrMsg='ok', Rps=123", res)
	}
}

// TestPostDistributedWorker_InvalidJSON verifies that an error is returned when JSON parsing fails
func TestPostDistributedWorker_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`not a json`))
	}))
	defer srv.Close()

	_, err := postDistributedWorker(srv.URL, nil)
	if err == nil {
		t.Fatal("expected unmarshal error, got nil")
	}
}

// TestPostAllDistributedWorker_mergeCollectResult verifies that results from multiple workers can be merged
func TestPostAllDistributedWorker_mergeCollectResult(t *testing.T) {
	// Returns ErrCode=1, size=10
	mkSrv := func(code, size int64) *httptest.Server {
		mux := http.NewServeMux()
		mux.HandleFunc("/api", func(w http.ResponseWriter, r *http.Request) {
			resp := CollectResult{ErrCode: int(code), ErrMsg: "", SizeTotal: size}
			data, _ := json.Marshal(resp)
			w.Header().Set("Content-Type", "application/json")
			w.Write(data)
		})
		return httptest.NewServer(mux)
	}
	s1 := mkSrv(1, 10)
	defer s1.Close()
	s2 := mkSrv(2, 20)
	defer s2.Close()

	// Use complete URL so that postAllDistributedWorker will recognize and append "/api"
	addrs := flagSlice{s1.URL, s2.URL}
	merged, err := postAllDistributedWorkers(addrs, []byte(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Merge logic defaults to max(slowest), min(fastest), sum(size), etc.
	wantSize := int64(10 + 20)
	if merged.SizeTotal != wantSize {
		t.Errorf("merged.Rps = %d; want %d", merged.SizeTotal, wantSize)
	}
	// Minimum error code check is just for example
	if merged.ErrCode != 0 {
		t.Errorf("merged.ErrCode = %d; want 0 (should not inherit single point ErrCode during merge)", merged.ErrCode)
	}
}
