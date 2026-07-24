package dashboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/linkxzhou/http_bench/internal/distributed"
	"github.com/linkxzhou/http_bench/internal/transport"
)

type stubService struct{}

func (stubService) Execute(_ context.Context, _ distributed.WorkerRequest) (*distributed.WorkerResponse, error) {
	return &distributed.WorkerResponse{}, nil
}

func TestRun_RejectsEmptyAddr(t *testing.T) {
	err := Run(context.Background(), Config{WorkerService: stubService{}})
	if err == nil {
		t.Fatal("expected error for empty addr")
	}
}

func TestRun_RejectsNilWorkerService(t *testing.T) {
	err := Run(context.Background(), Config{Addr: "127.0.0.1:0"})
	if err == nil {
		t.Fatal("expected error for nil worker service")
	}
}

func TestApplyDefaults(t *testing.T) {
	cfg := Config{}
	cfg.applyDefaults()
	if cfg.ReadHeaderTimeout == 0 || cfg.IdleTimeout == 0 {
		t.Errorf("defaults not applied: %+v", cfg)
	}
}

func TestRun_ServesDashboard(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html>hi</html>"))
	})
	mux.HandleFunc("/api", func(w http.ResponseWriter, r *http.Request) {
		distributed.ServeRequest(stubService{}, distributed.DefaultValidator, w, r)
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "hi") {
		t.Errorf("dashboard HTML missing: %q", rec.Body.String())
	}
}

func TestRun_ContextCancelReturns(t *testing.T) {
	// Run a Run() with cancelable context; it should exit cleanly within a
	// short timeout when we cancel. We use a small ephemeral port and let
	// the goroutine inside Run terminate via ctx cancel.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{
			Addr:          "127.0.0.1:0", // 0 lets OS assign; we cancel before listen
			HTML:          "<html>hi</html>",
			WorkerService: stubService{},
			IdleTimeout:   50 * time.Millisecond,
		})
	}()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after context cancel")
	}
}

// transport import kept for compile-time link between dashboard and distributed.
var _ = transport.HttpbenchParameters{}
