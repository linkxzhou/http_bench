// Package request defines the request definition (Spec) and the .http file
// parser. The package is decoupled from transport/execution settings so the
// parser can be tested independently and reused by alternative frontends.
package request

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/linkxzhou/http_bench/internal/logging"
	"github.com/linkxzhou/http_bench/internal/transport"
)

// --------------------------------------------------------------- Spec ---

// Spec captures the request definition portion of a benchmark. It holds only
// method, URL, headers, and body. Execution settings (C, N, Duration, Timeout,
// ProxyUrl, Qps) are stored on the transport DTO and applied by the caller via
// MergeDefaults.
type Spec struct {
	Method  string
	URL     string
	Headers map[string][]string
	Body    string
}

// MergeDefaults produces a fully-formed transport.HttpbenchParameters by
// applying CLI defaults to a spec. Spec fields override defaults.
func (s Spec) MergeDefaults(defaults *transport.HttpbenchParameters) transport.HttpbenchParameters {
	p := transport.HttpbenchParameters{
		N:                  defaults.N,
		C:                  defaults.C,
		Duration:           defaults.Duration,
		Timeout:            defaults.Timeout,
		QPS:                defaults.QPS,
		DisableCompression: defaults.DisableCompression,
		DisableKeepAlives:  defaults.DisableKeepAlives,
		ProxyURL:           defaults.ProxyURL,
		RequestBodyType:    defaults.RequestBodyType,
		RequestType:        defaults.RequestType,
		Output:             defaults.Output,
		From:               defaults.From,
		URL:                defaults.URL,
		RequestMethod:      defaults.RequestMethod,
		Headers:            defaults.Headers,
		RequestBody:        defaults.RequestBody,
	}
	if s.URL != "" {
		p.URL = s.URL
	}
	if s.Method != "" {
		p.RequestMethod = s.Method
	}
	if s.Headers != nil {
		p.Headers = s.Headers
	}
	if s.Body != "" {
		p.RequestBody = s.Body
	}
	return p
}

// ------------------------------------------------------------ Parser ---

func readFile(path string) ([]byte, error) { return os.ReadFile(path) }

var requestDelimiter = regexp.MustCompile(`(?m)^#{3,}.*$`)

const (
	stateRequestLine = iota
	stateHeaders
	stateBody
)

func ParseFile(filePath string) ([]Spec, error) {
	content, err := readFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}
	return ParseContent(content)
}

func ParseContent(content []byte) ([]Spec, error) {
	indices := requestDelimiter.FindAllIndex(content, -1)
	var requests []Spec
	var firstErr error
	start := 0
	blockIdx := 0
	processBlock := func(raw []byte) {
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) == 0 {
			return
		}
		blockIdx++
		req, err := parseBlock(string(trimmed), blockIdx)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			logging.Error(0, "parsing error in block #%d (offset %d): %v", blockIdx, start, err)
			return
		}
		requests = append(requests, req)
	}
	for _, idx := range indices {
		processBlock(content[start:idx[0]])
		start = idx[1]
	}
	if start < len(content) {
		processBlock(content[start:])
	}
	return requests, firstErr
}

func parseBlock(block string, blockNum int) (Spec, error) {
	spec := Spec{Headers: make(map[string][]string)}
	scanner := bufio.NewScanner(strings.NewReader(block))
	state := stateRequestLine
	var bodyBuilder strings.Builder
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		trimmedLine := strings.TrimSpace(line)
		if state != stateBody && (strings.HasPrefix(trimmedLine, "#") || strings.HasPrefix(trimmedLine, "//")) {
			continue
		}
		switch state {
		case stateRequestLine:
			if trimmedLine == "" {
				continue
			}
			if err := parseRequestLine(line, trimmedLine, &spec); err != nil {
				return spec, fmt.Errorf("block #%d line %d: %w", blockNum, lineNo, err)
			}
			state = stateHeaders
		case stateHeaders:
			if trimmedLine == "" {
				state = stateBody
				continue
			}
			if parseHeaders(line, &spec, &bodyBuilder) {
				state = stateBody
			}
		case stateBody:
			bodyBuilder.WriteString(line)
			bodyBuilder.WriteString("\n")
		}
	}
	if err := scanner.Err(); err != nil {
		return spec, fmt.Errorf("block #%d: scanner error: %w", blockNum, err)
	}
	body := bodyBuilder.String()
	if len(body) > 0 && strings.HasSuffix(body, "\n") {
		body = body[:len(body)-1]
	}
	spec.Body = body
	if spec.URL == "" {
		return spec, fmt.Errorf("block #%d: URL not found in request block", blockNum)
	}
	return spec, nil
}

func parseRequestLine(line, trimmedLine string, spec *Spec) error {
	parts := strings.Fields(trimmedLine)
	if len(parts) == 0 {
		return nil
	}
	firstToken := parts[0]
	if isHTTPMethod(firstToken) {
		spec.Method = firstToken
		spec.URL = strings.TrimSpace(trimmedLine[len(firstToken):])
	} else {
		spec.Method = "GET"
		spec.URL = trimmedLine
	}
	if idx := strings.LastIndex(spec.URL, " HTTP/"); idx != -1 {
		spec.URL = strings.TrimSpace(spec.URL[:idx])
	}
	_ = line
	return nil
}

func parseHeaders(line string, spec *Spec, bodyBuilder *strings.Builder) bool {
	colonIndex := strings.Index(line, ":")
	if colonIndex <= 0 {
		bodyBuilder.WriteString(line)
		bodyBuilder.WriteString("\n")
		return true
	}
	key := strings.TrimSpace(line[:colonIndex])
	value := strings.TrimSpace(line[colonIndex+1:])
	if spec.Headers == nil {
		spec.Headers = make(map[string][]string)
	}
	spec.Headers[key] = append(spec.Headers[key], value)
	return false
}

func isHTTPMethod(m string) bool {
	switch strings.ToUpper(m) {
	case "GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS", "CONNECT", "TRACE":
		return true
	}
	return false
}
