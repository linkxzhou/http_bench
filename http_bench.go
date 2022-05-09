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
	"os/exec"
	"os/signal"
	"regexp"
	"runtime"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"text/template"
	"time"

	"golang.org/x/net/http2"
)

// ========================= function begin =========================
// template functions
func intSum(v ...int64) int64 {
	var r int64
	for _, r1 := range v {
		r += int64(r1)
	}
	return r
}

func random(min, max int64) int64 {
	rand.Seed(time.Now().UnixNano())
	return rand.Int63n(max-min) + min
}

func formatTime(now time.Time, fmt string) string {
	switch fmt {
	case "YMD":
		return now.Format("20060201")
	case "HMS":
		return now.Format("150405")
	default:
		return now.Format("20060201-150405")
	}
}

func uuidStr() string {
	if out, err := exec.Command("uuidgen").Output(); err != nil {
		return randomString(10)
	} else {
		return string(out)
	}
}

// YMD = yyyyMMdd, HMS = HHmmss, YMDHMS = yyyyMMdd-HHmmss
func date(fmt string) string {
	return formatTime(time.Now(), fmt)
}

func randomDate(fmt string) string {
	return formatTime(time.Unix(rand.Int63n(time.Now().Unix()-94608000)+94608000, 0), fmt)
}

func escape(u string) string {
	return gourl.QueryEscape(u)
}

const (
	letterIdxBits  = 6                    // 6 bits to represent a letter index
	letterIdxMask  = 1<<letterIdxBits - 1 // All 1-bits, as many as letterIdxBits
	letterIdxMax   = 63 / letterIdxBits   // # of letter indices fitting in 63 bits
	letterBytes    = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	letterNumBytes = "0123456789"
)

var (
	// for functions
	fnSrc = rand.NewSource(time.Now().UnixNano())
	fnMap = template.FuncMap{
		"intSum":       intSum,
		"random":       random,
		"randomDate":   randomDate,
		"randomString": randomString,
		"randomNum":    randomNum,
		"date":         date,
		"UUID":         UUID,
		"escape":       escape,
		"getEnv":       getEnv,
	}
	fnUUID = uuidStr()
)

func randomString(n int) string {
	b := make([]byte, n)
	for i, cache, remain := n-1, fnSrc.Int63(), letterIdxMax; i >= 0; {
		if remain == 0 {
			cache, remain = fnSrc.Int63(), letterIdxMax
		}
		if idx := int(cache & letterIdxMask); idx < len(letterBytes) {
			b[i] = letterBytes[idx]
			i--
		}
		cache >>= letterIdxBits
		remain--
	}
	return string(b)
}

func randomNum(n int) string {
	b := make([]byte, n)
	for i, cache, remain := n-1, fnSrc.Int63(), letterIdxMax; i >= 0; {
		if remain == 0 {
			cache, remain = fnSrc.Int63(), letterIdxMax
		}
		if idx := int(cache & letterIdxMask); idx < len(letterNumBytes) {
			b[i] = letterNumBytes[idx]
			i--
		}
		cache >>= letterIdxBits
		remain--
	}
	return string(b)
}

func UUID() string {
	return fnUUID
}

func getEnv(key string) string {
	return os.Getenv(key)
}

// ========================= function end =========================

const (
	CMD_START int = iota
	CMD_STOP
	CMD_METRICS

	SCALE_NUM = 10000

	TYPE_HTTP1 = "http1"
	TYPE_HTTP2 = "http2"
	TYPE_HTTP3 = "http3"

	VERBOSE_TRACE = 0
	VERBOSE_DEBUG = 1
	VERBOSE_INFO  = 2
	VERBOSE_ERROR = 3

	INT_MAX = int(^uint(0) >> 1)
	INT_MIN = ^INT_MAX
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
	rdLock         sync.RWMutex     `json:"-"`
}

func (result *StressResult) print() {
	result.rdLock.RLock()
	defer result.rdLock.RUnlock()

	switch result.Output {
	case "csv":
		fmt.Printf("Duration,Count\n")
		for duration, val := range result.Lats {
			fmt.Printf("%s,%d\n", duration, val/SCALE_NUM)
		}
		return
	default:
		// pass
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
		} else if result.SizeTotal > 1048576 {
			fmt.Printf("  Total data:\t%4.3f MB\n", float64(result.SizeTotal)/1048576)
		} else if result.SizeTotal > 1024 {
			fmt.Printf("  Total data:\t%4.3f KB\n", float64(result.SizeTotal)/1024)
		} else if result.SizeTotal > 0 {
			fmt.Printf("  Total data:\t%4.3f bytes\n", float64(result.SizeTotal))
		} else {
			// pass
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
	var j int = 0
	var current int64 = 0
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

func (result *StressResult) marshal() ([]byte, error) {
	result.rdLock.RLock()
	defer result.rdLock.RUnlock()

	return json.Marshal(result)
}

func (result *StressResult) result(res *result) {
	result.rdLock.Lock()
	defer result.rdLock.Unlock()

	if res.err != nil {
		result.ErrorDist[res.err.Error()]++
	} else {
		result.Lats[fmt.Sprintf("%4.3f", res.duration.Seconds())]++
		duration := int64(res.duration.Seconds() * SCALE_NUM)
		result.LatsTotal++
		if result.Slowest < duration {
			result.Slowest = duration
		}
		if result.Fastest > duration {
			result.Fastest = duration
		}
		result.AvgTotal += duration
		result.StatusCodeDist[res.statusCode]++
		if res.contentLength > 0 {
			result.SizeTotal += res.contentLength
		}
	}
}

func (result *StressResult) combine(resultList ...StressResult) {
	result.rdLock.RLock()
	defer result.rdLock.RUnlock()

	for _, v := range resultList {
		if result.Slowest < v.Slowest {
			result.Slowest = v.Slowest
		}
		if result.Fastest > v.Fastest {
			result.Fastest = v.Fastest
		}
		result.LatsTotal += v.LatsTotal
		result.AvgTotal += v.AvgTotal
		for code, c := range v.StatusCodeDist {
			result.StatusCodeDist[code] += c
		}
		result.SizeTotal += v.SizeTotal
		for code, c := range v.ErrorDist {
			result.ErrorDist[code] += c
		}
		for lats, c := range v.Lats {
			result.Lats[lats] += c
		}
	}

	if result.Duration > 0 {
		result.Rps = int64((result.LatsTotal * SCALE_NUM * SCALE_NUM) / result.Duration)
	}

	if result.LatsTotal > 0 {
		result.Average = result.AvgTotal / result.LatsTotal
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
		currentResult StressResult

		totalTime time.Duration
		// Wait some task finish
		wg                        sync.WaitGroup
		bodyTemplate, urlTemplate *template.Template
	}
)

func (b *StressWorker) Start() {
	b.results = make(chan *result, 2*b.RequestParams.C+1)
	b.resultList = make([]StressResult, 0)
	b.collectReport()
	b.runWorkers()
	verbosePrint(VERBOSE_INFO, "Worker finished and wait result\n")
}

// Stop stop stress worker and wait coroutine finish
func (b *StressWorker) Stop(wait bool) {
	b.RequestParams.Cmd = CMD_STOP
	if wait {
		b.wg.Wait()
	}
}

func (b *StressWorker) IsStop() bool {
	return b.RequestParams.Cmd == CMD_STOP
}

func (b *StressWorker) Append(result ...StressResult) {
	b.resultList = append(b.resultList, result...)
}

func (b *StressWorker) Wait() *StressResult {
	b.wg.Wait()

	if len(b.resultList) <= 0 {
		fmt.Fprintf(os.Stderr, "Internal err: stress test result empty")
		return nil
	}

	b.resultList[0].combine(b.resultList[1:]...)
	verbosePrint(VERBOSE_DEBUG, "resultList len: %d\n", len(b.resultList))
	return &(b.resultList[0])
}

func (b *StressWorker) runWorker(n int) {
	var throttle <-chan time.Time
	var runCounts int = 0

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
	case TYPE_HTTP1:
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
	case TYPE_HTTP3: // TODO: not support http3
		fmt.Fprintf(os.Stderr, "Not support %s\n", TYPE_HTTP3)
		return
	}

	// random set seed
	rand.Seed(time.Now().UnixNano())

	for !b.IsStop() {
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
		// if req = nil and break
		req := b.getRequest(b.RequestParams.Urls[randv])
		if req == nil {
			b.Stop(false)
			break
		}
		resp, err := client.Do(req)
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
		start            = time.Now()
		wg               sync.WaitGroup
		err              error
		bodyTemplateName = fmt.Sprintf("BODY-%d", b.RequestParams.SequenceId)
		urlTemplateName  = fmt.Sprintf("URL-%d", b.RequestParams.SequenceId)
	)

	if b.urlTemplate, err = template.New(urlTemplateName).Funcs(fnMap).Parse(b.RequestParams.Urls[0]); err != nil {
		verbosePrint(VERBOSE_ERROR, "Parse urls function err: "+err.Error()+"\n")
	}

	if b.bodyTemplate, err = template.New(bodyTemplateName).Funcs(fnMap).Parse(b.RequestParams.RequestBody); err != nil {
		verbosePrint(VERBOSE_ERROR, "Parse request body function err: "+err.Error()+"\n")
	}

	// Ignore the case where b.RequestParams.N % b.RequestParams.C != 0.
	for i := 0; i < b.RequestParams.C && !(b.IsStop()); i++ {
		wg.Add(1)
		go func() {
			defer func() {
				wg.Done()
				if r := recover(); r != nil {
					fmt.Fprintf(os.Stderr, "Internal err: %v\n", r)
				}
			}()

			b.runWorker(b.RequestParams.N / b.RequestParams.C)
		}()
	}

	wg.Wait()
	b.Stop(false)
	b.totalTime = time.Now().Sub(start)
	close(b.results)
}

func (b *StressWorker) getRequest(url string) *http.Request {
	var urlBytes, bodyBytes bytes.Buffer

	if b.urlTemplate != nil && len(url) > 0 {
		b.urlTemplate.Execute(&urlBytes, nil)
	} else {
		urlBytes.WriteString(url)
	}

	if len(b.RequestParams.RequestBody) > 0 && b.bodyTemplate != nil {
		b.bodyTemplate.Execute(&bodyBytes, nil)
	} else {
		bodyBytes.WriteString(b.RequestParams.RequestBody)
	}

	if !checkURL(urlBytes.String()) {
		return nil
	}

	verbosePrint(VERBOSE_TRACE, "Request url: %s\n", urlBytes.String())
	verbosePrint(VERBOSE_TRACE, "Request body: %s\n", bodyBytes.String())
	req, err := http.NewRequest(b.RequestParams.RequestMethod, urlBytes.String(), strings.NewReader(bodyBytes.String()))
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
		b.currentResult = StressResult{
			ErrorDist:      make(map[string]int, 0),
			StatusCodeDist: make(map[int]int, 0),
			Lats:           make(map[string]int64, 0),
			Slowest:        int64(INT_MIN),
			Fastest:        int64(INT_MAX),
		}
		for {
			select {
			case res, ok := <-b.results:
				if !ok {
					b.currentResult.Duration = int64(b.totalTime.Seconds() * SCALE_NUM)
					b.resultList = append(b.resultList, b.currentResult)
					return
				}
				b.currentResult.result(res)
			case <-timeTicker.C:
				verbosePrint(VERBOSE_INFO, "Time ticker upcoming, duration: %ds\n", b.RequestParams.Duration)
				b.Stop(false) // Time ticker exec Stop commands
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

func checkURL(url string) bool {
	if _, err := gourl.ParseRequestURI(url); err != nil {
		fmt.Fprintln(os.Stderr, "Parse URL err: ", err.Error())
		return false
	}
	return true
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
	if *verbose > level {
		return
	}

	switch level {
	case VERBOSE_TRACE:
		fmt.Printf("[VERBOSE TRACE] "+vfmt, args...)
	case VERBOSE_DEBUG:
		fmt.Printf("[VERBOSE DEBUG] "+vfmt, args...)
	case VERBOSE_INFO:
		fmt.Printf("[VERBOSE INFO] "+vfmt, args...)
	default:
		fmt.Printf("[VERBOSE ERROR] "+vfmt, args...)
	}
}

func parseTime(timeStr string) int64 {
	var timeStrLen = len(timeStr) - 1
	var multi int64 = 1
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

func execStress(params StressParameters, stressTestPtr **StressWorker) *StressResult {
	var stressResult *StressResult
	var stressTest *StressWorker
	if v, ok := stressList.Load(params.SequenceId); ok && v != nil {
		stressTest = v.(*StressWorker)
	} else {
		stressTest = &StressWorker{
			RequestParams: &params,
		}
		stressList.Store(params.SequenceId, stressTest)
	}
	*stressTestPtr = stressTest
	switch params.Cmd {
	case CMD_START:
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
	case CMD_STOP:
		if len(workerList) > 0 {
			jsonBody, _ := json.Marshal(params)
			requestWorkerList(jsonBody, stressTest)
		}
		stressTest.Stop(true)
		stressList.Delete(params.SequenceId)
	case CMD_METRICS:
		if len(workerList) > 0 {
			jsonBody, _ := json.Marshal(params)
			if resultList := requestWorkerList(jsonBody, stressTest); len(resultList) > 0 {
				stressResult = &StressResult{}
				for i := 0; i < len(resultList); i++ {
					stressResult.LatsTotal += resultList[i].LatsTotal
				} // TODO: dispose other variable
			}
		} else {
			stressResult = &stressTest.currentResult
		}
	}
	return stressResult
}

func handleWorker(w http.ResponseWriter, r *http.Request) {
	if reqStr, err := ioutil.ReadAll(r.Body); err == nil {
		var params StressParameters
		if err := json.Unmarshal(reqStr, &params); err != nil {
			fmt.Fprintf(os.Stderr, "Unmarshal body err: %s\n", err.Error())
		} else {
			verbosePrint(VERBOSE_DEBUG, "Request params: %s\n", params.String())
			var stressWorker *StressWorker
			if result := execStress(params, &stressWorker); result != nil {
				if wbody, err := result.marshal(); err != nil {
					verbosePrint(VERBOSE_ERROR, "Marshal result: %v\n", err)
				} else {
					w.Write(wbody)
				}
			}
		}
	}
}

func requestWorker(uri string, body []byte) (*StressResult, error) {
	verbosePrint(VERBOSE_DEBUG, "Request body: %s\n", string(body))
	resp, err := http.Post(uri, "application/json", bytes.NewBuffer(body))
	if err != nil {
		fmt.Fprintf(os.Stderr, "RequestWorker addr(%s), err: %s\n", uri, err.Error())
		return nil, err
	}
	defer resp.Body.Close()
	var result StressResult
	respStr, _ := ioutil.ReadAll(resp.Body)
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
	verbose = flag.Int("verbose", 3, "")
	listen  = flag.String("listen", "", "")
	web     = flag.String("web", "", "")

	urlFile           = flag.String("url-file", "", "")
	bodyFile          = flag.String("body-file", "", "")
	requestWorkerList = func(paramsJson []byte, stressTest *StressWorker) []StressResult {
		var wg sync.WaitGroup
		var stressResult []StressResult
		for _, v := range workerList {
			wg.Add(1)
			go func(addr string) {
				defer wg.Done()
				if result, err := requestWorker("http://"+addr+"/", paramsJson); err == nil {
					stressResult = append(stressResult, *result)
				}
			}(v)
		}
		wg.Wait()
		return stressResult
	}
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
	-verbose 	Print detail logs, default 3(0:TRACE, 1:DEBUG, 2:INFO, 3:ERROR).
	-url-file 	Read url list from file and random stress test.
	-body-file  Request body from file.
	-listen 	Listen IP:PORT for distributed stress test and worker mechine (default empty). e.g. "127.0.0.1:12710".
	-web 		Listen web IP:PORT and operate stress params on browser. 
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
		usageAndExit("n cannot be less than c.")
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
		if readBody, err := parseFile(*bodyFile, nil); err != nil {
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
		defer func() {
			file.Close()
			pprof.StopCPUProfile()
		}()
		pprof.StartCPUProfile(file)
	}

	if len(*listen) > 0 {
		mux := http.NewServeMux()
		mux.HandleFunc("/", handleWorker)
		fmt.Fprintf(os.Stdout, "Worker listen %s\n", *listen)
		if err := http.ListenAndServe(*listen, mux); err != nil {
			fmt.Fprintf(os.Stderr, "ListenAndServe err: %s\n", err.Error())
		}
	} else if len(*web) > 0 {
		mux := http.NewServeMux()
		mux.Handle("/", http.FileServer(http.Dir("./")))
		mux.HandleFunc("/api", handleWorker)
		fmt.Fprintf(os.Stdout, "Web listen %s\n", *web)
		if err := http.ListenAndServe(*web, mux); err != nil {
			fmt.Fprintf(os.Stderr, "ListenAndServe err: %s\n", err.Error())
		}
	} else {
		if len(params.Urls) <= 0 || len(params.Urls[0]) <= 0 {
			usageAndExit("url or url-file empty.")
		}
		params.SequenceId = time.Now().Unix()
		params.Cmd = CMD_START
		verbosePrint(VERBOSE_DEBUG, "Request params: %s\n", params.String())
		stopSignal = make(chan os.Signal)
		signal.Notify(stopSignal, syscall.SIGINT, syscall.SIGTERM)

		var stressTest *StressWorker
		var stressResult *StressResult

		go func() {
			<-stopSignal
			verbosePrint(VERBOSE_INFO, "Recv stop signal")
			params.Cmd = CMD_STOP
			jsonBody, _ := json.Marshal(params)
			requestWorkerList(jsonBody, stressTest)
			stressTest.Stop(true) // Recv stop signal and Stop commands
		}()

		if stressResult = execStress(params, &stressTest); stressResult != nil {
			close(stopSignal)
			stressResult.print()
		}
	}
}
