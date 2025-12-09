package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// requestDelimiter is the regex to find request delimiters (###)
var requestDelimiter = regexp.MustCompile(`(?m)^#{3,}.*$`)

// ParseRestClientFile parses a .http file and returns a list of HttpbenchParameters
func ParseRestClientFile(filePath string) ([]HttpbenchParameters, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	return ParseRestClientContent(content)
}

// ParseRestClientContent parses .http file content and returns a list of HttpbenchParameters
func ParseRestClientContent(content []byte) ([]HttpbenchParameters, error) {
	// Find all delimiter indices
	// We handle splitting manually to preserve content correctly
	indices := requestDelimiter.FindAllIndex(content, -1)

	var requests []HttpbenchParameters

	start := 0
	for _, idx := range indices {
		end := idx[0]
		block := content[start:end]
		if len(bytes.TrimSpace(block)) > 0 {
			req, err := parseRequestBlock(string(bytes.TrimSpace(block)))
			if err != nil {
				logError(0, "parsing error in block starting at offset %d: %v", start, err)
			} else {
				requests = append(requests, req)
			}
		}
		start = idx[1]
	}

	// Process the last block
	if start < len(content) {
		block := content[start:]
		if len(bytes.TrimSpace(block)) > 0 {
			req, err := parseRequestBlock(string(bytes.TrimSpace(block)))
			if err != nil {
				logError(0, "parsing error in last block: %v", err)
			} else {
				requests = append(requests, req)
			}
		}
	}

	return requests, nil
}

func parseRequestBlock(block string) (HttpbenchParameters, error) {
	params := HttpbenchParameters{
		Headers: make(map[string][]string),
	}

	scanner := bufio.NewScanner(strings.NewReader(block))

	// State machine:
	// 0: Expecting Request Line
	// 1: Expecting Headers
	// 2: Reading Body
	state := 0
	var bodyBuilder strings.Builder

	for scanner.Scan() {
		line := scanner.Text()
		trimmedLine := strings.TrimSpace(line)

		// Check for comments (lines starting with # or //)
		// But only in state 0 and 1. In body, it might be content.
		if state != 2 {
			if strings.HasPrefix(trimmedLine, "#") || strings.HasPrefix(trimmedLine, "//") {
				continue
			}
		}

		switch state {
		case 0: // Request Line
			if trimmedLine == "" {
				continue // Skip empty lines before request
			}

			parts := strings.Fields(trimmedLine)
			if len(parts) == 0 {
				continue
			}

			// Try to identify Method
			firstToken := parts[0]
			if isHTTPMethod(firstToken) {
				params.RequestMethod = firstToken
				// Extract URL (rest of the line)
				params.Url = strings.TrimSpace(trimmedLine[len(firstToken):])
			} else {
				// No method specified (or WebSocket), default to GET
				params.RequestMethod = "GET"
				params.Url = trimmedLine
			}

			// Remove HTTP protocol version if present (e.g. HTTP/1.1)
			// This allows handling URLs with spaces (e.g. templates {{...}}) correctly
			if idx := strings.LastIndex(params.Url, " HTTP/"); idx != -1 {
				params.Url = strings.TrimSpace(params.Url[:idx])
			}

			state = 1

		case 1: // Headers
			if trimmedLine == "" {
				state = 2 // Empty line marks start of body
				continue
			}

			// Parse Header: Key: Value
			colonIndex := strings.Index(line, ":")
			if colonIndex > 0 {
				key := strings.TrimSpace(line[:colonIndex])
				value := strings.TrimSpace(line[colonIndex+1:])

				if params.Headers == nil {
					params.Headers = make(map[string][]string)
				}
				params.Headers[key] = append(params.Headers[key], value)
			} else {
				// If line is not empty and not a header, assume it's body (loose parsing)
				// or maybe user forgot empty line.
				// We treat it as body start.
				state = 2
				bodyBuilder.WriteString(line)
				bodyBuilder.WriteString("\n")
			}

		case 2: // Body
			bodyBuilder.WriteString(line)
			bodyBuilder.WriteString("\n")
		}
	}

	// Post-processing body
	body := bodyBuilder.String()
	// Remove the last newline added by the loop
	if len(body) > 0 && strings.HasSuffix(body, "\n") {
		body = body[:len(body)-1]
	}
	params.RequestBody = body

	if params.Url == "" {
		return params, fmt.Errorf("URL not found in request block")
	}

	return params, nil
}

func isWebSocketURL(url string) bool {
	lower := strings.ToLower(url)
	return strings.HasPrefix(lower, "ws://") || strings.HasPrefix(lower, "wss://")
}

func isHTTPMethod(m string) bool {
	// Common HTTP methods
	methods := map[string]bool{
		"GET":     true,
		"POST":    true,
		"PUT":     true,
		"DELETE":  true,
		"PATCH":   true,
		"HEAD":    true,
		"OPTIONS": true,
		"CONNECT": true,
		"TRACE":   true,
		// WebSocket isn't an HTTP method, but sometimes people might explicitly write it?
		// Standard is GET for upgrade.
	}
	return methods[strings.ToUpper(m)]
}
