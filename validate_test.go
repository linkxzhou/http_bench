package main

import (
	"github.com/linkxzhou/http_bench/internal/transport"
	"strings"
	"testing"
	"time"
)

func TestValidateParams(t *testing.T) {
	tests := []struct {
		name    string
		params  transport.HttpbenchParameters
		wantErr string
	}{
		{
			name:    "concurrency zero",
			params:  transport.HttpbenchParameters{C: 0, N: 10, Duration: 5 * time.Second},
			wantErr: "concurrency",
		},
		{
			name:    "concurrency negative",
			params:  transport.HttpbenchParameters{C: -1, N: 10, Duration: 5 * time.Second},
			wantErr: "concurrency",
		},
		{
			name:    "n less than c",
			params:  transport.HttpbenchParameters{C: 10, N: 5},
			wantErr: "less than concurrency",
		},
		{
			name:    "neither n nor duration",
			params:  transport.HttpbenchParameters{C: 10},
			wantErr: "either -n",
		},
		{
			name:   "valid n mode",
			params: transport.HttpbenchParameters{C: 10, N: 1000},
		},
		{
			name:   "valid duration mode",
			params: transport.HttpbenchParameters{C: 5, Duration: 10 * time.Second},
		},
		{
			name:   "valid n equals c",
			params: transport.HttpbenchParameters{C: 10, N: 10},
		},
		{
			name:   "duration zero with n positive",
			params: transport.HttpbenchParameters{C: 5, N: 50, Duration: 0},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateParams(&tt.params)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestValidateOutputFormat(t *testing.T) {
	valid := []string{"", "summary", "csv", "html"}
	for _, o := range valid {
		if err := validateOutputFormat(o); err != nil {
			t.Errorf("validateOutputFormat(%q) = %v, want nil", o, err)
		}
	}
	if err := validateOutputFormat("xml"); err == nil {
		t.Error("validateOutputFormat(\"xml\") = nil, want error")
	}
	if err := validateOutputFormat("json"); err == nil {
		t.Error("validateOutputFormat(\"json\") = nil, want error")
	}
}

func TestValidateProxyURL(t *testing.T) {
	if err := validateProxyURL(""); err != nil {
		t.Errorf("validateProxyURL(\"\") = %v, want nil", err)
	}
	if err := validateProxyURL("http://proxy.example.com:8080"); err != nil {
		t.Errorf("validateProxyURL valid = %v, want nil", err)
	}
	if err := validateProxyURL("://invalid"); err == nil {
		t.Error("validateProxyURL(\"://invalid\") = nil, want error")
	}
}

func TestCompileHeaders(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		h, err := compileHeaders(nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if h != nil {
			t.Fatalf("expected nil map, got %v", h)
		}
	})

	t.Run("valid headers", func(t *testing.T) {
		h, err := compileHeaders([]string{"Content-Type: application/json", "X-Trace: abc"}, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := h["Content-Type"]; len(got) != 1 || got[0] != "application/json" {
			t.Errorf("Content-Type = %v, want [application/json]", got)
		}
		if got := h["X-Trace"]; len(got) != 1 || got[0] != "abc" {
			t.Errorf("X-Trace = %v, want [abc]", got)
		}
	})

	t.Run("invalid header", func(t *testing.T) {
		_, err := compileHeaders([]string{"no-colon-here"}, "")
		if err == nil {
			t.Fatal("expected error for malformed header, got nil")
		}
		if !strings.Contains(err.Error(), "invalid header") {
			t.Errorf("error %q does not mention invalid header", err.Error())
		}
	})

	t.Run("valid auth", func(t *testing.T) {
		h, err := compileHeaders(nil, "admin:secret")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		auth := h["Authorization"]
		if len(auth) != 1 || !strings.HasPrefix(auth[0], "Basic ") {
			t.Errorf("Authorization = %v, want Basic prefix", auth)
		}
	})

	t.Run("invalid auth", func(t *testing.T) {
		_, err := compileHeaders(nil, "no-colon")
		if err == nil {
			t.Fatal("expected error for malformed auth, got nil")
		}
		if !strings.Contains(err.Error(), "invalid auth") {
			t.Errorf("error %q does not mention invalid auth", err.Error())
		}
	})

	t.Run("headers and auth combined", func(t *testing.T) {
		h, err := compileHeaders([]string{"Accept: */*"}, "user:pass")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := h["Accept"]; !ok {
			t.Error("missing Accept header")
		}
		if _, ok := h["Authorization"]; !ok {
			t.Error("missing Authorization header")
		}
	})
}
