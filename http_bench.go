package main

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	_ "net/http/pprof"
	gourl "net/url"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var (
	// workerAddrList stores addresses of distributed worker nodes
	workerAddrList flagSlice
)

// HttpBenchStartup starts HTTP benchmark testing
// It automatically determines whether to run in distributed mode or single-node mode
// based on the presence of worker addresses.
//
// Parameters:
//   - worker: The worker instance to execute the benchmark
//   - params: Configuration parameters for the benchmark test
//
// Returns:
//   - *CollectResult: Aggregated test results
//   - error: Error if the benchmark fails to start or execute
func HttpBenchStartup(worker *HttpbenchWorker, params HttpbenchParameters) (*CollectResult, error) {
	// Handle distributed worker nodes
	if len(workerAddrList) > 0 {
		return handleDistributedWorkers(params)
	}

	// Handle single worker node
	result := NewCollectResult()
	return handleSingleWorker(worker, params, result)
}

// handleDistributedWorkers handles distributed worker nodes
// It marshals the parameters and sends them to all worker nodes concurrently.
//
// Parameters:
//   - params: Configuration parameters to send to workers
//
// Returns:
//   - *CollectResult: Merged results from all workers
//   - error: Always returns nil (errors are embedded in result)
func handleDistributedWorkers(params HttpbenchParameters) (*CollectResult, error) {
	logTrace("distributing to worker list: %v", workerAddrList)

	// Marshal parameters to JSON for transmission
	jsonBody, err := json.Marshal(&params)
	if err != nil {
		logError("failed to marshal benchmark params: %v", err)
		result := NewCollectResult()
		result.ErrCode = -998
		result.ErrMsg = fmt.Sprintf("parameter marshaling failed: %v", err)
		return result, nil
	}

	// Send requests to all distributed workers
	result, err := postAllDistributedWorkers(workerAddrList, jsonBody)
	if err != nil {
		logError("distributed workers execution failed: %v", err)
		result = NewCollectResult()
		result.ErrCode = -999
		result.ErrMsg = fmt.Sprintf("distributed execution failed: %v", err)
		return result, nil
	}

	logInfo("distributed benchmark completed successfully")
	return result, nil
}

// handleSingleWorker handles single worker node execution
// It processes different commands (start, stop, metrics) for the worker.
//
// Parameters:
//   - worker: The worker instance to control
//   - params: Configuration parameters including the command to execute
//   - result: Pre-allocated result object (may be replaced)
//
// Returns:
//   - *CollectResult: Test results or error information
//   - error: Always returns nil (errors are embedded in result)
func handleSingleWorker(worker *HttpbenchWorker, params HttpbenchParameters, result *CollectResult) (*CollectResult, error) {
	logTrace("executing single worker command: %d", params.Cmd)

	switch params.Cmd {
	case cmdStart:
		return handleStartCommand(worker, params)

	case cmdStop:
		return handleStopCommand(worker, params, result)

	case cmdMetrics:
		return handleMetricsCommand(worker)

	default:
		logWarn("received unknown command: %d", params.Cmd)
		result.ErrCode = -2
		result.ErrMsg = fmt.Sprintf("unsupported command: %d", params.Cmd)
		return result, nil
	}
}

// handleStartCommand starts the benchmark worker
func handleStartCommand(worker *HttpbenchWorker, params HttpbenchParameters) (*CollectResult, error) {
	logInfo("starting benchmark worker (sequence: %d)...", params.SequenceId)

	result := worker.Start(params)

	// Print results if this is a remote request
	if params.From != "" {
		logDebug("printing results for remote request from: %s", params.From)
		result.print()
	}

	logInfo("benchmark completed - requests: %d, errors: %d, rps: %d",
		result.LatsTotal, result.ErrTotal, result.Rps)

	return result, nil
}

// handleStopCommand stops the benchmark worker
func handleStopCommand(worker *HttpbenchWorker, params HttpbenchParameters, result *CollectResult) (*CollectResult, error) {
	logInfo("stopping benchmark worker (sequence: %d)...", params.SequenceId)

	worker.Stop()

	// Remove worker from registry
	workerRegistry.Delete(params.SequenceId)

	logInfo("worker stopped and removed from registry")
	return result, nil
}

// handleMetricsCommand retrieves current metrics from the worker
func handleMetricsCommand(worker *HttpbenchWorker) (*CollectResult, error) {
	logInfo("retrieving worker metrics...")

	result := worker.GetResult()

	logDebug("metrics retrieved - requests: %d, errors: %d",
		result.LatsTotal, result.ErrTotal)

	return result, nil
}

func main() {
	// Set custom usage message
	flag.Usage = func() {
		fmt.Print(usage)
	}

	var (
		params      HttpbenchParameters
		headerSlice flagSlice
	)

	// Register custom flag types
	flag.Var(&headerSlice, "H", "")    // Custom HTTP header (repeatable)
	flag.Var(&workerAddrList, "W", "") // Worker machine addresses (repeatable)
	flag.Var(&workerAddrList, "w", "") // Worker machine addresses (lowercase alias)
	flag.Parse()

	// Handle positional URL argument
	for flag.NArg() > 0 {
		if len(*urlstr) == 0 {
			*urlstr = flag.Args()[0]
		}
		os.Args = flag.Args()[0:]
		flag.Parse()
	}

	// Print examples and exit if requested
	if *printExample {
		fmt.Print(examples)
		return
	}

	// Configure runtime
	runtime.GOMAXPROCS(*cpus)
	logDebug("using %d CPU cores", *cpus)

	// Initialize basic parameters
	params.N = *n
	params.C = *c
	params.Qps = *q
	params.Duration = parseTime(*d)

	// Validate concurrency parameters
	if params.C <= 0 {
		usageAndExit("concurrency (-c) must be at least 1")
	}

	if params.N > 0 && params.N < params.C {
		usageAndExit("total requests (-n) cannot be less than concurrency (-c)")
	}

	if params.N <= 0 && params.Duration <= 0 {
		usageAndExit("either -n (request count) or -d (duration) must be specified")
	}

	// Parse target URLs from command line or file
	var (
		requestUrls []string
		err         error
	)

	if *urlFile == "" && len(*urlstr) > 0 {
		// Single URL from command line
		requestUrls = append(requestUrls, *urlstr)
		logDebug("using single URL: %s", *urlstr)
	} else if len(*urlFile) > 0 {
		// Multiple URLs from file
		if requestUrls, err = parseFile(*urlFile, []rune{'\r', '\n'}); err != nil {
			usageAndExit(fmt.Sprintf("failed to read URL file %s: %v", *urlFile, err))
		}
		logDebug("loaded %d URLs from file: %s", len(requestUrls), *urlFile)
	}

	// Configure HTTP request parameters
	params.RequestMethod = strings.ToUpper(*m)
	params.DisableCompression = *disableCompression
	params.DisableKeepAlives = *disableKeepAlives
	params.RequestBody = *body
	params.RequestBodyType = *bodyType

	// Load request body from file if specified
	if *bodyFile != "" {
		var bodyContent []string
		if bodyContent, err = parseFile(*bodyFile, nil); err != nil {
			usageAndExit(fmt.Sprintf("failed to read body file %s: %v", *bodyFile, err))
		}
		if len(bodyContent) > 0 {
			params.RequestBody = bodyContent[0]
			logDebug("loaded request body from file (%d bytes)", len(params.RequestBody))
		}
	}

	// Load script body from file if specified
	if *scriptFile != "" {
		var scriptContent []string
		if scriptContent, err = parseFile(*scriptFile, nil); err != nil {
			usageAndExit(fmt.Sprintf("failed to read script file %s: %v", *scriptFile, err))
		}
		if len(scriptContent) > 0 {
			params.RequestScriptBody = scriptContent[0]
			logDebug("loaded script body from file (%d bytes)", len(params.RequestScriptBody))
		}
	}

	// Determine protocol type
	if strings.ToLower(*pType) != "" {
		params.RequestType = strings.ToLower(*pType)
	} else {
		params.RequestType = strings.ToLower(*httpType) // Default to HTTP/1.1
	}
	logDebug("using protocol: %s", params.RequestType)

	// Parse and set custom HTTP headers
	for _, header := range headerSlice {
		var match []string
		if match, err = parseInputWithRegexp(header, HeaderRegexp); err != nil {
			usageAndExit(fmt.Sprintf("invalid header format: %v", err))
		}
		if params.Headers == nil {
			params.Headers = make(map[string][]string)
		}
		params.Headers[match[1]] = []string{match[2]}
		logTrace("added custom header: %s: %s", match[1], match[2])
	}

	// Set HTTP Basic Authentication if provided
	if *authHeader != "" {
		var match []string
		if match, err = parseInputWithRegexp(*authHeader, AuthRegexp); err != nil {
			usageAndExit(fmt.Sprintf("invalid auth format: %v", err))
		}
		if params.Headers == nil {
			params.Headers = make(map[string][]string)
		}
		authValue := base64.StdEncoding.EncodeToString([]byte(match[1] + ":" + match[2]))
		params.Headers["Authorization"] = []string{fmt.Sprintf("Basic %s", authValue)}
		logDebug("added basic authentication for user: %s", match[1])
	}

	// Validate and set output format
	if *output != "" && *output != "csv" && *output != "html" {
		usageAndExit("invalid output format; supported formats: csv, html")
	}
	params.Output = *output

	// Set request timeout
	params.Timeout = *t
	logDebug("request timeout: %dms", *t)

	// Validate and set proxy URL
	if *proxyAddr != "" {
		if _, err = gourl.Parse(*proxyAddr); err != nil {
			usageAndExit(fmt.Sprintf("invalid proxy URL: %v", err))
		}
		params.ProxyUrl = *proxyAddr
		logDebug("using proxy: %s", *proxyAddr)
	}

	var server *http.Server

	// Configure Go garbage collector if specified
	if gogcValue := getEnv("HTTPBENCH_GOGC"); gogcValue != "" {
		if gcPercent, err := strconv.ParseInt(gogcValue, 10, 64); err == nil {
			debug.SetGCPercent(int(gcPercent))
			logDebug("set GC percent to: %d", gcPercent)
		} else {
			logWarn("invalid HTTPBENCH_GOGC value: %s", gogcValue)
		}
	}

	// Configure cloud worker API endpoint if specified
	if workerAPI := getEnv("HTTPBENCH_WORKERAPI"); workerAPI != "" {
		dashboardHtml = strings.ReplaceAll(dashboardHtml,
			"/cb9ab101f9f725cb7c3a355bd5631184", workerAPI)
		logDebug("configured worker API endpoint: %s", workerAPI)
	}

	// Use dashboard address if specified
	if len(*dashboard) > 0 {
		*listen = *dashboard
	}

	// Start HTTP server for dashboard and worker API
	if len(*listen) > 0 {
		mux := http.NewServeMux()

		// Serve dashboard HTML
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write([]byte(dashboardHtml))
		})

		// Serve worker API endpoint
		mux.HandleFunc(httpWorkerApiPath, serveDistributedWorker)

		server = &http.Server{
			Addr:    *listen,
			Handler: mux,
		}

		fmt.Printf("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
		fmt.Printf("Worker listening on: %s\n", *listen)
		fmt.Printf("Dashboard URL: http://%s/\n", *listen)
		fmt.Printf("Worker API: http://%s%s\n", *listen, httpWorkerApiPath)
		fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n\n")

		if err = server.ListenAndServe(); err != nil {
			logError("failed to start server: %v", err)
		}
		return
	}

	// Validate that at least one URL is provided
	if len(requestUrls) <= 0 {
		usageAndExit("no target URL specified; use -url or --url-file")
	}

	// Execute benchmark for each URL
	logInfo("starting benchmark for %d URL(s)", len(requestUrls))

	for i, url := range requestUrls {
		params.Url = url
		params.SequenceId = genSequenceId(i)
		params.Cmd = cmdStart

		logDebug("benchmark parameters: %s", params.String())

		// Setup signal handling for graceful shutdown
		stopSignal = make(chan os.Signal, 1)
		signal.Notify(stopSignal, syscall.SIGINT, syscall.SIGTERM)

		var (
			worker = NewWorker(params.SequenceId)
			result *CollectResult
		)

		// Start goroutine to handle stop signals and timeout
		go func() {
			select {
			case sig := <-stopSignal:
				logInfo("received stop signal: %v", sig)
				if len(workerAddrList) > 0 {
					// Stop distributed workers
					params.Cmd = cmdStop
					if _, err := HttpBenchStartup(worker, params); err != nil {
						logError("failed to stop distributed workers: %v", err)
					}
				} else {
					// Stop local worker
					worker.Stop()
				}

			case <-time.After(time.Duration(params.Duration) * time.Millisecond):
				if len(workerAddrList) > 0 && params.Duration > 0 {
					logInfo("duration timeout reached, stopping distributed workers")
					params.Cmd = cmdStop
					if _, err := HttpBenchStartup(worker, params); err != nil {
						logError("failed to stop distributed workers on timeout: %v", err)
					}
				}
			}
		}()

		// Execute the benchmark
		result, err = HttpBenchStartup(worker, params)
		if err != nil {
			logError("benchmark execution failed: %v", err)
			continue
		}

		// Print results
		if result != nil {
			result.print()
		} else {
			logWarn("benchmark completed but no results available")
		}
	}

	logInfo("all benchmarks completed")
}
