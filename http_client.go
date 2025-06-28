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

// 优化：使用原子操作和更高效的连接池设计
type ClientPool struct {
	clients chan *Client
	maxSize int32
	active  int32 // 使用原子操作
	closed  int32 // 使用原子操作标记池是否关闭
}

func NewClientPool(maxSize int) *ClientPool {
	return &ClientPool{
		clients: make(chan *Client, maxSize),
		maxSize: int32(maxSize),
	}
}

// 优化：减少锁使用，使用原子操作
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
		// 非阻塞获取失败，检查是否可以创建新连接
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
		// 成功放回连接池
	default:
		// 连接池已满，关闭连接
		p.Close(client)
	}
	atomic.AddInt32(&p.active, -1)
}

func (p *ClientPool) Close(client *Client) {
	if client != nil {
		client.Close()
	}
}

// 优化：添加连接池关闭方法
func (p *ClientPool) Shutdown() {
	atomic.StoreInt32(&p.closed, 1)
	close(p.clients)

	// 清理剩余连接
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
		// 优化：添加复用标记
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

// 优化：使用sync.Once确保http3Pool只初始化一次
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

	// 如果客户端已经初始化且类型相同，直接复用
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
				// 优化：添加连接池配置
				MaxHeaderListSize: 1 << 20, // 1MB
				ReadIdleTimeout:   30 * time.Second,
				PingTimeout:       15 * time.Second,
			},
		}
	case protocolHTTP1:
		// 优化：HTTP/1.1传输层设置
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
				// 优化：添加更多网络优化参数
				DualStack: true,
			}).DialContext,
			// 优化：调整连接池参数以提高性能
			MaxIdleConns:          200, // 增加最大空闲连接数
			MaxIdleConnsPerHost:   100, // 增加每个主机的最大空闲连接数
			MaxConnsPerHost:       200, // 增加每个主机的最大连接数
			IdleConnTimeout:       90 * time.Second,
			ResponseHeaderTimeout: time.Duration(c.opts.params.Timeout) * time.Millisecond,
			ExpectContinueTimeout: 1 * time.Second,
			// 优化：启用HTTP/2支持但禁用升级
			ForceAttemptHTTP2: false,
			// 优化：设置写缓冲区大小
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
		// 优化：WebSocket连接配置
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

// 优化：使用对象池减少内存分配
var (
	bufferPool = sync.Pool{
		New: func() interface{} {
			return make([]byte, 32*1024) // 32KB缓冲区
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
		// 优化：复用Reader对象
		reader := readerPool.Get().(*bytes.Reader)
		reader.Reset(reqBody)
		defer readerPool.Put(reader)

		req, err := http.NewRequestWithContext(ctx,
			c.opts.params.RequestMethod, string(url), reader)
		if err != nil {
			return 0, 0, fmt.Errorf("create request error: %v", err)
		}

		// 设置请求头
		for k, v := range c.opts.params.Headers {
			req.Header[k] = v
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return 0, 0, fmt.Errorf("http request error: %v", err)
		}
		defer resp.Body.Close()

		// 优化：内容长度处理
		contentLength := resp.ContentLength
		if contentLength < 0 {
			// 如果Content-Length未知，使用缓冲区读取并计算大小
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
					return resp.StatusCode, totalSize, err
				}
			}
			contentLength = totalSize
		} else {
			// 优化：直接丢弃响应体以释放连接
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
}

func (p *HttpbenchParameters) String() string {
	body, err := json.MarshalIndent(p, "", "\t")
	if err != nil {
		verbosePrint(logLevelError, "json marshal err: %v", err)
		return err.Error()
	}
	return string(body)
}

// 优化：使用对象池优化请求体处理
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
