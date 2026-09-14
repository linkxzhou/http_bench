/*
 * tls.go — TLS / 代理 / 超时公共构造器
 *
 *   buildTLSConfig(insecure) *tls.Config
 *   buildHTTP1TLSConfig(insecure) — 同上（HTTP/1 传输层）
 *   tlsConfigWithRootCAs(insecure, pool) — HTTP/3 显式 RootCAs
 *   loadHTTP3CertPool() — sync.Once 懒加载系统 CA（失败 panic，同旧行为）
 *
 *   applyProxy(tr, proxyURL) — http.Transport 代理装配
 *   resolveTimeout(default, override) — 每请求超时决策
 *
 *   超时下限常量（请求超时更大时以请求超时为准）：
 *     dialTimeout 10s / handshakeTimeout 10s
 *     expectContinueTimeout 1s / idleConnTimeout 90s
 *     └─ handshakeTimeoutFor / dialTimeoutFor — maxDuration 取大者
 *
 * 依赖：无（仅标准库）
 * 被依赖：protocols.go（全部构造器）
 */

package bench

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	gourl "net/url"
	"sync"
	"time"
)

// buildTLSConfig returns a *tls.Config with verification controlled by the
// insecure flag. When insecure is false, the system root CA pool is used and
// certificates are fully verified. When true, InsecureSkipVerify is set.
//
// Callers must not mutate the returned config's InsecureSkipVerify field
// directly — construct a new config via this builder if the policy changes.
func buildTLSConfig(insecure bool) *tls.Config {
	return &tls.Config{InsecureSkipVerify: insecure}
}

// buildHTTP1TLSConfig is like buildTLSConfig but returns a config whose
// RootCAs can be customized by the caller (e.g. HTTP/3's system pool). The
// returned config is independent and safe to mutate.
func buildHTTP1TLSConfig(insecure bool) *tls.Config {
	return buildTLSConfig(insecure)
}

// tlsConfigWithRootCAs returns a *tls.Config with the given root CA pool and
// verification policy. Used by HTTP/3, whose RoundTripper requires an explicit
// RootCAs (unlike HTTP/1's transport which reads the system pool implicitly).
func tlsConfigWithRootCAs(insecure bool, rootCAs *x509.CertPool) *tls.Config {
	cfg := &tls.Config{InsecureSkipVerify: insecure}
	if rootCAs != nil {
		cfg.RootCAs = rootCAs
	}
	return cfg
}

// http3CertPool and http3PoolOnce lazily load the system certificate pool for
// HTTP/3's RoundTripper, which requires an explicit RootCAs.
var (
	http3CertPool *x509.CertPool
	http3PoolOnce sync.Once
)

// loadHTTP3CertPool lazily initializes the system root CA pool used by the
// HTTP/3 round-tripper. Panics on failure (same behavior as pre-refactor).
func loadHTTP3CertPool() {
	http3PoolOnce.Do(func() {
		var err error
		if http3CertPool, err = x509.SystemCertPool(); err != nil {
			panic(ProtocolHTTP3 + " initialization error: " + err.Error())
		}
	})
}

// applyProxy wires an HTTP/1 transport's proxy from the params, returning an
// error if the proxy URL is malformed.
func applyProxy(tr *http.Transport, proxyUrl string) error {
	if proxyUrl == "" {
		return nil
	}
	u, err := gourl.Parse(proxyUrl)
	if err != nil {
		return fmt.Errorf("invalid proxy URL: %v", err)
	}
	tr.Proxy = http.ProxyURL(u)
	return nil
}

// resolveTimeout returns the effective per-request timeout: override if > 0,
// otherwise the client's configured default.
func resolveTimeout(defaultTimeout, override time.Duration) time.Duration {
	if override > 0 {
		return override
	}
	return defaultTimeout
}

// Timeout floor constants. They define the distinct timeout roles; when the
// request timeout is larger, the request timeout wins; when smaller, the
// floor ensures handshake and dial have adequate time even for very short
// per-request budgets.
const (
	// dialTimeout is the floor for establishing a TCP connection.
	dialTimeout = 10 * time.Second
	// handshakeTimeout is the floor for the TLS handshake phase.
	handshakeTimeout = 10 * time.Second
	// expectContinueTimeout is the grace period for a 100-continue response.
	expectContinueTimeout = 1 * time.Second
	// idleConnTimeout is how long an idle keep-alive connection is retained.
	idleConnTimeout = 90 * time.Second
)

// handshakeTimeoutFor returns the TLS handshake timeout: the larger of the
// request timeout and the floor. This prevents a tiny per-request budget
// (e.g. 500ms) from starving the handshake on a fresh connection.
func handshakeTimeoutFor(requestTimeout time.Duration) time.Duration {
	return maxDuration(requestTimeout, handshakeTimeout)
}

// dialTimeoutFor returns the TCP dial timeout, floored to dialTimeout.
func dialTimeoutFor(requestTimeout time.Duration) time.Duration {
	return maxDuration(requestTimeout, dialTimeout)
}

// maxDuration returns the larger of two durations.
func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}
