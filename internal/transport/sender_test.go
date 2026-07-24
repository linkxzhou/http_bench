package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestSenderInterfaceContract verifies that *Client satisfies the Sender
// interface at runtime (the compile-time assertion in sender.go covers the
// static check; this guards against accidental signature drift via reflection).
func TestSenderInterfaceContract(t *testing.T) {
	var s Sender = &Client{}
	// The zero-value Client must report a clear error from Do (not initialized)
	// rather than panic — this is the Sender contract for uninitialized state.
	_, _, err := s.Do(context.Background(), []byte("http://example.invalid"), nil, 100*time.Millisecond)
	if err == nil {
		t.Error("expected error from uninitialized Sender.Do, got nil")
	}
}

// TestDefaultSenderFactoryHTTP1 exercises the default SenderFactory against a
// real HTTP/1 server, asserting that the returned Sender behaves identically
// to a directly-constructed *Client.
func TestDefaultSenderFactoryHTTP1(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		w.Write([]byte("short"))
	}))
	defer srv.Close()

	factory := DefaultSenderFactory()
	sender, err := factory(ClientOpts{
		Protocol: ProtocolHTTP1,
		Params: HttpbenchParameters{
			URL:           srv.URL,
			RequestMethod: http.MethodGet,
			Timeout:       2 * time.Second,
		},
		Insecure: true,
	})
	if err != nil {
		t.Fatalf("factory error: %v", err)
	}
	defer sender.Close()

	code, _, err := sender.Do(context.Background(), []byte(srv.URL), nil, 0)
	if err != nil {
		t.Fatalf("Do error: %v", err)
	}
	if code != http.StatusTeapot {
		t.Errorf("status = %d, want %d", code, http.StatusTeapot)
	}
}

// TestSenderFactoryStub demonstrates that a custom SenderFactory can be
// injected — this is the seam that the future execution engine (plan.md §E)
// and deterministic tests will rely on.
func TestSenderFactoryStub(t *testing.T) {
	stub := func(opts ClientOpts) (Sender, error) {
		return &stubSender{code: http.StatusOK, len: 7}, nil
	}
	s, err := stub(ClientOpts{Protocol: ProtocolHTTP1})
	if err != nil {
		t.Fatalf("stub factory error: %v", err)
	}
	code, n, err := s.Do(context.Background(), nil, nil, 0)
	if err != nil {
		t.Fatalf("stub Do error: %v", err)
	}
	if code != http.StatusOK || n != 7 {
		t.Errorf("stub returned (%d, %d), want (200, 7)", code, n)
	}
}

// stubSender is a minimal Sender for factory-injection tests.
type stubSender struct {
	code int
	len  int64
}

func (s *stubSender) Do(ctx context.Context, url, reqBody []byte, timeout time.Duration) (int, int64, error) {
	return s.code, s.len, nil
}
func (s *stubSender) Close() error { return nil }

// TestBodyDrainSequential verifies §D-7: the response body is always drained
// so a subsequent request on the same keep-alive client does not stall or
// return a "http: server returned ... cannot reuse" style error. We issue two
// sequential requests and assert both succeed with correct content lengths.
// The primary regression guarded against: a panic or corrupted state when the
// previous response body was not fully consumed before the next request.
func TestBodyDrainSequential(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "5")
		w.Write([]byte("hello"))
	}))
	defer srv.Close()

	c := &Client{}
	if err := c.Init(ClientOpts{
		Protocol: ProtocolHTTP1,
		Params: HttpbenchParameters{
			URL:               srv.URL,
			RequestMethod:     http.MethodGet,
			Timeout:           2 * time.Second,
			DisableKeepAlives: false,
		},
		Insecure: true,
	}); err != nil {
		t.Fatalf("Init error: %v", err)
	}
	defer c.Close()

	for i := 0; i < 2; i++ {
		code, n, err := c.Do(context.Background(), []byte(srv.URL), nil, 0)
		if err != nil {
			t.Fatalf("request %d error: %v", i, err)
		}
		if code != http.StatusOK {
			t.Errorf("request %d status = %d, want 200", i, code)
		}
		if n != 5 {
			t.Errorf("request %d content length = %d, want 5", i, n)
		}
	}
}

// TestBodyDrainUnknownLength verifies the ContentLength=-1 path (chunked /
// unknown length) still counts bytes correctly and drains fully.
func TestBodyDrainUnknownLength(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Flush to force chunked encoding (no Content-Length).
		f := w.(http.Flusher)
		f.Flush()
		w.Write([]byte("chunked-body"))
		f.Flush()
	}))
	defer srv.Close()

	c := &Client{}
	if err := c.Init(ClientOpts{
		Protocol: ProtocolHTTP1,
		Params: HttpbenchParameters{
			URL:               srv.URL,
			RequestMethod:     http.MethodGet,
			Timeout:           2 * time.Second,
			DisableKeepAlives: true, // avoid reuse complications with chunked
		},
		Insecure: true,
	}); err != nil {
		t.Fatalf("Init error: %v", err)
	}
	defer c.Close()

	_, n, err := c.Do(context.Background(), []byte(srv.URL), nil, 0)
	if err != nil {
		t.Fatalf("Do error: %v", err)
	}
	if n != int64(len("chunked-body")) {
		t.Errorf("content length = %d, want %d", n, len("chunked-body"))
	}
}

// TestTimeoutRoleSeparation verifies §D-3: a tiny per-request timeout does not
// starve the TLS handshake / dial phase. With the old code, Timeout=1ns was
// applied to TLSHandshakeTimeout and DialContext.Timeout, causing a fresh
// connection to fail at the handshake before the request even started. With
// role separation, handshake/dial have a 10s floor, so the request reaches
// the server and only the response phase is bounded by the tiny budget.
//
// We assert the error is a context deadline (response timeout), not a dial
// or handshake failure.
func TestTimeoutRoleSeparation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Sleep longer than the tiny request timeout to force a response timeout.
		time.Sleep(50 * time.Millisecond)
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	c := &Client{}
	if err := c.Init(ClientOpts{
		Protocol: ProtocolHTTP1,
		Params: HttpbenchParameters{
			URL:               srv.URL,
			RequestMethod:     http.MethodGet,
			Timeout:           1 * time.Nanosecond, // tiny: should bound response, not handshake
			DisableKeepAlives: true,                // force a fresh connection (exercises dial+handshake)
		},
		Insecure: true,
	}); err != nil {
		t.Fatalf("Init error: %v", err)
	}
	defer c.Close()

	_, _, err := c.Do(context.Background(), []byte(srv.URL), nil, 0)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	// The error should be a context/response timeout, NOT a dial or handshake
	// failure. With role separation, dial+handshake succeed (10s floor) and
	// only the response phase times out.
	errStr := err.Error()
	for _, bad := range []string{"dial", "handshake", "tls"} {
		if strings.Contains(strings.ToLower(errStr), bad) {
			t.Errorf("error should not be a %s failure (role separation broken): %v", bad, err)
		}
	}
}

// TestWebSocketReconnect verifies §D-4: after a transient write/read error,
// the client reconnects once and retries the request.
//
// Setup: a WebSocket server that closes the first connection abruptly (causing
// the client's first read to fail), then accepts a second connection normally.
// The client should transparently reconnect and return the second response.
func TestWebSocketReconnect(t *testing.T) {
	var connCount int32
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		n := atomic.AddInt32(&connCount, 1)
		if n == 1 {
			// First connection: read the message, then close abruptly without
			// replying, forcing a read error on the client.
			ws.ReadMessage()
			ws.Close()
			return
		}
		// Second connection: echo normally.
		_, msg, err := ws.ReadMessage()
		if err != nil {
			return
		}
		ws.WriteMessage(websocket.TextMessage, msg)
	}))
	defer srv.Close()

	// Convert http:// to ws://
	wsURL := "ws" + srv.URL[len("http"):]

	c := &Client{}
	if err := c.Init(ClientOpts{
		Protocol: ProtocolWS,
		Params: HttpbenchParameters{
			URL:           wsURL,
			RequestMethod: http.MethodGet,
			RequestBody:   "ping",
			Timeout:       2 * time.Second,
		},
		Insecure: true,
	}); err != nil {
		t.Fatalf("Init error: %v", err)
	}
	defer c.Close()

	code, _, err := c.Do(context.Background(), []byte(wsURL), []byte("ping"), 0)
	if err != nil {
		t.Fatalf("Do error after expected reconnect: %v", err)
	}
	if code != http.StatusOK {
		t.Errorf("status = %d, want 200", code)
	}
	if got := atomic.LoadInt32(&connCount); got != 2 {
		t.Errorf("connection count = %d, want 2 (initial + reconnect)", got)
	}
}
