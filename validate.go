package main

// Validation helpers for CLI parameters. These are pure functions returning
// errors so they can be unit-tested without os.Exit. The main() entry point
// decides how to surface the error (via usageAndExit).
//
// This addresses plan.md §8 "待实施 — 10（余）错误边界：剩余 usageAndExit
// 调用点（校验类）移除" (阶段 B/H).

import (
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/linkxzhou/http_bench/internal/transport"
	"regexp"

	gourl "net/url"
)

func parseInputWithRegexp(input string, re *regexp.Regexp) ([]string, error) {
	matches := re.FindStringSubmatch(input)
	if len(matches) < 1 {
		return nil, fmt.Errorf("could not parse the provided input; input = %v", input)
	}
	return matches, nil
}

// outputFormats maps the supported -o values. "" (summary) is the default.
var outputFormats = map[string]bool{
	"":        true,
	"summary": true,
	"csv":     true,
	"html":    true,
}

// validateParams checks the core run options (concurrency, request count,
// duration) and returns a descriptive error on violation.
//
// Semantics (per plan.md §1.5/#6):
//   - C must be >= 1
//   - when N > 0, N must be >= C (each worker gets at least one request)
//   - at least one of N (>0) or Duration (>0) must be set
func validateParams(p *transport.HttpbenchParameters) error {
	if p.C <= 0 {
		return errors.New("concurrency (-c) must be at least 1")
	}
	if p.N > 0 && p.N < p.C {
		return fmt.Errorf("total requests (-n) cannot be less than concurrency (-c): n=%d c=%d", p.N, p.C)
	}
	if p.N <= 0 && p.Duration <= 0 {
		return errors.New("either -n (request count) or -d (duration) must be specified")
	}
	return nil
}

// validateOutputFormat checks the -o flag value against the supported set.
func validateOutputFormat(output string) error {
	if !outputFormats[output] {
		return fmt.Errorf("invalid output format %q; supported formats: csv, html", output)
	}
	return nil
}

// validateProxyURL parses the proxy address and returns an error on failure.
func validateProxyURL(proxyAddr string) error {
	if _, err := gourl.Parse(proxyAddr); err != nil {
		return fmt.Errorf("invalid proxy URL: %v", err)
	}
	return nil
}

// parseCLIHeaders parses a slice of "Key: Value" header strings using the given
// regexp and appends them into out. Returns the first error encountered.
// The regexp must capture exactly two groups (key, value).
func parseCLIHeaders(headers []string, re *regexp.Regexp, out map[string][]string) error {
	for _, h := range headers {
		match, err := parseInputWithRegexp(h, re)
		if err != nil {
			return fmt.Errorf("invalid header format: %v", err)
		}
		out[match[1]] = []string{match[2]}
	}
	return nil
}

// parseAuth parses a "user:pass" auth string using the given regexp and sets
// the Authorization header on out. Returns the first error encountered.
func parseAuth(authHeader string, re *regexp.Regexp, out map[string][]string) error {
	match, err := parseInputWithRegexp(authHeader, re)
	if err != nil {
		return fmt.Errorf("invalid auth format: %v", err)
	}
	authValue := base64.StdEncoding.EncodeToString([]byte(match[1] + ":" + match[2]))
	out["Authorization"] = []string{fmt.Sprintf("Basic %s", authValue)}
	return nil
}

// compileHeaders builds the headers map from CLI inputs. It parses custom
// headers and optional basic auth, returning the resulting map or the first
// error.
func compileHeaders(headerSlice []string, authHeader string) (map[string][]string, error) {
	var headers map[string][]string
	if len(headerSlice) > 0 {
		headers = make(map[string][]string, len(headerSlice))
		if err := parseCLIHeaders(headerSlice, HeaderRegexp, headers); err != nil {
			return nil, err
		}
	}
	if authHeader != "" {
		if headers == nil {
			headers = make(map[string][]string)
		}
		if err := parseAuth(authHeader, AuthRegexp, headers); err != nil {
			return nil, err
		}
	}
	return headers, nil
}
