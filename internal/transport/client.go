// Package transport provides the HTTP/WebSocket client abstractions, connection
// pooling, and the HttpbenchParameters DTO used across the benchmark tool.
//
// The package is self-contained: it owns protocol constants, body-format
// constants, the default concurrency value, and the command enum used by the
// distributed protocol. Root-package code re-exports these via type/const
// aliases during the incremental directory migration (see plan.md §3).
package transport

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/linkxzhou/http_bench/internal/logging"
)

// Command enum for the distributed worker protocol.
const (
	CmdStart int = iota
	CmdStop
	CmdMetrics // dashboard polling; local to CLI but kept here for enum cohesion
)

// Protocol identifiers for the -http flag.
const (
	ProtocolHTTP1 = "http1"
	ProtocolHTTP2 = "http2"
	ProtocolHTTP3 = "http3"
	ProtocolWS    = "ws"
	ProtocolWSS   = "wss"
)

// Request body encoding formats (-body-type).
const (
	BodyString = "string"
	BodyHex    = "hex"
)

// DefaultConcurrency is the default value for the -c (concurrency) flag.
const DefaultConcurrency = 50

// HttpbenchParameters is the DTO for benchmark run parameters. It is JSON-
// marshalled to communicate with distributed workers, so field tags are part
// of the public API and must not change.
type HttpbenchParameters struct {
	SequenceId         int64               `json:"sequence_id"`
	Cmd                int                 `json:"cmd"`
	RequestMethod      string              `json:"request_method"`
	RequestBody        string              `json:"request_body"`
	RequestBodyType    string              `json:"request_bodytype"`
	RequestType        string              `json:"request_type"`
	ProxyURL           string              `json:"proxy_url"`
	N                  int                 `json:"n"`
	C                  int                 `json:"c"`
	Duration           time.Duration       `json:"duration"`
	Timeout            time.Duration       `json:"timeout"`
	QPS                int                 `json:"qps"`
	DisableCompression bool                `json:"disable_compression"`
	DisableKeepAlives  bool                `json:"disable_keepalives"`
	Insecure           bool                `json:"insecure"`
	Headers            map[string][]string `json:"headers"`
	URL                string              `json:"url"`
	Output             string              `json:"output"`
	From               string              `json:"from"`
}

// String returns an indented JSON representation for logging.
func (p *HttpbenchParameters) String() string {
	body, err := json.MarshalIndent(p, "", "\t")
	if err != nil {
		logging.Error(p.SequenceId, "json marshal err: %v", err)
		return err.Error()
	}
	return string(body)
}

// GetRequestBody returns the request body as bytes and a reader.
// Supports both string and hex-encoded body formats.
func (p *HttpbenchParameters) GetRequestBody() ([]byte, io.Reader) {
	if p.RequestBody == "" {
		return nil, nil
	}

	if p.RequestBodyType == BodyHex {
		decoded, err := hex.DecodeString(p.RequestBody)
		if err != nil {
			logging.Error(p.SequenceId, "hex decode error: %v", err)
			return nil, nil
		}
		return decoded, bytes.NewReader(decoded)
	}

	body := []byte(p.RequestBody)
	return body, bytes.NewReader(body)
}

// Merge overlays non-zero/non-empty fields from params onto p. Used when a
// distributed worker contributes partial overrides.
func (p *HttpbenchParameters) Merge(params *HttpbenchParameters) {
	if params.RequestMethod != "" {
		p.RequestMethod = params.RequestMethod
	}
	if params.RequestBody != "" {
		p.RequestBody = params.RequestBody
	}
	if params.RequestBodyType != "" {
		p.RequestBodyType = params.RequestBodyType
	}
	if params.RequestType != "" {
		p.RequestType = params.RequestType
	}
	if params.ProxyURL != "" {
		p.ProxyURL = params.ProxyURL
	}
	if params.N > 0 {
		p.N = params.N
	}
	if params.C > 0 {
		p.C = params.C
	}
	if params.Duration > 0 {
		p.Duration = params.Duration
	}
	if params.Timeout > 0 {
		p.Timeout = params.Timeout
	}
	if params.QPS > 0 {
		p.QPS = params.QPS
	}
	if params.DisableCompression {
		p.DisableCompression = params.DisableCompression
	}
	if params.DisableKeepAlives {
		p.DisableKeepAlives = params.DisableKeepAlives
	}
	if params.Headers != nil {
		p.Headers = params.Headers
	}
	if params.URL != "" {
		p.URL = params.URL
	}
	if params.Output != "" {
		p.Output = params.Output
	}
	if params.From != "" {
		p.From = params.From
	}
}

// Client represents a reusable HTTP/WebSocket client.
type Client struct {
	httpClient  *http.Client
	wsClient    *websocket.Conn
	opts        ClientOpts
	initialized bool
	mu          sync.Mutex
}

// ClientOpts contains configuration options for client initialization.
type ClientOpts struct {
	Protocol string
	Params   HttpbenchParameters
	// Insecure controls TLS certificate verification. When false, the client
	// performs full verification against the system/root CA pool. When true,
	// InsecureSkipVerify is set on the TLS config (plan.md §D-6).
	//
	// Defaults to true for backwards compatibility with pre-refactor behavior;
	// callers can opt into strict verification with --insecure=false.
	Insecure bool
}

// clientOptsEqual reports whether two ClientOpts have identical connection-
// affecting configuration.
func clientOptsEqual(a, b ClientOpts) bool {
	if a.Protocol != b.Protocol {
		return false
	}
	if a.Insecure != b.Insecure {
		return false
	}
	pa, pb := a.Params, b.Params
	if pa.URL != pb.URL ||
		pa.ProxyURL != pb.ProxyURL ||
		pa.Timeout != pb.Timeout ||
		pa.DisableCompression != pb.DisableCompression ||
		pa.DisableKeepAlives != pb.DisableKeepAlives {
		return false
	}
	return headersEqual(pa.Headers, pb.Headers)
}

func headersEqual(a, b map[string][]string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	if len(a) != len(b) {
		return false
	}
	for k, va := range a {
		vb, ok := b[k]
		if !ok || len(va) != len(vb) {
			return false
		}
		for i := range va {
			if va[i] != vb[i] {
				return false
			}
		}
	}
	return true
}

// Init initializes the client with specified options.
func (c *Client) Init(opts ClientOpts) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	logging.Debug(0, "initializing client with protocol: %s", opts.Protocol)

	if c.initialized && c.httpClient != nil && clientOptsEqual(c.opts, opts) {
		logging.Debug(0, "reusing existing client (identical config)")
		return nil
	}

	c.opts = opts

	var err error

	switch c.opts.Protocol {
	case ProtocolHTTP3:
		c.httpClient, err = c.initHTTP3Client()
	case ProtocolHTTP2:
		c.httpClient = c.initHTTP2Client()
	case ProtocolHTTP1:
		c.httpClient, err = c.initHTTP1Client()
	case ProtocolWS, ProtocolWSS:
		err = c.initWebSocketClient()
	default:
		err = fmt.Errorf("unsupported protocol: %s", opts.Protocol)
		logging.Error(0, "unsupported protocol: %s", opts.Protocol)
	}

	if err != nil {
		logging.Error(0, "initializing client: %v", err)
		return err
	}

	c.initialized = true
	logging.Debug(0, "client initialized successfully")
	return nil
}

var (
	bufferPool = sync.Pool{
		New: func() interface{} {
			return make([]byte, 64*1024)
		},
	}
	readerPool = sync.Pool{
		New: func() interface{} {
			return &bytes.Reader{}
		},
	}
)

// Do executes an HTTP/WebSocket request and returns status code, content
// length, and error.
func (c *Client) Do(ctx context.Context, url, reqBody []byte, timeout time.Duration) (int, int64, error) {
	if !c.initialized {
		return 0, 0, fmt.Errorf("client not initialized")
	}

	curTimeout := resolveTimeout(c.opts.Params.Timeout, timeout)

	reqCtx, cancel := context.WithTimeout(ctx, curTimeout)
	defer cancel()

	logging.Trace(0, "executing request: %s %s, timeout: %v seconds",
		c.opts.Params.RequestMethod, string(url), curTimeout.Seconds())

	switch c.opts.Protocol {
	case ProtocolHTTP1, ProtocolHTTP2, ProtocolHTTP3:
		return c.doHTTPRequest(reqCtx, url, reqBody)

	case ProtocolWS, ProtocolWSS:
		return c.doWebSocketRequest(reqCtx, reqBody)
	}

	return 0, 0, fmt.Errorf("unsupported protocol type: %s", c.opts.Protocol)
}

func (c *Client) doHTTPRequest(ctx context.Context, url, reqBody []byte) (int, int64, error) {
	reader := readerPool.Get().(*bytes.Reader)
	reader.Reset(reqBody)
	defer readerPool.Put(reader)

	req, err := http.NewRequestWithContext(ctx,
		c.opts.Params.RequestMethod, string(url), reader)
	if err != nil {
		return 0, 0, fmt.Errorf("create request error: %v", err)
	}

	for k, v := range c.opts.Params.Headers {
		req.Header[k] = v
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, 0, fmt.Errorf("http request error: %v", err)
	}
	defer resp.Body.Close()

	// Drain the response body so the underlying connection can be reused for
	// keep-alive (plan.md §D-7). When Content-Length is unknown (-1), the drain
	// also serves as the byte counter; when known, we still drain to enable
	// connection reuse and log any read failures rather than ignoring them.
	contentLength := resp.ContentLength
	buf := bufferPool.Get().([]byte)
	defer bufferPool.Put(buf)

	var totalSize int64
	for {
		n, readErr := resp.Body.Read(buf)
		totalSize += int64(n)
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			// Return the bytes observed so far plus the status code; the error
			// is surfaced so the caller can record it as a failed request.
			return resp.StatusCode, totalSize, fmt.Errorf("read response error: %v", readErr)
		}
	}
	if contentLength < 0 {
		contentLength = totalSize
	}

	return resp.StatusCode, contentLength, nil
}

func (c *Client) doWebSocketRequest(ctx context.Context, reqBody []byte) (int, int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.wsClient == nil {
		return 0, 0, fmt.Errorf("websocket client not initialized")
	}

	// applyDeadline sets read/write deadlines from the context; clearDeadline
	// resets them. Called per attempt to keep deadlines fresh after reconnect.
	applyDeadline := func() {
		if deadline, ok := ctx.Deadline(); ok {
			c.wsClient.SetReadDeadline(deadline)
			c.wsClient.SetWriteDeadline(deadline)
		}
	}
	clearDeadline := func() {
		c.wsClient.SetReadDeadline(time.Time{})
		c.wsClient.SetWriteDeadline(time.Time{})
	}

	// Attempt the request; on a transient I/O error, reconnect once and retry
	// (plan.md §D-4). Context-cancellation errors are not retried.
	for attempt := 0; attempt < 2; attempt++ {
		applyDeadline()

		writeErr := c.wsClient.WriteMessage(websocket.TextMessage, reqBody)
		if writeErr == nil {
			_, msg, readErr := c.wsClient.ReadMessage()
			if readErr == nil {
				clearDeadline()
				return http.StatusOK, int64(len(msg)), nil
			}
			// Read failed — retryable unless context is done.
			if ctx.Err() != nil {
				clearDeadline()
				return 0, 0, fmt.Errorf("websocket read error: %v", readErr)
			}
			if attempt == 0 {
				logging.Debug(0, "websocket read error, will reconnect: %v", readErr)
				if rcErr := c.reconnectWebSocket(); rcErr != nil {
					return 0, 0, fmt.Errorf("websocket reconnect failed: %v (original: %v)", rcErr, readErr)
				}
				continue
			}
			clearDeadline()
			return 0, 0, fmt.Errorf("websocket read error: %v", readErr)
		}

		// Write failed — retryable unless context is done.
		if ctx.Err() != nil {
			clearDeadline()
			return 0, 0, fmt.Errorf("websocket write error: %v", writeErr)
		}
		if attempt == 0 {
			logging.Debug(0, "websocket write error, will reconnect: %v", writeErr)
			if rcErr := c.reconnectWebSocket(); rcErr != nil {
				return 0, 0, fmt.Errorf("websocket reconnect failed: %v (original: %v)", rcErr, writeErr)
			}
			continue
		}
		clearDeadline()
		return 0, 0, fmt.Errorf("websocket write error: %v", writeErr)
	}

	return 0, 0, fmt.Errorf("websocket request failed after reconnect")
}

// Close closes the client and releases resources.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.initialized = false

	switch c.opts.Protocol {
	case ProtocolHTTP1, ProtocolHTTP2, ProtocolHTTP3:
		if c.httpClient != nil {
			c.httpClient.CloseIdleConnections()
			logging.Trace(0, "http client connections closed")
		}
		return nil
	case ProtocolWS, ProtocolWSS:
		if c.wsClient != nil {
			err := c.wsClient.Close()
			if err != nil {
				logging.Error(0, "websocket close error: %v", err)
				return fmt.Errorf("websocket close error: %v", err)
			}
			logging.Trace(0, "websocket client closed")
		}
		return nil
	}

	return fmt.Errorf("unsupported protocol type: %s", c.opts.Protocol)
}
