package main

import (
	"github.com/linkxzhou/http_bench/internal/request"
	"github.com/linkxzhou/http_bench/internal/transport"
	"testing"
	"time"
)

func TestRequestSpec_MergeDefaults(t *testing.T) {
	defaults := &transport.HttpbenchParameters{
		N:                 100,
		C:                 10,
		Duration:          5 * time.Second,
		Timeout:           30 * time.Second,
		QPS:               0,
		ProxyURL:          "http://proxy:8080",
		DisableKeepAlives: true,
		URL:               "https://default.example.com",
		RequestMethod:     "GET",
		Headers:           map[string][]string{"X-Default": {"v"}},
		RequestBody:       "default-body",
		RequestBodyType:   "text",
		Output:            "json",
	}

	t.Run("spec_overrides_defaults", func(t *testing.T) {
		spec := request.Spec{
			Method: "POST",
			URL:    "https://spec.example.com/api",
			Headers: map[string][]string{
				"Authorization": {"Bearer t"},
				"Content-Type":  {"application/json"},
			},
			Body: `{"k":"v"}`,
		}
		p := spec.MergeDefaults(defaults)

		// Spec fields win.
		if p.RequestMethod != "POST" {
			t.Errorf("method = %s, want POST (spec)", p.RequestMethod)
		}
		if p.URL != "https://spec.example.com/api" {
			t.Errorf("url = %s, want spec URL", p.URL)
		}
		if p.RequestBody != `{"k":"v"}` {
			t.Errorf("body = %s, want spec body", p.RequestBody)
		}
		if got := p.Headers["Authorization"]; len(got) != 1 || got[0] != "Bearer t" {
			t.Errorf("headers = %v, want spec headers", p.Headers)
		}

		// Execution params come from defaults.
		if p.N != 100 || p.C != 10 {
			t.Errorf("N/C = %d/%d, want 100/10 (defaults)", p.N, p.C)
		}
		if p.Duration != 5*time.Second || p.Timeout != 30*time.Second {
			t.Errorf("duration/timeout mismatch: %v/%v", p.Duration, p.Timeout)
		}
		if p.ProxyURL != "http://proxy:8080" {
			t.Errorf("proxy = %s, want default proxy", p.ProxyURL)
		}
		if !p.DisableKeepAlives {
			t.Error("DisableKeepAlives should come from defaults")
		}
		if p.Output != "json" {
			t.Errorf("output = %s, want json (defaults)", p.Output)
		}
	})

	t.Run("empty_spec_uses_defaults", func(t *testing.T) {
		spec := request.Spec{}
		p := spec.MergeDefaults(defaults)

		if p.RequestMethod != "GET" {
			t.Errorf("method = %s, want GET (defaults)", p.RequestMethod)
		}
		if p.URL != "https://default.example.com" {
			t.Errorf("url = %s, want default URL", p.URL)
		}
		if p.RequestBody != "default-body" {
			t.Errorf("body = %s, want default body", p.RequestBody)
		}
		if got := p.Headers["X-Default"]; len(got) != 1 || got[0] != "v" {
			t.Errorf("headers = %v, want default headers", p.Headers)
		}
	})
}
