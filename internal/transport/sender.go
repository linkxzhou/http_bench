package transport

// Sender is the minimal transport-layer abstraction for executing a single
// benchmark request. It is implemented by the concrete per-protocol clients
// (HTTP/1, HTTP/2, HTTP/3, WebSocket) and decouples the execution engine from
// the transport implementation details.
//
// This addresses plan.md §D-1: "定义最小接口：Sender 提供 Send(...) 和
// Close() error；按 Protocol 由 SenderFactory 创建实现。"
//
// The interface deliberately keeps the signature of the existing (*Client).Do
// method so that Client satisfies it without modification — the two coexist
// during the incremental migration and call sites can switch gradually.
//
// STATUS (plan2.0.md §3.2): no production call site (including
// http_worker.go) constructs a Sender via SenderFactory yet — http_worker.go
// still depends on *Client directly. This file is currently exercised only
// by sender_test.go. It remains the designated injection point for a future
// execution-engine refactor; do not assume it is already wired into the
// request path.

import (
	"context"
	"time"
)

// Sender executes a single request against the configured target.
//
// url and reqBody are the rendered URL and request body bytes (templates
// already executed by the caller). timeout, when > 0, overrides the client's
// default per-request timeout. Returns the HTTP status code (or 0 on failure),
// the response content length in bytes, and an error describing any failure.
type Sender interface {
	// Do executes one request. Kept as Do (rather than Send) so the existing
	// *Client type satisfies Sender with zero changes.
	Do(ctx context.Context, url, reqBody []byte, timeout time.Duration) (statusCode int, contentLength int64, err error)
	// Close releases transport resources (idle connections, WebSocket links).
	Close() error
}

// SenderFactory builds a Sender for the given options. Implementations select
// the concrete protocol client based on opts.Protocol.
type SenderFactory func(opts ClientOpts) (Sender, error)

// defaultSenderFactory is the standard factory: it constructs a *Client and
// returns it as a Sender. Callers that need protocol-specific wiring can
// inject their own factory (e.g. for testing determinism).
func defaultSenderFactory(opts ClientOpts) (Sender, error) {
	c := &Client{}
	if err := c.Init(opts); err != nil {
		return nil, err
	}
	return c, nil
}

// DefaultSenderFactory returns the package-level default factory. Tests may
// capture this and restore it after injecting a stub.
func DefaultSenderFactory() SenderFactory { return defaultSenderFactory }

// Compile-time assertion that *Client satisfies Sender.
var _ Sender = (*Client)(nil)
