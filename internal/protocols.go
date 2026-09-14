/*
 * protocols.go — 协议客户端构造器（HTTP/1、HTTP/2、HTTP/3、WebSocket）
 *
 *   Client.Init(opts)
 *     ├─ ProtocolHTTP1 ──► initHTTP1Client()  *http.Client + http.Transport
 *     ├─ ProtocolHTTP2 ──► initHTTP2Client()  *http.Client + http2.Transport
 *     ├─ ProtocolHTTP3 ──► initHTTP3Client()  *http.Client + quic http3.RoundTripper
 *     └─ ProtocolWS/WSS ─► initWebSocketClient()  直连 websocket.Conn
 *                              └─ reconnectWebSocket() 断线重连（复用 init）
 *
 *   超时职责分离（详见 tls.go）：
 *     DialContext.Timeout   — TCP 建连（下限 10s）
 *     TLSHandshakeTimeout   — TLS 握手（下限 10s）
 *     ResponseHeaderTimeout — 响应头（吃请求预算）
 *     http.Client.Timeout   — HTTP/1 为 0，每请求超时由 Do() 的 ctx 控制
 *
 * 依赖：logging, tls.go (buildTLSConfig/handshakeTimeoutFor/...)
 * 被依赖：transport.go (Init 分发), sender.go (defaultSenderFactory)
 */

package bench

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/quic-go/quic-go/http3"
	"golang.org/x/net/http2"
)

// ------------------------------------------------------------------ HTTP/1 ---

// initHTTP1Client builds an *http.Client for HTTP/1.1 with keep-alive,
// compression, proxy, and timeout settings sourced from c.opts.
func (c *Client) initHTTP1Client() (*http.Client, error) {
	reqTimeout := c.opts.Params.Timeout

	tr := &http.Transport{
		TLSClientConfig: buildHTTP1TLSConfig(c.opts.Insecure),

		DisableCompression:  c.opts.Params.DisableCompression,
		DisableKeepAlives:   c.opts.Params.DisableKeepAlives,
		TLSHandshakeTimeout: handshakeTimeoutFor(reqTimeout),
		TLSNextProto:        make(map[string]func(string, *tls.Conn) http.RoundTripper),
		DialContext: (&net.Dialer{
			Timeout:   dialTimeoutFor(reqTimeout),
			KeepAlive: 60 * time.Second,
			DualStack: true,
		}).DialContext,
		MaxIdleConns:          200,
		MaxIdleConnsPerHost:   100,
		MaxConnsPerHost:       200,
		IdleConnTimeout:       idleConnTimeout,
		ResponseHeaderTimeout: reqTimeout,
		ExpectContinueTimeout: expectContinueTimeout,
		ForceAttemptHTTP2:     false,
		WriteBufferSize:       32 * 1024,
		ReadBufferSize:        32 * 1024,
	}

	if err := applyProxy(tr, c.opts.Params.ProxyURL); err != nil {
		return nil, err
	}

	// Client.Timeout is 0: the per-request deadline is enforced by the
	// context passed to Do() (see resolveTimeout + context.WithTimeout).
	// Setting a non-zero Client.Timeout would create a second independent
	// deadline that races with the context and can cancel a request early
	// when the context budget legitimately exceeds the configured default.
	return &http.Client{
		Timeout:   0,
		Transport: tr,
	}, nil
}

// ------------------------------------------------------------------ HTTP/2 ---

// initHTTP2Client builds an *http.Client configured for HTTP/2 (including
// h2c / cleartext support via AllowHTTP). TLS verification is controlled by
// c.opts.Insecure.
func (c *Client) initHTTP2Client() *http.Client {
	return &http.Client{
		Timeout: c.opts.Params.Timeout,
		Transport: &http2.Transport{
			TLSClientConfig:            buildTLSConfig(c.opts.Insecure),
			DisableCompression:         c.opts.Params.DisableCompression,
			AllowHTTP:                  true,
			MaxReadFrameSize:           1 << 20,
			StrictMaxConcurrentStreams: true,
			MaxHeaderListSize:          1 << 20,
			ReadIdleTimeout:            30 * time.Second,
			PingTimeout:                15 * time.Second,
		},
	}
}

// ------------------------------------------------------------------ HTTP/3 ---

// initHTTP3Client builds an *http.Client backed by the quic-go HTTP/3
// round-tripper. The system root CA pool is loaded lazily; TLS verification
// is controlled by c.opts.Insecure.
func (c *Client) initHTTP3Client() (*http.Client, error) {
	loadHTTP3CertPool()
	return &http.Client{
		Timeout: c.opts.Params.Timeout,
		Transport: &http3.RoundTripper{
			TLSClientConfig: tlsConfigWithRootCAs(c.opts.Insecure, http3CertPool),
		},
	}, nil
}

// --------------------------------------------------------------- WebSocket ---

// initWebSocketClient dials the target WebSocket URL and stores the
// connection on c.wsClient. The handshake timeout is floored to
// handshakeTimeout (handshake has a distinct, adequately-sized budget
// independent of the per-request timeout); buffer sizes and compression
// policy are sourced from c.opts.Params.
func (c *Client) initWebSocketClient() error {
	dialer := websocket.Dialer{
		HandshakeTimeout:  handshakeTimeoutFor(c.opts.Params.Timeout),
		ReadBufferSize:    32 * 1024,
		WriteBufferSize:   32 * 1024,
		EnableCompression: !c.opts.Params.DisableCompression,
		// TLS verification follows the same policy as the HTTP protocols.
		TLSClientConfig: buildTLSConfig(c.opts.Insecure),
	}

	var err error
	c.wsClient, _, err = dialer.Dial(c.opts.Params.URL, c.opts.Params.Headers)
	if err != nil {
		Error(0, "websocket dial error: %v", err)
		return fmt.Errorf("websocket dial error: %v", err)
	}

	if c.wsClient == nil {
		return fmt.Errorf("websocket connection is nil")
	}

	return nil
}

// reconnectWebSocket closes the current connection (if any) and re-establishes
// it. Called by doWebSocketRequest after a transient I/O error.
func (c *Client) reconnectWebSocket() error {
	if c.wsClient != nil {
		// Best-effort close; ignore errors since the connection is already in
		// a bad state.
		_ = c.wsClient.Close()
		c.wsClient = nil
	}
	Debug(0, "websocket reconnecting after transient error")
	return c.initWebSocketClient()
}
