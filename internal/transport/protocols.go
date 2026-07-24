package transport

// Protocol transport constructors (plan.md §D-2). Each function builds an
// *http.Client (or WebSocket connection) configured from c.opts, sharing TLS,
// proxy, compression, and timeout policies defined in tls_config.go.
//
// Timeout responsibilities (plan.md §D-3):
//   - DialContext.Timeout     — TCP connection establishment (floor 10s)
//   - TLSHandshakeTimeout     — TLS handshake phase (floor 10s)
//   - ResponseHeaderTimeout   — time to receive response headers (request budget)
//   - http.Client.Timeout     — 0 for HTTP/1; the per-request total timeout is
//                                enforced via context.WithTimeout in Do(),
//                                avoiding a double-deadline that can prematurely
//                                abort a request whose context budget is larger.
//
// WebSocket reconnect policy (plan.md §D-4): doWebSocketRequest in client.go
// retries once after a reconnect on transient I/O errors.

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/quic-go/quic-go/http3"
	"golang.org/x/net/http2"

	"github.com/linkxzhou/http_bench/internal/logging"
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
// handshakeTimeout (plan.md §D-3: handshake has a distinct, adequately-sized
// budget independent of the per-request timeout); buffer sizes and
// compression policy are sourced from c.opts.Params.
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
		logging.Error(0, "websocket dial error: %v", err)
		return fmt.Errorf("websocket dial error: %v", err)
	}

	if c.wsClient == nil {
		return fmt.Errorf("websocket connection is nil")
	}

	return nil
}

// reconnectWebSocket closes the current connection (if any) and re-establishes
// it. Called by doWebSocketRequest after a transient I/O error per §D-4.
func (c *Client) reconnectWebSocket() error {
	if c.wsClient != nil {
		// Best-effort close; ignore errors since the connection is already in
		// a bad state.
		_ = c.wsClient.Close()
		c.wsClient = nil
	}
	logging.Debug(0, "websocket reconnecting after transient error")
	return c.initWebSocketClient()
}
