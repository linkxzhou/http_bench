package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	gourl "net/url"
	"os"
	"os/signal"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	CMD_START = 0x01
	CMD_STOP  = 0x02
)

type (
	Report struct {
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
	}

	headerSlice []string

	BenchParameters struct {
		// Commands, eg: "START、STOP、RESULT".
		Cmd int `json:"cmd"`
		// Request Method.
		RequestMethod string `json:"request_method"`
		// Request Body.
		RequestBody string `json:"request_body"`
		// N is the total number of requests to make.
		N int `json:"n"`
		// C is the concurrency level, the number of concurrent workers to run.
		C int `json:"c"`
		// Timeout in seconds.
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
		AuthUsername string              `json:"auth_username"`
		AuthPassword string              `json:"auth_password"`
		Headers      map[string][]string `json:"headers"`
		Urls         []string            `json:"urls"`
	}
)

func (h *headerSlice) String() string {
	return fmt.Sprintf("%s", *h)
}

func (h *headerSlice) Set(value string) error {
	*h = append(*h, value)
	return nil
}

func (r *Report) finalize() {
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

func (r *Report) print() {
	sort.Float64s(r.lats)

	switch r.output {
	case "csv":
		r.printCSV()
		return
	default:
		// pass
	}

	if len(r.lats) > 0 {
		r.fastest = r.lats[0]
		r.slowest = r.lats[len(r.lats)-1]

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
		r.printLatencies()
	}

	if len(r.errorDist) > 0 {
		r.printErrors()
	}
}

func (r *Report) printCSV() {
	for i, val := range r.lats {
		fmt.Printf("%v,%4.4f\n", i+1, val)
	}
}

func (r *Report) printLatencies() {
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

// Prints status code distribution.
func (r *Report) printStatusCodes() {
	fmt.Printf("\nStatus code distribution:\n")
	for code, num := range r.statusCodeDist {
		fmt.Printf("  [%d]\t%d responses\n", code, num)
	}
}

func (r *Report) printErrors() {
	fmt.Printf("\nError distribution:\n")
	for err, num := range r.errorDist {
		fmt.Printf("  [%d]\t%s\n", num, err)
	}
}

type (
	result struct {
		err           error
		statusCode    int
		duration      time.Duration
		contentLength int64
	}

	BenchProducer struct {
		RequestParams *BenchParameters
		// Output represents the output type. If "csv" is provided, the
		// output will be dumped as a csv stream.
		Output string
		// ProxyAddr is the address of HTTP proxy server in the format on "host:port".
		ProxyURL *url.URL
		results  chan *result
		signal   chan os.Signal
		wg       sync.WaitGroup
	}
)

func (b *BenchProducer) Start() {
	var wg sync.WaitGroup

	b.results = make(chan *result, b.RequestParams.N)

	start := time.Now()
	b.signal = make(chan os.Signal, 1)
	signal.Notify(b.signal, os.Interrupt)

	wg.Add(2)

	go func() {
		<-b.signal
		b.RequestParams.Cmd = CMD_STOP
		close(b.results)
		wg.Done()
		wg.Wait()
	}()

	go func() {
		b.newReport(time.Now().Sub(start)).finalize()
		wg.Done()
	}()

	b.runWorkers()
}

func (b *BenchProducer) runWorker(n int) {
	var throttle <-chan time.Time
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
		Proxy:               http.ProxyURL(b.ProxyURL),
		TLSNextProto:        make(map[string]func(string, *tls.Conn) http.RoundTripper),
	}

	client := &http.Client{Transport: tr}
	for i := 0; i < n && b.RequestParams.Cmd != CMD_STOP; i++ {
		if b.RequestParams.Qps > 0 {
			<-throttle
		}

		var t = time.Now()
		var size int64
		var code int

		resp, err := client.Do(b.getRequest())
		if err == nil {
			size = resp.ContentLength
			code = resp.StatusCode
			io.Copy(ioutil.Discard, resp.Body)
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

func (b *BenchProducer) runWorkers() {
	// Ignore the case where b.RequestParams.N % b.RequestParams.C != 0.
	var wg sync.WaitGroup
	for i := 0; i < b.RequestParams.C && b.RequestParams.Cmd != CMD_STOP; i++ {
		wg.Add(1)
		go func() {
			b.runWorker(b.RequestParams.N / b.RequestParams.C)
			wg.Done()
		}()
	}
	wg.Wait()
}

func (b *BenchProducer) getRequest() *http.Request {
	// shallow copy of the struct
	r2 := new(http.Request)
	return r2
}

func (b *BenchProducer) newReport(total time.Duration) *Report {
	return &Report{
		output:         b.Output,
		results:        b.results,
		total:          total,
		statusCodeDist: make(map[int]int),
		errorDist:      make(map[string]int),
	}
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

var (
	params BenchParameters

	headerRegexp = `^([\w-]+):\s*(.+)`
	authRegexp   = `^(.+):([^\s].+)`

	m          = flag.String("m", "GET", "")
	body       = flag.String("d", "", "")
	authHeader = flag.String("a", "", "")

	output = flag.String("o", "", "") // Output type

	c = flag.Int("c", 50, "")  // Number of requests to run concurrently
	n = flag.Int("n", 200, "") // Number of requests to run
	q = flag.Int("q", 0, "")   // Rate limit, in seconds (QPS)
	t = flag.Int("t", 0, "")   // Timeout in ms

	cpus = flag.Int("cpus", runtime.GOMAXPROCS(-1), "")

	disableCompression = flag.Bool("disable-compression", false, "")
	disableKeepAlives  = flag.Bool("disable-keepalive", false, "")
	proxyAddr          = flag.String("x", "", "")

	urlFile = flag.String("file", "", "")
)

var usage = `Usage: go_bench [options...] <url>
Options:
  -n  Number of requests to run.
  -c  Number of requests to run concurrently. Total number of requests cannot
      be smaller than the concurency level.
  -q  Rate limit, in seconds (QPS).
  -t  Timeout in ms.
  -o  Output type. If none provided, a summary is printed.
      "csv" is the only supported alternative. Dumps the response
      metrics in comma-seperated values format.
  -m  HTTP method, one of GET, POST, PUT, DELETE, HEAD, OPTIONS.
  -H  Custom HTTP header. You can specify as many as needed by repeating the flag.
	  for example, -H "Accept: text/html" -H "Content-Type: application/xml", 
	  but "Host: ***", replace that with -host.
  -a  Basic authentication, username:password.
  -x  HTTP Proxy address as host:port.
  -disable-compression  Disable compression.
  -disable-keepalive    Disable keep-alive, prevents re-use of TCP
                        connections between different HTTP requests.
  -cpus                 Number of used cpu cores.
                        (default for current machine is %d cores)
  -file  Request url file, a launch request in the random selection file
Example:
  ./http_bench -n 1000 -c 10 -t 3000 -m GET http://127.0.0.1/test1
  or
  ./http_bench -n 1000 -c 10 -t 3000 -m GET -file urls.txt

Notice:
  urls.txt format like this:
		http://127.0.0.1/test1
  		http://127.0.0.1/test2
`

func main() {
	flag.Usage = func() {
		fmt.Fprint(os.Stderr, fmt.Sprintf(usage, runtime.NumCPU()))
	}

	var headerslice headerSlice
	flag.Var(&headerslice, "H", "") // Custom HTTP header
	flag.Parse()

	if flag.NArg() < 1 {
		usageAndExit("")
	}

	runtime.GOMAXPROCS(*cpus)
	params.N = *n
	params.C = *c
	params.Qps = *q

	if params.N <= 0 || params.C <= 0 {
		usageAndExit("n and c cannot be smaller than 1.")
	}

	if params.N < params.C {
		usageAndExit("n cannot be less than c")
	}

	if *urlFile == "" {
		params.Urls = append(params.Urls, flag.Args()[0])
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

	var proxyUrl *gourl.URL
	if *proxyAddr != "" {
		var err error
		if proxyUrl, err = gourl.Parse(*proxyAddr); err != nil {
			usageAndExit(err.Error())
		}
		params.ProxyAddr = *proxyAddr
	}

	p := &BenchProducer{
		RequestParams: &params,
		Output:        *output,
		ProxyURL:      proxyUrl,
	}
	p.Start()
}
