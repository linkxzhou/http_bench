package main

import (
	"flag"
	"fmt"
	"io"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/linkxzhou/http_bench/internal/transport"
)

// Options is the parsed CLI configuration. main() consumes it to drive
// scenario construction and dashboard startup.
type Options struct {
	SequenceId         int64
	Count              int
	Concurrency        int
	QPS                int
	Duration           time.Duration
	Timeout            time.Duration
	Method             string
	Headers            []string
	Auth               string
	Body               string
	BodyType           string
	HTTPType           string
	Protocol           string
	Output             string
	URL                string
	File               string
	Listen             string
	Verbose            int
	CPUs               int
	DisableCompression bool
	DisableKeepAlives  bool
	ProxyAddr          string
	Insecure           bool
	WorkerAddrs        []string
	PrintExample       bool
	GCPercent          int
	WorkerAPIPath      string
	AuthKey            string
	GOGC               string
}

// ParseConfig is the CLI parser. It returns Options or an error and never
// calls os.Exit so the caller (main or a test) controls the failure path.
func ParseConfig(args []string, getenv func(string) string, stderr io.Writer) (Options, error) {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	if stderr == nil {
		stderr = io.Discard
	}
	fs := flag.NewFlagSet("http_bench", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var opts Options
	fs.IntVar(&opts.Count, "n", 0, "Total number of requests")
	fs.IntVar(&opts.Concurrency, "c", transport.DefaultConcurrency, "Concurrent workers")
	fs.IntVar(&opts.QPS, "q", 0, "QPS limit (0 = unlimited)")
	fs.StringVar(&opts.Method, "m", "GET", "HTTP method")
	fs.StringVar(&opts.Body, "body", "", "Request body")
	fs.StringVar(&opts.BodyType, "bodytype", "string", "Body format (string|hex)")
	fs.StringVar(&opts.Auth, "a", "", "Basic auth")
	fs.StringVar(&opts.Output, "o", "", "Output format (summary|csv|html)")
	fs.StringVar(&opts.HTTPType, "http", transport.ProtocolHTTP1, "HTTP protocol")
	fs.StringVar(&opts.Protocol, "p", "", "Protocol alias")
	fs.StringVar(&opts.URL, "url", "", "Target URL")
	fs.StringVar(&opts.File, "file", "", "URL list file")
	fs.StringVar(&opts.Listen, "listen", "", "Dashboard listen address")
	fs.IntVar(&opts.Verbose, "verbose", defaultVerboseLevel, "Verbosity level")
	fs.IntVar(&opts.CPUs, "cpus", runtime.GOMAXPROCS(-1), "CPU limit")
	fs.BoolVar(&opts.DisableCompression, "disable-compression", false, "Disable compression")
	fs.BoolVar(&opts.DisableKeepAlives, "disable-keepalive", false, "Disable keep-alive")
	fs.StringVar(&opts.ProxyAddr, "x", "", "Proxy address")
	fs.BoolVar(&opts.Insecure, "insecure", true, "Skip TLS verification")
	fs.BoolVar(&opts.PrintExample, "example", false, "Print examples and exit")
	rawDur := fs.String("d", defaultDuration, "Test duration")
	rawTimeout := fs.String("t", defaultTimeout, "Request timeout")
	fs.Var(stringSliceFlag{&opts.Headers}, "H", "Custom header (repeatable)")
	fs.Var(stringSliceFlag{&opts.WorkerAddrs}, "W", "Worker address (repeatable)")
	fs.Var(stringSliceFlag{&opts.WorkerAddrs}, "w", "Worker address alias")
	if err := fs.Parse(args); err != nil {
		return Options{}, err
	}
	// Detect whether -d was explicitly set on the command line. When the
	// user only passes -n (without -d), Duration must be zeroed so the
	// request-count limiter in worker.do() activates. Otherwise the
	// default 10s duration hijacks every -n-only run, making `-n 1` take
	// 10 seconds. fs.Visit only reports flags that were explicitly set.
	durationExplicitlySet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "d" {
			durationExplicitlySet = true
		}
	})
	// Positional URL: if -url is unset and a positional arg remains, treat
	// the first positional arg as the target URL. If both are set, prefer
	// -url and reject the conflict to avoid ambiguity.
	if posArgs := fs.Args(); len(posArgs) > 0 {
		if opts.URL != "" {
			return Options{}, fmt.Errorf("conflicting URL sources: -url %q and positional %q", opts.URL, posArgs[0])
		}
		opts.URL = posArgs[0]
	}
	opts.GOGC = getenv("HTTPBENCH_GOGC")
	opts.AuthKey = getenv("HTTPBENCH_AUTH_KEY")
	opts.WorkerAPIPath = getenv("HTTPBENCH_WORKERAPI")
	if opts.GOGC != "" {
		if v, err := strconv.Atoi(opts.GOGC); err == nil {
			opts.GCPercent = v
		}
	}
	dur, err := parseDuration(*rawDur)
	if err != nil {
		return Options{}, fmt.Errorf("invalid -d: %w", err)
	}
	opts.Duration = dur
	// If -d was not explicitly set and -n > 0, clear Duration so the
	// request-count limiter governs the run instead of a phantom 10s timer.
	if !durationExplicitlySet && opts.Count > 0 {
		opts.Duration = 0
	}
	timeout, err := parseDuration(*rawTimeout)
	if err != nil {
		return Options{}, fmt.Errorf("invalid -t: %w", err)
	}
	opts.Timeout = timeout
	return opts, nil
}

// stringSliceFlag implements flag.Value for repeatable string flags.
type stringSliceFlag struct{ target *[]string }

func (f stringSliceFlag) String() string {
	if f.target == nil {
		return ""
	}
	return strings.Join(*f.target, ",")
}
func (f stringSliceFlag) Set(v string) error {
	*f.target = append(*f.target, v)
	return nil
}
