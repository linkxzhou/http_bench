/*
 * distributed.go — 分布式 worker 服务与 HTTP 入口（worker 节点侧）
 *
 *   Worker 节点（-listen 模式，dashboard.go 挂载）
 *     └─ POST /api{path} ──► ServeRequest(service, validator, w, r)
 *           ├─ CORS / Bearer APIKey 鉴权 / 1MB body 上限
 *           ├─ json → WorkerRequest (= HttpbenchParameters)
 *           ├─ RequestValidator（DefaultValidator 镜像 CLI 校验）
 *           └─ WorkerService.Execute
 *                └─ NewDefaultService(runner).Execute → WorkerRunner.RunWorker
 *
 *   DTO：WorkerRequest / WorkerResponse (类型别名)
 *        WorkerError / WorkerResultDetail / DistributedResult
 *   var APIKey / AllowOrigins — 鉴权与 CORS 白名单
 *
 * 依赖：logging, metrics.go (CollectResult), transport.go (HttpbenchParameters)
 * 被依赖：dashboard.go (路由挂载), controller.go (PostWorker 响应类型),
 *         http_bench.go (defaultRunner)
 */

package bench

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// ----------------------------------------------------------------- DTOs ---

// WorkerRequest is the wire DTO sent to a worker. It is structurally the same
// as HttpbenchParameters so we share its JSON tags.
type WorkerRequest = HttpbenchParameters

// WorkerResponse is the successful worker response.
type WorkerResponse = CollectResult

// WorkerError is the standard JSON error returned by the worker API.
type WorkerError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// WorkerResultDetail records the outcome of one controller dispatch.
type WorkerResultDetail struct {
	Address string         `json:"address"`
	Success bool           `json:"success"`
	Result  *CollectResult `json:"result,omitempty"`
	Error   string         `json:"error,omitempty"`
	Elapsed time.Duration  `json:"elapsed"`
}

// DistributedResult is the controller-side aggregate output.
type DistributedResult struct {
	Merged  *CollectResult       `json:"merged,omitempty"`
	Workers []WorkerResultDetail `json:"workers"`
}

// ----------------------------------------------------------- Service ---

// WorkerService executes a benchmark request independently of any HTTP
// transport. main wires the default implementation; tests can substitute a
// stub.
type WorkerService interface {
	Execute(ctx context.Context, request WorkerRequest) (*WorkerResponse, error)
}

type defaultWorkerService struct{ runner WorkerRunner }

func (d defaultWorkerService) Execute(ctx context.Context, request WorkerRequest) (*WorkerResponse, error) {
	if d.runner == nil {
		return nil, fmt.Errorf("worker runner not configured")
	}
	return d.runner.RunWorker(ctx, request)
}

// WorkerRunner executes one benchmark. Implemented by the runtime worker.
type WorkerRunner interface {
	RunWorker(ctx context.Context, params HttpbenchParameters) (*WorkerResponse, error)
}

// NewDefaultService returns the production worker service backed by runner.
func NewDefaultService(runner WorkerRunner) WorkerService {
	return defaultWorkerService{runner: runner}
}

// RequestValidator decides whether an incoming request is acceptable.
type RequestValidator func(WorkerRequest) error

// DefaultValidator ensures the request mirrors the validateParams contract.
func DefaultValidator(p HttpbenchParameters) error {
	if p.C <= 0 {
		return fmt.Errorf("concurrency must be at least 1")
	}
	if p.N > 0 && p.N < p.C {
		return fmt.Errorf("total requests cannot be less than concurrency")
	}
	if p.N <= 0 && p.Duration <= 0 {
		return fmt.Errorf("either N (request count) or Duration must be specified")
	}
	return nil
}

// ----------------------------------------------------------- Handler ---

// ServeRequest is the standard worker API entry point. The default validation
// can be replaced by passing a custom RequestValidator.
func ServeRequest(service WorkerService, validator RequestValidator, w http.ResponseWriter, r *http.Request) {
	// A benchmark run may last far longer than the dashboard server's
	// WriteTimeout (30s default): the handler stays busy for the whole run
	// and only then writes the response. Without this, the connection is
	// force-closed mid-run, the response is truncated, and the controller
	// reports the worker as failed even though it completed the benchmark.
	// SetWriteDeadline is unsupported on some protocols (e.g. HTTP/2);
	// the error is safely ignorable there.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	setCORSHeaders(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		Warn(0, "invalid method %s, only POST is allowed", r.Method)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if len(APIKey) > 0 {
		if r.Header.Get("Authorization") != "Bearer "+APIKey {
			Warn(0, "invalid Authorization header")
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var request WorkerRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeWorkerError(w, http.StatusBadRequest, "invalid_request", fmt.Errorf("invalid request body: %w", err))
		return
	}
	if validator != nil {
		if err := validator(request); err != nil {
			writeWorkerError(w, http.StatusBadRequest, "validation_failed", err)
			return
		}
	}
	result, err := service.Execute(r.Context(), request)
	if err != nil {
		Error(request.SequenceId, "benchmark execution failed: %v", err)
		writeWorkerError(w, http.StatusInternalServerError, "execution_failed", err)
		return
	}
	if result == nil {
		writeWorkerError(w, http.StatusInternalServerError, "empty_result", fmt.Errorf("benchmark returned nil result"))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(result); err != nil {
		Error(request.SequenceId, "failed to encode response: %v", err)
	}
}

func writeWorkerError(w http.ResponseWriter, status int, code string, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(WorkerError{Code: code, Message: err.Error()})
}

// APIKey is the optional bearer token required to dispatch a benchmark.
var APIKey string

// AllowOrigins is the CORS allowlist.
var AllowOrigins = map[string]struct{}{"http://localhost": {}, "http://127.0.0.1": {}}

func setCORSHeaders(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return
	}
	if _, ok := AllowOrigins[origin]; !ok {
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Add("Vary", "Origin")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
}
