package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/rand"
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

const (
	cmdStart int = iota
	cmdStop
	cmdMetrics

	typeHttp1 = "http1"
	typeHttp2 = "http2"
	typeHttp3 = "http3"
	typeWs    = "ws"
	typeWss   = "wss"
	typeTCP   = "tcp"  // TODO: fix next version
	typeGrpc  = "grpc" // TODO: next version to support

	bodyHex = "hex" // hex body to request

	vTRACE = 0
	vDEBUG = 1
	vINFO  = 2
	vERROR = 3
)

type flagSlice []string

func (h *flagSlice) String() string {
	return fmt.Sprintf("%s", *h)
}

func (h *flagSlice) Set(value string) error {
	*h = append(*h, value)
	return nil
}

// StressParameters stress params for worker
type StressParameters struct {
	SequenceId         int64               `json:"sequence_id"`         // Sequence
	Cmd                int                 `json:"cmd"`                 // Commands
	RequestMethod      string              `json:"request_method"`      // Request Method.
	RequestBody        string              `json:"request_body"`        // Request Body.
	RequestBodyType    string              `json:"request_bodytype"`    // Request BodyType.
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
		results                   chan *result
		resultList                []StressResult
		currentResult             StressResult
		totalTime                 time.Duration
		wg                        sync.WaitGroup // Wait some task finish
		err                       error
		bodyTemplate, urlTemplate *template.Template
	}
)

func (b *StressWorker) Start() {
	b.results = make(chan *result, 2*b.RequestParams.C+1)
	b.resultList = make([]StressResult, 0)
	b.collectReport()
	b.runWorkers()
	verbosePrint(vINFO, "worker finished and wait result")
}

// Stop stop stress worker and wait coroutine finish
func (b *StressWorker) Stop(wait bool, err error) {
	b.RequestParams.Cmd = cmdStop
	b.err = err
	if wait {
		b.wg.Wait()
	}
}

func (b *StressWorker) IsStop() bool {
	return b.RequestParams.Cmd == cmdStop
}

func (b *StressWorker) Append(result ...StressResult) {
	b.resultList = append(b.resultList, result...)
}

func (b *StressWorker) Wait() *StressResult {
	b.wg.Wait()

	if b.resultList == nil || len(b.resultList) <= 0 {
		fmt.Fprintf(os.Stderr, "internal err: stress test result empty\n")
		return nil
	}

	b.resultList[0].combine(b.resultList[1:]...)
	verbosePrint(vDEBUG, "result length = %d", len(b.resultList))
	return &(b.resultList[0])
}

func (b *StressWorker) runWorker(n, sleep int, client *StressClient) {
	var runCounts int = 0
	// random set seed
	rand.Seed(time.Now().UnixNano())
	for !b.IsStop() {
		if n > 0 && runCounts > n {
			return
		}

		runCounts++
		if sleep > 0 {
			time.Sleep(time.Duration(sleep) * time.Microsecond)
		}

		var t = time.Now()
		code, size, err := b.doClient(client)
		if err != nil {
			verbosePrint(vERROR, "err: %v", err)
			b.Stop(false, err)
			return
		}

		b.results <- &result{
			statusCode:    code,
			duration:      time.Now().Sub(t),
			err:           err,
			contentLength: size,
		}
	}
}

func (b *StressWorker) runWorkers() {
	fmt.Printf("running %d connections, @ %s\n", b.RequestParams.C, b.RequestParams.Url)
	var (
		wg               sync.WaitGroup
		err              error
		start            = time.Now()
		bodyTemplateName = fmt.Sprintf("BODY-%d", b.RequestParams.SequenceId)
		urlTemplateName  = fmt.Sprintf("URL-%d", b.RequestParams.SequenceId)
	)

	if b.urlTemplate, err = template.New(urlTemplateName).Funcs(fnMap).Parse(b.RequestParams.Url); err != nil {
		verbosePrint(vERROR, "parse urls function err: "+err.Error()+"")
	}

	if b.bodyTemplate, err = template.New(bodyTemplateName).Funcs(fnMap).Parse(b.RequestParams.RequestBody); err != nil {
		verbosePrint(vERROR, "parse request body function err: "+err.Error()+"")
	}

	// ignore the case where b.RequestParams.N % b.RequestParams.C != 0.
	for i := 0; i < b.RequestParams.C && !(b.IsStop()); i++ {
		wg.Add(1)
		go func() {
			client := b.getClient()
			if client == nil {
				return
			}

			defer func() {
				b.closeClient(client)
				wg.Done()
				if r := recover(); r != nil {
					fmt.Fprintf(os.Stderr, "internal err: %v\n", r)
				}
			}()

			sleep := 0
			if b.RequestParams.Qps > 0 {
				sleep = 1e6 / (b.RequestParams.Qps * b.RequestParams.C) // sleep XXus send request
			}
			b.runWorker(b.RequestParams.N/b.RequestParams.C, sleep, client)
		}()
	}

	wg.Wait()
	b.Stop(false, nil)
	b.totalTime = time.Now().Sub(start)
	close(b.results)
}

func (b *StressWorker) getClient() *StressClient {
	client := &StressClient{}
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
			},
		}
	case typeHttp1:
		tr := &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
			DisableCompression:  b.RequestParams.DisableCompression,
			DisableKeepAlives:   b.RequestParams.DisableKeepAlives,
			TLSHandshakeTimeout: time.Duration(b.RequestParams.Timeout) * time.Millisecond,
			TLSNextProto:        make(map[string]func(string, *tls.Conn) http.RoundTripper),
			DialContext: (&net.Dialer{
				Timeout:   time.Duration(b.RequestParams.Timeout) * time.Second,
				KeepAlive: time.Duration(60) * time.Second,
			}).DialContext,
			MaxIdleConns:        10,
			MaxIdleConnsPerHost: 10,
			MaxConnsPerHost:     10,
			IdleConnTimeout:     time.Duration(90) * time.Second,
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
		c, err := DialTCP(b.RequestParams.Url, b.RequestParams.Timeout)
		if err != nil || c == nil {
			verbosePrint(vERROR, "tcp err: %s", err)
			return nil
		}
		client.tcpClient = c
	default:
		// pass
	}

	return client
}

func (b *StressWorker) doClient(client *StressClient) (code int, size int64, err error) {
	var urlBytes, bodyBytes bytes.Buffer
	var url = b.RequestParams.Url

	if b.urlTemplate != nil && len(url) > 0 {
		b.urlTemplate.Execute(&urlBytes, nil)
	} else {
		urlBytes.WriteString(url)
	}

	switch b.RequestParams.RequestBodyType {
	case bodyHex:
		hexb, hexbErr := hex.DecodeString(b.RequestParams.RequestBody)
		if hexbErr != nil {
			return -1, 0, errors.New("invalid hex: " + hexbErr.Error())
		}
		bodyBytes.Write(hexb)
	default:
		if len(b.RequestParams.RequestBody) > 0 && b.bodyTemplate != nil {
			b.bodyTemplate.Execute(&bodyBytes, nil)
		} else {
			bodyBytes.WriteString(b.RequestParams.RequestBody)
		}
	}

	verbosePrint(vTRACE, "request url: %s, request type: %s, request bodytype: %s",
		urlBytes.String(), b.RequestParams.RequestType, b.RequestParams.RequestBodyType)
	verbosePrint(vTRACE, "request body: %s", bodyBytes.String())

	switch b.RequestParams.RequestType {
	case typeHttp1, typeHttp2, typeHttp3:
		req, reqErr := http.NewRequest(b.RequestParams.RequestMethod, urlBytes.String(), strings.NewReader(bodyBytes.String()))
		if reqErr != nil || req == nil {
			err = errors.New("request err: " + err.Error())
			return
		}
		req.Header = b.RequestParams.Headers
		resp, respErr := client.httpClient.Do(req)
		if respErr == nil {
			size = resp.ContentLength
			code = resp.StatusCode
			defer resp.Body.Close()
			if n, _ := fastRead(resp.Body, true); size <= 0 {
				size = n
			}
		}
		err = respErr
	case typeWs:
		if err = client.wsClient.WriteMessage(websocket.TextMessage, bodyBytes.Bytes()); err != nil {
			return
		}
		_, message, readErr := client.wsClient.ReadMessage()
		if readErr != nil {
			err = readErr
			return
		}
		size = int64(len(message))
		code = http.StatusOK
	case typeTCP:
		if size, err = client.tcpClient.Do(bodyBytes.Bytes()); err != nil {
			return
		}
		code = http.StatusOK
	default:
		// pass
	}

	return
}

func (b *StressWorker) closeClient(client *StressClient) {
	switch b.RequestParams.RequestType {
	case typeHttp1, typeHttp2, typeHttp3:
		client.httpClient.CloseIdleConnections()
	case typeWs:
		client.wsClient.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	case typeTCP:
		client.tcpClient.Close()
	default:
		// pass
	}
}

type StressClient struct {
	httpClient *http.Client
	wsClient   *websocket.Conn
	tcpClient  *tcpConn
}

func (b *StressWorker) collectReport() {
	b.wg.Add(1)

	go func() {
		timeTicker := time.NewTicker(time.Duration(b.RequestParams.Duration) * time.Second)
		defer func() {
			timeTicker.Stop()
			b.wg.Done()
		}()

		b.currentResult = StressResult{
			ErrorDist:      make(map[string]int, 0),
			StatusCodeDist: make(map[int]int, 0),
			Lats:           make(map[string]int64, 0),
			Slowest:        int64(IntMin),
			Fastest:        int64(IntMax),
		}

		for {
			select {
			case res, ok := <-b.results:
				if !ok {
					b.currentResult.Duration = int64(b.totalTime.Seconds() * scaleNum)
					b.resultList = append(b.resultList, b.currentResult)
					return
				}
				b.currentResult.result(res)
			case <-timeTicker.C:
				verbosePrint(vINFO, "time ticker upcoming, duration: %ds", b.RequestParams.Duration)
				b.Stop(false, nil) // Time ticker exec Stop commands
			}
		}
	}()
}

func usageAndExit(msg string) {
	if msg != "" {
		fmt.Fprintf(os.Stderr, msg+"\n")
	}
	flag.Usage()
	fmt.Fprintf(os.Stderr, "\n")
	os.Exit(1)
}

func verbosePrint(level int, vfmt string, args ...interface{}) {
	if *verbose > level {
		return
	}

	switch level {
	case vTRACE:
		fmt.Printf("[TRACE] "+vfmt+"\n", args...)
	case vDEBUG:
		fmt.Printf("[DEBUG] "+vfmt+"\n", args...)
	case vINFO:
		fmt.Printf("[INFO] "+vfmt+"\n", args...)
	default:
		fmt.Printf("[ERROR] "+vfmt+"\n", args...)
	}
}

func runStress(params StressParameters, stressTestPtr **StressWorker) *StressResult {
	var stressResult *StressResult
	var stressTest *StressWorker

	if v, ok := stressList.Load(params.SequenceId); ok && v != nil {
		stressTest = v.(*StressWorker)
	} else {
		stressTest = &StressWorker{RequestParams: &params}
		stressList.Store(params.SequenceId, stressTest)
	}

	*stressTestPtr = stressTest
	switch params.Cmd {
	case cmdStart:
		if len(workerList) > 0 {
			jsonBody, _ := json.Marshal(params)
			resultList := requestWorkerList(jsonBody, stressTest)
			stressTest.Append(resultList...)
		} else {
			stressTest.Start()
		}
		stressResult = stressTest.Wait()
		if stressResult != nil {
			stressResult.print()
		}
		stressList.Delete(params.SequenceId)
	case cmdStop:
		if len(workerList) > 0 {
			jsonBody, _ := json.Marshal(params)
			requestWorkerList(jsonBody, stressTest)
		}
		stressTest.Stop(true, nil)
		stressList.Delete(params.SequenceId)
	case cmdMetrics:
		if len(workerList) > 0 {
			jsonBody, _ := json.Marshal(params)
			if resultList := requestWorkerList(jsonBody, stressTest); len(resultList) > 0 {
				stressResult = &StressResult{}
				for i := 0; i < len(resultList); i++ {
					stressResult.LatsTotal += resultList[i].LatsTotal
				} // TODO: assign other variable
			}
		} else {
			stressResult = &stressTest.currentResult
		}
	}

	if stressTest.err != nil {
		stressResult.ErrCode = -1
		stressResult.ErrMsg = stressTest.err.Error()
	}

	return stressResult
}

func handleWorker(w http.ResponseWriter, r *http.Request) {
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
			fmt.Fprintf(os.Stderr, "unmarshal body err: %s\n", err.Error())
			result = &StressResult{
				ErrCode: -1,
				ErrMsg:  err.Error(),
			}
		} else {
			verbosePrint(vDEBUG, "request params: %s", params.String())
			var stressWorker *StressWorker
			result = runStress(params, &stressWorker)
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

func requestWorker(uri string, body []byte) (*StressResult, error) {
	verbosePrint(vDEBUG, "request body: %s", string(body))
	resp, err := http.Post(uri, httpContentTypeJSON, bytes.NewBuffer(body))
	if err != nil {
		fmt.Fprintf(os.Stderr, "requestWorker addr(%s), err: %s\n", uri, err.Error())
		return nil, err
	}
	defer resp.Body.Close()
	var result StressResult
	respStr, _ := io.ReadAll(resp.Body)
	err = json.Unmarshal(respStr, &result)
	return &result, err
}

func joinHttpUri(addr string) string {
	return fmt.Sprintf("http://%s%s", addr, httpWorkerApiPath)
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

	urlFile           = flag.String("url-file", "", "")
	bodyFile          = flag.String("body-file", "", "")
	scriptFile        = flag.String("script", "", "")
	requestWorkerList = func(paramsJson []byte, stressTest *StressWorker) []StressResult {
		var wg sync.WaitGroup
		var stressResult []StressResult

		for _, v := range workerList {
			wg.Add(1)
			go func(workerAddr string) {
				defer wg.Done()
				if result, err := requestWorker(joinHttpUri(workerAddr), paramsJson); err == nil {
					stressResult = append(stressResult, *result)
				}
			}(v)
		}

		wg.Wait()
		return stressResult
	}

	http3Pool *x509.CertPool
)

const (
	usage = `Usage: http_bench [options...] <url>
Options:
	-n  Number of requests to run.
	-c  Number of requests to run concurrently. Total number of requests cannot
		be smaller than the concurency level.
	-q  Rate limit, in seconds (QPS).
	-d  Duration of the stress test, e.g. 2s, 2m, 2h
	-t  Timeout in ms (default 3000ms).
	-o  Output type. If none provided, a summary is printed.
		"csv" is the only supported alternative. Dumps the response
		metrics in comma-seperated values format.
	-m  HTTP method, one of GET, POST, PUT, DELETE, HEAD, OPTIONS.
	-H  Custom HTTP header. You can specify as many as needed by repeating the flag.
		for example, -H "Accept: text/html" -H "Content-Type: application/xml", 
		but "Host: ***", replace that with -host.
	-http  		Support protocol http1, http2, ws, wss (default http1).
	-body  		Request body, default empty.
	-bodytype   Request body type, support string, hex (default string).
	-a  		Basic authentication, username:password.
	-x  		HTTP Proxy address as host:port.
	-disable-compression  Disable compression.
	-disable-keepalive    Disable keep-alive, prevents re-use of TCP connections between different HTTP requests.
	-cpus		Number of used cpu cores. (default for current machine is %d cores).
	-url		Request single url.
	-verbose 	Print detail logs, default 3(0:TRACE, 1:DEBUG, 2:INFO, 3:ERROR).
	-url-file 	Read url list from file and random stress test.
	-body-file	Request body from file.
	-listen 	Listen IP:PORT for distributed stress test and worker node (default empty). e.g. "127.0.0.1:12710".
	-dashboard 	Listen dashboard IP:PORT and operate stress params on browser.
	-w/W		Running distributed stress test worker node list. e.g. -w "127.0.0.1:12710" -W "127.0.0.1:12711".
	-example 	Print some stress test examples (default false).`

	examples = `1.Example stress test:
	./http_bench -n 1000 -c 10 -t 3000 -m GET "http://127.0.0.1/test1"
	./http_bench -n 1000 -c 10 -t 3000 -m GET "http://127.0.0.1/test1" -url-file urls.txt
	./http_bench -d 10s -c 10 -m POST -body "{}" -url-file urls.txt

2.Example http2 test:
	./http_bench -d 10s -c 10 -http http2 -m POST "http://127.0.0.1/test1" -body "{}"

3.Example http3 test:
	./http_bench -d 10s -c 10 -http http3 -m POST "http://127.0.0.1/test1" -body "{}"

4.Example dashboard test:
	./http_bench -dashboard "127.0.0.1:12345" -verbose 1

5.Example support function and variable test:
	./http_bench -c 1 -n 1 "https://127.0.0.1:18090?data={{ randomString 10}}" -verbose 0

6.Example distributed stress test:
	(1) ./http_bench -listen "127.0.0.1:12710" -verbose 1
	(2) ./http_bench -c 1 -d 10s "http://127.0.0.1:18090/test1" -body "{}" -verbose 1 -W "127.0.0.1:12710"`
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
		fmt.Printf(examples + "\n")
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
	if *urlFile == "" && len(*urlstr) > 0 {
		requestUrls = append(requestUrls, *urlstr)
	} else if len(*urlFile) > 0 {
		var err error
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
		switch strings.ToLower(*httpType) {
		case typeHttp1, typeHttp2, typeWs, typeWss:
			params.RequestType = strings.ToLower(*httpType)
		case typeHttp3:
			params.RequestType = strings.ToLower(*httpType)
			var err error
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

	if *output != "csv" && *output != "" {
		usageAndExit("invalid output type; only csv is supported.")
	}

	// set request timeout
	params.Timeout = *t

	if *proxyAddr != "" {
		var err error
		if proxyUrl, err = gourl.Parse(*proxyAddr); err != nil {
			usageAndExit(err.Error())
		}
	}

	var mainServer *http.Server
	_, mainCancel := context.WithCancel(context.Background())

	// decrease go gc rate
	benchGOGC := getEnv("BENCH_GOGC")
	if n, err := strconv.ParseInt(benchGOGC, 2, 64); err == nil {
		debug.SetGCPercent(int(n))
	}

	if *listen != "" || *dashboard != "" {
		mux := http.NewServeMux()
		if *dashboard != "" { // if startup dashboard and return index.html
			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(dashboardHtml)) // export dashboard index.html
			})
			*listen = *dashboard
		}
		mux.HandleFunc(httpWorkerApiPath, handleWorker)
		mainServer = &http.Server{
			Addr:    *listen,
			Handler: mux,
		}
		if err := mainServer.ListenAndServe(); err != nil {
			fmt.Fprintf(os.Stderr, "listen err: %s\n", err.Error())
		}
	} else {
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

			var stressTest *StressWorker
			var stressResult *StressResult

			go func() {
				<-stopSignal
				verbosePrint(vINFO, "recv stop signal")
				params.Cmd = cmdStop
				jsonBody, _ := json.Marshal(params)
				requestWorkerList(jsonBody, stressTest)
				stressTest.Stop(true, nil) // recv stop signal and stop commands
				mainCancel()
			}()

			if stressResult = runStress(params, &stressTest); stressResult != nil {
				close(stopSignal)
				stressResult.print()
			}
		}
	}
}
