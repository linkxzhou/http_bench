package main

import (
	"flag"
	"fmt"
	"net/http"
	gourl "net/url"
	"regexp"
	"runtime"
	"strings"
	"sort"
	"crypto/tls"
	"io"
	"bufio"
	"io/ioutil"
	"net/url"
	"os"
	"os/signal"
	"sync"
	"time"
	"math/rand"

	"golang.org/x/net/http2"
)

const (
	barChar = "∎"
)

type report struct {
	avgTotal float64
	fastest  float64
	slowest  float64
	average  float64
	rps      float64

	results chan *result
	total   time.Duration

	errorDist      map[string]int
	statusCodeDist map[int]int
	lats           []float64
	sizeTotal      int64

	output string

	req			   *BenchRequest
}

type BenchRequest struct {
	http.Request
	BRequest []*http.Request // 请求以数组形式
	BUrl []string // 存储请求的url
	BReport *report // 对应report报告信息

	BReportUrls []string // 每次请求的url
}

var benchRequestUrl map[string]int = make(map[string]int)
var command chan string // 发送消息管道

// 返回随机请求
func (b * BenchRequest) GetRandomRequest() *http.Request {
	rint := rand.Intn(len(b.BRequest))
	url := b.BUrl[rint]
	b.BReportUrls = append(b.BReportUrls, url)
	command <- url
	return b.BRequest[rint]
}

func (b * BenchRequest) NewReport(size int, results chan *result, 
	output string, total time.Duration) *report {
	if b.BReport == nil {
		b.BReport = &report{
			output:         output,
			results:        results,
			total:          total,
			statusCodeDist: make(map[int]int),
			errorDist:      make(map[string]int),
			req:			b,
		}
	}

	return b.BReport
}

// 解析urls.txt
func ParseUrlsFile(file string) ([]string, error) {
	var retArr []string
	f, err := os.Open(file)
	if err != nil {
		return retArr, err
	}
	buf := bufio.NewReader(f)
	for {
		line, err := buf.ReadString('\n')
		line = strings.TrimSpace(line)
		if err != nil {
			if err == io.EOF {
				return retArr, nil
			}
			return retArr, err
		}
		retArr = append(retArr, line)
	}

	return retArr, nil
}

func NewRequest(method string, urlStr []string, body io.Reader) (*BenchRequest, error) {
	breq := &BenchRequest{}
	if len(urlStr) > 1 {
		for _, url := range urlStr {
			req, err := http.NewRequest(method, url, body)
			if err != nil {
				continue
			}
			breq.BRequest = append(breq.BRequest, req)
			breq.BUrl = append(breq.BUrl, url)
		}

		return breq, nil
	} else {
		req, err := http.NewRequest(method, urlStr[0], body)
		breq.BRequest = append(breq.BRequest, req)
		breq.BUrl = append(breq.BUrl, urlStr[0])

		return breq, err
	}
}

func (r *report) finalize() {
	for {
		select {
		case res := <-r.results:
			if res.err != nil {
				r.errorDist[res.err.Error()]++
			} else {
				r.lats = append(r.lats, res.duration.Seconds())
				r.avgTotal += res.duration.Seconds()
				r.statusCodeDist[res.statusCode]++
				if res.contentLength > 0 {
					r.sizeTotal += res.contentLength
				}
			}
		default:
			r.rps = float64(len(r.lats)) / r.total.Seconds()
			r.average = r.avgTotal / float64(len(r.lats))
			r.print()
			return
		}
	}
}

func (r *report) print() {
	sort.Float64s(r.lats)

	if r.output == "csv" {
		r.printCSV()
		return
	}

	if len(r.lats) > 0 {
		r.fastest = r.lats[0]
		r.slowest = r.lats[len(r.lats)-1]
		// 答应请求的url
		fmt.Printf("Requests:\n")
		for k, v := range benchRequestUrl {
			fmt.Printf("  [%d] %s\n", v, k)
		}
		
		fmt.Printf("\nSummary:\n")
		fmt.Printf("  Total:\t%4.4f secs\n", r.total.Seconds())
		fmt.Printf("  Slowest:\t%4.4f secs\n", r.slowest)
		fmt.Printf("  Fastest:\t%4.4f secs\n", r.fastest)
		fmt.Printf("  Average:\t%4.4f secs\n", r.average)
		fmt.Printf("  Requests/sec:\t%4.4f\n", r.rps)
		if r.sizeTotal > 0 {
			fmt.Printf("  Total data:\t%d bytes\n", r.sizeTotal)
			fmt.Printf("  Size/request:\t%d bytes\n", r.sizeTotal/int64(len(r.lats)))
		}
		r.printStatusCodes()

		// 不需要打印
		// r.printHistogram()

		r.printLatencies()
	}

	if len(r.errorDist) > 0 {
		r.printErrors()
	}
}

func (r *report) printCSV() {
	for i, val := range r.lats {
		fmt.Printf("%v,%4.4f,%s\n", i+1, val, r.req.BReportUrls[i])
	}
}

func (r *report) printLatencies() {
	pctls := []int{10, 25, 50, 75, 90, 95, 99}
	data := make([]float64, len(pctls))
	j := 0
	for i := 0; i < len(r.lats) && j < len(pctls); i++ {
		current := i * 100 / len(r.lats)
		if current >= pctls[j] {
			data[j] = r.lats[i]
			j++
		}
	}
	fmt.Printf("\nLatency distribution:\n")
	for i := 0; i < len(pctls); i++ {
		if data[i] > 0 {
			fmt.Printf("  %v%% in %4.4f secs\n", pctls[i], data[i])
		}
	}
}

func (r *report) printHistogram() {
	bc := 10
	buckets := make([]float64, bc+1)
	counts := make([]int, bc+1)
	bs := (r.slowest - r.fastest) / float64(bc)
	for i := 0; i < bc; i++ {
		buckets[i] = r.fastest + bs*float64(i)
	}
	buckets[bc] = r.slowest
	var bi int
	var max int
	for i := 0; i < len(r.lats); {
		if r.lats[i] <= buckets[bi] {
			i++
			counts[bi]++
			if max < counts[bi] {
				max = counts[bi]
			}
		} else if bi < len(buckets)-1 {
			bi++
		}
	}
	fmt.Printf("\nResponse time histogram:\n")
	for i := 0; i < len(buckets); i++ {
		// Normalize bar lengths.
		var barLen int
		if max > 0 {
			barLen = counts[i] * 40 / max
		}
		fmt.Printf("  %4.3f [%v]\t|%v\n", buckets[i], counts[i], strings.Repeat(barChar, barLen))
	}
}

// Prints status code distribution.
func (r *report) printStatusCodes() {
	fmt.Printf("\nStatus code distribution:\n")
	for code, num := range r.statusCodeDist {
		fmt.Printf("  [%d]\t%d responses\n", code, num)
	}
}

func (r *report) printErrors() {
	fmt.Printf("\nError distribution:\n")
	for err, num := range r.errorDist {
		fmt.Printf("  [%d]\t%s\n", num, err)
	}
}

type result struct {
	err           error
	statusCode    int
	duration      time.Duration
	contentLength int64
}

type Bencher struct {
	// Request is the request to be made.
	Request *BenchRequest

	RequestBody string

	// N is the total number of requests to make.
	N int

	// C is the concurrency level, the number of concurrent workers to run.
	C int

	// H2 is an option to make HTTP/2 requests
	H2 bool

	// Timeout in seconds.
	Timeout int

	// Qps is the rate limit.
	Qps int

	// DisableCompression is an option to disable compression in response
	DisableCompression bool

	// DisableKeepAlives is an option to prevents re-use of TCP connections between different HTTP requests
	DisableKeepAlives bool

	// Output represents the output type. If "csv" is provided, the
	// output will be dumped as a csv stream.
	Output string

	// ProxyAddr is the address of HTTP proxy server in the format on "host:port".
	// Optional.
	ProxyAddr *url.URL

	results chan *result
}

// Run makes all the requests, prints the summary. It blocks until
// all work is done.
func (b *Bencher) Run() {
	b.results = make(chan *result, b.N)

	start := time.Now()
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)

	go func() {
		<-c
		b.Request.NewReport(b.N, b.results, b.Output, time.Now().Sub(start)).finalize()
		os.Exit(1)
	}()

	b.runWorkers()
	b.Request.NewReport(b.N, b.results, b.Output, time.Now().Sub(start)).finalize()
	close(b.results)
}

func (b *Bencher) makeRequest(c *http.Client) {
	s := time.Now()
	var size int64
	var code int

	resp, err := c.Do(cloneRequest(b.Request.GetRandomRequest(), b.RequestBody))
	if err == nil {
		size = resp.ContentLength
		code = resp.StatusCode
		io.Copy(ioutil.Discard, resp.Body)
		resp.Body.Close()
	}
	b.results <- &result{
		statusCode:    code,
		duration:      time.Now().Sub(s),
		err:           err,
		contentLength: size,
	}
}

func (b *Bencher) runWorker(n int) {
	var throttle <-chan time.Time
	if b.Qps > 0 {
		throttle = time.Tick(time.Duration(1e6/(b.Qps)) * time.Microsecond)
	}

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
		DisableCompression: b.DisableCompression,
		DisableKeepAlives:  b.DisableKeepAlives,
		// TODO(jbd): Add dial timeout.
		TLSHandshakeTimeout: time.Duration(b.Timeout) * time.Millisecond,
		Proxy:               http.ProxyURL(b.ProxyAddr),
	}
	if b.H2 {
		http2.ConfigureTransport(tr)
	} else {
		tr.TLSNextProto = make(map[string]func(string, *tls.Conn) http.RoundTripper)
	}
	client := &http.Client{Transport: tr}
	for i := 0; i < n; i++ {
		if b.Qps > 0 {
			<-throttle
		}
		b.makeRequest(client)
	}
}

func (b *Bencher) runWorkers() {
	var wg sync.WaitGroup
	wg.Add(b.C)

	// Ignore the case where b.N % b.C != 0.
	for i := 0; i < b.C; i++ {
		go func() {
			b.runWorker(b.N / b.C)
			wg.Done()
		}()
	}
	wg.Wait()
}

// cloneRequest returns a clone of the provided *http.Request.
// The clone is a shallow copy of the struct and its Header map.
func cloneRequest(r *http.Request, body string) *http.Request {
	// shallow copy of the struct
	r2 := new(http.Request)
	*r2 = *r
	// deep copy of the Header
	r2.Header = make(http.Header, len(r.Header))
	for k, s := range r.Header {
		r2.Header[k] = append([]string(nil), s...)
	}
	r2.Body = ioutil.NopCloser(strings.NewReader(body))
	return r2
}

const (
	headerRegexp = `^([\w-]+):\s*(.+)`
	authRegexp   = `^(.+):([^\s].+)`
)

type headerSlice []string

func (h *headerSlice) String() string {
	return fmt.Sprintf("%s", *h)
}

func (h *headerSlice) Set(value string) error {
	*h = append(*h, value)
	return nil
}

var (
	headerslice headerSlice
	m           = flag.String("m", "GET", "")
	headers     = flag.String("h", "", "")
	body        = flag.String("d", "", "")
	accept      = flag.String("A", "", "")
	contentType = flag.String("T", "text/html", "")
	authHeader  = flag.String("a", "", "")
	hostHeader  = flag.String("host", "", "")

	output = flag.String("o", "", "")

	c = flag.Int("c", 50, "")
	n = flag.Int("n", 200, "")
	q = flag.Int("q", 0, "")
	t = flag.Int("t", 0, "")

	h2 = flag.Bool("h2", false, "")

	cpus = flag.Int("cpus", runtime.GOMAXPROCS(-1), "")

	disableCompression = flag.Bool("disable-compression", false, "")
	disableKeepAlives  = flag.Bool("disable-keepalive", false, "")
	proxyAddr          = flag.String("x", "", "")

	urls = flag.String("f", "", "")
)

var usage = `Usage: go_bench [options...] <url>
Options:
  -n  Number of requests to run.
  -c  Number of requests to run concurrently. Total number of requests cannot
      be smaller than the concurency level.
  -q  Rate limit, in seconds (QPS).
  -o  Output type. If none provided, a summary is printed.
      "csv" is the only supported alternative. Dumps the response
      metrics in comma-seperated values format.
  -m  HTTP method, one of GET, POST, PUT, DELETE, HEAD, OPTIONS.
  -H  Custom HTTP header. You can specify as many as needed by repeating the flag.
	  for example, -H "Accept: text/html" -H "Content-Type: application/xml", 
	  but "Host: ***", replace that with -host.
  -t  Timeout in ms.
  -A  HTTP Accept header.
  -d  HTTP request body.
  -T  Content-type, defaults to "text/html".
  -a  Basic authentication, username:password.
  -x  HTTP Proxy address as host:port.
  -h2  Make HTTP/2 requests.
  -disable-compression  Disable compression.
  -disable-keepalive    Disable keep-alive, prevents re-use of TCP
                        connections between different HTTP requests.
  -cpus                 Number of used cpu cores.
                        (default for current machine is %d cores)
  -host                 HTTP Host header.
  -f  Request url file, a launch request in the random selection file
Example:
  ./go_bench -n 1000 -c 10 -t 3000 -m GET http://127.0.0.1/test1
  or
  ./go_bench -n 1000 -c 10 -t 3000 -m GET -f urls.txt

Notice:
  urls.txt format like this:
		http://127.0.0.1/test1
  		http://127.0.0.1/test2
`

func main() {
	defer func() {
		close(command)
	}()

	command = make(chan string, 100)
	go func() {
		for {
			select{
			case c := <-command:
				if _, ok := benchRequestUrl[c]; ok {
					benchRequestUrl[c]++
				} else {
					benchRequestUrl[c] = 1
				}
			default:
				break
			}
		}
	}()

	flag.Usage = func() {
		fmt.Fprint(os.Stderr, fmt.Sprintf(usage, runtime.NumCPU()))
	}

	flag.Var(&headerslice, "H", "")
	flag.Parse()

	if flag.NArg() < 1 && *urls == "" {
		usageAndExit("")
	}

	runtime.GOMAXPROCS(*cpus)
	num := *n
	conc := *c
	q := *q

	if num <= 0 || conc <= 0 {
		usageAndExit("n and c cannot be smaller than 1.")
	}

	if num < conc {
		usageAndExit("n cannot be less than c")
	}

	var urlArr []string
	if *urls == "" {
		urlArr = append(urlArr,flag.Args()[0])
	} else {
		// 解析urls.txt文件
		var err error
		if urlArr, err = ParseUrlsFile(*urls); err != nil {
			usageAndExit("urls.txt file read error.")
		}
	}
	method := strings.ToUpper(*m)

	// set content-type
	header := make(http.Header)
	header.Set("Content-Type", *contentType)
	// set any other additional headers
	if *headers != "" {
		usageAndExit("flag '-h' is deprecated, please use '-H' instead.")
	}
	// set any other additional repeatable headers
	for _, h := range headerslice {
		match, err := parseInputWithRegexp(h, headerRegexp)
		if err != nil {
			usageAndExit(err.Error())
		}
		header.Set(match[1], match[2])
	}

	if *accept != "" {
		header.Set("Accept", *accept)
	}

	// set basic auth if set
	var username, password string
	if *authHeader != "" {
		match, err := parseInputWithRegexp(*authHeader, authRegexp)
		if err != nil {
			usageAndExit(err.Error())
		}
		username, password = match[1], match[2]
	}

	if *output != "csv" && *output != "" {
		usageAndExit("Invalid output type; only csv is supported.")
	}

	var proxyURL *gourl.URL
	if *proxyAddr != "" {
		var err error
		proxyURL, err = gourl.Parse(*proxyAddr)
		if err != nil {
			usageAndExit(err.Error())
		}
	}

	req, err := NewRequest(method, urlArr, nil)
	if err != nil {
		usageAndExit(err.Error())
	}
	req.Header = header
	if username != "" || password != "" {
		req.SetBasicAuth(username, password)
	}

	// set host header if set
	if *hostHeader != "" {
		req.Host = *hostHeader
	}

	(&Bencher{
		Request:            req,
		RequestBody:        *body,
		N:                  num,
		C:                  conc,
		Qps:                q,
		Timeout:            *t,
		DisableCompression: *disableCompression,
		DisableKeepAlives:  *disableKeepAlives,
		H2:                 *h2,
		ProxyAddr:          proxyURL,
		Output:             *output,
	}).Run()
}

func usageAndExit(msg string) {
	if msg != "" {
		fmt.Fprintf(os.Stderr, msg)
		fmt.Fprintf(os.Stderr, "\n\n")
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