package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net"
	"net/http"
	_ "net/http/pprof"
	gourl "net/url"
	"os"
	"os/signal"
	"regexp"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/net/http2"
)

const (
	CMD_START int = iota
	CMD_STOP

	SCALE_NUM = 10000

	TYPE_HTTP1 = "http1"
	TYPE_HTTP2 = "http2"
	TYPE_HTTP3 = "http3"

	VERBOSE_TRACE = 0
	VERBOSE_DEBUG = 1
	VERBOSE_INFO  = 2
)

type flagSlice []string

func (h *flagSlice) String() string {
	return fmt.Sprintf("%s", *h)
}

func (h *flagSlice) Set(value string) error {
	*h = append(*h, value)
	return nil
}

type StressResult struct {
	ErrCode  int    `json:"err_code"`
	ErrMsg   string `json:"err_msg"`
	AvgTotal int64  `json:"avg_total"`
	Fastest  int64  `json:"fastest"`
	Slowest  int64  `json:"slowest"`
	Average  int64  `json:"average"`
	Rps      int64  `json:"rps"`

	ErrorDist      map[string]int   `json:"error_dist"`
	StatusCodeDist map[int]int      `json:"status_code_dist"`
	Lats           map[string]int64 `json:"lats"`
	LatsTotal      int64            `json:"lats_total"`
	SizeTotal      int64            `json:"size_total"`
	Duration       int64            `json:"duration"`
	Output         string           `json:"output"`
}

func (result *StressResult) print() {
	switch result.Output {
	case "csv":
		fmt.Printf("Duration,Count\n")
		for duration, val := range result.Lats {
			fmt.Printf("%s,%d\n", duration, val/SCALE_NUM)
		}
		return
	default:
	}

	if len(result.Lats) > 0 {
		fmt.Printf("\nSummary:\n")
		fmt.Printf("  Total:\t%4.3f secs\n", float32(result.Duration)/SCALE_NUM)
		fmt.Printf("  Slowest:\t%4.3f secs\n", float32(result.Slowest)/SCALE_NUM)
		fmt.Printf("  Fastest:\t%4.3f secs\n", float32(result.Fastest)/SCALE_NUM)
		fmt.Printf("  Average:\t%4.3f secs\n", float32(result.Average)/SCALE_NUM)
		fmt.Printf("  Requests/sec:\t%4.3f\n", float32(result.Rps)/SCALE_NUM)
		if result.SizeTotal > 1073741824 {
			fmt.Printf("  Total data:\t%4.3f GB\n", float64(result.SizeTotal)/1073741824)
		} else if result.SizeTotal > 1024*1024 {
			fmt.Printf("  Total data:\t%4.3f MB\n", float64(result.SizeTotal)/1048576)
		} else if result.SizeTotal > 1024 {
			fmt.Printf("  Total data:\t%4.3f KB\n", float64(result.SizeTotal)/1024)
		} else if result.SizeTotal > 0 {
			fmt.Printf("  Total data:\t%4.3f bytes\n", float64(result.SizeTotal))
		} else {
		}
		fmt.Printf("  Size/request:\t%d bytes\n", result.SizeTotal/result.LatsTotal)
		result.printStatusCodes()
		result.printLatencies()
	}

	if len(result.ErrorDist) > 0 {
		result.printErrors()
	}
}

// Print latency distribution.
func (result *StressResult) printLatencies() {
	pctls := []int{10, 25, 50, 75, 90, 95, 99}
	data := make([]string, len(pctls))
	durationLats := make([]string, 0)
	for duration, _ := range result.Lats {
		durationLats = append(durationLats, duration)
	}
	sort.Strings(durationLats)
	var (
		j       int   = 0
		current int64 = 0
	)
	for i := 0; i < len(durationLats) && j < len(pctls); i++ {
		current = current + result.Lats[durationLats[i]]
		if int(current*100/result.LatsTotal) >= pctls[j] {
			data[j] = durationLats[i]
			j++
		}
	}
	fmt.Printf("\nLatency distribution:\n")
	for i := 0; i < len(pctls); i++ {
		fmt.Printf("  %v%% in %s secs\n", pctls[i], data[i])
	}
}

// Print status code distribution.
func (result *StressResult) printStatusCodes() {
	fmt.Printf("\nStatus code distribution:\n")
	for code, num := range result.StatusCodeDist {
		fmt.Printf("  [%d]\t%d responses\n", code, num)
	}
}

func (result *StressResult) printErrors() {
	fmt.Printf("\nError distribution:\n")
	for err, num := range result.ErrorDist {
		fmt.Printf("  [%d]\t%s\n", num, err)
	}
}

type StressParameters struct {
	// Sequence
	SequenceId int64 `json:"sequence_id"`
	// Commands
	Cmd int `json:"cmd"`
	// Request Method.
	RequestMethod string `json:"request_method"`
	// Request Body.
	RequestBody string `json:"request_body"`
	// Request HTTP Type
	RequestHttpType string `json:"request_httptype"`
	// N is the total number of requests to make.
	N int `json:"n"`
	// C is the concurrency level, the number of concurrent workers to run.
	C int `json:"c"`
	// D is the duration for stress test
	Duration int64 `json:"duration"`
	// Timeout in ms.
	Timeout int `json:"timeout"`
	// Qps is the rate limit.
	Qps int `json:"qps"`
	// DisableCompression is an option to disable compression in response
	DisableCompression bool `json:"disable_compression"`
	// DisableKeepAlives is an option to prevents re-use of TCP connections between different HTTP requests
	DisableKeepAlives bool `json:"disable_keepalives"`
	// Basic authentication, username:password.
	AuthUsername string `json:"auth_username"`
	AuthPassword string `json:"auth_password"`
	// Custom HTTP header.
	Headers map[string][]string `json:"headers"`
	Urls    []string            `json:"urls"`
	// Output represents the output type. If "csv" is provided, the
	// output will be dumped as a csv stream.
	Output string `json:"output"`
}

func (p *StressParameters) String() string {
	if body, err := json.MarshalIndent(p, "", "\t"); err != nil {
		return err.Error()
	} else {
		return string(body)
	}
}

type (
	result struct {
		err           error
		statusCode    int
		duration      time.Duration
		contentLength int64
	}

	StressWorker struct {
		RequestParams *StressParameters
		results       chan *result
		resultList    []StressResult

		totalTime time.Duration
		// Wait some task finish
		wg sync.WaitGroup
	}
)

func (b *StressWorker) Start() {
	b.results = make(chan *result, 2*b.RequestParams.C+1)
	b.resultList = make([]StressResult, 0)

	b.collectReport()
	b.runWorkers()

	verbosePrint(VERBOSE_INFO, "Worker finished and wait result")
	b.wg.Wait()
}

func (b *StressWorker) Stop() {
	b.RequestParams.Cmd = CMD_STOP
	b.wg.Wait()
}

func (b *StressWorker) IsStop() bool {
	return b.RequestParams.Cmd == CMD_STOP
}

func (b *StressWorker) Append(result StressResult) {
	b.resultList = append(b.resultList, result)
}

func (b *StressWorker) Add(n int) {
	b.wg.Add(n)
}

func (b *StressWorker) Done() {
	b.wg.Done()
}

func (b *StressWorker) Wait() *StressResult {
	b.wg.Wait()

	if len(b.resultList) <= 0 {
		fmt.Fprintf(os.Stderr, "Internal err: stress test result empty")
		return nil
	}

	if len(b.resultList) > 1 {
		for _, v := range b.resultList[1:] {
			if b.resultList[0].Slowest < v.Slowest {
				b.resultList[0].Slowest = v.Slowest
			}
			if b.resultList[0].Fastest > v.Fastest {
				b.resultList[0].Fastest = v.Fastest
			}
			b.resultList[0].LatsTotal += v.LatsTotal
			b.resultList[0].AvgTotal += v.AvgTotal
			for code, c := range v.StatusCodeDist {
				b.resultList[0].StatusCodeDist[code] += c
			}
			b.resultList[0].SizeTotal += v.SizeTotal
			for code, c := range v.ErrorDist {
				b.resultList[0].ErrorDist[code] += c
			}
			for lats, c := range v.Lats {
				b.resultList[0].Lats[lats] += c
			}
		}
	}

	if b.resultList[0].Duration > 0 {
		b.resultList[0].Rps = int64((b.resultList[0].LatsTotal * SCALE_NUM * SCALE_NUM) / b.resultList[0].Duration)
	}

	if b.resultList[0].LatsTotal > 0 {
		b.resultList[0].Average = b.resultList[0].AvgTotal / b.resultList[0].LatsTotal
	}

	return &(b.resultList[0])
}

func (b *StressWorker) runWorker(n int) {
	var (
		throttle  <-chan time.Time
		runCounts int = 0
	)

	if b.RequestParams.Qps > 0 {
		throttle = time.Tick(time.Duration(1e6/(b.RequestParams.Qps)) * time.Microsecond)
	}

	client := &http.Client{
		Timeout: time.Duration(b.RequestParams.Timeout) * time.Millisecond,
	}

	switch b.RequestParams.RequestHttpType {
	case TYPE_HTTP2:
		tr := &http2.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
			DisableCompression: b.RequestParams.DisableCompression,
		}
		client.Transport = tr
	default:
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
			MaxIdleConns:    200,
			IdleConnTimeout: time.Duration(60) * time.Second,
		}
		if proxyUrl != nil {
			tr.Proxy = http.ProxyURL(proxyUrl)
		}
		client.Transport = tr
	}

	// random set seed
	rand.Seed(time.Now().UnixNano())

	for b.RequestParams.Cmd != CMD_STOP {
		if n > 0 && runCounts > n {
			break
		}
		runCounts++

		if b.RequestParams.Qps > 0 {
			<-throttle
		}

		var t = time.Now()
		var size int64
		var code int

		randv := rand.Intn(len(b.RequestParams.Urls)) % len(b.RequestParams.Urls)
		resp, err := client.Do(b.getRequest(b.RequestParams.Urls[randv]))
		if err == nil {
			size = resp.ContentLength
			code = resp.StatusCode
			resp.Body.Close()
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
	if len(b.RequestParams.Urls) > 1 {
		fmt.Printf("Running %d connections, @ random urls.txt\n", b.RequestParams.C)
	} else {
		fmt.Printf("Running %d connections, @ %s\n", b.RequestParams.C, b.RequestParams.Urls[0])
	}

	var (
		start = time.Now()
		wg    sync.WaitGroup
	)

	// Ignore the case where b.RequestParams.N % b.RequestParams.C != 0.
	for i := 0; i < b.RequestParams.C && b.RequestParams.Cmd != CMD_STOP; i++ {
		wg.Add(1)
		go func() {
			b.runWorker(b.RequestParams.N / b.RequestParams.C)
			wg.Done()
		}()
	}

	// Wait all task finish.
	wg.Wait()
	b.totalTime = time.Now().Sub(start)
	close(b.results)
}

func (b *StressWorker) getRequest(url string) *http.Request {
	req, err := http.NewRequest(b.RequestParams.RequestMethod, url,
		strings.NewReader(b.RequestParams.RequestBody))
	if err != nil {
		return nil
	}
	req.Header = b.RequestParams.Headers
	return req
}

func (b *StressWorker) collectReport() {
	b.wg.Add(1)

	go func() {
		timeTicker := time.NewTicker(time.Duration(b.RequestParams.Duration) * time.Second)
		defer func() {
			timeTicker.Stop()
			b.wg.Done()
		}()

		result := StressResult{
			ErrorDist:      make(map[string]int, 0),
			StatusCodeDist: make(map[int]int, 0),
			Lats:           make(map[string]int64, 0),
		}

		for {
			select {
			case res, ok := <-b.results:
				if !ok {
					result.Duration = int64(b.totalTime.Seconds() * SCALE_NUM)
					b.resultList = append(b.resultList, result)
					return
				}
				if res.err != nil {
					result.ErrorDist[res.err.Error()]++
				} else {
					result.Lats[fmt.Sprintf("%4.3f", res.duration.Seconds())]++
					duration := int64(res.duration.Seconds() * SCALE_NUM)
					if result.LatsTotal == 0 {
						result.Slowest = duration
						result.Fastest = duration
					} else {
						if result.Slowest < duration {
							result.Slowest = duration
						}
						if result.Fastest > duration {
							result.Fastest = duration
						}
					}
					result.LatsTotal++
					result.AvgTotal += duration
					result.StatusCodeDist[res.statusCode]++
					if res.contentLength > 0 {
						result.SizeTotal += res.contentLength
					}
				}
			case <-timeTicker.C:
				verbosePrint(VERBOSE_INFO, "Time ticker upcoming, duration: %ds\n", b.RequestParams.Duration)
				b.RequestParams.Cmd = CMD_STOP // Time ticker exec Stop commands
			}
		}
	}()
}

func usageAndExit(msg string) {
	if msg != "" {
		fmt.Fprintf(os.Stderr, msg+"\n\n")
	}
	flag.Usage()
	fmt.Fprintf(os.Stderr, "\n")
	os.Exit(1)
}

func parseInputWithRegexp(input, regx string) ([]string, error) {
	re := regexp.MustCompile(regx)
	matches := re.FindStringSubmatch(input)
	if len(matches) < 1 {
		return nil, fmt.Errorf("could not parse the provided input; input = %v", input)
	}
	return matches, nil
}

func parseFile(fileName string, delimiter []rune) ([]string, error) {
	var contentList []string
	file, err := os.Open(fileName)
	if err != nil {
		return contentList, err
	}

	defer file.Close()

	if content, err := ioutil.ReadAll(file); err != nil {
		return contentList, err
	} else {
		if delimiter == nil {
			return []string{string(content)}, nil
		}
		lines := strings.FieldsFunc(string(content), func(r rune) bool {
			for _, v := range delimiter {
				if r == v {
					return true
				}
			}
			return false
		})
		for _, line := range lines {
			if len(line) > 0 {
				contentList = append(contentList, line)
			}
		}
	}

	return contentList, nil
}

func verbosePrint(level int, vfmt string, args ...interface{}) {
	switch level {
	case VERBOSE_TRACE:
		fmt.Printf("[TREACE VERBOSE] "+vfmt, args...)
	case VERBOSE_DEBUG:
		fmt.Printf("[DEBUG VERBOSE] "+vfmt, args...)
	default:
		fmt.Printf("[VERBOSE] "+vfmt, args...)
	}
}

func parseTime(timeStr string) int64 {
	var (
		timeStrLen       = len(timeStr) - 1
		multi      int64 = 1
	)
	if timeStrLen > 0 {
		switch timeStr[timeStrLen] {
		case 's':
			timeStr = timeStr[:timeStrLen]
		case 'm':
			timeStr = timeStr[:timeStrLen]
			multi = 60
		case 'h':
			timeStr = timeStr[:timeStrLen]
			multi = 3600
		}
	}

	t, err := strconv.ParseInt(timeStr, 10, 64)
	if err != nil || t <= 0 {
		usageAndExit("Duration parse err: " + err.Error())
	}

	return multi * t
}

func handleWorker(w http.ResponseWriter, r *http.Request) {
	if reqStr, err := ioutil.ReadAll(r.Body); err == nil {
		var params StressParameters
		if err := json.Unmarshal(reqStr, &params); err != nil {
			fmt.Fprintf(os.Stderr, "Unmarshal body err: %s\n", err.Error())
		} else {
			verbosePrint(VERBOSE_DEBUG, "Request params: %s\n", params.String())
			var stressTest *StressWorker
			if v, ok := stressList.Load(params.SequenceId); ok && v != nil {
				stressTest = v.(*StressWorker)
			} else {
				stressTest = &StressWorker{
					RequestParams: &params,
				}
				stressList.Store(params.SequenceId, stressTest)
			}
			switch params.Cmd {
			case CMD_START:
				stressTest.Start()
				respResult := stressTest.Wait()
				wbody, _ := json.Marshal(*respResult)
				w.Write(wbody)
				respResult.print()
			case CMD_STOP:
				stressTest.Stop()
			}
			stressList.Delete(params.SequenceId)
		}
	}
}

func requestWorker(addr string, body []byte, needResult bool) *StressResult {
	verbosePrint(VERBOSE_DEBUG, "Request body: %s\n", string(body))
	resp, err := http.Post("http://"+addr+"/", "application/json", bytes.NewBuffer(body))
	if err != nil {
		fmt.Fprintf(os.Stderr, "RequestWorker addr(%s), err: %s\n", addr, err.Error())
		return nil
	}

	defer resp.Body.Close()

	var result StressResult
	respStr, _ := ioutil.ReadAll(resp.Body)
	if err := json.Unmarshal(respStr, &result); err != nil && needResult {
		fmt.Fprintf(os.Stderr, "RequestWorker body(%s), err: %s\n", string(respStr), err.Error())
		return nil
	}

	return &result
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
	authHeader = flag.String("a", "", "")

	output = flag.String("o", "", "") // Output type

	c        = flag.Int("c", 50, "")               // Number of requests to run concurrently
	n        = flag.Int("n", 0, "")                // Number of requests to run
	q        = flag.Int("q", 0, "")                // Rate limit, in seconds (QPS)
	d        = flag.String("d", "10s", "")         // Duration for stress test
	t        = flag.Int("t", 3000, "")             // Timeout in ms
	httpType = flag.String("http", TYPE_HTTP1, "") // HTTP Version

	cpus = flag.Int("cpus", runtime.GOMAXPROCS(-1), "")

	disableCompression = flag.Bool("disable-compression", false, "")
	disableKeepAlives  = flag.Bool("disable-keepalive", false, "")
	proxyAddr          = flag.String("x", "", "")

	urlstr  = flag.String("url", "", "")
	verbose = flag.Int("verbose", 2, "")
	listen  = flag.String("listen", "", "")

	urlFile  = flag.String("url-file", "", "")
	bodyFile = flag.String("body-file", "", "")
)

var usage = `Usage: http_bench [options...] <url>
Options:
	-n  Number of requests to run.
	-c  Number of requests to run concurrently. Total number of requests cannot
		be smaller than the concurency level.
	-q  Rate limit, in seconds (QPS).
	-d  Duration of the stress test, e.g. 2s, 2m, 2h
	-t  Timeout in ms.
	-o  Output type. If none provided, a summary is printed.
		"csv" is the only supported alternative. Dumps the response
		metrics in comma-seperated values format.
	-m  HTTP method, one of GET, POST, PUT, DELETE, HEAD, OPTIONS.
	-H  Custom HTTP header. You can specify as many as needed by repeating the flag.
		for example, -H "Accept: text/html" -H "Content-Type: application/xml", 
		but "Host: ***", replace that with -host.
	-http  Support HTTP/1 HTTP/2, default HTTP/1.
	-body  Request body, default empty.
	-a  Basic authentication, username:password.
	-x  HTTP Proxy address as host:port.
	-disable-compression  Disable compression.
	-disable-keepalive    Disable keep-alive, prevents re-use of TCP
						connections between different HTTP requests.
	-cpus                 Number of used cpu cores.
						(default for current machine is %d cores).
	-url 		Request single url.
	-verbose 	Print detail logs, default 2(0:TRACE, 1:DEBUG, 2:INFO ~ ERROR).
	-url-file 	Read url list from file and random stress test.
	-body-file  Request body from file.
	-listen 	Listen IP:PORT for distributed stress test and worker mechine (default empty). e.g. "127.0.0.1:12710".
	-W  Running distributed stress test worker mechine list.
				for example, -W "127.0.0.1:12710" -W "127.0.0.1:12711".

Example stress test:
	./http_bench -n 1000 -c 10 -t 3000 -m GET -url "http://127.0.0.1/test1"
	./http_bench -n 1000 -c 10 -t 3000 -m GET "http://127.0.0.1/test1"
	./http_bench -n 1000 -c 10 -t 3000 -m GET "http://127.0.0.1/test1" -url-file urls.txt

Example distributed stress test:
	(1) ./http_bench -listen "127.0.0.1:12710" -verbose 1
	(2) ./http_bench -c 1 -d 10s "http://127.0.0.1:18090/test1" -body "{}" -verbose 1 -W "127.0.0.1:12710"
`

func main() {
	flag.Usage = func() {
		fmt.Fprint(os.Stderr, fmt.Sprintf(usage, runtime.NumCPU()))
	}

	var params StressParameters
	var headerslice flagSlice
	flag.Var(&headerslice, "H", "") // Custom HTTP header
	flag.Var(&workerList, "W", "")  // Worker mechine
	flag.Parse()

	if len(flag.Args()) <= 0 {
		usageAndExit("args invalid.")
	}

	for flag.NArg() > 0 {
		if len(*urlstr) == 0 {
			*urlstr = flag.Args()[0]
		}
		os.Args = flag.Args()[0:]
		flag.Parse()
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
		usageAndExit("n cannot be less than c")
	}

	if *urlFile == "" {
		params.Urls = append(params.Urls, *urlstr)
	} else {
		var err error
		if params.Urls, err = parseFile(*urlFile, []rune{'\r', '\n', ' '}); err != nil {
			usageAndExit(*urlFile + " file read error(" + err.Error() + ").")
		}
	}

	params.RequestMethod = strings.ToUpper(*m)
	params.DisableCompression = *disableCompression
	params.DisableKeepAlives = *disableKeepAlives
	params.RequestBody = *body

	if *bodyFile != "" {
		if readBody, err := parseFile(*urlFile, nil); err != nil {
			usageAndExit(*bodyFile + " file read error(" + err.Error() + ").")
		} else {
			if len(readBody) > 0 {
				params.RequestBody = readBody[0]
			}
		}
	}

	switch strings.ToLower(*httpType) {
	case TYPE_HTTP1, TYPE_HTTP2:
		params.RequestHttpType = strings.ToLower(*httpType)
	default:
		usageAndExit("Not support -http: " + *httpType)
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
		if match, err := parseInputWithRegexp(*authHeader, authRegexp); err != nil {
			usageAndExit(err.Error())
		} else {
			params.AuthUsername, params.AuthPassword = match[1], match[2]
		}
	}

	if *output != "csv" && *output != "" {
		usageAndExit("Invalid output type; only csv is supported.")
	}

	// set request timeout
	params.Timeout = *t

	if *proxyAddr != "" {
		var err error
		if proxyUrl, err = gourl.Parse(*proxyAddr); err != nil {
			usageAndExit(err.Error())
		}
	}

	if *verbose == VERBOSE_TRACE {
		file, _ := os.OpenFile("cpu.pprof", os.O_CREATE|os.O_RDWR, 0644)
		defer file.Close()
		pprof.StartCPUProfile(file)
		defer pprof.StopCPUProfile()
	}

	debug.SetGCPercent(500)

	if len(*listen) > 0 {
		mux := http.NewServeMux()
		mux.HandleFunc("/", handleWorker)
		fmt.Fprintf(os.Stdout, "Server listen %s\n", *listen)
		if err := http.ListenAndServe(*listen, mux); err != nil {
			fmt.Fprintf(os.Stderr, "ListenAndServe err: %s\n", err.Error())
		}
	} else {
		params.SequenceId = time.Now().Unix()
		verbosePrint(VERBOSE_DEBUG, "Request params: %s\n", params.String())

		stopSignal = make(chan os.Signal)
		signal.Notify(stopSignal, syscall.SIGINT, syscall.SIGTERM)
		stressTest := &StressWorker{
			RequestParams: &params,
		}

		var requestFunc func(needResult bool) error
		var err error

		if len(workerList) > 0 {
			requestFunc = func(needResult bool) error {
				paramsJson, err := json.Marshal(params)
				if err != nil {
					return err
				}
				for _, v := range workerList {
					stressTest.Add(1)
					go func(addr string) {
						result := requestWorker(addr, paramsJson, needResult)
						if needResult && result != nil {
							stressTest.Append(*result)
						}
						stressTest.Done()
					}(v)
				}
				return nil
			}
		}

		go func() {
			select {
			case <-stopSignal:
				verbosePrint(VERBOSE_INFO, "Recv stop signal\n")
				params.Cmd = CMD_STOP
				if requestFunc != nil {
					err = requestFunc(false)
				}
				stressTest.Stop() // Recv stop signal and Stop commands
			}
		}()

		if len(workerList) > 0 {
			err = requestFunc(true)
		} else {
			stressTest.Start()
		}

		if err != nil {
			fmt.Fprintf(os.Stderr, "Internal err: %s\n", err.Error())
		}

		if r := stressTest.Wait(); r != nil {
			close(stopSignal)
			r.print()
		}
	}
}
