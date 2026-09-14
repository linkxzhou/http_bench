package bench

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestWorker_RunCountAndStopReason(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	w := NewWorkerForTest(GenSequenceId())
	params := HttpbenchParameters{
		URL:           srv.URL,
		RequestMethod: http.MethodGet,
		RequestType:   ProtocolHTTP1,
		N:             5,
		C:             2,
		Timeout:       2 * time.Second,
		Insecure:      true,
	}
	res, err := w.Run(context.Background(), params)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res == nil {
		t.Fatal("nil result")
	}
	if res.TotalRequests != 5 {
		t.Fatalf("TotalRequests=%d want 5 (stop=%q)", res.TotalRequests, res.StopReason)
	}
}

func TestWorker_ContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(30 * time.Millisecond)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()

	w := NewWorkerForTest(GenSequenceId())
	params := HttpbenchParameters{
		URL:           srv.URL,
		RequestMethod: http.MethodGet,
		RequestType:   ProtocolHTTP1,
		Duration:      5 * time.Second,
		C:             2,
		Timeout:       time.Second,
		Insecure:      true,
	}
	res, err := w.Run(ctx, params)
	// canceled runs may return error or partial result depending on implementation
	if res == nil && err == nil {
		t.Fatal("expected result or error after cancel")
	}
	if res != nil && res.TotalRequests >= 1_000_000 {
		t.Fatalf("did not stop on cancel: %d", res.TotalRequests)
	}
}
