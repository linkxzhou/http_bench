/*
 * worker.go — 并发压测引擎
 *
 *   HttpbenchWorker.Run(ctx, params) *CollectResult
 *     │  Duration>0 → ctx 套 WithTimeout；均缺省 → defaultWorkerTimeout
 *     │
 *     ├─► do(ctx, params)
 *     │     ├─ 解析 URL/body 模板（templatefn.go FnMap）
 *     │     ├─ NewLimiter(QPS) — 全局令牌桶（C 个 goroutine 共享）
 *     │     ├─ remaining *atomic.Int64 — 计数模式预算（N%C 余数不丢）
 *     │     └─ doClient ×C goroutines
 *     │           ├─ Limiter.Wait(ctx)        ← 限流
 *     │           ├─ 模板渲染 URL/body         ← hex 解码（BodyHex）
 *     │           ├─ Client.Do(ctx, url, body) ← 请求
 *     │           └─ AppendResult(seqId, Result) ← 采样（模板错误也计失败样本）
 *     │
 *     └─► Stop() → StopResult() → SetStopReason(count/duration/canceled)
 *
 * 依赖：limiter.go, transport.go (Client), metrics.go (AppendResult...),
 *       templatefn.go (FnMap), config.go (defaultWorkerTimeout), logging
 * 被依赖：http_bench.go (handleStartup / defaultRunner)
 */

package bench

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"sync"
	"sync/atomic"
	"text/template"
	"time"
)

// HttpbenchWorker manages the execution of HTTP benchmark tests
// It coordinates multiple concurrent clients and collects results
type HttpbenchWorker struct {
	seqId             int64
	ctx               context.Context    // Cancellation source for the current run
	cancel            context.CancelFunc // Triggers cancellation (Stop, timeout, signal)
	isStop            atomic.Bool        // Thread-safe stop flag (also guards double-cancel)
	urlTmpl, bodyTmpl *template.Template // URL and body templates for dynamic content
	bodyType          string             // Request body type ("string" or "hex")
	mu                sync.Mutex         // Protects worker state
}

// NewWorker creates a new HttpbenchWorker for the given run ID.
//
// Each run gets a fresh worker; state is scoped to the run, not shared across
// runs.
func NewWorker(seqId int64) *HttpbenchWorker {
	Info(seqId, "worker %d created", seqId)
	return &HttpbenchWorker{seqId: seqId}
}

// NewWorkerForTest creates a worker with the given run ID without the
// creation log line. Intended for tests that construct workers inline.
func NewWorkerForTest(seqId int64) *HttpbenchWorker {
	return &HttpbenchWorker{seqId: seqId}
}

// Run executes one benchmark with the caller's cancellation context.
func (w *HttpbenchWorker) Run(ctx context.Context, params HttpbenchParameters) (*CollectResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if params.Duration <= 0 && params.N <= 0 {
		params.Duration = defaultWorkerTimeout
	}
	if params.Duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, params.Duration)
		defer cancel()
	}
	w.mu.Lock()
	w.isStop.Store(false)
	w.ctx, w.cancel = context.WithCancel(ctx)
	NewResult(w.seqId)
	w.mu.Unlock()

	stopReason, err := w.do(w.ctx, params)
	w.Stop()
	StopResult(w.seqId)
	SetStopReason(w.seqId, stopReason)
	if err != nil {
		return nil, err
	}
	return GetCollectResult(w.seqId)
}

// Stop signals the worker to stop execution
// This method is thread-safe and can be called multiple times
func (w *HttpbenchWorker) Stop() error {
	// Use atomic operation to avoid race conditions
	if w.isStop.Swap(true) {
		return nil
	}

	// Cancel the run context. This unblocks doClient's limiter.Wait and
	// interrupts in-flight client.Do calls (HTTP request + WebSocket I/O).
	w.mu.Lock()
	if w.cancel != nil {
		w.cancel()
		Debug(w.seqId, "worker context canceled")
	}
	w.mu.Unlock()

	return nil
}

// GetResult returns the current test results
// If the worker was stopped prematurely, it marks the result with an error
func (w *HttpbenchWorker) GetResult() *CollectResult {
	w.mu.Lock()
	defer w.mu.Unlock()

	result, err := GetCollectResult(w.seqId)
	if err != nil {
		Error(w.seqId, "failed to get collect result: %v", err)
		return nil
	}
	return result
}

// do executes the actual benchmark test by spawning concurrent clients
// Each client makes requests according to the specified parameters
func (w *HttpbenchWorker) do(ctx context.Context, params HttpbenchParameters) (string, error) {
	concurrency := params.C

	fmt.Printf("[%v][%v] running %d connections for %d secs @ %s\n",
		params.RequestType, params.RequestMethod, concurrency,
		int(params.Duration.Seconds()), params.URL)

	var (
		wg               sync.WaitGroup
		err              error
		bodyTemplateName = fmt.Sprintf("body-template-%d", params.SequenceId)
		urlTemplateName  = fmt.Sprintf("url-template-%d", params.SequenceId)
	)

	// Parse URL template with custom functions
	w.urlTmpl, err = template.New(urlTemplateName).Funcs(FnMap).Parse(params.URL)
	if err != nil {
		Error(w.seqId, "failed to parse URL template: %v", err)
		return "", err
	}
	Debug(w.seqId, "URL template parsed: %s", params.URL)

	// Parse request body template
	w.bodyTmpl, err = template.New(bodyTemplateName).Funcs(FnMap).Parse(params.RequestBody)
	if err != nil {
		Error(w.seqId, "failed to parse body template: %v", err)
		return "", err
	}
	w.bodyType = params.RequestBodyType
	Debug(w.seqId, "body template parsed successfully")

	// Global QPS rate limiter shared by all client goroutines.
	// A single token bucket replaces the previous per-goroutine sleep, whose
	// interval formula (1e6/(C*QPS) µs) yielded C*QPS req/s per goroutine and
	// thus C^2*QPS in total — a quadratic amplification of the target rate.
	rl := NewLimiter(params.QPS)
	defer rl.Stop()
	if params.QPS > 0 {
		Debug(w.seqId, "global QPS limiter enabled: %d qps", params.QPS)
	}

	// Shared atomic counter for total request budget.
	// nil means unlimited (duration mode); non-nil ensures the sum across
	// all clients equals exactly params.N, including the N%C remainder
	// that static per-client division (N/C) would drop.
	//
	// When both -n and -d are set, Duration takes precedence (matches
	// ab -t, wrk -d, hey -z behavior): the test runs for the full duration
	// and the request count is ignored. Only when -d is absent does -n
	// control the exact request count.
	var remaining *atomic.Int64
	if params.N > 0 && params.Duration <= 0 {
		remaining = new(atomic.Int64)
		remaining.Store(int64(params.N))
	}

	// Spawn concurrent client goroutines
	for i := 0; i < concurrency; i++ {
		wg.Add(1)

		go func(clientID int) {
			defer wg.Done()

			client := &Client{}
			if err := client.Init(ClientOpts{
				Protocol: params.RequestType,
				Params:   params,
				Insecure: params.Insecure,
			}); err != nil {
				Error(w.seqId, "client %d initialization failed: %v", clientID, err)
				return
			}

			defer func() {
				if err := client.Close(); err != nil {
					Debug(w.seqId, "client %d close failed: %v", clientID, err)
				}
				if r := recover(); r != nil {
					Error(w.seqId, "client %d panic recovered: %v", clientID, r)
				}
			}()

			// Execute requests for this client
			w.doClient(ctx, client, remaining, rl)
		}(i)
	}

	// Wait for all clients to complete
	wg.Wait()
	Debug(w.seqId, "all client goroutines completed")

	// Determine stop reason. When -d is set, the context deadline drives the
	// stop (duration). When only -n is set, the request budget exhaustion
	// stops the loop while ctx is still alive (count).
	// Cancellation (Ctrl-C / remote Stop) overrides both.
	stopReason := ""
	if ctx.Err() == nil {
		// ctx alive => only count mode (no Duration) can reach here
		stopReason = "count"
	} else if ctx.Err() == context.DeadlineExceeded {
		stopReason = "duration"
	} else {
		stopReason = "canceled"
	}
	return stopReason, nil
}

// doClient executes requests for a single client
// It continues until the ctx is canceled, request limit reached, or circuit
// breaker triggered. If remaining is nil, the client runs in unlimited mode
// (duration-based); otherwise each iteration decrements the shared counter to
// claim a slot, so the total across all clients equals exactly params.N
// (including the N%C remainder). rl enforces the global QPS budget; a
// nil/disabled limiter is a no-op.
func (w *HttpbenchWorker) doClient(ctx context.Context, client *Client, remaining *atomic.Int64, rl *Limiter) {
	var requestCount int

	// Reuse buffers to reduce memory allocations
	var urlBuf bytes.Buffer
	var bodyBuf bytes.Buffer

	// Continue until ctx canceled or request budget exhausted. Observing
	// ctx.Done() here (in addition to the per-request Do ctx) lets clients
	// stop claiming new slots immediately on Stop/timeout, rather than
	// looping back and burning a limiter token first.
	for ctx.Err() == nil && !w.isStop.Load() {
		// Claim a request slot from the shared budget when in count mode
		if remaining != nil {
			if remaining.Add(-1) < 0 {
				// Budget exhausted; restore the overdrawn slot and stop
				remaining.Add(1)
				break
			}
		}
		requestCount++

		// Acquire a token from the global rate limiter before issuing the
		// request. This blocks here rather than in the I/O path so that all
		// clients share a single token bucket and the aggregate rate is the
		// configured QPS (not C^2*QPS as the old per-goroutine sleep did).
		// Wait respects ctx, so cancellation unblocks immediately.
		if err := rl.Wait(ctx); err != nil {
			// ctx canceled while waiting for a token — record nothing and exit
			return
		}

		// Execute URL template to generate dynamic URL. A template error is
		// recorded as a failed request sample and the loop continues, rather
		// than aborting the whole client goroutine (which would silently drop
		// the remaining request budget for this client and skew statistics).
		urlBuf.Reset()
		if err := w.urlTmpl.Execute(&urlBuf, nil); err != nil {
			Error(w.seqId, "failed to execute URL template: %v", err)
			recordTemplateError(w.seqId, err, &requestCount)
			continue
		}

		// Execute body template to generate dynamic request body
		bodyBuf.Reset()
		if err := w.bodyTmpl.Execute(&bodyBuf, nil); err != nil {
			Error(w.seqId, "failed to execute body template: %v", err)
			recordTemplateError(w.seqId, err, &requestCount)
			continue
		}

		// Decode hex body if bodytype is "hex" (BodyHex).
		// The template may produce a hex-encoded string; decode it to raw
		// bytes before sending so the server receives binary data.
		reqBody := bodyBuf.Bytes()
		if w.bodyType == BodyHex && len(reqBody) > 0 {
			decoded, err := hex.DecodeString(string(reqBody))
			if err != nil {
				Error(w.seqId, "hex decode error: %v", err)
				recordTemplateError(w.seqId, err, &requestCount)
				continue
			}
			reqBody = decoded
		}

		Trace(w.seqId, "request #%d: url=%s, body=%s", requestCount, urlBuf.String(), bodyBuf.String())

		// Execute HTTP request and measure duration. ctx propagates worker
		// cancellation/timeout into the request so a slow server cannot keep
		// a client goroutine alive past Stop.
		startTime := time.Now()
		statusCode, contentLength, err := client.Do(ctx, urlBuf.Bytes(), reqBody, 0)
		duration := time.Since(startTime)

		Trace(w.seqId, "request #%d completed: status=%d, size=%d, duration=%v, err=%v",
			requestCount, statusCode, contentLength, duration, err)

		// Record result
		_, resultErr := AppendResult(w.seqId, &Result{
			StatusCode:    statusCode,
			Duration:      duration,
			ContentLength: contentLength,
			Err:           err,
		})

		if err != nil {
			Warn(w.seqId, "request #%d failed: %v", requestCount, err)
		}

		// Check circuit breaker on error
		if resultErr != nil {
			Error(w.seqId, "failed to append result: %v", resultErr)
			return
		}
	}

	Debug(w.seqId, "client completed %d requests", requestCount)
}

// recordTemplateError logs and samples a template-rendering failure as a
// failed request result so that the aggregate statistics (error rate, total
// request count) stay accurate instead of silently dropping the attempt.
// requestCount is incremented to reflect the attempted request.
func recordTemplateError(seqId int64, err error, requestCount *int) {
	*requestCount++
	_, _ = AppendResult(seqId, &Result{
		Err:        fmt.Errorf("template error: %w", err),
		StatusCode: 0,
		Duration:   0,
	})
}
