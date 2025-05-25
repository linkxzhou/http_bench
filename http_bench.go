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

var workerAddrList flagSlice // Worker mechine addr list.

func HttpBenchStartup(hbWorker *HttpbenchWorker, params HttpbenchParameters) (*CollectResult, error) {
	var result *CollectResult = NewCollectResult()

	if len(workerAddrList) > 0 {
		verbosePrint(logLevelTrace, "worker list: %v", workerAddrList)
		jsonBody, err := json.Marshal(params)
		if err != nil {
			verbosePrint(logLevelError, "invalid stress testing params")
			return nil, fmt.Errorf("invalid stress testing params")
		}
		result, err = postAllDistributedWorkers(workerAddrList, jsonBody)
		if err != nil {
			result.ErrCode = -999
			result.ErrMsg = err.Error()
		}
	} else {
		verbosePrint(logLevelTrace, "single worker and cmd: %s", params.Cmd)
		switch params.Cmd {
		case cmdStart:
			verbosePrint(logLevelInfo, "start worker!!!")
			result = hbWorker.Start(params)
			verbosePrint(logLevelInfo, "worker result: %v", result)
		case cmdStop:
			verbosePrint(logLevelInfo, "stop worker!!!")
			hbWorker.Stop()
			verbosePrint(logLevelInfo, "worker result: %v", result)
			hbWorkerList.Delete(params.SequenceId)
		case cmdMetrics:
			verbosePrint(logLevelInfo, "get metrics!!!")
			result = hbWorker.GetResult()
		}

		if hbWorker.err != nil {
			result.ErrCode = -1
			result.ErrMsg = hbWorker.err.Error()
		}
	}

	return result, nil
}

func main() {
	flag.Usage = func() {
		fmt.Println(fmt.Sprintf(usage, runtime.NumCPU()))
	}

	var params HttpbenchParameters
	var headerslice flagSlice

	flag.Var(&headerslice, "H", "")    // Custom HTTP header
	flag.Var(&workerAddrList, "W", "") // Worker mechine, support W/w
	flag.Var(&workerAddrList, "w", "")
	flag.Parse()

	for flag.NArg() > 0 {
		if len(*urlstr) == 0 {
			*urlstr = flag.Args()[0]
		}
		os.Args = flag.Args()[0:]
		flag.Parse()
	}

	if *printExample {
		println(examples)
		return
	}

	runtime.GOMAXPROCS(*cpus)
	params.N = *n
	params.C = *c
	params.Qps = *q
	params.Duration = parseTime(*d)

	if params.C <= 0 {
		usageAndExit("n and c cannot be smaller than 1.")
	}

	if (params.N < params.C) && (params.Duration < 0) {
		usageAndExit("n cannot be less than c.")
	}

	var requestUrls []string
	var err error
	if *urlFile == "" && len(*urlstr) > 0 {
		requestUrls = append(requestUrls, *urlstr)
	} else if len(*urlFile) > 0 {
		if requestUrls, err = parseFile(*urlFile, []rune{'\r', '\n'}); err != nil {
			usageAndExit(*urlFile + " file read error(" + err.Error() + ").")
		}
	}

	params.RequestMethod = strings.ToUpper(*m)
	params.DisableCompression = *disableCompression
	params.DisableKeepAlives = *disableKeepAlives
	params.RequestBody = *body
	params.RequestBodyType = *bodyType

	if *bodyFile != "" {
		readBody, err := parseFile(*bodyFile, nil)
		if err != nil {
			usageAndExit(*bodyFile + " file read error(" + err.Error() + ").")
		}
		if len(readBody) > 0 {
			params.RequestBody = readBody[0]
		}
	}

	if *scriptFile != "" {
		scriptBody, err := parseFile(*scriptFile, nil)
		if err != nil {
			usageAndExit(*scriptFile + " file read error(" + err.Error() + ").")
		}
		if len(scriptBody) > 0 {
			params.RequestScriptBody = scriptBody[0]
		}
	}

	if strings.ToLower(*pType) != "" {
		params.RequestType = strings.ToLower(*pType)
	} else {
		params.RequestType = strings.ToLower(*httpType) // default http request
	}

	// set any other additional repeatable headers
	for _, h := range headerslice {
		match, err := parseInputWithRegexp(h, headerRegexp)
		if err != nil {
			usageAndExit(err.Error())
		}
		if params.Headers == nil {
			params.Headers = make(map[string][]string, 0)
		}
		params.Headers[match[1]] = []string{match[2]}
	}

	// set basic auth if set
	if *authHeader != "" {
		match, err := parseInputWithRegexp(*authHeader, authRegexp)
		if err != nil {
			usageAndExit(err.Error())
		}
		params.Headers["Authorization"] = []string{
			fmt.Sprintf("Basic %s", base64.StdEncoding.EncodeToString([]byte(match[1]+":"+match[2]))),
		}
	}

	if *output != "" && *output != "csv" {
		usageAndExit("invalid output type; only csv is supported.")
	}

	// set request timeout
	params.Timeout = *t

	if *proxyAddr != "" {
		if _, err := gourl.Parse(*proxyAddr); err != nil {
			usageAndExit(err.Error())
		}
		params.ProxyUrl = *proxyAddr
	}

	var hbServer *http.Server

	// decrease go gc rate
	hbGOGC := getEnv("HTTPBENCH_GOGC")
	if n, err := strconv.ParseInt(hbGOGC, 2, 64); err == nil {
		debug.SetGCPercent(int(n))
	}

	// cloud worker API
	hbWorkerAPI := getEnv("HTTPBENCH_WORKERAPI")
	if hbWorkerAPI != "" {
		dashboardHtml = strings.ReplaceAll(dashboardHtml,
			"/cb9ab101f9f725cb7c3a355bd5631184", hbWorkerAPI)
	}

	if len(*dashboard) > 0 {
		*listen = *dashboard
	}

	// start http server to serve dashboard
	if len(*listen) > 0 {
		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(dashboardHtml)) // export dashboard index.html
		})
		mux.HandleFunc(httpWorkerApiPath, serveDistributedWorker)
		hbServer = &http.Server{
			Addr:    *listen,
			Handler: mux,
		}
		println("worker listen: %s, and you can open http://%s/index.html on browser", *listen, *listen)
		if err := hbServer.ListenAndServe(); err != nil {
			verbosePrint(logLevelError, "listen err: %s", err.Error())
		}
		return
	}

	// start http bench
	if len(requestUrls) <= 0 {
		usageAndExit("url or url-file empty.")
	}

	for i, url := range requestUrls {
		params.Url = url
		params.SequenceId = genSequenceId(i)
		params.Cmd = cmdStart

		verbosePrint(logLevelDebug, "request params: %s", params.String())
		stopSignal = make(chan os.Signal)
		signal.Notify(stopSignal, syscall.SIGINT, syscall.SIGTERM)

		var hbWorker = NewWorker(params.SequenceId)
		var hbResult *CollectResult

		go func() {
			select {
			case <-stopSignal:
				verbosePrint(logLevelInfo, "recv stop signal!!!")
				if len(workerAddrList) > 0 {
					params.Cmd = cmdStop
					HttpBenchStartup(hbWorker, params)
				} else {
					hbWorker.Stop()
				}
			case <-time.After(time.Duration(params.Duration) * time.Millisecond):
				if len(workerAddrList) > 0 {
					verbosePrint(logLevelInfo, "recv timeout signal!!!")
					params.Cmd = cmdStop
					HttpBenchStartup(hbWorker, params)
				}
			}
		}()

		hbResult, err = HttpBenchStartup(hbWorker, params)
		if err != nil {
			verbosePrint(logLevelError, "http bench err: %s", err.Error())
		}

		if hbResult != nil {
			hbResult.print()
		}
	}
}
