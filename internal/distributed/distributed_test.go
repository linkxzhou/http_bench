package distributed

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/linkxzhou/http_bench/internal/transport"
)

func TestServeRequest_RejectsOversized(t *testing.T) {
	request := strings.NewReader(strings.Repeat("x", 1<<20+1))
	req := httptest.NewRequest(http.MethodPost, "/api", request)
	recorder := httptest.NewRecorder()
	ServeRequest(stubWorkerService{}, nil, recorder, req)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	var response WorkerError
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if response.Code != "invalid_request" {
		t.Fatalf("error code = %q, want invalid_request", response.Code)
	}
}

func TestServeRequest_ValidatesScenario(t *testing.T) {
	req := transport.HttpbenchParameters{C: 1, N: 0, URL: "http://127.0.0.1:1"}
	body, _ := json.Marshal(req)
	recorder := httptest.NewRecorder()
	httpReq := httptest.NewRequest(http.MethodPost, "/api", strings.NewReader(string(body)))
	ServeRequest(stubWorkerService{}, DefaultValidator, recorder, httpReq)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (rejected because N=0 and Duration=0)", recorder.Code, http.StatusBadRequest)
	}
}

func TestWorkerService_UsesRequestContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := stubWorkerService{}.Execute(ctx, transport.HttpbenchParameters{C: 1, N: 1})
	if err == nil {
		t.Fatal("expected canceled context error")
	}
}

type stubWorkerService struct{}

func (stubWorkerService) Execute(ctx context.Context, _ WorkerRequest) (*WorkerResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &WorkerResponse{}, nil
}

func TestPostWorker_Success(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		resp := WorkerResponse{ErrCode: 0, RPS: 123}
		data, _ := json.Marshal(&resp)
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	res, err := PostWorker(srv.URL, []byte(`{"foo":1}`), 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.RPS != 123 {
		t.Errorf("RPS = %d, want 123", res.RPS)
	}
}

func TestPostAllWorkers_Merges(t *testing.T) {
	mk := func(result *WorkerResponse) *httptest.Server {
		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			data, _ := json.Marshal(&result)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(data)
		})
		return httptest.NewServer(mux)
	}
	s1 := mk(&WorkerResponse{BytesReceived: 10})
	defer s1.Close()
	s2 := mk(&WorkerResponse{BytesReceived: 20})
	defer s2.Close()
	res, err := PostAllWorkers([]string{s1.URL, s2.URL}, []byte(`{}`), 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Merged.BytesReceived != 30 {
		t.Errorf("merged.BytesReceived = %d, want 30", res.Merged.BytesReceived)
	}
}
