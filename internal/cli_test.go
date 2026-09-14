package bench

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestParseConfig_NClearsDefaultDuration(t *testing.T) {
	opts, err := ParseConfig([]string{"-n", "10", "-c", "2", "http://example.com"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if opts.Duration != 0 {
		t.Fatalf("Duration=%v, want 0 when only -n set", opts.Duration)
	}
	if opts.Count != 10 || opts.Concurrency != 2 || opts.URL != "http://example.com" {
		t.Fatalf("opts=%+v", opts)
	}
}

func TestParseConfig_ExplicitDurationKept(t *testing.T) {
	opts, err := ParseConfig([]string{"-n", "10", "-d", "5s", "http://example.com"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if opts.Duration != 5*time.Second {
		t.Fatalf("Duration=%v, want 5s", opts.Duration)
	}
}

func TestParseConfig_DayWeekSuffix(t *testing.T) {
	opts, err := ParseConfig([]string{"-d", "1D", "-c", "1", "http://example.com"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if opts.Duration != 24*time.Hour {
		t.Fatalf("Duration=%v", opts.Duration)
	}
}

func TestParseConfig_RepeatableFlagsAndReorder(t *testing.T) {
	opts, err := ParseConfig([]string{
		"-n", "1",
		"http://example.com/a",
		"-H", "X-A: 1",
		"-H", "X-B: 2",
		"-W", "127.0.0.1:1",
		"-w", "127.0.0.1:2",
		"-body", "hi",
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if opts.URL != "http://example.com/a" {
		t.Fatalf("URL=%q", opts.URL)
	}
	if opts.Body != "hi" {
		t.Fatalf("Body=%q", opts.Body)
	}
	if len(opts.Headers) != 2 || len(opts.WorkerAddrs) != 2 {
		t.Fatalf("headers=%v workers=%v", opts.Headers, opts.WorkerAddrs)
	}
}

func TestParseConfig_EnvAndGCPercent(t *testing.T) {
	env := map[string]string{
		"HTTPBENCH_GOGC":      "50",
		"HTTPBENCH_AUTH_KEY":  "secret",
		"HTTPBENCH_WORKERAPI": "/w",
	}
	opts, err := ParseConfig([]string{"-n", "1", "http://x"}, func(k string) string { return env[k] }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if opts.GCPercent != 50 || opts.AuthKey != "secret" || opts.WorkerAPIPath != "/w" {
		t.Fatalf("%+v", opts)
	}
}

func TestParseConfig_UnknownFlagWritesStderr(t *testing.T) {
	var buf bytes.Buffer
	_, err := ParseConfig([]string{"-not-a-real-flag"}, nil, &buf)
	if err == nil {
		t.Fatal("want error")
	}
	if buf.Len() == 0 {
		t.Fatal("expected stderr output from flag package")
	}
}

func TestReorderArgs_BoolAndEquals(t *testing.T) {
	got := reorderArgs([]string{
		"-n", "1",
		"http://example.com",
		"-disable-compression",
		"-verbose=0",
		"-m", "POST",
	})
	joined := strings.Join(got, " ")
	if !strings.HasSuffix(joined, "http://example.com") {
		t.Fatalf("positional should be last: %v", got)
	}
	if !strings.Contains(joined, "-disable-compression") || !strings.Contains(joined, "-verbose=0") {
		t.Fatalf("flags lost: %v", got)
	}
}

func TestStringSliceFlag(t *testing.T) {
	var vals []string
	f := stringSliceFlag{&vals}
	_ = f.Set("a")
	_ = f.Set("b")
	if f.String() != "a,b" {
		t.Fatalf("String=%q", f.String())
	}
	empty := stringSliceFlag{}
	if empty.String() != "" {
		t.Fatalf("nil target String=%q", empty.String())
	}
}
