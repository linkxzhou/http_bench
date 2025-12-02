package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	gourl "net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/quic-go/quic-go/http3"
	"golang.org/x/net/http2"
)

// ClientPool manages a pool of HTTP clients for connection reuse
// It provides thread-safe client pooling with automatic lifecycle management
type ClientPool struct {
	clients chan *Client
	maxSize int32
	active  int32      // Active connections count (atomic)
	closed  int32      // Pool closed flag (atomic, 0=open, 1=closed)
	mu      sync.Mutex // Protects pool operations during shutdown
}

// NewClientPool creates a new client pool with specified maximum size
func NewClientPool(maxSize int) *ClientPool {
	if maxSize <= 0 {
		maxSize = defaultConcurrency
	}
	return &ClientPool{
		clients: make(chan *Client, maxSize),
		maxSize: int32(maxSize),
	}
}

// Get retrieves a client from the pool or creates a new one if available
// Returns nil if pool is closed or at capacity
func (p *ClientPool) Get() *Client {
	if atomic.LoadInt32(&p.closed) == 1 {
		logDebug("client pool is closed, cannot get client")
		return nil
	}

	select {
	case client := <-p.clients:
		if client != nil {
			atomic.AddInt32(&p.active, 1)
			return client
		}
	default:
		// Pool is empty, create new client if under limit
		if atomic.LoadInt32(&p.active) < p.maxSize {
			atomic.AddInt32(&p.active, 1)
			return &Client{}
		}
		logDebug("client pool at capacity: %d", p.maxSize)
	}
	return nil
}

// Put returns a client to the pool or closes it if pool is full
func (p *ClientPool) Put(client *Client) {
	if client == nil {
		atomic.AddInt32(&p.active, -1)
		return
	}

	if atomic.LoadInt32(&p.closed) == 1 {
		p.closeClient(client)
		atomic.AddInt32(&p.active, -1)
		return
	}

	select {
	case p.clients <- client:
		// Successfully returned to pool
		logDebug("client returned to pool")
	default:
		// Pool is full, close client
		logDebug("pool full, closing client")
		p.closeClient(client)
	}
	atomic.AddInt32(&p.active, -1)
}

// closeClient safely closes a single client
func (p *ClientPool) closeClient(client *Client) {
	if client != nil {
		if err := client.Close(); err != nil {
			logDebug("error closing client: %v", err)
		}
	}
}

// Shutdown gracefully closes the connection pool and all clients
func (p *ClientPool) Shutdown() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if atomic.LoadInt32(&p.closed) == 1 {
		return // Already closed
	}

	atomic.StoreInt32(&p.closed, 1)
	close(p.clients)

	// Close all remaining connections
	for client := range p.clients {
		p.closeClient(client)
	}

	logDebug("client pool shutdown complete")
}

// Client represents a reusable HTTP/WebSocket client
type Client struct {
	httpClient  *http.Client
	wsClient    *websocket.Conn
	opts        ClientOpts
	initialized bool       // Whether client has been initialized and can be reused
	mu          sync.Mutex // Protects client state during concurrent operations
}

// ClientOpts contains configuration options for client initialization
type ClientOpts struct {
	Protocol string              // Protocol type (http1, http2, http3, ws, wss)
	Params   HttpbenchParameters // Request parameters
}

var (
	http3Pool     *x509.CertPool
	http3PoolOnce sync.Once
)

// initHTTP3Pool initializes the HTTP/3 certificate pool (thread-safe)
func initHTTP3Pool() {
	http3PoolOnce.Do(func() {
		var err error
		if http3Pool, err = x509.SystemCertPool(); err != nil {
			panic(protocolHTTP3 + " initialization error: " + err.Error())
		}
	})
}

// Init initializes the client with specified options
// Returns error if initialization fails
func (c *Client) Init(opts ClientOpts) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	logDebug("initializing client with protocol: %s", opts.Protocol)
	c.opts = opts

	// If client is already initialized and protocol is the same, reuse directly
	if c.initialized && c.httpClient != nil && c.opts.Protocol == opts.Protocol {
		logDebug("reusing existing client")
		return nil
	}

	var err error

	switch c.opts.Protocol {
	case protocolHTTP3:
		c.httpClient, err = c.initHTTP3Client()
	case protocolHTTP2:
		c.httpClient = c.initHTTP2Client()
	case protocolHTTP1:
		c.httpClient, err = c.initHTTP1Client()
	case protocolWS, protocolWSS:
		err = c.initWebSocketClient()
	default:
		err = fmt.Errorf("unsupported protocol: %s", opts.Protocol)
		logError("unsupported protocol: %s", opts.Protocol)
	}

	if err != nil {
		return err
	}

	c.initialized = true
	logDebug("client initialized successfully")
	return nil
}

// initHTTP3Client initializes HTTP/3 client
func (c *Client) initHTTP3Client() (*http.Client, error) {
	initHTTP3Pool()
	return &http.Client{
		Timeout: time.Duration(c.opts.Params.Timeout) * time.Millisecond,
		Transport: &http3.RoundTripper{
			TLSClientConfig: &tls.Config{
				RootCAs:            http3Pool,
				InsecureSkipVerify: true,
			},
		},
	}, nil
}

// initHTTP2Client initializes HTTP/2 client
func (c *Client) initHTTP2Client() *http.Client {
	return &http.Client{
		Timeout: time.Duration(c.opts.Params.Timeout) * time.Millisecond,
		Transport: &http2.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
			DisableCompression:         c.opts.Params.DisableCompression,
			AllowHTTP:                  true,
			MaxReadFrameSize:           1 << 20, // 1MB
			StrictMaxConcurrentStreams: true,
			MaxHeaderListSize:          1 << 20, // 1MB
			ReadIdleTimeout:            30 * time.Second,
			PingTimeout:                15 * time.Second,
		},
	}
}

// initHTTP1Client initializes HTTP/1.1 client
func (c *Client) initHTTP1Client() (*http.Client, error) {
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
		DisableCompression:  c.opts.Params.DisableCompression,
		DisableKeepAlives:   c.opts.Params.DisableKeepAlives,
		TLSHandshakeTimeout: time.Duration(c.opts.Params.Timeout) * time.Millisecond,
		TLSNextProto:        make(map[string]func(string, *tls.Conn) http.RoundTripper),
		DialContext: (&net.Dialer{
			Timeout:   time.Duration(c.opts.Params.Timeout) * time.Millisecond,
			KeepAlive: 60 * time.Second,
			DualStack: true,
		}).DialContext,
		// Connection pool optimization
		MaxIdleConns:          200,
		MaxIdleConnsPerHost:   100,
		MaxConnsPerHost:       200,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: time.Duration(c.opts.Params.Timeout) * time.Millisecond,
		ExpectContinueTimeout: 1 * time.Second,
		ForceAttemptHTTP2:     false,
		WriteBufferSize:       32 * 1024, // 32KB
		ReadBufferSize:        32 * 1024, // 32KB
	}

	if c.opts.Params.ProxyUrl != "" {
		proxyUrl, err := gourl.Parse(c.opts.Params.ProxyUrl)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy URL: %v", err)
		}
		tr.Proxy = http.ProxyURL(proxyUrl)
	}

	return &http.Client{
		Timeout:   time.Duration(c.opts.Params.Timeout) * time.Millisecond,
		Transport: tr,
	}, nil
}

// initWebSocketClient initializes WebSocket client
func (c *Client) initWebSocketClient() error {
	dialer := websocket.Dialer{
		HandshakeTimeout:  time.Duration(c.opts.Params.Timeout) * time.Millisecond,
		ReadBufferSize:    32 * 1024, // 32KB
		WriteBufferSize:   32 * 1024, // 32KB
		EnableCompression: !c.opts.Params.DisableCompression,
	}

	var err error
	c.wsClient, _, err = dialer.Dial(c.opts.Params.Url, c.opts.Params.Headers)
	if err != nil {
		logError("websocket dial error: %v", err)
		return fmt.Errorf("websocket dial error: %v", err)
	}

	if c.wsClient == nil {
		return fmt.Errorf("websocket connection is nil")
	}

	return nil
}

// Object pools to reduce memory allocation and GC pressure
var (
	// bufferPool provides reusable byte buffers for reading response bodies
	bufferPool = sync.Pool{
		New: func() interface{} {
			return make([]byte, 64*1024) // 64KB buffer for better performance
		},
	}
	// readerPool provides reusable bytes.Reader instances
	readerPool = sync.Pool{
		New: func() interface{} {
			return &bytes.Reader{}
		},
	}
)

// Do executes an HTTP/WebSocket request and returns status code, content length, and error
// Parameters:
//   - url: target URL as byte slice
//   - reqBody: request body as byte slice
//   - timeoutMs: request timeout in milliseconds (0 uses default)
//
// Returns: (statusCode, contentLength, error)
func (c *Client) Do(url, reqBody []byte, timeoutMs int) (int, int64, error) {
	if !c.initialized {
		return 0, 0, fmt.Errorf("client not initialized")
	}

	curTimeout := time.Duration(c.opts.Params.Timeout) * time.Millisecond
	if timeoutMs > 0 {
		curTimeout = time.Duration(timeoutMs) * time.Millisecond
	}

	ctx, cancel := context.WithTimeout(context.Background(), curTimeout)
	defer cancel()

	switch c.opts.Protocol {
	case protocolHTTP1, protocolHTTP2, protocolHTTP3:
		return c.doHTTPRequest(ctx, url, reqBody)

	case protocolWS, protocolWSS:
		return c.doWebSocketRequest(reqBody)
	}

	return 0, 0, fmt.Errorf("unsupported protocol type: %s", c.opts.Protocol)
}

// doHTTPRequest executes an HTTP request (HTTP/1.1, HTTP/2, or HTTP/3)
func (c *Client) doHTTPRequest(ctx context.Context, url, reqBody []byte) (int, int64, error) {
	// Reuse Reader object from pool
	reader := readerPool.Get().(*bytes.Reader)
	reader.Reset(reqBody)
	defer readerPool.Put(reader)

	req, err := http.NewRequestWithContext(ctx,
		c.opts.Params.RequestMethod, string(url), reader)
	if err != nil {
		return 0, 0, fmt.Errorf("create request error: %v", err)
	}

	// Set request headers
	for k, v := range c.opts.Params.Headers {
		req.Header[k] = v
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, 0, fmt.Errorf("http request error: %v", err)
	}
	defer resp.Body.Close()

	// Handle content length
	contentLength := resp.ContentLength
	if contentLength < 0 {
		// Content-Length unknown, read and calculate size
		buf := bufferPool.Get().([]byte)
		defer bufferPool.Put(buf)

		var totalSize int64
		for {
			n, err := resp.Body.Read(buf)
			totalSize += int64(n)
			if err == io.EOF {
				break
			}
			if err != nil {
				return resp.StatusCode, totalSize, fmt.Errorf("read response error: %v", err)
			}
		}
		contentLength = totalSize
	} else {
		// Discard response body to release connection
		if _, err := io.Copy(io.Discard, resp.Body); err != nil {
			logDebug("error discarding response body: %v", err)
		}
	}

	return resp.StatusCode, contentLength, nil
}

// doWebSocketRequest executes a WebSocket request
func (c *Client) doWebSocketRequest(reqBody []byte) (int, int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.wsClient == nil {
		return 0, 0, fmt.Errorf("websocket client not initialized")
	}

	err := c.wsClient.WriteMessage(websocket.TextMessage, reqBody)
	if err != nil {
		return 0, 0, fmt.Errorf("websocket write error: %v", err)
	}

	_, msg, err := c.wsClient.ReadMessage()
	if err != nil {
		return 0, 0, fmt.Errorf("websocket read error: %v", err)
	}

	return http.StatusOK, int64(len(msg)), nil
}

// Close closes the client and releases resources
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.initialized = false

	switch c.opts.Protocol {
	case protocolHTTP1, protocolHTTP2, protocolHTTP3:
		if c.httpClient != nil {
			c.httpClient.CloseIdleConnections()
			logDebug("http client connections closed")
		}
		return nil
	case protocolWS, protocolWSS:
		if c.wsClient != nil {
			err := c.wsClient.Close()
			if err != nil {
				logDebug("websocket close error: %v", err)
				return fmt.Errorf("websocket close error: %v", err)
			}
			logDebug("websocket client closed")
		}
		return nil
	}

	return fmt.Errorf("unsupported protocol type: %s", c.opts.Protocol)
}

// HttpbenchParameters stress params for worker
type HttpbenchParameters struct {
	SequenceId         int64               `json:"sequence_id"`         // Sequence
	Cmd                int                 `json:"cmd"`                 // Commands
	RequestMethod      string              `json:"request_method"`      // Request Method.
	RequestBody        string              `json:"request_body"`        // Request Body.
	RequestBodyType    string              `json:"request_bodytype"`    // Request BodyType, default string.
	RequestScriptBody  string              `json:"request_script_body"` // Request Script Body.
	RequestType        string              `json:"request_type"`        // Request Type
	ProxyUrl           string              `json:"proxy_url"`           // proxy url
	N                  int                 `json:"n"`                   // N is the total number of requests to make.
	C                  int                 `json:"c"`                   // C is the concurrency level, the number of concurrent workers to run.
	Duration           int64               `json:"duration"`            // D is the duration for stress test
	Timeout            int                 `json:"timeout"`             // Timeout in ms.
	Qps                int                 `json:"qps"`                 // Qps is the rate limit.
	DisableCompression bool                `json:"disable_compression"` // DisableCompression is an option to disable compression in response
	DisableKeepAlives  bool                `json:"disable_keepalives"`  // DisableKeepAlives is an option to prevents re-use of TCP connections between different HTTP requests
	Headers            map[string][]string `json:"headers"`             // Custom HTTP header.
	Url                string              `json:"url"`                 // Request url.
	Output             string              `json:"output"`              // Output represents the output type. If "csv" is provided, the output will be dumped as a csv stream.
	From               string              `json:"from"`                // request from
}

func (p *HttpbenchParameters) String() string {
	body, err := json.MarshalIndent(p, "", "\t")
	if err != nil {
		logError("json marshal err: %v", err)
		return err.Error()
	}
	return string(body)
}

// GetRequestBody returns the request body as bytes and a reader
// Supports both string and hex-encoded body formats
// Note: This function creates a new Reader each time and is primarily used for testing
func (p *HttpbenchParameters) GetRequestBody() ([]byte, io.Reader) {
	if p.RequestBody == "" {
		return nil, nil
	}

	if p.RequestBodyType == bodyHex {
		decoded, err := hex.DecodeString(p.RequestBody)
		if err != nil {
			logError("hex decode error: %v", err)
			return nil, nil
		}
		return decoded, bytes.NewReader(decoded)
	}

	body := []byte(p.RequestBody)
	return body, bytes.NewReader(body)
}
