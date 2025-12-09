package main

import (
	"bytes"
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
	stopChan          chan bool
	isStop            atomic.Bool        // Thread-safe stop flag
	urlTmpl, bodyTmpl *template.Template // URL and body templates for dynamic content
	mu                sync.Mutex         // Protects worker state
}

// workerRegistry maintains a registry of active workers by sequence ID
// This allows reusing workers for multiple test runs
var workerRegistry sync.Map

// NewWorker creates or retrieves an existing worker by sequence ID
// Returns an existing worker if one is already registered, otherwise creates a new one
func NewWorker(seqId int64) *HttpbenchWorker {
	var worker *HttpbenchWorker

	if v, ok := workerRegistry.Load(seqId); ok && v != nil {
		worker = v.(*HttpbenchWorker)
		logInfo(seqId, "worker %d already exists, reusing", seqId)
	} else {
		worker = &HttpbenchWorker{
			seqId: seqId,
		}
		workerRegistry.Store(seqId, worker)
		logInfo(seqId, "worker %d created", seqId)
	}

	return worker
}

// Start initiates the benchmark test with the given parameters
// It spawns concurrent clients and waits for completion or timeout
// Returns the aggregated test results
func (w *HttpbenchWorker) Start(params HttpbenchParameters) error {
	w.mu.Lock()
	w.stopChan = make(chan bool, stopChannelSize)
	w.isStop.Store(false) // Reset stop flag
	if params.Duration <= 0 {
		params.Duration = defaultWorkerTimeout
	}
	NewResult(w.seqId)
	w.mu.Unlock()

	// Execute benchmark in separate goroutine
	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				logError(w.seqId, "worker panic recovered: %v", r)
			}
			logDebug(w.seqId, "worker execution finished")
			w.Stop()
			wg.Done()
		}()

		w.do(params)
	}()

	// Wait for stop signal or timeout
	select {
	case isStop, ok := <-w.stopChan:
		if ok && isStop {
			logDebug(w.seqId, "worker stopped by explicit signal")
		}
	case <-time.After(params.Duration):
		logDebug(w.seqId, "worker stopped by timeout after %v ms",
			params.Duration.Milliseconds())
	}

	// Ensure worker is stopped
	w.Stop()
	logInfo(w.seqId, "worker finished, waiting for goroutines to complete")
	wg.Wait()
	stopResult(w.seqId)
	logInfo(w.seqId, "worker results collected")

	return nil
}

// Stop signals the worker to stop execution
// This method is thread-safe and can be called multiple times
func (w *HttpbenchWorker) Stop() error {
	// Use atomic operation to avoid race conditions
	if w.isStop.Swap(true) {
		return nil
	}

	// Send stop signal (non-blocking)
	select {
	case w.stopChan <- true:
		logDebug(w.seqId, "stop signal sent")
	default:
		// Channel already has a signal or is closed
		logDebug(w.seqId, "stop signal already present")
	}

	return nil
}

// GetResult returns the current test results
// If the worker was stopped prematurely, it marks the result with an error
func (w *HttpbenchWorker) GetResult() *CollectResult {
	w.mu.Lock()
	defer w.mu.Unlock()

	result, err := getCollectResult(w.seqId)
	if err != nil {
		logError(w.seqId, "failed to get collect result: %v", err)
		return nil
	}
	return result
}

// do executes the actual benchmark test by spawning concurrent clients
// Each client makes requests according to the specified parameters
func (w *HttpbenchWorker) do(params HttpbenchParameters) error {
	concurrency := params.C

	fmt.Printf("[%v][%v] running %d connections for %d secs @ %s\n",
		params.RequestType, params.RequestMethod, concurrency,
		int(params.Duration.Seconds()), params.Url)

	var (
		wg               sync.WaitGroup
		err              error
		bodyTemplateName = fmt.Sprintf("body-template-%d", params.SequenceId)
		urlTemplateName  = fmt.Sprintf("url-template-%d", params.SequenceId)

		// Initialize connection pool with proper size limit
		connPool = NewClientPool(concurrency * 2)
	)

	defer connPool.Shutdown()

	// Parse URL template with custom functions
	w.urlTmpl, err = template.New(urlTemplateName).Funcs(fnMap).Parse(params.Url)
	if err != nil {
		logError(w.seqId, "failed to parse URL template: %v", err)
		return err
	}
	logDebug(w.seqId, "URL template parsed: %s", params.Url)

	// Parse request body template
	w.bodyTmpl, err = template.New(bodyTemplateName).Funcs(fnMap).Parse(params.RequestBody)
	if err != nil {
		logError(w.seqId, "failed to parse body template: %v", err)
		return err
	}
	logDebug(w.seqId, "body template parsed successfully")

	// Calculate sleep interval for QPS rate limiting (in microseconds)
	sleepInterval := 0
	if params.Qps > 0 {
		sleepInterval = 1e6 / (concurrency * params.Qps)
		logDebug(w.seqId, "QPS rate limiting enabled: %d qps, sleep interval: %d µs", params.Qps, sleepInterval)
	}

	// Calculate requests per client
	requestsPerClient := params.N / concurrency

	// Spawn concurrent client goroutines
	for i := 0; i < concurrency; i++ {
		wg.Add(1)

		go func(clientID int) {
			defer wg.Done()

			// Get client from pool
			client := connPool.Get()
			if client == nil {
				logError(w.seqId, "failed to get client from pool")
				return
			}

			// Initialize client with protocol and parameters
			err := client.Init(ClientOpts{
				Protocol: params.RequestType,
				Params:   params,
			})
			if err != nil {
				logError(w.seqId, "client %d initialization failed: %v", clientID, err)
				return
			}

			// Ensure client is returned to pool and panic is recovered
			defer func() {
				connPool.Put(client)
				if r := recover(); r != nil {
					logError(w.seqId, "client %d panic recovered: %v", clientID, r)
				}
			}()

			// Execute requests for this client
			w.doClient(client, requestsPerClient, sleepInterval)
		}(i)
	}

	// Wait for all clients to complete
	wg.Wait()
	logDebug(w.seqId, "all client goroutines completed")
	return nil
}

// doClient executes requests for a single client
// It continues until stopped, request limit reached, or circuit breaker triggered
func (w *HttpbenchWorker) doClient(client *Client, maxRequests, sleepMicroseconds int) {
	var requestCount int

	// Reuse buffers to reduce memory allocations
	var urlBuf bytes.Buffer
	var bodyBuf bytes.Buffer

	// Continue until stopped or request limit reached
	for !w.isStop.Load() && (maxRequests <= 0 || requestCount < maxRequests) {
		requestCount++

		// Apply rate limiting if configured
		if sleepMicroseconds > 0 {
			time.Sleep(time.Duration(sleepMicroseconds) * time.Microsecond)
		}

		// Execute URL template to generate dynamic URL
		urlBuf.Reset()
		if err := w.urlTmpl.Execute(&urlBuf, nil); err != nil {
			logError(w.seqId, "failed to execute URL template: %v", err)
			return
		}

		// Execute body template to generate dynamic request body
		bodyBuf.Reset()
		if err := w.bodyTmpl.Execute(&bodyBuf, nil); err != nil {
			logError(w.seqId, "failed to execute body template: %v", err)
			return
		}

		logTrace(w.seqId, "request #%d: url=%s, body=%s", requestCount, urlBuf.String(), bodyBuf.String())

		// Execute HTTP request and measure duration
		startTime := time.Now()
		statusCode, contentLength, err := client.Do(urlBuf.Bytes(), bodyBuf.Bytes(), 0)
		duration := time.Since(startTime)

		logTrace(w.seqId, "request #%d completed: status=%d, size=%d, duration=%v, err=%v",
			requestCount, statusCode, contentLength, duration, err)

		// Record result
		_, resultErr := appendResult(w.seqId, &Result{
			statusCode:    statusCode,
			duration:      duration,
			contentLength: contentLength,
			err:           err,
		})

		if err != nil {
			logWarn(w.seqId, "request #%d failed: %v", requestCount, err)
		}

		// Check circuit breaker on error
		if resultErr != nil {
			logError(w.seqId, "failed to append result: %v", resultErr)
			return
		}
	}

	logDebug(w.seqId, "client completed %d requests", requestCount)
}
