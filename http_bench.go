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
	"net/url"
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
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

const (
	MUTEX_LOCKED = 1 << iota

	CMD_START int = iota
	CMD_STOP
)

type Mutex struct {
	sync.Mutex
}

func (m *Mutex) TryLock() bool {
	return atomic.CompareAndSwapInt32((*int32)(unsafe.Pointer(&m.Mutex)), 0, MUTEX_LOCKED)
}

type flagSlice []string

func (h *flagSlice) String() string {
	return fmt.Sprintf("%s", *h)
}

func (h *flagSlice) Set(value string) error {
	*h = append(*h, value)
	return nil
}

type BenchResult struct {
	ErrCode  int     `json:"err_code"`
	ErrMsg   string  `json:"err_msg"`
	AvgTotal float64 `json:"avg_total"`
	Fastest  float64 `json:"fastest"`
	Slowest  float64 `json:"slowest"`
	Average  float64 `json:"average"`
	Rps      float64 `json:"rps"`

	ErrorDist      map[string]int   `json:"error_dist"`
	StatusCodeDist map[int]int      `json:"status_code_dist"`
	Lats           map[string]int64 `json:"lats"`
	LatsTotal      int64            `json:"lats_total"`
	SizeTotal      int64            `json:"size_total"`
	Duration       float64          `json:"duration"`
	Output         string           `json:"output"`
}

func (result *BenchResult) print() {
	switch result.Output {
	case "csv":
		result.printCSV()
		return
	default:
		// pass
	}

	if len(result.Lats) > 0 {
		fmt.Printf("\nSummary:\n")
		fmt.Printf("  Total:\t%4.3f secs\n", result.Duration)
		fmt.Printf("  Slowest:\t%4.3f secs\n", result.Slowest)
		fmt.Printf("  Fastest:\t%4.3f secs\n", result.Fastest)
		fmt.Printf("  Average:\t%4.3f secs\n", result.Average)
		fmt.Printf("  Requests/sec:\t%4.3f\n", result.Rps)
		if result.SizeTotal > 1073741824 {
			fmt.Printf("  Total data:\t%4.3f GB\n", float64(result.SizeTotal)/1073741824)
			fmt.Printf("  Size/request:\t%d bytes\n", result.SizeTotal/result.LatsTotal)
		} else if result.SizeTotal > 1024*1024 {
			fmt.Printf("  Total data:\t%4.3f MB\n", float64(result.SizeTotal)/1048576)
			fmt.Printf("  Size/request:\t%d bytes\n", result.SizeTotal/result.LatsTotal)
		} else if result.SizeTotal > 1024 {
			fmt.Printf("  Total data:\t%4.3f KB\n", float64(result.SizeTotal)/1024)
			fmt.Printf("  Size/request:\t%d bytes\n", result.SizeTotal/result.LatsTotal)
		} else if result.SizeTotal > 0 {
			fmt.Printf("  Total data:\t%4.3f bytes\n", float64(result.SizeTotal))
			fmt.Printf("  Size/request:\t%d bytes\n", result.SizeTotal/result.LatsTotal)
		}
		result.printStatusCodes()
		result.printLatencies()
	}

	if len(result.ErrorDist) > 0 {
		result.printErrors()
	}
}

func (result *BenchResult) printCSV() {
	fmt.Printf("Duration,Count\n")
	for duration, val := range result.Lats {
		fmt.Printf("%s,%d\n", duration, val)
	}
}

// Prints latency distribution.
func (result *BenchResult) printLatencies() {
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

// Prints status code distribution.
func (result *BenchResult) printStatusCodes() {
	fmt.Printf("\nStatus code distribution:\n")
	for code, num := range result.StatusCodeDist {
		fmt.Printf("  [%d]\t%d responses\n", code, num)
	}
}

func (result *BenchResult) printErrors() {
	fmt.Printf("\nError distribution:\n")
	for err, num := range result.ErrorDist {
		fmt.Printf("  [%d]\t%s\n", num, err)
	}
}

type BenchParameters struct {
	// Commands
	Cmd int `json:"cmd"`
	// Request Method.
	RequestMethod string `json:"request_method"`
	// Request Body.
	RequestBody string `json:"request_body"`
	// N is the total number of requests to make.
	N int `json:"n"`
	// C is the concurrency level, the number of concurrent workers to run.
	C int `json:"c"`
	// D is the duration for benchmark
	Duration int64 `json:"duration"`
	// Timeout in ms.
	Timeout int `json:"timeout"`
	// Qps is the rate limit.
	Qps int `json:"qps"`
	// DisableCompression is an option to disable compression in response
	DisableCompression bool `json:"disable_compression"`
	// DisableKeepAlives is an option to prevents re-use of TCP connections between different HTTP requests
	DisableKeepAlives bool `json:"disable_keepalives"`
	// ProxyAddr is the address of HTTP proxy server in the format on "host:port".
	ProxyAddr string `json:"proxy_addr"`
	// Basic authentication, username:password.
	AuthUsername string `json:"auth_username"`
	AuthPassword string `json:"auth_password"`
	// Custom HTTP header.
	Headers map[string][]string `json:"headers"`
	Urls    []string            `json:"urls"`
}

func (p *BenchParameters) String() string {
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

	BenchWorker struct {
		RequestParams *BenchParameters
		// Output represents the output type. If "csv" is provided, the
		// output will be dumped as a csv stream.
		Output string
		// ProxyAddr is the address of HTTP proxy server in the format on "host:port".
		ProxyURL   *url.URL
		results    chan *result
		resultList []BenchResult

		stopSignal chan os.Signal
		totalTime  time.Duration
		// Wait some task finish
		wg    sync.WaitGroup
		mutex Mutex
	}
)

func (b *BenchWorker) Start() {
	if !b.mutex.TryLock() {
		fmt.Printf("Worker is running and wait")
		return
	}

	b.mutex.Lock()
	defer b.mutex.Unlock()

	b.results = make(chan *result, 2*b.RequestParams.C+1)
	b.resultList = make([]BenchResult, 0)
	b.stopSignal = make(chan os.Signal)
	signal.Notify(b.stopSignal, syscall.SIGHUP, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGSTOP)

	b.collectReport()
	b.runWorkers()

	b.wg.Wait()
}

func (b *BenchWorker) Stop() {
	b.RequestParams.Cmd = CMD_STOP
	b.wg.Wait()
}

func (b *BenchWorker) Append(result BenchResult) {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	if len(b.resultList) > 0 {
		b.resultList = append(b.resultList, result)
	}
}

func (b *BenchWorker) Print() *BenchResult {
	if len(b.resultList) < 0 {
		fmt.Fprintf(os.Stderr, "Benchmark result empty")
		return nil
	}

	b.mutex.Lock()
	defer b.mutex.Unlock()

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
	b.resultList = b.resultList[:1]
	b.resultList[0].Rps = float64(b.resultList[0].LatsTotal) / b.resultList[0].Duration
	b.resultList[0].Average = b.resultList[0].AvgTotal / float64(b.resultList[0].LatsTotal)
	return &b.resultList[0]
}

func (b *BenchWorker) runWorker(n int) {
	var (
		throttle  <-chan time.Time
		runCounts int = 0
	)
	if b.RequestParams.Qps > 0 {
		throttle = time.Tick(time.Duration(1e6/(b.RequestParams.Qps)) * time.Microsecond)
	}

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

	if b.ProxyURL != nil {
		tr.Proxy = http.ProxyURL(b.ProxyURL)
	}

	client := &http.Client{
		Transport: tr,
		Timeout:   time.Duration(b.RequestParams.Timeout) * time.Millisecond,
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

func (b *BenchWorker) runWorkers() {
	if len(b.RequestParams.Urls) > 1 {
		fmt.Printf("Running %d connections, @ random urls.txt\n", b.RequestParams.C)
	} else {
		fmt.Printf("Running %d connections, @ %s\n", b.RequestParams.C, b.RequestParams.Urls[0])
	}

	var start = time.Now()
	var wg sync.WaitGroup

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

func (b *BenchWorker) getRequest(url string) *http.Request {
	req, err := http.NewRequest(b.RequestParams.RequestMethod, url,
		strings.NewReader(b.RequestParams.RequestBody))
	if err != nil {
		return nil
	}
	for k, v := range b.RequestParams.Headers {
		if v != nil && len(v) > 0 {
			req.Header.Set(k, v[0])
		}
	}
	return req
}

func (b *BenchWorker) collectReport() {
	b.wg.Add(1)

	go func() {
		timeTicker := time.NewTicker(time.Duration(b.RequestParams.Duration) * time.Second)
		defer func() {
			timeTicker.Stop()
			b.wg.Done()
		}()

		result := BenchResult{
			ErrorDist:      make(map[string]int, 0),
			StatusCodeDist: make(map[int]int, 0),
			Lats:           make(map[string]int64, 0),
			Output:         b.Output,
		}

		for {
			select {
			case res, ok := <-b.results:
				if !ok {
					result.Duration = b.totalTime.Seconds()
					b.resultList = append(b.resultList, result)
					return
				}
				if res.err != nil {
					result.ErrorDist[res.err.Error()]++
				} else {
					duration := res.duration.Seconds()
					lats := fmt.Sprintf("%4.3f", duration)
					result.Lats[lats]++
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
			case <-b.stopSignal:
				verbosePrint("Recv stop signal")
				b.RequestParams.Cmd = CMD_STOP // Recv stop signal and Stop commands
			case <-timeTicker.C:
				verbosePrint("Time ticker upcoming, duration: %ds", b.RequestParams.Duration)
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

func parseUrlsFile(fname string) ([]string, error) {
	var retArr []string
	if file, err := os.Open(fname); err != nil {
		return retArr, err
	} else {
		if content, err := ioutil.ReadAll(file); err != nil {
			return retArr, err
		} else {
			arr := strings.FieldsFunc(string(content), func(r rune) bool {
				return r == '\n' || r == '\r' || r == ' '
			})
			for _, v := range arr {
				if len(v) > 0 {
					retArr = append(retArr, v)
				}
			}
		}
	}

	return retArr, nil
}

func verbosePrint(vfmt string, args ...interface{}) {
	if *verbose {
		fmt.Printf("[VERBOSE] "+vfmt, args...)
	}
}

func parseTime(st string) int64 {
	var (
		tst  string = st
		tcov int64  = 1
	)
	if len(tst) > 1 {
		switch tst[len(tst)-1] {
		case 's':
			tst = tst[:len(tst)-1]
			tcov = 1
		case 'm':
			tst = tst[:len(tst)-1]
			tcov = 60
		case 'h':
			tst = tst[:len(tst)-1]
			tcov = 3600
		default:
			// pass
		}
	}
	if t, err := strconv.ParseInt(tst, 10, 64); err != nil {
		usageAndExit("Duration parse err: " + err.Error())
	} else {
		if t*tcov > 0 {
			return t * tcov
		}
	}

	return 1
}

func handleStart(w http.ResponseWriter, r *http.Request) {
	if sbody, err := ioutil.ReadAll(r.Body); err == nil {
		if err := json.Unmarshal(sbody, &benchmark.RequestParams); err != nil {
			fmt.Fprintf(os.Stderr, "Unmarshal body err: %s\n", err.Error())
		} else {
			switch benchmark.RequestParams.Cmd {
			case CMD_START:
				benchmark.Start()
				rbody := benchmark.Print()
				if *verbose {
					rbody.print()
				}
				wbody, _ := json.Marshal(benchmark.Print())
				w.Write(wbody)
			case CMD_STOP:
				benchmark.Stop()
			}
		}
	}
}

func requestWorker(addr string, body []byte) {
	resp, err := http.Post("http://"+addr+"/", "application/json", bytes.NewBuffer(body))
	if err != nil {
		fmt.Fprintf(os.Stderr, "RequestWorker addr(%s), err: %s\n", addr, err.Error())
		return
	}

	defer resp.Body.Close()

	var result BenchResult
	rbody, _ := ioutil.ReadAll(resp.Body)
	if err := json.Unmarshal(rbody, &result); err != nil {
		fmt.Fprintf(os.Stderr, "RequestWorker Unmarshal body(%s), err: %s\n", string(rbody), err.Error())
	} else {
		benchmark.resultList = append(benchmark.resultList, result)
	}
}

var (
	mux        *http.ServeMux
	params     BenchParameters
	benchmark  *BenchWorker
	workerList flagSlice // Worker mechine addr list.

	headerRegexp = `^([\w-]+):\s*(.+)`
	authRegexp   = `^(.+):([^\s].+)`

	m          = flag.String("m", "GET", "")
	body       = flag.String("body", "", "")
	authHeader = flag.String("a", "", "")

	output = flag.String("o", "", "") // Output type

	c = flag.Int("c", 50, "")       // Number of requests to run concurrently
	n = flag.Int("n", 0, "")        // Number of requests to run
	q = flag.Int("q", 0, "")        // Rate limit, in seconds (QPS)
	d = flag.String("d", "10s", "") // Duration for benchmark
	t = flag.Int("t", 3000, "")     // Timeout in ms

	cpus = flag.Int("cpus", runtime.GOMAXPROCS(-1), "")

	disableCompression = flag.Bool("disable-compression", false, "")
	disableKeepAlives  = flag.Bool("disable-keepalive", false, "")
	proxyAddr          = flag.String("x", "", "")

	urlstr  = flag.String("url", "", "")
	verbose = flag.Bool("verbose", false, "")

	urlFile = flag.String("file", "", "")
	listen  = flag.String("listen", "", "")
)

var usage = `Usage: http_bench [options...] <url>
Options:
	-n  Number of requests to run.
	-c  Number of requests to run concurrently. Total number of requests cannot
		be smaller than the concurency level.
	-q  Rate limit, in seconds (QPS).
	-d  Duration of the benchmark, e.g. 2s, 2m, 2h
	-t  Timeout in ms.
	-o  Output type. If none provided, a summary is printed.
		"csv" is the only supported alternative. Dumps the response
		metrics in comma-seperated values format.
	-m  HTTP method, one of GET, POST, PUT, DELETE, HEAD, OPTIONS.
	-H  Custom HTTP header. You can specify as many as needed by repeating the flag.
		for example, -H "Accept: text/html" -H "Content-Type: application/xml", 
		but "Host: ***", replace that with -host.
	-body  Request body, default empty.
	-a  Basic authentication, username:password.
	-x  HTTP Proxy address as host:port.
	-disable-compression  Disable compression.
	-disable-keepalive    Disable keep-alive, prevents re-use of TCP
						connections between different HTTP requests.
	-cpus                 Number of used cpu cores.
						(default for current machine is %d cores).
	-url 		Request single url.
	-verbose 	Print detail logs.
	-file 		Read url list from file and random benchmark.
	-listen 	Listen ip and port (default empty). e.g. "127.0.0.1:12710".
	-W			Running benchmark worker mechine list.
				for example, -W "127.0.0.1:12710" -W "127.0.0.1:12711".
Example:
	./http_bench -n 1000 -c 10 -t 3000 -m GET -url "http://127.0.0.1/test1"
	./http_bench -n 1000 -c 10 -t 3000 -m GET "http://127.0.0.1/test1"
	./http_bench -n 1000 -c 10 -t 3000 -m GET "http://127.0.0.1/test1" -file urls.txt
`

func main() {
	flag.Usage = func() {
		fmt.Fprint(os.Stderr, fmt.Sprintf(usage, runtime.NumCPU()))
	}

	var headerslice flagSlice
	flag.Var(&headerslice, "H", "") // Custom HTTP header
	flag.Var(&workerList, "W", "")  // Worker mechine
	flag.Parse()

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
		if params.Urls, err = parseUrlsFile(*urlFile); err != nil {
			usageAndExit(*urlFile + " file read error(" + err.Error() + ").")
		}
	}

	params.RequestMethod = strings.ToUpper(*m)

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

	var proxyUrl *gourl.URL
	if *proxyAddr != "" {
		var err error
		if proxyUrl, err = gourl.Parse(*proxyAddr); err != nil {
			usageAndExit(err.Error())
		}
		params.ProxyAddr = *proxyAddr
	}

	if *verbose {
		file, _ := os.OpenFile("cpu.pprof", os.O_CREATE|os.O_RDWR, 0644)
		defer file.Close()
		pprof.StartCPUProfile(file)
		defer pprof.StopCPUProfile()
	}

	debug.SetGCPercent(500)

	benchmark = &BenchWorker{
		RequestParams: &params,
		Output:        *output,
		ProxyURL:      proxyUrl,
	}

	if len(*listen) > 0 {
		mux = http.NewServeMux()
		mux.HandleFunc("/", handleStart)
		if err := http.ListenAndServe(*listen, mux); err != nil {
			fmt.Fprintf(os.Stderr, "ListenAndServe err: %s\n", err.Error())
		} else {
			fmt.Fprintf(os.Stdout, "Server listen %s\n", *listen)
		}
	} else {
		verbosePrint("Request params: %s\n", params)
		if len(workerList) > 0 {
			rbody, err := json.Marshal(params)
			if err != nil {
				usageAndExit("Params err: " + err.Error())
			}
			// TODO: fix stop
			var wg sync.WaitGroup
			for _, v := range workerList {
				wg.Add(1)
				go func(addr string) {
					requestWorker(addr, rbody)
					wg.Done()
				}(v)
			}
			wg.Wait()
		} else {
			benchmark.Start()
		}
	}

	benchmark.Print().print()
}
