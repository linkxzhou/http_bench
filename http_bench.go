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

// handleStartup starts HTTP benchmark testing
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
func handleStartup(worker *HttpbenchWorker, params HttpbenchParameters) (result *CollectResult, err error) {
	// Handle distributed worker nodes
	if len(workerAddrList) > 0 {
		fmt.Printf("[%v][%v] running distributed worker %v for %d secs @ %s\n",
			params.RequestType, params.RequestMethod, workerAddrList,
			int(params.Duration.Seconds()), params.Url)
		logInfo(0, "distributed mode: %v", workerAddrList)
		return handleDistributedWorkers(params)
	}

	var seqId = params.SequenceId

	switch params.Cmd {
	case cmdStart:
		logDebug(seqId, "starting benchmark worker...")
		worker.Start(params)
		result, err = getCollectResult(seqId)
		if err != nil {
			logError(seqId, "failed to get collect result: %v", err)
			return nil, err
		}

		// Print results if this is a remote request
		if params.From != "" {
			logDebug(seqId, "printing results for remote request from: %s", params.From)
			result.print()
		}

		logDebug(seqId, "benchmark completed - requests: %d, errors: %d, rps: %d",
			result.LatsTotal, result.ErrTotal, result.Rps)

	case cmdStop:
		logDebug(seqId, "stopping benchmark worker...")
		worker.Stop()
		result, err = getCollectResult(seqId)
		if err != nil {
			logError(seqId, "failed to get collect result: %v", err)
			return nil, err
		}

		// Remove worker from registry
		workerRegistry.Delete(seqId)
		logDebug(seqId, "worker stopped and removed from registry")

	case cmdMetrics:
		logDebug(seqId, "retrieving worker metrics...")
		result, err = getCollectResult(seqId)
		if err != nil {
			logError(seqId, "failed to get collect result: %v", err)
			return nil, err
		}

		logDebug(seqId, "metrics retrieved - requests: %d, errors: %d, rps: %d",
			result.LatsTotal, result.ErrTotal, result.Rps)

	default:
		logWarn(seqId, "received unknown command: %d", params.Cmd)
		return nil, fmt.Errorf("unsupported command: %d", params.Cmd)
	}

	result = mergeCollectResult(nil, result)
	return
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
	seqId := params.SequenceId
	logTrace(seqId, "distributing to worker list: %v", workerAddrList)

	// Marshal parameters to JSON for transmission
	jsonBody, err := json.Marshal(&params)
	if err != nil {
		logError(seqId, "failed to marshal benchmark params: %v", err)
		result := NewCollectResult()
		result.ErrCode = -998
		result.ErrMsg = fmt.Sprintf("parameter marshaling failed: %v", err)
		return result, nil
	}

	// Send requests to all distributed workers
	result, err := postAllDistributedWorkers(workerAddrList, jsonBody)
	if err != nil {
		logError(seqId, "distributed workers execution failed: %v", err)
		result = NewCollectResult()
		result.ErrCode = -999
		result.ErrMsg = fmt.Sprintf("distributed execution failed: %v", err)
		return result, nil
	}

	logInfo(seqId, "distributed benchmark completed successfully")
	return result, nil
}

func main() {
	// Set custom usage message
	flag.Usage = func() {
		fmt.Print(usage)
	}

	var (
		paramsList []HttpbenchParameters
		seqId      = genSequenceId(0)
		params     = HttpbenchParameters{
			SequenceId: seqId,
		}
		err         error
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
	logDebug(seqId, "using %d CPU cores", *cpus)

	// Initialize basic parameters
	params.N = *n
	params.C = *c
	params.Qps = *q
	params.Duration = parseTimeToDuration(*d)

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

	// Configure HTTP request parameters
	params.RequestMethod = strings.ToUpper(*m)
	params.DisableCompression = *disableCompression
	params.DisableKeepAlives = *disableKeepAlives
	params.RequestBody = *body
	params.RequestBodyType = *bodyType

	// Determine protocol type
	if strings.ToLower(*pType) != "" {
		params.RequestType = strings.ToLower(*pType)
	} else {
		params.RequestType = strings.ToLower(*httpType) // Default to HTTP/1.1
	}
	logDebug(seqId, "using protocol: %s", params.RequestType)

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
		logTrace(seqId, "added custom header: %s: %s", match[1], match[2])
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
		logDebug(seqId, "added basic authentication for user: %s", match[1])
	}

	// Validate and set output format
	if *output != "" && *output != "csv" && *output != "html" {
		usageAndExit("invalid output format; supported formats: csv, html")
	}
	params.Output = *output

	// Set request timeout if specified
	params.Timeout = parseTimeToDuration(*t)
	logDebug(seqId, "request timeout: %v seconds", params.Timeout.Seconds())

	// Validate and set proxy URL
	if *proxyAddr != "" {
		if _, err = gourl.Parse(*proxyAddr); err != nil {
			usageAndExit(fmt.Sprintf("invalid proxy URL: %v", err))
		}
		params.ProxyUrl = *proxyAddr
		logDebug(seqId, "using proxy: %s", *proxyAddr)
	}

	// Configure Go garbage collector if specified
	if gogcValue != "" {
		gcPercent, gcErr := strconv.ParseInt(gogcValue, 10, 64)
		if gcErr != nil {
			logWarn(seqId, "invalid HTTPBENCH_GOGC value: %s", gogcValue)
		}
		debug.SetGCPercent(int(gcPercent))
		logDebug(seqId, "set GC percent to: %d", gcPercent)
	}

	// Configure cloud worker API endpoint if specified
	if httpWorkerApiPath != "" {
		dashboardHtml = strings.ReplaceAll(dashboardHtml,
			"/cb9ab101f9f725cb7c3a355bd5631184", httpWorkerApiPath)
		logDebug(seqId, "configured worker API endpoint: %s", httpWorkerApiPath)
	}

	if len(*urlstr) > 0 {
		// Single URL from command line
		params.Url = *urlstr
		paramsList = append(paramsList, params)
		logDebug(seqId, "using single URL: %s", *urlstr)
	} else if len(*httpFile) > 0 {
		// Multiple URLs from file
		if paramsList, err = ParseRestClientFile(*httpFile); err != nil {
			usageAndExit(fmt.Sprintf("failed to read URL file %s: %v", *httpFile, err))
		}
		logDebug(seqId, "loaded %d URLs from file: %s", len(paramsList), *httpFile)
		for i := range paramsList {
			paramsList[i].Merge(&params)
			logTrace(seqId, "merged parameters: %s", paramsList[i].String())
		}
	}

	// Start HTTP server for dashboard and worker API
	if len(*listen) > 0 {
		runDashboardServer(*listen)
		return
	}

	if len(paramsList) == 0 {
		usageAndExit("no valid URLs")
	}

	runBenchmark(paramsList)
	logInfo(seqId, "all benchmarks completed")
}

func runDashboardServer(listen string) {
	mux := http.NewServeMux()
	apiPath := httpWorkerApiURL + httpWorkerApiPath

	// Serve dashboard HTML
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(dashboardHtml))
	})

	// Serve worker API endpoint
	mux.HandleFunc(apiPath, serveDistributedWorker)

	server := &http.Server{
		Addr:    listen,
		Handler: mux,
	}

	fmt.Printf("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	fmt.Printf("Dashboard URL: http://%s/\n", listen)
	fmt.Printf("Worker API: http://%s%s\n", listen, apiPath)
	fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n\n")

	if err := server.ListenAndServe(); err != nil {
		logError(0, "failed to start server: %v", err)
	}
}

func runBenchmark(paramsList []HttpbenchParameters) {
	for i, params := range paramsList {
		seqId := genSequenceId(i)
		params.SequenceId = seqId
		params.Cmd = cmdStart
		logDebug(seqId, "benchmark parameters: %s", params.String())

		// Setup signal handling for graceful shutdown
		stopSignal = make(chan os.Signal, 1)
		signal.Notify(stopSignal, syscall.SIGINT, syscall.SIGTERM)

		var (
			worker = NewWorker(seqId)
			result *CollectResult
			err    error
		)

		// Start goroutine to handle stop signals and timeout
		go func() {
			select {
			case sig := <-stopSignal:
				logInfo(seqId, "received stop signal: %v", sig)
				if len(workerAddrList) > 0 {
					params.Cmd = cmdStop
					if _, err = handleStartup(worker, params); err != nil {
						logError(seqId, "failed to stop distributed workers: %v", err)
					}
				} else {
					worker.Stop()
				}

			case <-time.After(params.Duration):
				if len(workerAddrList) > 0 && params.Duration > 0 {
					logInfo(seqId, "duration timeout reached, stopping distributed workers")
					params.Cmd = cmdStop
					if _, err = handleStartup(worker, params); err != nil {
						logError(seqId, "failed to stop distributed workers: %v", err)
					}
				}
			}
		}()

		// Execute the benchmark
		result, err = handleStartup(worker, params)
		if err != nil {
			logError(seqId, "benchmark execution failed: %v", err)
			continue
		}

		// Print results
		logTrace(seqId, "benchmark result: %v", result.String())
		if result != nil {
			result.print()
		}
	}
}
