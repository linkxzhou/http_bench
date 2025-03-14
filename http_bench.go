package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	_ "net/http/pprof"
	gourl "net/url"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"text/template"
	"time"

	_ "embed"

	"github.com/gorilla/websocket"
	"github.com/quic-go/quic-go/http3"
	"golang.org/x/net/http2"
)

//go:embed index.html
var dashboardHtml string
var globalStop int

// Command types for stress testing control
const (
	cmdStart   int = iota // Start stress testing
	cmdStop               // Stop stress testing
	cmdMetrics            // Get metrics of stress testing
)

// Protocol types supported by the stress tester
const (
	typeHttp1 = "http1" // HTTP/1.1 protocol
	typeHttp2 = "http2" // HTTP/2 protocol
	typeHttp3 = "http3" // HTTP/3 protocol
	typeWs    = "ws"    // WebSocket protocol
	typeWss   = "wss"   // WebSocket Secure protocol
	typeTCP   = "tcp"   // TCP protocol (beta)
	typeGrpc  = "grpc"  // gRPC protocol (planned)
)

// Body format types
const (
	bodyHex = "hex" // Hexadecimal body format
)

// Log levels for verbose output
const (
	vTRACE = iota // Trace level logging
	vDEBUG        // Debug level logging
	vINFO         // Info level logging
	vERROR        // Error level logging
)

// StressParameters stress params for worker
type StressParameters struct {
	SequenceId         int64               `json:"sequence_id"`         // Sequence
	Cmd                int                 `json:"cmd"`                 // Commands
	RequestMethod      string              `json:"request_method"`      // Request Method.
	RequestBody        string              `json:"request_body"`        // Request Body.
	RequestBodyType    string              `json:"request_bodytype"`    // Request BodyType, default string.
	RequestScriptBody  string              `json:"request_script_body"` // Request Script Body.
	RequestType        string              `json:"request_type"`        // Request Type
	N                  int                 `json:"n"`                   // N is the total number of requests to make.
	C                  int                 `json:"c"`                   // C is the concurrency level, the number of concurrent workers to run.
	Duration           int64               `json:"duration"`            // D is the duration for stress test
	Timeout            int                 `json:"timeout"`             // Timeout in ms.
	Qps                int                 `json:"qps"`                 // Qps is the rate limit.
	DisableCompression bool                `json:"disable_compression"` // DisableCompression is an option to disable compression in response
	DisableKeepAlives  bool                `json:"disable_keepalives"`  // DisableKeepAlives is an option to prevents re-use of TCP connections between different HTTP requests
	Headers            map[string][]string `json:"headers"`             // Custom HTTP header.
	Url                string              `json:"url"`                 // Request url.
	Output             string              `json:"output"`              // Output represents the output type. If "csv" is provided, the output will be dumped as a csv stream.
}

func (p *StressParameters) String() string {
	body, err := json.MarshalIndent(p, "", "\t")
	if err != nil {
		return err.Error()
	}
	return string(body)
}

type (
	result struct {
		err           error
		statusCode    int
		duration      time.Duration
		contentLength int64
	}

	StressWorker struct {
		RequestParams             *StressParameters
		resultChan                chan *result
		curResult                 *StressResult  // current worker result
		workersResult             []StressResult // multi workers result
		resultWg                  sync.WaitGroup // Wait some task finish
		totalTime                 time.Duration
		err                       error
		bodyTemplate, urlTemplate *template.Template
		connPool                  *ConnPool
		workerSem                 chan struct{}
	}

	StressClient struct {
		httpClient *http.Client
		wsClient   *websocket.Conn
		tcpClient  *tcpConn
	}

	ConnPool struct {
		clients chan *StressClient
		factory func() *StressClient
		mu      sync.Mutex  // Mutex to protect connection pool operations
		active  int         // Track number of active connections
		maxSize int         // Maximum pool size
	}
)

func (b *StressWorker) Start() {
	b.resultChan = make(chan *result, 2*b.RequestParams.C+1)
	b.workersResult = make([]StressResult, 0)
	b.curResult = GetStressResult()
	b.asyncCollectResult()
	b.startClients()
	verbosePrint(vINFO, "worker finished and waiting result")
}

// Stop stop stress worker and wait coroutine finish
func (b *StressWorker) Stop(wait bool, err error) {
	b.RequestParams.Cmd = cmdStop
	b.err = err
	if wait {
		b.resultWg.Wait()
	}
}

func (b *StressWorker) IsStop() bool {
	return b.RequestParams.Cmd == cmdStop || globalStop == cmdStop
}

func (b *StressWorker) WaitResult() *StressResult {
	b.resultWg.Wait()
	return calculateMultiStressResult(nil, *b.curResult)
}

func (b *StressWorker) WaitWorkersResult() *StressResult {
	b.resultWg.Wait()
	verbosePrint(vDEBUG, "result length = %d", len(b.workersResult))
	return calculateMultiStressResult(nil, b.workersResult...)
}

// Optimize execute function with better random number generation and request counting
func (b *StressWorker) execute(n, sleep int, client *StressClient) {
	var runCounts int = 0
	// Use a dedicated random source for better concurrency
	// Remove or use the random source
	
	for !b.IsStop() {
		if n > 0 && runCounts >= n {
			return
		}

		runCounts++
		if sleep > 0 {
			time.Sleep(time.Duration(sleep) * time.Microsecond)
		}

		t := time.Now()
		code, size, err := b.doClient(client)

		b.resultChan <- &result{
			statusCode:    code,
			duration:      time.Since(t),
			err:           err,
			contentLength: size,
		}

		if err != nil {
			verbosePrint(vERROR, "err: %v", err)
			b.Stop(false, err)
			return
		}
	}
}

func (b *StressWorker) getClient() *StressClient {
	return b.connPool.Get()
}

func (b *StressWorker) closeClient(client *StressClient) {
	b.connPool.Put(client)
}

func (b *StressWorker) asyncCollectResult() {
	b.resultWg.Add(1)

	go func() {
		timeTicker := time.NewTicker(time.Duration(b.RequestParams.Duration) * time.Second)
		defer func() {
			timeTicker.Stop()
			b.resultWg.Done()
		}()

		for {
			select {
			case res, ok := <-b.resultChan:
				if !ok || (res != nil && res.err != nil) {
					b.curResult.Duration = int64(b.totalTime.Seconds())
					if res != nil && res.err != nil {
						b.err = res.err
					}
					return
				}
				b.curResult.append(res)
			case <-timeTicker.C:
				verbosePrint(vINFO, "time ticker upcoming, duration: %ds", b.RequestParams.Duration)
				b.Stop(false, nil) // Time ticker exec Stop commands
			}
		}
	}()
}

func (b *StressWorker) startClients() {
	println("running %d connections, @ %s", b.RequestParams.C, b.RequestParams.Url)

	var (
		wg               sync.WaitGroup
		err              error
		startTime        = time.Now()
		bodyTemplateName = fmt.Sprintf("BODY-%d", b.RequestParams.SequenceId)
		urlTemplateName  = fmt.Sprintf("URL-%d", b.RequestParams.SequenceId)
	)

	// Initialize connection pool with proper size limit
	poolSize := b.RequestParams.C	
	b.connPool = NewConnPool(poolSize, func() *StressClient {
		return b.createNewClient()
	})

	// Initialize worker semaphore
	b.workerSem = make(chan struct{}, b.RequestParams.C)

	if b.urlTemplate, err = template.New(urlTemplateName).Funcs(fnMap).Parse(b.RequestParams.Url); err != nil {
		verbosePrint(vERROR, "parse urls function err: "+err.Error())
	}

	if b.bodyTemplate, err = template.New(bodyTemplateName).Funcs(fnMap).Parse(b.RequestParams.RequestBody); err != nil {
		verbosePrint(vERROR, "parse request body function err: "+err.Error())
	}

	for i := 0; i < b.RequestParams.C && !b.IsStop(); i++ {
		wg.Add(1)
		b.workerSem <- struct{}{}

		go func(workerIndex int) {
			defer wg.Done()
			defer func() { <-b.workerSem }()

			client := b.getClient()
			if client == nil {
				return
			}

			defer func() {
				b.closeClient(client)
				if r := recover(); r != nil {
					verbosePrint(vERROR, "internal err: %v", r)
				}
			}()

			sleep := 0
			if b.RequestParams.Qps > 0 {
				sleep = 1e6 / (b.RequestParams.C * b.RequestParams.Qps)
			}

			// Distribute requests evenly among workers
			requestsPerWorker := b.RequestParams.N / b.RequestParams.C
			if b.RequestParams.N % b.RequestParams.C > 0 && workerIndex < b.RequestParams.N % b.RequestParams.C {
				requestsPerWorker++ // Distribute remainder evenly
			}
			
			b.execute(requestsPerWorker, sleep, client)
		}(i)
	}

	wg.Wait()
	b.Stop(false, nil)
	b.totalTime = time.Since(startTime)
	close(b.resultChan)
}

func (b *StressWorker) doClient(client *StressClient) (int, int64, error) {
    if client == nil {
        return 0, 0, fmt.Errorf("client is nil")
    }

    // 对 TCP 协议进行特殊处理
    if b.RequestParams.RequestType == typeTCP {
        n, err := client.tcpClient.Do([]byte(b.RequestParams.RequestBody))
        if err != nil {
            return 0, 0, fmt.Errorf("tcp write error: %v", err)
        }
        return http.StatusOK, int64(n), nil
    }

    // HTTP 相关协议的处理
    ctx, cancel := context.WithTimeout(context.Background(), 
        time.Duration(b.RequestParams.Timeout)*time.Millisecond)
    defer cancel()

    req, err := http.NewRequestWithContext(ctx, 
        b.RequestParams.RequestMethod, 
        b.RequestParams.Url, 
        b.getRequestBody())
    if err != nil {
        return 0, 0, fmt.Errorf("create request error: %v", err)
    }

    // Set headers
    for k, v := range b.RequestParams.Headers {
        req.Header[k] = v
    }

    // Execute request based on protocol type
    switch b.RequestParams.RequestType {
    case typeHttp1, typeHttp2, typeHttp3:
        resp, err := client.httpClient.Do(req)
        if err != nil {
            return 0, 0, fmt.Errorf("http request error: %v", err)
        }
        defer resp.Body.Close()

        // Optimize content length handling
        contentLength := resp.ContentLength
        if contentLength < 0 {
            contentLength = 0
        }

        // Discard response body to free connections
        _, _ = io.Copy(io.Discard, resp.Body)

        return resp.StatusCode, contentLength, nil

    case typeWs, typeWss:
        err := client.wsClient.WriteMessage(websocket.TextMessage, []byte(b.RequestParams.RequestBody))
        if err != nil {
            return 0, 0, fmt.Errorf("websocket write error: %v", err)
        }

        _, msg, err := client.wsClient.ReadMessage()
        if err != nil {
            return 0, 0, fmt.Errorf("websocket read error: %v", err)
        }

        return http.StatusOK, int64(len(msg)), nil

    default:
        return 0, 0, fmt.Errorf("unsupported protocol type: %s", b.RequestParams.RequestType)
    }
}

func (b *StressWorker) getRequestBody() io.Reader {
    if b.RequestParams.RequestBody == "" {
        return nil
    }

    if b.RequestParams.RequestBodyType == bodyHex {
        decoded, err := hex.DecodeString(b.RequestParams.RequestBody)
        if err != nil {
            verbosePrint(vERROR, "hex decode error: %v", err)
            return nil
        }
        return bytes.NewReader(decoded)
    }
    
    return strings.NewReader(b.RequestParams.RequestBody)
}

func executeStress(params StressParameters) (*StressWorker, *StressResult) {
	var (
		stressTesting        *StressWorker
		stressResult         *StressResult
		isDistributedTesting bool
	)

	if len(workerList) > 0 {
		isDistributedTesting = true
	}

	if v, ok := stressList.Load(params.SequenceId); ok && v != nil {
		stressTesting = v.(*StressWorker)
	} else {
		stressTesting = &StressWorker{RequestParams: &params}
		stressList.Store(params.SequenceId, stressTesting)
	}

	jsonBody, err := json.Marshal(params)
	if err != nil {
		verbosePrint(vERROR, "invalid stress testing params!")
	}

	switch params.Cmd {
	case cmdStart:
		if isDistributedTesting {
			stressTesting.workersResult = waitWorkerListReq(jsonBody)
			stressResult = stressTesting.WaitWorkersResult()
		} else {
			stressTesting.Start()
			stressResult = stressTesting.WaitResult()
		}
		if stressResult != nil && !isDistributedTesting {
			stressResult.print()
		}
		stressList.Delete(params.SequenceId)
	case cmdStop:
		if isDistributedTesting {
			waitWorkerListReq(jsonBody)
		}
		stressTesting.Stop(true, nil)
		stressList.Delete(params.SequenceId)
	case cmdMetrics:
		if isDistributedTesting {
			workersResult := waitWorkerListReq(jsonBody)
			stressResult = calculateMultiStressResult(nil, workersResult...)
		} else {
			if stressTesting.curResult != nil {
				stressResult = calculateMultiStressResult(nil, *stressTesting.curResult)
			}
		}
	}

	if stressTesting.err != nil {
		stressResult.ErrCode = -1
		stressResult.ErrMsg = stressTesting.err.Error()
	}

	return stressTesting, stressResult
}

func serveWorker(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	if reqStr, err := io.ReadAll(r.Body); err == nil {
		var params StressParameters
		var result *StressResult
		if err := json.Unmarshal(reqStr, &params); err != nil {
			verbosePrint(vERROR, "unmarshal body err: %s", err.Error())
			result = &StressResult{
				ErrCode: -1,
				ErrMsg:  err.Error(),
			}
		} else {
			verbosePrint(vDEBUG, "request params: %s", params.String())
			_, result = executeStress(params)
		}

		if result != nil {
			wbody, err := result.marshal()
			if err != nil {
				verbosePrint(vERROR, "marshal result: %v", err)
				return
			}
			w.Write(wbody)
		}
	}
}

var waitWorkerListReq = func(paramsJson []byte) []StressResult {
	var wg sync.WaitGroup
	var stressResult []StressResult

	for _, v := range workerList {
		wg.Add(1)

		addr := fmt.Sprintf("http://%s%s", v, httpWorkerApiPath)
		if strings.Contains(v, "http://") || strings.Contains(v, "https://") {
			addr = fmt.Sprintf("%s%s", v, httpWorkerApiPath)
		}

		go func(workerAddr string) {
			defer wg.Done()
			result, err := executeWorkerReq(workerAddr, paramsJson)
			if err == nil && result != nil {
				stressResult = append(stressResult, *result)
			}
		}(addr)
	}

	wg.Wait()
	return stressResult
}

func executeWorkerReq(uri string, body []byte) (*StressResult, error) {
	verbosePrint(vDEBUG, "request body: %s", string(body))
	resp, err := http.Post(uri, httpContentTypeJSON, bytes.NewBuffer(body)) // default not timeout
	if err != nil {
		verbosePrint(vERROR, "executeWorkerReq addr(%s) err: %s", uri, err.Error())
		return nil, err
	}
	defer resp.Body.Close()

	var result StressResult
	respStr, _ := io.ReadAll(resp.Body)
	err = json.Unmarshal(respStr, &result)
	return &result, err
}

var (
	stressList sync.Map
	workerList flagSlice // Worker mechine addr list.

	headerRegexp = `^([\w-]+):\s*(.+)`
	authRegexp   = `^(.+):([^\s].+)`

	proxyUrl   *gourl.URL
	stopSignal chan os.Signal

	m          = flag.String("m", "GET", "")
	body       = flag.String("body", "", "")
	bodyType   = flag.String("bodytype", "", "")
	authHeader = flag.String("a", "", "")

	output = flag.String("o", "", "") // Output type

	c        = flag.Int("c", 50, "")              // Number of requests to run concurrently
	n        = flag.Int("n", 0, "")               // Number of requests to run
	q        = flag.Int("q", 0, "")               // Rate limit, in seconds (QPS)
	d        = flag.String("d", "10s", "")        // Duration for stress test
	t        = flag.Int("t", 3000, "")            // Timeout in ms
	httpType = flag.String("http", typeHttp1, "") // HTTP Version
	pType    = flag.String("p", "", "")           // TCP/UDP Type

	printExample = flag.Bool("example", false, "")

	cpus = flag.Int("cpus", runtime.GOMAXPROCS(-1), "")

	disableCompression = flag.Bool("disable-compression", false, "")
	disableKeepAlives  = flag.Bool("disable-keepalive", false, "")
	proxyAddr          = flag.String("x", "", "")

	urlstr    = flag.String("url", "", "")
	verbose   = flag.Int("verbose", 3, "")
	listen    = flag.String("listen", "", "")
	dashboard = flag.String("dashboard", "", "")

	urlFile    = flag.String("url-file", "", "")
	bodyFile   = flag.String("body-file", "", "")
	scriptFile = flag.String("script", "", "")

	http3Pool *x509.CertPool
)

const (
	usage = `Usage: http_bench [options...] <url>
Options:
	-n  Number of requests to run.
	-c  Number of requests to run concurrently. Total number of requests cannot
		be smaller than the concurrency level.
	-q  Rate limit, in queries per second (QPS).
	-d  Duration of the stress test, e.g. 2s, 2m, 2h
	-t  Timeout in ms (default 3000ms).
	-o  Output type. If none provided, a summary is printed.
		"csv" is the only supported alternative. Dumps the response
		metrics in comma-separated values format.
	-m  HTTP method, one of GET, POST, PUT, DELETE, HEAD, OPTIONS.
	-H  Custom HTTP header. You can specify as many as needed by repeating the flag.
		For example, -H "Accept: text/html" -H "Content-Type: application/xml"
	-http  		Support protocol http1, http2, http3, ws, wss (default http1).
	-body  		Request body, default empty.
	-bodytype   Request body type, support string, hex (default string).
	-a  		Basic authentication, username:password.
	-x  		HTTP Proxy address as host:port.
	-disable-compression  Disable compression.
	-disable-keepalive    Disable keep-alive, prevents re-use of TCP connections between different HTTP requests.
	-cpus		Number of used CPU cores. (default for current machine is %d cores).
	-url		Request single url.
	-verbose 	Print detail logs, default 3(0:TRACE, 1:DEBUG, 2:INFO, 3:ERROR).
	-url-file 	Read url list from file and random stress test.
	-body-file	Request body from file.
	-listen 	Listen IP:PORT for distributed stress test and worker node (default empty). e.g. "127.0.0.1:12710".
	-dashboard 	Listen dashboard IP:PORT and operate stress params on browser.
	-w/W		Running distributed stress test worker node list. e.g. -w "127.0.0.1:12710" -W "127.0.0.1:12711".
	-example 	Print some stress test examples (default false).`

	examples = `
1. Basic stress test:
	./http_bench -n 1000 -c 10 -t 3000 -m GET "http://127.0.0.1/test1"
	./http_bench -n 1000 -c 10 -t 3000 -m GET "http://127.0.0.1/test1" -url-file urls.txt
	./http_bench -d 10s -c 10 -m POST -body '{"key":"value"}' -url-file urls.txt

2. HTTP/2 test:
	./http_bench -d 10s -c 10 -http http2 -m POST "https://127.0.0.1/test1" -body '{"key":"value"}'

3. HTTP/3 test:
	./http_bench -d 10s -c 10 -http http3 -m POST "https://127.0.0.1/test1" -body '{"key":"value"}'

4. WebSocket test:
	./http_bench -d 10s -c 10 -http ws "ws://127.0.0.1/ws" -body '{"message":"hello"}'

5. Dashboard mode:
	./http_bench -dashboard "127.0.0.1:12345" -verbose 1

6. Template function test:
	./http_bench -c 1 -n 1 "https://127.0.0.1:18090?data={{ randomString 10 }}" -verbose 0

7. Distributed stress test:
	# Start worker node
	./http_bench -listen "127.0.0.1:12710" -verbose 1
	
	# Start controller with worker reference
	./http_bench -c 100 -d 10s "http://127.0.0.1:18090/test1" -body '{"key":"value"}' -verbose 1 -W "127.0.0.1:12710"

8. Advanced options:
	./http_bench -n 1000 -c 50 -q 100 -t 5000 -m POST -H "Content-Type: application/json" -H "Authorization: Bearer token123" "https://api.example.com/endpoint"`
)

func main() {
	flag.Usage = func() {
		fmt.Println(fmt.Sprintf(usage, runtime.NumCPU()))
	}

	var params StressParameters
	var headerslice flagSlice

	flag.Var(&headerslice, "H", "") // Custom HTTP header
	flag.Var(&workerList, "W", "")  // Worker mechine, support W/w
	flag.Var(&workerList, "w", "")
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
		switch t := strings.ToLower(*httpType); t {
		case typeHttp1, typeHttp2, typeWs, typeWss:
			params.RequestType = t
		case typeHttp3:
			params.RequestType = t
			if http3Pool, err = x509.SystemCertPool(); err != nil {
				panic(typeHttp3 + " err: " + err.Error())
			}
		default:
			usageAndExit("not support -http: " + *httpType)
		}
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
		if proxyUrl, err = gourl.Parse(*proxyAddr); err != nil {
			usageAndExit(err.Error())
		}
	}

	var mainServer *http.Server
	_, mainCancel := context.WithCancel(context.Background())

	// decrease go gc rate
	stressGOGC := getEnv("STRESS_GOGC")
	if n, err := strconv.ParseInt(stressGOGC, 2, 64); err == nil {
		debug.SetGCPercent(int(n))
	}

	// cloud worker API
	stressWorkerAPI := getEnv("STRESS_WORKERAPI")
	if stressWorkerAPI != "" {
		dashboardHtml = strings.ReplaceAll(dashboardHtml, "/api", stressWorkerAPI)
	}

	if len(*dashboard) > 0 {
		*listen = *dashboard
	}

	if len(*listen) > 0 {
		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(dashboardHtml)) // export dashboard index.html
		})
		mux.HandleFunc(httpWorkerApiPath, serveWorker)
		mainServer = &http.Server{
			Addr:    *listen,
			Handler: mux,
		}
		println("listen %s, and you can open http://%s/index.html on browser", *listen, *listen)
		if err := mainServer.ListenAndServe(); err != nil {
			verbosePrint(vERROR, "listen err: %s", err.Error())
		}
		return
	}

	if len(requestUrls) <= 0 {
		usageAndExit("url or url-file empty.")
	}

	for _, url := range requestUrls {
		params.Url = url
		params.SequenceId = time.Now().Unix()
		params.Cmd = cmdStart

		verbosePrint(vDEBUG, "request params: %s", params.String())
		stopSignal = make(chan os.Signal)
		signal.Notify(stopSignal, syscall.SIGINT, syscall.SIGTERM)

		var stressTesting *StressWorker
		var stressResult *StressResult

		go func() {
			<-stopSignal
			verbosePrint(vINFO, "recv stop signal")
			params.Cmd = cmdStop // stop workers
			globalStop = cmdStop // stop all
			jsonBody, _ := json.Marshal(params)
			waitWorkerListReq(jsonBody)
			mainCancel()
		}()

		if stressTesting, stressResult = executeStress(params); stressResult != nil {
			close(stopSignal)
			stressTesting.Stop(true, nil) // recv stop signal and stop commands
			stressResult.print()
		}
	}
}

func NewConnPool(maxSize int, factory func() *StressClient) *ConnPool {
	return &ConnPool{
		clients: make(chan *StressClient, maxSize),
		factory: factory,
		maxSize: maxSize,
	}
}

func (p *ConnPool) Get() *StressClient {
    p.mu.Lock()
    defer p.mu.Unlock()
    
    select {
    case client := <-p.clients:
        if client != nil {
            p.active++
            return client
        }
    default:
        if p.active < p.maxSize {
            p.active++
            return p.factory()
        }
    }
    return nil
}

func (p *ConnPool) Put(client *StressClient) {
    if client == nil {
        return
    }
    
    p.mu.Lock()
    defer p.mu.Unlock()
    
    select {
    case p.clients <- client:
        // Successfully returned to connection pool
    default:
        // Connection pool is full, close the connection
        p.closeClient(client)
    }
    p.active--
}

func (p *ConnPool) closeClient(client *StressClient) {
    if client.httpClient != nil {
        client.httpClient.CloseIdleConnections()
    }
    if client.wsClient != nil {
        client.wsClient.Close()
    }
    if client.tcpClient != nil {
        client.tcpClient.Close()
    }
}

func (b *StressWorker) createNewClient() *StressClient {
    client := &StressClient{}
    
    // 对 TCP 协议进行特殊处理
    if b.RequestParams.RequestType == typeTCP {
        c, err := DialTCP(b.RequestParams.Url, ConnOption{
            Timeout:           time.Duration(b.RequestParams.Timeout) * time.Millisecond,
            DisableKeepAlives: b.RequestParams.DisableKeepAlives,
        })
        if err != nil || c == nil {
            verbosePrint(vERROR, "tcp dial error: %s", err)
            return nil
        }
        client.tcpClient = c
        return client
    }

    // 其他协议的处理
    switch b.RequestParams.RequestType {
    case typeHttp3:
        client.httpClient = &http.Client{
            Timeout: time.Duration(b.RequestParams.Timeout) * time.Millisecond,
            Transport: &http3.RoundTripper{
                TLSClientConfig: &tls.Config{
                    RootCAs:            http3Pool,
                    InsecureSkipVerify: true,
                },
            },
        }
    case typeHttp2:
        client.httpClient = &http.Client{
            Timeout: time.Duration(b.RequestParams.Timeout) * time.Millisecond,
            Transport: &http2.Transport{
                TLSClientConfig: &tls.Config{
                    InsecureSkipVerify: true,
                },
                DisableCompression: b.RequestParams.DisableCompression,
                AllowHTTP:          true,
                MaxReadFrameSize:   1 << 20, // 1MB
                StrictMaxConcurrentStreams: true,
            },
        }
    case typeHttp1:
        // Optimize HTTP/1.1 transport settings
        tr := &http.Transport{
            TLSClientConfig: &tls.Config{
                InsecureSkipVerify: true,
            },
            DisableCompression:  b.RequestParams.DisableCompression,
            DisableKeepAlives:   b.RequestParams.DisableKeepAlives,
            TLSHandshakeTimeout: time.Duration(b.RequestParams.Timeout) * time.Millisecond,
            TLSNextProto:        make(map[string]func(string, *tls.Conn) http.RoundTripper),
            DialContext: (&net.Dialer{
                Timeout:   time.Duration(b.RequestParams.Timeout) * time.Millisecond,
                KeepAlive: 60 * time.Second,
            }).DialContext,
            MaxIdleConns:        100,
            MaxIdleConnsPerHost: 100,
            MaxConnsPerHost:     100,
            IdleConnTimeout:     90 * time.Second,
            ResponseHeaderTimeout: time.Duration(b.RequestParams.Timeout) * time.Millisecond,
            ExpectContinueTimeout: 1 * time.Second,
        }
        if proxyUrl != nil {
            tr.Proxy = http.ProxyURL(proxyUrl)
        }
        client.httpClient = &http.Client{
            Timeout:   time.Duration(b.RequestParams.Timeout) * time.Millisecond,
            Transport: tr,
        }
    case typeWs, typeWss:
        c, _, err := websocket.DefaultDialer.Dial(b.RequestParams.Url, b.RequestParams.Headers)
        if err != nil || c == nil {
            verbosePrint(vERROR, "websocket err: %v", err)
            return nil
        }
        client.wsClient = c
    case typeTCP:
        c, err := DialTCP(b.RequestParams.Url, ConnOption{
            Timeout:           time.Duration(b.RequestParams.Duration) * time.Second,
            DisableKeepAlives: b.RequestParams.DisableKeepAlives,
        })
        if err != nil || c == nil {
            verbosePrint(vERROR, "tcp err: %s", err)
            return nil
        }
        client.tcpClient = c
    default:
        verbosePrint(vERROR, "not support %s", b.RequestParams.RequestType)
        return nil
    }

    return client
}
