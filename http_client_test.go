// client_test.go
package main

import (
	"bytes"
	"crypto/tls"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/net/http2"
)

// TestGetRequestBody for various input scenarios
func TestGetRequestBody(t *testing.T) {
	p := HttpbenchParameters{}
	b, r := p.GetRequestBody()
	if b != nil || r != nil {
		t.Fatalf("expected nil,nil; got %v,%v", b, r)
	}

	// ordinary string
	p.RequestBody = "hello"
	p.RequestBodyType = ""
	b, r = p.GetRequestBody()
	if !bytes.Equal(b, []byte("hello")) {
		t.Errorf("expected body bytes %q; got %q", "hello", b)
	}
	buf := new(bytes.Buffer)
	io.Copy(buf, r)
	if buf.String() != "hello" {
		t.Errorf("reader content mismatch; got %q", buf.String())
	}

	// hex format
	p.RequestBody = hex.EncodeToString([]byte("world"))
	p.RequestBodyType = bodyHex
	b, r = p.GetRequestBody()
	if !bytes.Equal(b, []byte("world")) {
		t.Errorf("expected decoded bytes %q; got %q", "world", b)
	}
	buf.Reset()
	io.Copy(buf, r)
	if buf.String() != "world" {
		t.Errorf("hex reader content mismatch; got %q", buf.String())
	}
}

// TestClientPool Get/Put behavior
func TestClientPool(t *testing.T) {
	p := NewClientPool(1)
	c1 := p.Get()
	if c1 == nil {
		t.Fatal("expected first Get non-nil")
	}
	c2 := p.Get()
	if c2 != nil {
		t.Fatal("expected second Get nil when pool is exhausted")
	}
	p.Put(c1)
	c3 := p.Get()
	if c3 != c1 {
		t.Fatalf("expected reused client; got %p vs %p", c3, c1)
	}
}

// Test HTTP/1.1 client Do method
func TestClientDoHTTP1(t *testing.T) {
	// Setup a simple echo server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Write(body)
	}))
	defer srv.Close()

	time.Sleep(100 * time.Millisecond)
	params := HttpbenchParameters{
		Url:                srv.URL,
		RequestMethod:      http.MethodPost,
		RequestBody:        "ping",
		RequestBodyType:    "",
		RequestType:        protocolHTTP1,
		Timeout:            500 * time.Millisecond,
		DisableCompression: false,
		DisableKeepAlives:  false,
		Headers:            map[string][]string{"X-Test": {"yes"}},
	}

	c := &Client{}
	if err := c.Init(ClientOpts{Protocol: protocolHTTP1, Params: params}); err != nil {
		t.Fatalf("Init error: %v", err)
	}

	code, length, err := c.Do([]byte(params.Url), []byte(params.RequestBody), 0)
	if err != nil {
		t.Fatalf("Do error: %v", err)
	}
	if code != http.StatusOK {
		t.Errorf("expected status 200; got %d", code)
	}
	if int(length) != len("ping") {
		t.Errorf("expected length %d; got %d", len("ping"), length)
	}
}

// Test HTTP/2 client Do method
func TestClientDoHTTP2(t *testing.T) {
	// Use TLS with HTTP/2
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Write(body)
	}))
	// 加载自定义服务器证书并设置 ALPN
	cert, err := tls.LoadX509KeyPair("./test/server.crt", "./test/server.key")
	if err != nil {
		t.Fatalf("load server cert error: %v", err)
	}
	srv.TLS = &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"h2", "http/1.1"},
	}
	http2.ConfigureServer(srv.Config, &http2.Server{})
	srv.StartTLS()
	defer srv.Close()

	time.Sleep(100 * time.Millisecond)
	params := HttpbenchParameters{
		Url:             srv.URL,
		RequestMethod:   http.MethodPost,
		RequestBody:     "hello2",
		RequestBodyType: "",
		Timeout:         500 * time.Millisecond,
		RequestType:     protocolHTTP2,
	}

	c := &Client{}
	if err := c.Init(ClientOpts{Protocol: protocolHTTP2, Params: params}); err != nil {
		t.Fatalf("Init HTTP2 error: %v", err)
	}
	code, length, err := c.Do([]byte(params.Url), []byte(params.RequestBody), 0)
	if err != nil {
		t.Fatalf("Do HTTP2 error: %v", err)
	}
	if code != http.StatusOK {
		t.Errorf("expected status 200; got %d", code)
	}
	if int(length) != len("hello2") {
		t.Errorf("expected length %d; got %d", len("hello2"), length)
	}
}

// Test WebSocket client Do method
func TestClientDoWS(t *testing.T) {
	// Start a WebSocket echo server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("upgrade error: %v", err)
		}
		defer conn.Close()
		for {
			mt, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			conn.WriteMessage(mt, msg)
		}
	}))
	defer srv.Close()

	time.Sleep(100 * time.Millisecond)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	params := HttpbenchParameters{
		Url:             wsURL,
		RequestMethod:   http.MethodGet,
		RequestBody:     "pingws",
		RequestBodyType: "",
		Timeout:         500,
		RequestType:     protocolWS,
	}

	c := &Client{}
	if err := c.Init(ClientOpts{Protocol: protocolWS, Params: params}); err != nil {
		t.Fatalf("Init WS error: %v", err)
	}
	code, length, err := c.Do([]byte(params.Url), []byte(params.RequestBody), 0)
	if err != nil {
		t.Fatalf("Do WS error: %v", err)
	}
	if code != http.StatusOK {
		t.Errorf("expected status 200; got %d", code)
	}
	if int(length) != len("pingws") {
		t.Errorf("expected length %d; got %d", len("pingws"), length)
	}
}

// Test HTTP/3 client Do method (skipped)
func TestClientDoHTTP3(t *testing.T) {
	t.Skip("HTTP3 test requires QUIC environment")
}

// Test Do method timeout behavior
func TestClientDoTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	params := HttpbenchParameters{
		Url:           srv.URL,
		RequestMethod: http.MethodGet,
		Timeout:       10, // very short timeout
	}

	c := &Client{}
	if err := c.Init(ClientOpts{Protocol: protocolHTTP1, Params: params}); err != nil {
		t.Fatalf("Init error: %v", err)
	}

	_, _, err := c.Do([]byte(params.Url), []byte(params.RequestBody), 0)
	if err == nil {
		t.Fatal("expected timeout error; got nil")
	}
}

// BenchmarkClientPool_GetPut benchmarks the performance of getting and putting clients
func BenchmarkClientPool_GetPut(b *testing.B) {
	pool := NewClientPool(100)
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		c := pool.Get()
		if c != nil {
			pool.Put(c)
		}
	}
}

// BenchmarkClient_Do benchmarks the performance of HTTP client requests
func BenchmarkClient_Do(b *testing.B) {
	// Setup a simple echo server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	params := HttpbenchParameters{
		Url:                srv.URL,
		RequestMethod:      http.MethodGet,
		RequestType:        protocolHTTP1,
		Timeout:            500,
		DisableCompression: true,
		DisableKeepAlives:  false,
	}

	c := &Client{}
	if err := c.Init(ClientOpts{Protocol: protocolHTTP1, Params: params}); err != nil {
		b.Fatalf("Init error: %v", err)
	}

	urlBytes := []byte(params.Url)
	reqBody := []byte("benchmark")

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		code, _, err := c.Do(urlBytes, reqBody, 0)
		if err != nil {
			b.Fatalf("Do error: %v", err)
		}
		if code != http.StatusOK {
			b.Fatalf("expected status 200; got %d", code)
		}
	}
}
