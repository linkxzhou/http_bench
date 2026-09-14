package bench

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	// package main embeds ../index.html; internal tests inject it here.
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), ".."))
	html, err := os.ReadFile(filepath.Join(root, "index.html"))
	if err != nil {
		panic(err)
	}
	SetDashboardHTML(string(html))
	os.Exit(m.Run())
}

func TestAPI_DashboardHTMLAndDocs(t *testing.T) {
	html := DashboardHTML()
	if html == "" || !strings.Contains(html, "<") {
		t.Fatalf("DashboardHTML empty or not HTML")
	}
	if len(Usage) < 20 {
		t.Fatalf("Usage too short: %q", Usage)
	}
	if len(Examples) < 50 {
		t.Fatalf("Examples too short")
	}
}

func TestAPI_Wrappers(t *testing.T) {
	id1 := GenSequenceId()
	id2 := GenSequenceId()
	if id1 == id2 {
		t.Fatalf("GenSequenceId not unique: %d", id1)
	}

	addrs := NormalizeWorkerAddrs([]string{"10.0.0.1:8080"}, "")
	if len(addrs) != 1 || !strings.Contains(addrs[0], "/api") {
		t.Fatalf("NormalizeWorkerAddrs = %v", addrs)
	}

	if err := ValidateParams(&HttpbenchParameters{C: 0, N: 10}); err == nil {
		t.Fatal("ValidateParams(C=0) want error")
	}
	if err := ValidateParams(&HttpbenchParameters{C: 2, N: 10, Duration: 0}); err != nil {
		t.Fatalf("ValidateParams valid: %v", err)
	}
	if err := ValidateOutputFormat("json"); err == nil {
		t.Fatal("ValidateOutputFormat(json) want error")
	}
	if err := ValidateOutputFormat("csv"); err != nil {
		t.Fatalf("ValidateOutputFormat(csv): %v", err)
	}
	if err := ValidateProxyURL("http://127.0.0.1:8080"); err != nil {
		t.Fatalf("ValidateProxyURL: %v", err)
	}

	h, err := CompileHeaders([]string{"X-A: 1"}, "user:pass")
	if err != nil {
		t.Fatalf("CompileHeaders: %v", err)
	}
	if h["X-A"][0] != "1" {
		t.Errorf("header X-A = %v", h["X-A"])
	}
	if !strings.HasPrefix(h["Authorization"][0], "Basic ") {
		t.Errorf("auth = %v", h["Authorization"])
	}
	_ = time.Second
}
