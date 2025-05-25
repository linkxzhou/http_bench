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
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/quic-go/quic-go/http3"
	"golang.org/x/net/http2"
)

type ClientPool struct {
	clients chan *Client
	mu      sync.Mutex // Mutex to protect connection pool operations
	active  int        // Track number of active connections
	maxSize int        // Maximum pool size
}

func NewClientPool(maxSize int) *ClientPool {
	return &ClientPool{
		clients: make(chan *Client, maxSize),
		maxSize: maxSize,
	}
}

func (p *ClientPool) Get() *Client {
	p.mu.Lock()
	defer p.mu.Unlock()

	select {
	case client := <-p.clients:
		if client != nil {
			p.active++
			return client
		}
	default:
		if p.active < p.maxSize {
			p.active++
			return &Client{}
		}
	}
	return nil
}

func (p *ClientPool) Put(client *Client) {
	if client == nil {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	select {
	case p.clients <- client:
		// Successfully returned to connection pool
	default:
		p.Close(client) // Connection pool is full, close the connection
	}
	p.active--
}

func (p *ClientPool) Close(client *Client) {
	client.Close()
}

type (
	Client struct {
		httpClient *http.Client
		wsClient   *websocket.Conn
		opts       ClientOpts
	}

	ClientOpts struct {
		typ    string
		params HttpbenchParameters
	}
)

var http3Pool *x509.CertPool

func (c *Client) Init(opts ClientOpts) (err error) {
	verbosePrint(logLevelDebug, "client Init opts: %v", opts)
	c.opts = opts
	switch c.opts.typ {
	case protocolHTTP3:
		if http3Pool == nil {
			if http3Pool, err = x509.SystemCertPool(); err != nil {
				panic(protocolHTTP3 + " err: " + err.Error())
			}
		}

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
			},
		}
	case protocolHTTP1:
		// Optimize HTTP/1.1 transport settings
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
			}).DialContext,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   100,
			MaxConnsPerHost:       100,
			IdleConnTimeout:       90 * time.Second,
			ResponseHeaderTimeout: time.Duration(c.opts.params.Timeout) * time.Millisecond,
			ExpectContinueTimeout: 1 * time.Second,
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
		c.wsClient, _, err = websocket.DefaultDialer.Dial(c.opts.params.Url, c.opts.params.Headers)
		if err != nil || c.wsClient == nil {
			verbosePrint(logLevelError, "websocket err: %v", err)
			return fmt.Errorf("websocket error")
		}
	default:
		verbosePrint(logLevelError, "not support %s", opts.typ)
	}

	return nil
}

func (c *Client) Do(url, reqBody []byte, timeoutMs int) (int, int64, error) {
	curTimeout := time.Duration(c.opts.params.Timeout) * time.Millisecond
	if timeoutMs > 0 {
		curTimeout = time.Duration(timeoutMs) * time.Millisecond
	}

	ctx, cancel := context.WithTimeout(context.Background(), curTimeout)
	defer cancel()

	switch c.opts.typ {
	case protocolHTTP1, protocolHTTP2, protocolHTTP3:
		req, err := http.NewRequestWithContext(ctx,
			c.opts.params.RequestMethod, string(url), bytes.NewReader(reqBody))
		if err != nil {
			return 0, 0, fmt.Errorf("create request error: %v", err)
		}

		// Set headers
		for k, v := range c.opts.params.Headers {
			req.Header[k] = v
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return 0, 0, fmt.Errorf("http request error: %v", err)
		}
		defer resp.Body.Close()

		// Optimize content length handling
		contentLength := resp.ContentLength
		if contentLength < 0 {
			contentLength = 0
		}

		// Discard response body to free connections
		_, _ = io.Copy(io.Discard, resp.Body)
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
	switch c.opts.typ {
	case protocolHTTP1, protocolHTTP2, protocolHTTP3:
		c.httpClient.CloseIdleConnections()
		return nil
	case protocolWS, protocolWSS:
		return c.wsClient.Close()
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
}

func (p *HttpbenchParameters) String() string {
	body, err := json.MarshalIndent(p, "", "\t")
	if err != nil {
		verbosePrint(logLevelError, "json marshal err: %v", err)
		return err.Error()
	}
	return string(body)
}

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
		return decoded, bytes.NewReader(decoded)
	}

	return []byte(p.RequestBody), strings.NewReader(p.RequestBody)
}
