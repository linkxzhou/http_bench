package main

import (
	"testing"
)

func TestParseRestClientFile(t *testing.T) {
	// Create a temporary .http file
	content := `
# Global comment
GET https://httpbin.org/get
Authorization: Bearer token123

###

POST https://httpbin.org/post HTTP/1.1
Content-Type: application/x-www-form-urlencoded

name=foo
&password=bar

### Request with comments
# This is a comment
PUT https://httpbin.org/put
Content-Type: application/json

{
    "id": 1,
    "name": "test"
}

### DELETE Request
DELETE https://httpbin.org/delete
X-Custom-Header: value

### PATCH Request
PATCH https://httpbin.org/patch
Content-Type: application/json

{
    "patch": "work"
}

### HEAD Request
HEAD https://httpbin.org/get

### OPTIONS Request
OPTIONS https://httpbin.org/get

### TRACE Request
TRACE https://httpbin.org/trace

### CONNECT Request
CONNECT https://httpbin.org:443

### HTTP/2 Request
GET https://httpbin.org/get HTTP/2
X-Protocol: 2

### HTTP/3 Request
GET https://httpbin.org/get HTTP/3
X-Protocol: 3

### GraphQL Request
POST https://httpbin.org/post
Content-Type: application/json
X-Request-Type: GraphQL

{
  "query": "query { user(id: 1) { name } }"
}

### WebSocket Request
ws://echo.websocket.org
`
	// Parse the content directly
	requests, err := ParseRestClientContent([]byte(content))
	if err != nil {
		t.Fatalf("ParseRestClientContent failed: %v", err)
	}

	if len(requests) != 13 {
		t.Fatalf("Expected 13 requests, got %d", len(requests))
	}

	// Verify Request 1 (GET)
	req1 := requests[0]
	if req1.RequestMethod != "GET" {
		t.Errorf("Req1 method: expected GET, got %s", req1.RequestMethod)
	}
	if req1.Url != "https://httpbin.org/get" {
		t.Errorf("Req1 url: expected https://httpbin.org/get, got %s", req1.Url)
	}
	if req1.Headers["Authorization"][0] != "Bearer token123" {
		t.Errorf("Req1 header mismatch")
	}

	// Verify Request 2 (POST)
	req2 := requests[1]
	if req2.RequestMethod != "POST" {
		t.Errorf("Req2 method: expected POST, got %s", req2.RequestMethod)
	}
	if req2.Url != "https://httpbin.org/post" {
		t.Errorf("Req2 url: expected https://httpbin.org/post, got %s", req2.Url)
	}
	expectedBody2 := "name=foo\n&password=bar"
	if req2.RequestBody != expectedBody2 {
		t.Errorf("Req2 body: expected %q, got %q", expectedBody2, req2.RequestBody)
	}

	// Verify Request 3 (PUT)
	req3 := requests[2]
	if req3.RequestMethod != "PUT" {
		t.Errorf("Req3 method: expected PUT, got %s", req3.RequestMethod)
	}
	if req3.Url != "https://httpbin.org/put" {
		t.Errorf("Req3 url: expected https://httpbin.org/put, got %s", req3.Url)
	}
	expectedBody3 := "{\n    \"id\": 1,\n    \"name\": \"test\"\n}"
	if req3.RequestBody != expectedBody3 {
		t.Errorf("Req3 body: expected %q, got %q", expectedBody3, req3.RequestBody)
	}

	// Verify Request 4 (DELETE)
	req4 := requests[3]
	if req4.RequestMethod != "DELETE" {
		t.Errorf("Req4 method: expected DELETE, got %s", req4.RequestMethod)
	}
	if req4.Url != "https://httpbin.org/delete" {
		t.Errorf("Req4 url: expected https://httpbin.org/delete, got %s", req4.Url)
	}

	// Verify Request 5 (PATCH)
	req5 := requests[4]
	if req5.RequestMethod != "PATCH" {
		t.Errorf("Req5 method: expected PATCH, got %s", req5.RequestMethod)
	}
	expectedBody5 := "{\n    \"patch\": \"work\"\n}"
	if req5.RequestBody != expectedBody5 {
		t.Errorf("Req5 body: expected %q, got %q", expectedBody5, req5.RequestBody)
	}

	// Verify Request 6 (HEAD)
	req6 := requests[5]
	if req6.RequestMethod != "HEAD" {
		t.Errorf("Req6 method: expected HEAD, got %s", req6.RequestMethod)
	}

	// Verify Request 7 (OPTIONS)
	req7 := requests[6]
	if req7.RequestMethod != "OPTIONS" {
		t.Errorf("Req7 method: expected OPTIONS, got %s", req7.RequestMethod)
	}

	// Verify Request 8 (TRACE)
	req8 := requests[7]
	if req8.RequestMethod != "TRACE" {
		t.Errorf("Req8 method: expected TRACE, got %s", req8.RequestMethod)
	}

	// Verify Request 9 (CONNECT)
	req9 := requests[8]
	if req9.RequestMethod != "CONNECT" {
		t.Errorf("Req9 method: expected CONNECT, got %s", req9.RequestMethod)
	}

	// Verify Request 10 (HTTP/2)
	req10 := requests[9]
	if req10.RequestMethod != "GET" {
		t.Errorf("Req10 method: expected GET, got %s", req10.RequestMethod)
	}
	// Note: The parser currently ignores the HTTP version suffix, so we verify parsing succeeds
	if req10.Headers["X-Protocol"][0] != "2" {
		t.Errorf("Req10 header mismatch")
	}

	// Verify Request 11 (HTTP/3)
	req11 := requests[10]
	if req11.RequestMethod != "GET" {
		t.Errorf("Req11 method: expected GET, got %s", req11.RequestMethod)
	}
	if req11.Headers["X-Protocol"][0] != "3" {
		t.Errorf("Req11 header mismatch")
	}

	// Verify Request 12 (GraphQL)
	req12 := requests[11]
	if req12.RequestMethod != "POST" {
		t.Errorf("Req12 method: expected POST, got %s", req12.RequestMethod)
	}
	expectedBody12 := "{\n  \"query\": \"query { user(id: 1) { name } }\"\n}"
	if req12.RequestBody != expectedBody12 {
		t.Errorf("Req12 body: expected %q, got %q", expectedBody12, req12.RequestBody)
	}

	// Verify Request 13 (WebSocket)
	req13 := requests[12]
	if req13.RequestMethod != "GET" {
		t.Errorf("Req13 method: expected GET, got %s", req13.RequestMethod)
	}
	if req13.Url != "ws://echo.websocket.org" {
		t.Errorf("Req13 url: expected ws://echo.websocket.org, got %s", req13.Url)
	}
}
