/*
 * sender.go — Sender / SenderFactory 抽象（未来执行引擎注入点）
 *
 *   Sender interface { Do(ctx, url, body, timeout); Close() }
 *     └─ *Client 编译期即满足（var _ Sender = (*Client)(nil)）
 *
 *   SenderFactory func(ClientOpts) (Sender, error)
 *     └─ DefaultSenderFactory() — 构造 *Client 并作为 Sender 返回
 *
 *   STATUS: 生产路径（worker.go）目前仍直接使用 *Client，
 *   尚无调用点通过 SenderFactory 构造 Sender——本文件仅由
 *   sender_test.go 驱动，作为未来执行引擎重构的预留注入点。
 *
 * 依赖：transport.go (Client, ClientOpts)
 * 被依赖：sender_test.go（仅测试）
 */

package bench

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
