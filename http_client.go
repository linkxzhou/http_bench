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

// Optimization: Use atomic operations and more efficient connection pool design
type ClientPool struct {
	clients chan *Client
	maxSize int32
	active  int32 // Use atomic operations
	closed  int32 // Use atomic operations to mark whether the pool is closed
}

func NewClientPool(maxSize int) *ClientPool {
	return &ClientPool{
		clients: make(chan *Client, maxSize),
		maxSize: int32(maxSize),
	}
}

// Optimization: Reduce lock usage, use atomic operations
func (p *ClientPool) Get() *Client {
	if atomic.LoadInt32(&p.closed) == 1 {
		return nil
	}

	select {
	case client := <-p.clients:
		if client != nil {
			atomic.AddInt32(&p.active, 1)
			return client
		}
	default:
		// Non-blocking get failed, check if new connection can be created
		if atomic.LoadInt32(&p.active) < p.maxSize {
			atomic.AddInt32(&p.active, 1)
			return &Client{}
		}
	}
	return nil
}

func (p *ClientPool) Put(client *Client) {
	if client == nil || atomic.LoadInt32(&p.closed) == 1 {
		atomic.AddInt32(&p.active, -1)
		return
	}

	select {
	case p.clients <- client:
		// Successfully returned to connection pool
	default:
		// Connection pool is full, close connection
		p.Close(client)
	}
	atomic.AddInt32(&p.active, -1)
}

func (p *ClientPool) Close(client *Client) {
	if client != nil {
		client.Close()
	}
}

// Optimization: Add connection pool shutdown method
func (p *ClientPool) Shutdown() {
	atomic.StoreInt32(&p.closed, 1)
	close(p.clients)

	// Clean up remaining connections
	for client := range p.clients {
		if client != nil {
			client.Close()
		}
	}
}

type (
	Client struct {
		httpClient *http.Client
		wsClient   *websocket.Conn
		opts       ClientOpts
		// Optimization: Add reuse flag
		reused bool
	}

	ClientOpts struct {
		typ    string
		params HttpbenchParameters
	}
)

var (
	http3Pool     *x509.CertPool
	http3PoolOnce sync.Once
)

// Optimization: Use sync.Once to ensure http3Pool is initialized only once
func initHTTP3Pool() {
	http3PoolOnce.Do(func() {
		var err error
		if http3Pool, err = x509.SystemCertPool(); err != nil {
			panic(protocolHTTP3 + " err: " + err.Error())
		}
	})
}

func (c *Client) Init(opts ClientOpts) (err error) {
	verbosePrint(logLevelDebug, "client Init opts: %v", opts)
	c.opts = opts

	// If client is already initialized and type is the same, reuse directly
	if c.reused && c.httpClient != nil && c.opts.typ == opts.typ {
		return nil
	}

	switch c.opts.typ {
	case protocolHTTP3:
		initHTTP3Pool()
		c.httpClient = &http.Client{
			Timeout: time.Duration(c.opts.params.Timeout) * time.Millisecond,
			Transport: &http3.RoundTripper{
				TLSClientConfig: &tls.Config{
					RootCAs:            http3Pool,
					InsecureSkipVerify: true,
				},
			},
		}
	case protocolHTTP2:
		c.httpClient = &http.Client{
			Timeout: time.Duration(c.opts.params.Timeout) * time.Millisecond,
			Transport: &http2.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: true,
				},
				DisableCompression:         c.opts.params.DisableCompression,
				AllowHTTP:                  true,
				MaxReadFrameSize:           1 << 20, // 1MB
				StrictMaxConcurrentStreams: true,
				// Optimization: Add connection pool configuration
				MaxHeaderListSize: 1 << 20, // 1MB
				ReadIdleTimeout:   30 * time.Second,
				PingTimeout:       15 * time.Second,
			},
		}
	case protocolHTTP1:
		// Optimization: HTTP/1.1 transport layer settings
		tr := &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
			DisableCompression:  c.opts.params.DisableCompression,
			DisableKeepAlives:   c.opts.params.DisableKeepAlives,
			TLSHandshakeTimeout: time.Duration(c.opts.params.Timeout) * time.Millisecond,
			TLSNextProto:        make(map[string]func(string, *tls.Conn) http.RoundTripper),
			DialContext: (&net.Dialer{
				Timeout:   time.Duration(c.opts.params.Timeout) * time.Millisecond,
				KeepAlive: 60 * time.Second,
				// Optimization: Add more network optimization parameters
				DualStack: true,
			}).DialContext,
			// Optimization: Adjust connection pool parameters to improve performance
			MaxIdleConns:          200, // Increase maximum idle connections
			MaxIdleConnsPerHost:   100, // Increase maximum idle connections per host
			MaxConnsPerHost:       200, // Increase maximum connections per host
			IdleConnTimeout:       90 * time.Second,
			ResponseHeaderTimeout: time.Duration(c.opts.params.Timeout) * time.Millisecond,
			ExpectContinueTimeout: 1 * time.Second,
			// Optimization: Enable HTTP/2 support but disable upgrade
			ForceAttemptHTTP2: false,
			// Optimization: Set write buffer size
			WriteBufferSize: 32 * 1024, // 32KB
			ReadBufferSize:  32 * 1024, // 32KB
		}
		if c.opts.params.ProxyUrl != "" {
			proxyUrl, _ := gourl.Parse(c.opts.params.ProxyUrl)
			tr.Proxy = http.ProxyURL(proxyUrl)
		}
		c.httpClient = &http.Client{
			Timeout:   time.Duration(c.opts.params.Timeout) * time.Millisecond,
			Transport: tr,
		}
	case protocolWS, protocolWSS:
		// Optimization: WebSocket connection configuration
		dialer := websocket.Dialer{
			HandshakeTimeout:  time.Duration(c.opts.params.Timeout) * time.Millisecond,
			ReadBufferSize:    32 * 1024, // 32KB
			WriteBufferSize:   32 * 1024, // 32KB
			EnableCompression: !c.opts.params.DisableCompression,
		}
		c.wsClient, _, err = dialer.Dial(c.opts.params.Url, c.opts.params.Headers)
		if err != nil || c.wsClient == nil {
			verbosePrint(logLevelError, "websocket err: %v", err)
			return fmt.Errorf("websocket error: %v", err)
		}
	default:
		verbosePrint(logLevelError, "not support %s", opts.typ)
		return fmt.Errorf("unsupported protocol: %s", opts.typ)
	}

	c.reused = true
	return nil
}

// Optimization: Use object pool to reduce memory allocation
var (
	bufferPool = sync.Pool{
		New: func() interface{} {
			return make([]byte, 32*1024) // 32KB buffer
		},
	}
	readerPool = sync.Pool{
		New: func() interface{} {
			return &bytes.Reader{}
		},
	}
)

func (c *Client) Do(url, reqBody []byte, timeoutMs int) (int, int64, error) {
	curTimeout := time.Duration(c.opts.params.Timeout) * time.Millisecond
	if timeoutMs > 0 {
		curTimeout = time.Duration(timeoutMs) * time.Millisecond
	}

	ctx, cancel := context.WithTimeout(context.Background(), curTimeout)
	defer cancel()

	switch c.opts.typ {
	case protocolHTTP1, protocolHTTP2, protocolHTTP3:
		// Optimization: Reuse Reader object
		reader := readerPool.Get().(*bytes.Reader)
		reader.Reset(reqBody)
		defer readerPool.Put(reader)

		req, err := http.NewRequestWithContext(ctx,
			c.opts.params.RequestMethod, string(url), reader)
		if err != nil {
			return 0, 0, fmt.Errorf("create request error: %v", err)
		}

		// Set request headers
		for k, v := range c.opts.params.Headers {
			req.Header[k] = v
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return 0, 0, fmt.Errorf("http request error: %v", err)
		}
		defer resp.Body.Close()

		// Optimization: Content length handling
		contentLength := resp.ContentLength
		if contentLength < 0 {
			// If Content-Length is unknown, use buffer to read and calculate size
			buf := bufferPool.Get().([]byte)
			defer bufferPool.Put(&buf)

			var totalSize int64
			for {
				n, err := resp.Body.Read(buf)
				totalSize += int64(n)
				if err == io.EOF {
					break
				}
				if err != nil {
					return resp.StatusCode, totalSize, err
				}
			}
			contentLength = totalSize
		} else {
			// Optimization: Directly discard response body to release connection
			_, _ = io.Copy(io.Discard, resp.Body)
		}
		return resp.StatusCode, contentLength, nil

	case protocolWS, protocolWSS:
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

	return 0, 0, fmt.Errorf("unsupported protocol type: %s", c.opts.typ)
}

func (c *Client) Close() error {
	c.reused = false
	switch c.opts.typ {
	case protocolHTTP1, protocolHTTP2, protocolHTTP3:
		if c.httpClient != nil {
			c.httpClient.CloseIdleConnections()
		}
		return nil
	case protocolWS, protocolWSS:
		if c.wsClient != nil {
			return c.wsClient.Close()
		}
		return nil
	}

	return fmt.Errorf("unsupported protocol type: %s", c.opts.typ)
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
		verbosePrint(logLevelError, "json marshal err: %v", err)
		return err.Error()
	}
	return string(body)
}

// Optimization: Use object pool to optimize request body processing
func (p *HttpbenchParameters) GetRequestBody() ([]byte, io.Reader) {
	if p.RequestBody == "" {
		return nil, nil
	}

	if p.RequestBodyType == bodyHex {
		decoded, err := hex.DecodeString(p.RequestBody)
		if err != nil {
			verbosePrint(logLevelError, "hex decode error: %v", err)
			return nil, nil
		}
		reader := readerPool.Get().(*bytes.Reader)
		reader.Reset(decoded)
		return decoded, reader
	}

	body := []byte(p.RequestBody)
	reader := readerPool.Get().(*bytes.Reader)
	reader.Reset(body)
	return body, reader
}
