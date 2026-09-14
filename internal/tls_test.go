package bench

import (
	"crypto/tls"
	"net/http"
	"testing"
	"time"
)

func TestBuildTLSConfig_Insecure(t *testing.T) {
	cfg := buildTLSConfig(true)
	if !cfg.InsecureSkipVerify {
		t.Fatal("want InsecureSkipVerify")
	}
	cfg2 := buildHTTP1TLSConfig(false)
	if cfg2.InsecureSkipVerify {
		t.Fatal("secure config should not skip verify")
	}
	_ = tls.VersionTLS12 // keep import used; MinVersion may be 0 (Go default)
	if cfg2 == nil {
		t.Fatal("nil tls config")
	}
}

func TestResolveAndMaxDuration(t *testing.T) {
	if got := resolveTimeout(3*time.Second, 0); got != 3*time.Second {
		t.Fatalf("default override empty: %v", got)
	}
	if got := resolveTimeout(3*time.Second, 5*time.Second); got != 5*time.Second {
		t.Fatalf("override: %v", got)
	}
	if maxDuration(time.Second, 2*time.Second) != 2*time.Second {
		t.Fatal("maxDuration")
	}
	if handshakeTimeoutFor(0) <= 0 || dialTimeoutFor(0) <= 0 {
		t.Fatal("timeouts should be positive")
	}
}

func TestApplyProxy(t *testing.T) {
	tr := &http.Transport{}
	if err := applyProxy(tr, ""); err != nil {
		t.Fatal(err)
	}
	if err := applyProxy(tr, "://bad"); err == nil {
		t.Fatal("want proxy parse error")
	}
	if err := applyProxy(tr, "http://127.0.0.1:8080"); err != nil {
		t.Fatal(err)
	}
	if tr.Proxy == nil {
		t.Fatal("Proxy func not set")
	}
}
