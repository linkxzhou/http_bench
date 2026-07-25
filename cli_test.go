package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestParseConfig_Defaults(t *testing.T) {
	opts, err := ParseConfig(nil, nil, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if opts.Concurrency == 0 {
		t.Errorf("default concurrency not set")
	}
	if opts.Duration <= 0 {
		t.Errorf("default duration not parsed")
	}
	if opts.Timeout <= 0 {
		t.Errorf("default timeout not parsed")
	}
	if opts.Verbose != defaultVerboseLevel {
		t.Errorf("default verbose mismatch: %d", opts.Verbose)
	}
}

func TestParseConfig_Values(t *testing.T) {
	opts, err := ParseConfig([]string{"-n", "100", "-c", "8", "-d", "2s", "-t", "500ms", "-q", "50", "-url", "http://example.com", "-H", "X-Trace: 1", "-W", "127.0.0.1:9001"}, func(string) string { return "" }, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if opts.Count != 100 {
		t.Errorf("Count = %d", opts.Count)
	}
	if opts.Concurrency != 8 {
		t.Errorf("Concurrency = %d", opts.Concurrency)
	}
	if opts.Duration != 2*time.Second {
		t.Errorf("Duration = %v", opts.Duration)
	}
	if opts.Timeout != 500*time.Millisecond {
		t.Errorf("Timeout = %v", opts.Timeout)
	}
	if opts.QPS != 50 {
		t.Errorf("QPS = %d", opts.QPS)
	}
	if opts.URL != "http://example.com" {
		t.Errorf("URL = %q", opts.URL)
	}
	if len(opts.Headers) != 1 || opts.Headers[0] != "X-Trace: 1" {
		t.Errorf("Headers = %v", opts.Headers)
	}
	if len(opts.WorkerAddrs) != 1 || opts.WorkerAddrs[0] != "127.0.0.1:9001" {
		t.Errorf("WorkerAddrs = %v", opts.WorkerAddrs)
	}
}

func TestParseConfig_EnvInjection(t *testing.T) {
	env := map[string]string{"HTTPBENCH_AUTH_KEY": "secret", "HTTPBENCH_GOGC": "200", "HTTPBENCH_WORKERAPI": "/custom"}
	getter := func(k string) string { return env[k] }
	opts, err := ParseConfig(nil, getter, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if opts.AuthKey != "secret" {
		t.Errorf("AuthKey = %q", opts.AuthKey)
	}
	if opts.GCPercent != 200 {
		t.Errorf("GCPercent = %d", opts.GCPercent)
	}
	if opts.WorkerAPIPath != "/custom" {
		t.Errorf("WorkerAPIPath = %q", opts.WorkerAPIPath)
	}
}

func TestParseConfig_InvalidDuration(t *testing.T) {
	_, err := ParseConfig([]string{"-d", "abc"}, nil, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected error for invalid duration")
	}
	if !strings.Contains(err.Error(), "invalid -d") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParseConfig_InvalidTimeout(t *testing.T) {
	_, err := ParseConfig([]string{"-t", "xyz"}, nil, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected error for invalid timeout")
	}
	if !strings.Contains(err.Error(), "invalid -t") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParseConfig_UnknownFlag(t *testing.T) {
	var buf bytes.Buffer
	_, err := ParseConfig([]string{"-unknown"}, nil, &buf)
	if err == nil {
		t.Fatal("expected error for unknown flag")
	}
}

func TestParseConfig_PositionalURL(t *testing.T) {
	opts, err := ParseConfig([]string{"-c", "1", "-d", "1s", "http://example.com"}, nil, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if opts.URL != "http://example.com" {
		t.Errorf("URL = %q, want %q", opts.URL, "http://example.com")
	}
}

func TestParseConfig_ConflictingURL(t *testing.T) {
	_, err := ParseConfig([]string{"-url", "http://a.com", "http://b.com"}, nil, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected error for conflicting URL sources")
	}
	if !strings.Contains(err.Error(), "conflicting URL sources") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestParseConfig_FlagsAfterPositionalURL covers the documented invocation
// style where flags follow the positional URL (README distributed example).
// flag.Parse would otherwise stop at the URL and silently drop -body/-W.
func TestParseConfig_FlagsAfterPositionalURL(t *testing.T) {
	opts, err := ParseConfig([]string{
		"-n", "10000", "-c", "10", "-d", "30s", "-m", "POST",
		"http://www.baidu.com/api/test",
		"-body", `{"key":"value"}`, "-W", "127.0.0.1:12710", "-W", "127.0.0.1:12711",
		"-disable-keepalive", "-insecure=false",
	}, nil, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if opts.URL != "http://www.baidu.com/api/test" {
		t.Errorf("URL = %q", opts.URL)
	}
	if opts.Body != `{"key":"value"}` {
		t.Errorf("Body = %q", opts.Body)
	}
	if len(opts.WorkerAddrs) != 2 || opts.WorkerAddrs[0] != "127.0.0.1:12710" || opts.WorkerAddrs[1] != "127.0.0.1:12711" {
		t.Errorf("WorkerAddrs = %v", opts.WorkerAddrs)
	}
	if !opts.DisableKeepAlives {
		t.Error("DisableKeepAlives should be true")
	}
	if opts.Insecure {
		t.Error("Insecure should be false")
	}
	if opts.Method != "POST" || opts.Count != 10000 || opts.Concurrency != 10 {
		t.Errorf("unexpected opts: %+v", opts)
	}
}
